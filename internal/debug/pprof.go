package debug

import (
	"context"
	"errors"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
)

var manager = &pprofManager{}

type pprofManager struct {
	mu      sync.Mutex
	server  *http.Server
	addr    string
	enabled bool
}

func ApplySettings() error {
	enabled, err := op.SettingGetBool(model.SettingKeyPprofEnabled)
	if err != nil {
		return err
	}
	addr, err := op.SettingGetString(model.SettingKeyPprofAddr)
	if err != nil {
		return err
	}
	return manager.apply(enabled, addr)
}

func Close() error {
	return manager.stop()
}

func (m *pprofManager) apply(enabled bool, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !enabled {
		return m.stopLocked()
	}

	if m.enabled && m.addr == addr && m.server != nil {
		return nil
	}

	if err := m.stopLocked(); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{Addr: addr, Handler: mux}
	m.server = srv
	m.addr = addr
	m.enabled = true

	go func() {
		log.Infof("pprof server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("pprof server error: %v", err)
			m.mu.Lock()
			if m.server == srv {
				m.server = nil
				m.enabled = false
			}
			m.mu.Unlock()
		}
	}()

	return nil
}

func (m *pprofManager) stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *pprofManager) stopLocked() error {
	if m.server == nil {
		m.enabled = false
		m.addr = ""
		return nil
	}

	srv := m.server
	addr := m.addr
	m.server = nil
	m.enabled = false
	m.addr = ""

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Infof("pprof server stopped: %s", addr)
	return nil
}
