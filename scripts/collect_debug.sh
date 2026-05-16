#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: collect_debug.sh [options]

Collect Octopus runtime diagnostics for memory/CPU spikes.

Options:
  -u, --pprof-url URL       pprof base URL (default: http://127.0.0.1:6060)
  -p, --pid PID             Octopus process PID. Auto-detects when omitted.
  -c, --container NAME      Container name for docker/podman metadata (default: octopus)
      --cpu                 Capture CPU profile (default: off)
      --cpu-seconds N       CPU profile duration in seconds (default: 30)
  -o, --output-dir DIR      Output parent directory (default: /tmp)
  -h, --help                Show this help

Examples:
  sh scripts/collect_debug.sh
  sh scripts/collect_debug.sh --cpu --cpu-seconds 30
  sh scripts/collect_debug.sh -u http://127.0.0.1:6060 -p 12345
EOF
}

PPROF_URL="${PPROF_URL:-http://127.0.0.1:6060}"
PID="${PID:-}"
CONTAINER="${CONTAINER:-octopus}"
CPU=0
CPU_SECONDS="${CPU_SECONDS:-30}"
OUTPUT_DIR="${OUTPUT_DIR:-/tmp}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -u|--pprof-url)
      PPROF_URL="$2"
      shift 2
      ;;
    -p|--pid)
      PID="$2"
      shift 2
      ;;
    -c|--container)
      CONTAINER="$2"
      shift 2
      ;;
    --cpu)
      CPU=1
      shift
      ;;
    --cpu-seconds)
      CPU_SECONDS="$2"
      shift 2
      ;;
    -o|--output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

run_capture() {
  desc="$1"
  outfile="$2"
  shift 2
  {
    echo "# $desc"
    echo "# command: $*"
    echo
    "$@"
  } >"$outfile" 2>&1 || true
}

curl_capture() {
  url="$1"
  outfile="$2"
  if need_cmd curl; then
    curl -fsS "$url" -o "$outfile" >"$outfile.curl.log" 2>&1 || true
  elif need_cmd wget; then
    wget -qO "$outfile" "$url" >"$outfile.wget.log" 2>&1 || true
  else
    echo "curl/wget not found" >"$outfile.error"
  fi
}

detect_pid() {
  if [ -n "$PID" ]; then
    return
  fi
  if need_cmd pgrep; then
    PID="$(pgrep -of 'octopus.*start|/octopus|octopus' || true)"
  fi
  if [ -z "$PID" ] && need_cmd pidof; then
    PID="$(pidof octopus 2>/dev/null | awk '{print $1}' || true)"
  fi
}

TS="$(date +%Y%m%d-%H%M%S)"
DIR="$OUTPUT_DIR/octopus-debug-$TS"
mkdir -p "$DIR"

{
  echo "timestamp=$TS"
  echo "hostname=$(hostname 2>/dev/null || true)"
  echo "uname=$(uname -a 2>/dev/null || true)"
  echo "pprof_url=$PPROF_URL"
  echo "container=$CONTAINER"
  echo "cpu_profile=$CPU"
  echo "cpu_seconds=$CPU_SECONDS"
} >"$DIR/meta.txt"

detect_pid
echo "pid=$PID" >>"$DIR/meta.txt"

run_capture "process list sorted by rss" "$DIR/process-top.txt" sh -c "ps -eo pid,ppid,comm,%cpu,%mem,rss,vsz,nlwp --sort=-rss | head -40"
run_capture "memory info" "$DIR/free.txt" sh -c "free -h 2>/dev/null || cat /proc/meminfo"
run_capture "disk usage" "$DIR/df.txt" df -h
run_capture "kernel messages" "$DIR/dmesg-tail.txt" sh -c "dmesg -T 2>/dev/null | tail -200 || dmesg 2>/dev/null | tail -200"

if [ -n "$PID" ] && [ -d "/proc/$PID" ]; then
  run_capture "target process" "$DIR/process.txt" ps -p "$PID" -o pid,ppid,comm,%cpu,%mem,rss,vsz,nlwp
  cp "/proc/$PID/status" "$DIR/proc-status.txt" 2>/dev/null || true
  cp "/proc/$PID/smaps_rollup" "$DIR/smaps-rollup.txt" 2>/dev/null || true
  run_capture "open fds" "$DIR/fd.txt" ls -l "/proc/$PID/fd"
  run_capture "fd count" "$DIR/fd-count.txt" sh -c "ls /proc/$PID/fd 2>/dev/null | wc -l"
  if need_cmd ss; then
    run_capture "tcp connections for pid" "$DIR/tcp.txt" sh -c "ss -tanp 2>/dev/null | grep '$PID' || true"
    run_capture "tcp states for pid" "$DIR/tcp-states.txt" sh -c "ss -tanp 2>/dev/null | grep '$PID' | awk '{print \$1}' | sort | uniq -c || true"
  fi
  if need_cmd pmap; then
    run_capture "pmap" "$DIR/pmap.txt" pmap -x "$PID"
  fi
  if need_cmd top; then
    run_capture "top threads" "$DIR/top-threads.txt" top -b -H -n 1 -p "$PID"
  fi
else
  echo "PID not found or /proc entry missing" >"$DIR/pid-not-found.txt"
fi

if need_cmd podman; then
  run_capture "podman ps" "$DIR/podman-ps.txt" podman ps -a
  run_capture "podman inspect" "$DIR/podman-inspect-$CONTAINER.json" podman inspect "$CONTAINER"
  run_capture "podman logs" "$DIR/podman-logs-$CONTAINER.txt" podman logs --tail=300 "$CONTAINER"
elif need_cmd docker; then
  run_capture "docker ps" "$DIR/docker-ps.txt" docker ps -a
  run_capture "docker inspect" "$DIR/docker-inspect-$CONTAINER.json" docker inspect "$CONTAINER"
  run_capture "docker logs" "$DIR/docker-logs-$CONTAINER.txt" docker logs --tail=300 "$CONTAINER"
fi

curl_capture "$PPROF_URL/debug/pprof/" "$DIR/pprof-index.html"
curl_capture "$PPROF_URL/debug/pprof/heap" "$DIR/heap.pb.gz"
curl_capture "$PPROF_URL/debug/pprof/allocs" "$DIR/allocs.pb.gz"
curl_capture "$PPROF_URL/debug/pprof/goroutine?debug=2" "$DIR/goroutine.txt"
curl_capture "$PPROF_URL/debug/pprof/block" "$DIR/block.pb.gz"
curl_capture "$PPROF_URL/debug/pprof/mutex" "$DIR/mutex.pb.gz"

if [ "$CPU" = "1" ]; then
  curl_capture "$PPROF_URL/debug/pprof/profile?seconds=$CPU_SECONDS" "$DIR/cpu.pb.gz"
fi

if need_cmd go; then
  [ -s "$DIR/heap.pb.gz" ] && run_capture "heap inuse space" "$DIR/heap-inuse-space.txt" go tool pprof -top -inuse_space "$DIR/heap.pb.gz"
  [ -s "$DIR/heap.pb.gz" ] && run_capture "heap inuse objects" "$DIR/heap-inuse-objects.txt" go tool pprof -top -inuse_objects "$DIR/heap.pb.gz"
  [ -s "$DIR/allocs.pb.gz" ] && run_capture "alloc space" "$DIR/alloc-space.txt" go tool pprof -top -alloc_space "$DIR/allocs.pb.gz"
  [ -s "$DIR/cpu.pb.gz" ] && run_capture "cpu top" "$DIR/cpu-top.txt" go tool pprof -top "$DIR/cpu.pb.gz"
else
  echo "go command not found; raw .pb.gz profiles were still collected" >"$DIR/go-tool-missing.txt"
fi

if [ -s "$DIR/goroutine.txt" ]; then
  grep -nE "internal/relay|sse|streamagg|chan send|pprof" "$DIR/goroutine.txt" >"$DIR/goroutine-interesting.txt" 2>/dev/null || true
fi

TAR="$DIR.tar.gz"
tar -czf "$TAR" -C "$OUTPUT_DIR" "$(basename "$DIR")"

echo "Collected debug bundle: $TAR"
echo "Send this archive for analysis. If attachments are unavailable, paste:"
echo "  $DIR/heap-inuse-space.txt"
echo "  $DIR/alloc-space.txt"
echo "  $DIR/goroutine-interesting.txt"
echo "  $DIR/process.txt"
