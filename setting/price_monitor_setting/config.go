package price_monitor_setting

import "github.com/QuantumNous/new-api/setting/config"

const (
	defaultIntervalMinutes = 360
	MinIntervalMinutes     = 5
	// MaxIntervalMinutes 是 30 天。巡检间隔同时决定分享密码的有效期（checkedAt + 间隔），
	// 不设上限时超大值会让有效期计算溢出成过去的时间。
	MaxIntervalMinutes    = 43200
	defaultTimeoutSeconds = 10
	MinTimeoutSeconds     = 1
	MaxTimeoutSeconds     = 120

	// 成本系数核对（docs/design/price-monitor-channel-cost-check.md §8）。
	// 官方 /api/log/token 挂 CriticalRateLimit（同一 IP 默认 20 分钟 20 次），上限留出余量。
	defaultUpstreamLogQueriesPerHost = 8
	MaxUpstreamLogQueriesPerHost     = 20
	DefaultUpstreamRatioRefreshHours = 6
	MaxUpstreamRatioRefreshHours     = 168
	DefaultUpstreamRatioMaxAgeDays   = 7
	MaxUpstreamRatioMaxAgeDays       = 30
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
	// UpstreamLogQueriesPerHost 是每个上游主机每轮最多用日志补查几个渠道；0 表示不用日志。
	UpstreamLogQueriesPerHost int `json:"upstream_log_queries_per_host"`
	// UpstreamRatioRefreshHours 是同一渠道的上游分组倍率多久重新获取一次。
	UpstreamRatioRefreshHours int `json:"upstream_ratio_refresh_hours"`
	// UpstreamRatioMaxAgeDays 是上一次取到的上游分组倍率最多沿用多久。
	UpstreamRatioMaxAgeDays int `json:"upstream_ratio_max_age_days"`
}

func defaultPriceMonitorSetting() PriceMonitorSetting {
	return PriceMonitorSetting{
		Enabled:                   false,
		IntervalMinutes:           defaultIntervalMinutes,
		TimeoutSeconds:            defaultTimeoutSeconds,
		IncludeOfficial:           true,
		IncludeModelsDev:          false,
		UpstreamLogQueriesPerHost: defaultUpstreamLogQueriesPerHost,
		UpstreamRatioRefreshHours: DefaultUpstreamRatioRefreshHours,
		UpstreamRatioMaxAgeDays:   DefaultUpstreamRatioMaxAgeDays,
	}
}

var (
	priceMonitorSetting  = defaultPriceMonitorSetting()
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

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (setting PriceMonitorSetting) Normalized() PriceMonitorSetting {
	setting.IncludeOfficial = true
	setting.IntervalMinutes = clampInt(setting.IntervalMinutes, MinIntervalMinutes, MaxIntervalMinutes)
	if setting.TimeoutSeconds <= 0 {
		setting.TimeoutSeconds = defaultTimeoutSeconds
	} else if setting.TimeoutSeconds > MaxTimeoutSeconds {
		setting.TimeoutSeconds = MaxTimeoutSeconds
	}
	setting.CustomEndpoints = normalizeCustomEndpoints(setting.CustomEndpoints)
	// 日志次数为 0 有意义（不用日志），只做区间裁剪；两个时长为 0 没有意义，回退默认值。
	setting.UpstreamLogQueriesPerHost = clampInt(setting.UpstreamLogQueriesPerHost, 0, MaxUpstreamLogQueriesPerHost)
	if setting.UpstreamRatioRefreshHours <= 0 {
		setting.UpstreamRatioRefreshHours = DefaultUpstreamRatioRefreshHours
	}
	setting.UpstreamRatioRefreshHours = clampInt(setting.UpstreamRatioRefreshHours, 1, MaxUpstreamRatioRefreshHours)
	if setting.UpstreamRatioMaxAgeDays <= 0 {
		setting.UpstreamRatioMaxAgeDays = DefaultUpstreamRatioMaxAgeDays
	}
	setting.UpstreamRatioMaxAgeDays = clampInt(setting.UpstreamRatioMaxAgeDays, 1, MaxUpstreamRatioMaxAgeDays)
	return setting
}
