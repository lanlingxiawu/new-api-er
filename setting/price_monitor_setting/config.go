package price_monitor_setting

import "github.com/QuantumNous/new-api/setting/config"

const (
	defaultIntervalMinutes = 360
	minimumIntervalMinutes = 5
	defaultTimeoutSeconds  = 10
	maximumTimeoutSeconds  = 120
)

type PriceMonitorSetting struct {
	Enabled          bool   `json:"enabled"`
	IntervalMinutes  int    `json:"interval_minutes"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	IncludeOfficial  bool   `json:"include_official"`
	IncludeModelsDev bool   `json:"include_models_dev"`
	ModelWhitelist   string `json:"model_whitelist"`
	// CustomEndpoints 是人工指定的渠道价格接口：key 为渠道 ID，value 为端点路径或完整地址。
	// 没有配置的渠道由巡检自动按 /api/pricing、/api/ratio_config 顺序探测。
	CustomEndpoints map[string]string `json:"custom_endpoints"`
}

var (
	priceMonitorSetting = PriceMonitorSetting{
		Enabled:          false,
		IntervalMinutes:  defaultIntervalMinutes,
		TimeoutSeconds:   defaultTimeoutSeconds,
		IncludeOfficial:  true,
		IncludeModelsDev: false,
	}
	priceMonitorSnapshot config.Snapshot[PriceMonitorSetting]
)

func init() {
	config.GlobalConfig.RegisterSnapshot("price_monitor_setting", &priceMonitorSetting, publishPriceMonitorSetting)
}

func publishPriceMonitorSetting() {
	priceMonitorSnapshot.Publish(priceMonitorSetting)
}

func GetPriceMonitorSetting() *PriceMonitorSetting {
	return priceMonitorSnapshot.Load()
}

func (setting PriceMonitorSetting) Normalized() PriceMonitorSetting {
	setting.IncludeOfficial = true
	if setting.IntervalMinutes < minimumIntervalMinutes {
		setting.IntervalMinutes = minimumIntervalMinutes
	}
	if setting.TimeoutSeconds <= 0 {
		setting.TimeoutSeconds = defaultTimeoutSeconds
	} else if setting.TimeoutSeconds > maximumTimeoutSeconds {
		setting.TimeoutSeconds = maximumTimeoutSeconds
	}
	setting.CustomEndpoints = normalizeCustomEndpoints(setting.CustomEndpoints)
	return setting
}
