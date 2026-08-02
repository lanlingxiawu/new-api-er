package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// 额度展示类型
const (
	QuotaDisplayTypeUSD    = "USD"
	QuotaDisplayTypeCNY    = "CNY"
	QuotaDisplayTypeTokens = "TOKENS"
	QuotaDisplayTypeCustom = "CUSTOM"
)

type GeneralSetting struct {
	DocsLink            string `json:"docs_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// 当前站点额度展示类型：USD / CNY / TOKENS
	QuotaDisplayType string `json:"quota_display_type"`
	// 自定义货币符号，用于 CUSTOM 展示类型
	CustomCurrencySymbol string `json:"custom_currency_symbol"`
	// 自定义货币与美元汇率（1 USD = X Custom）
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:                   "https://docs.newapi.pro",
	PingIntervalEnabled:        false,
	PingIntervalSeconds:        60,
	QuotaDisplayType:           QuotaDisplayTypeUSD,
	CustomCurrencySymbol:       "¤",
	CustomCurrencyExchangeRate: 1.0,
}

var generalSettingSnapshot config.Snapshot[GeneralSetting]

func init() {
	// 注册到全局配置管理器，并登记快照发布函数。
	// 这个模块含 string 字段且被 relay 路径读取，原地改写时读侧可能拿到半个值。
	config.GlobalConfig.RegisterSnapshot("general_setting", &generalSetting, publishGeneralSetting)
}

// publishGeneralSetting 只允许在配置草稿锁内调用（由 RegisterSnapshot 保证）。
func publishGeneralSetting() { generalSettingSnapshot.Publish(generalSetting) }

// GetGeneralSetting 返回不可变快照。通过它写入不会生效——
// 配置变更必须走管理接口，由 ConfigManager 改草稿后重新发布。
func GetGeneralSetting() *GeneralSetting {
	return generalSettingSnapshot.Load()
}

// ReplaceGeneralSetting 整体替换配置并立即重新发布快照。
// 供需要在运行时改这份配置的调用方使用（目前只有测试）。
func ReplaceGeneralSetting(s GeneralSetting) {
	config.WithConfigDraft(func() {
		generalSetting = s
		publishGeneralSetting()
	})
}

// IsCurrencyDisplay 是否以货币形式展示（美元或人民币）
func IsCurrencyDisplay() bool {
	return GetGeneralSetting().QuotaDisplayType != QuotaDisplayTypeTokens
}

// IsCNYDisplay 是否以人民币展示
func IsCNYDisplay() bool {
	return GetGeneralSetting().QuotaDisplayType == QuotaDisplayTypeCNY
}

// GetQuotaDisplayType 返回额度展示类型
func GetQuotaDisplayType() string {
	return GetGeneralSetting().QuotaDisplayType
}

// GetCurrencySymbol 返回当前展示类型对应符号
func GetCurrencySymbol() string {
	switch GetGeneralSetting().QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return "$"
	case QuotaDisplayTypeCNY:
		return "¥"
	case QuotaDisplayTypeCustom:
		if GetGeneralSetting().CustomCurrencySymbol != "" {
			return GetGeneralSetting().CustomCurrencySymbol
		}
		return "¤"
	default:
		return ""
	}
}

// GetUsdToCurrencyRate 返回 1 USD = X <currency> 的 X（TOKENS 不适用）
func GetUsdToCurrencyRate(usdToCny float64) float64 {
	switch GetGeneralSetting().QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return 1
	case QuotaDisplayTypeCNY:
		return usdToCny
	case QuotaDisplayTypeCustom:
		if GetGeneralSetting().CustomCurrencyExchangeRate > 0 {
			return GetGeneralSetting().CustomCurrencyExchangeRate
		}
		return 1
	default:
		return 1
	}
}
