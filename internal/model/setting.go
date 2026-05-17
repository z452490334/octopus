package model

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
)

type SettingKey string

const (
	SettingKeyProxyURL                  SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval         SettingKey = "stats_save_interval"           // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval   SettingKey = "model_info_update_interval"    // 模型信息更新间隔(小时)
	SettingKeySyncLLMInterval           SettingKey = "sync_llm_interval"             // LLM 同步间隔(小时)
	SettingKeyRelayLogKeepPeriod        SettingKey = "relay_log_keep_period"         // 日志保存时间范围(天)
	SettingKeyRelayLogKeepEnabled       SettingKey = "relay_log_keep_enabled"        // 是否保留历史日志
	SettingKeyRelayDirectStreamMinBytes SettingKey = "relay_direct_stream_min_bytes" // 非流式响应超过该字节数时，兼容协议下直接流式转发；0 表示关闭
	SettingKeyCORSAllowOrigins          SettingKey = "cors_allow_origins"            // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyPprofEnabled              SettingKey = "pprof_enabled"                 // 是否启用 pprof 调试服务
	SettingKeyPprofAddr                 SettingKey = "pprof_addr"                    // pprof 监听地址，如 127.0.0.1:6060
	SettingKeyCircuitBreakerThreshold   SettingKey = "circuit_breaker_threshold"     // 熔断触发阈值（连续失败次数）
	SettingKeyCircuitBreakerCooldown    SettingKey = "circuit_breaker_cooldown"      // 熔断基础冷却时间（秒）
	SettingKeyCircuitBreakerMaxCooldown SettingKey = "circuit_breaker_max_cooldown"  // 熔断最大冷却时间（秒），指数退避上限
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},          // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},             // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"},    // 默认24小时更新一次模型信息
		{Key: SettingKeySyncLLMInterval, Value: "24"},            // 默认24小时同步一次LLM
		{Key: SettingKeyRelayLogKeepPeriod, Value: "7"},          // 默认日志保存7天
		{Key: SettingKeyRelayLogKeepEnabled, Value: "true"},      // 默认保留历史日志
		{Key: SettingKeyRelayDirectStreamMinBytes, Value: "0"},   // 默认关闭大响应直通
		{Key: SettingKeyPprofEnabled, Value: "false"},            // 默认关闭 pprof
		{Key: SettingKeyPprofAddr, Value: "127.0.0.1:6060"},      // 默认仅本机可访问
		{Key: SettingKeyCircuitBreakerThreshold, Value: "5"},     // 默认连续失败5次触发熔断
		{Key: SettingKeyCircuitBreakerCooldown, Value: "60"},     // 默认基础冷却60秒
		{Key: SettingKeyCircuitBreakerMaxCooldown, Value: "600"}, // 默认最大冷却600秒（10分钟）
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyModelInfoUpdateInterval, SettingKeySyncLLMInterval, SettingKeyRelayLogKeepPeriod,
		SettingKeyCircuitBreakerThreshold, SettingKeyCircuitBreakerCooldown, SettingKeyCircuitBreakerMaxCooldown,
		SettingKeyRelayDirectStreamMinBytes:
		v, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("%s must be an integer", s.Key)
		}
		if v < 0 {
			return fmt.Errorf("%s cannot be negative", s.Key)
		}
		return nil
	case SettingKeyRelayLogKeepEnabled, SettingKeyPprofEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("%s must be true or false", s.Key)
		}
		return nil
	case SettingKeyPprofAddr:
		if s.Value == "" {
			return fmt.Errorf("pprof addr cannot be empty")
		}
		if _, _, err := net.SplitHostPort(s.Value); err != nil {
			return fmt.Errorf("pprof addr must be host:port")
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":   true,
			"https":  true,
			"socks5": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks, or socks5")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}
