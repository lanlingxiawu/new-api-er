package operation_setting

import (
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool    `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes float64 `json:"auto_test_channel_minutes"`

	// Health-status thresholds for group/model monitoring.
	// StatusWarningThreshold: success rate below this → Warning (default 95%)
	// StatusUnhealthyThreshold: success rate below this → Unhealthy (default 90%)
	// StatusUnavailableThreshold: success rate below this → Unavailable (default 75%)
	// P95HealthyThresholdMs: P95 latency below this is healthy (default 2000 ms)
	// P95WarningThresholdMs: P95 latency below this is warning (default 5000 ms)
	StatusWarningThreshold     float64 `json:"status_warning_threshold"`
	StatusUnhealthyThreshold   float64 `json:"status_unhealthy_threshold"`
	StatusUnavailableThreshold float64 `json:"status_unavailable_threshold"`
	P95HealthyThresholdMs      int     `json:"p95_healthy_threshold_ms"`
	P95WarningThresholdMs      int     `json:"p95_warning_threshold_ms"`
}

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled:     false,
	AutoTestChannelMinutes:     10,
	StatusWarningThreshold:     95,
	StatusUnhealthyThreshold:   90,
	StatusUnavailableThreshold: 75,
	P95HealthyThresholdMs:      2000,
	P95WarningThresholdMs:      5000,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

func GetMonitorSetting() *MonitorSetting {
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err == nil && frequency > 0 {
			monitorSetting.AutoTestChannelEnabled = true
			monitorSetting.AutoTestChannelMinutes = float64(frequency)
		}
	}
	return &monitorSetting
}
