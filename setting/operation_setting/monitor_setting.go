package operation_setting

import (
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool    `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes float64 `json:"auto_test_channel_minutes"`

	// ModelHealthCheck controls the scheduled group-model availability probe.
	// Enabled: master switch (independent of the scheduler task's own enabled flag).
	// Workers: max concurrent active probes per run (1–20).
	// IntervalMs: pause between probe completions in milliseconds (0–5000).
	ModelHealthCheckEnabled    bool `json:"model_health_check_enabled"`
	ModelHealthCheckWorkers    int  `json:"model_health_check_workers"`
	ModelHealthCheckIntervalMs int  `json:"model_health_check_interval_ms"`
}

var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled:     false,
	AutoTestChannelMinutes:     10,
	ModelHealthCheckEnabled:    true,
	ModelHealthCheckWorkers:    3,
	ModelHealthCheckIntervalMs: 200,
}

func init() {
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
	// Clamp ModelHealthCheckWorkers to [1, 20] so a bad config value never
	// deadlocks the probe pool or floods upstreams.
	if monitorSetting.ModelHealthCheckWorkers < 1 {
		monitorSetting.ModelHealthCheckWorkers = 1
	} else if monitorSetting.ModelHealthCheckWorkers > 20 {
		monitorSetting.ModelHealthCheckWorkers = 20
	}
	// Clamp ModelHealthCheckIntervalMs to [0, 5000].
	if monitorSetting.ModelHealthCheckIntervalMs < 0 {
		monitorSetting.ModelHealthCheckIntervalMs = 0
	} else if monitorSetting.ModelHealthCheckIntervalMs > 5000 {
		monitorSetting.ModelHealthCheckIntervalMs = 5000
	}
	return &monitorSetting
}
