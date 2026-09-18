package setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

var (
	InfiniEnabled       bool
	InfiniApiKey        string // keyId，用于请求签名
	InfiniApiSecret     string // HMAC-SHA256 请求签名密钥
	InfiniWebhookSecret string // HMAC-SHA256 Webhook 验签密钥
	InfiniSandbox       bool
	InfiniNotifyUrl     string        // Webhook 回调地址（空则自动推导）
	InfiniReturnUrl     string        // 支付成功后跳转
	InfiniFailUrl       string        // 支付失败后跳转
	InfiniUnitPrice     float64 = 1.0 // 单币种模式兼容字段，多币种时不使用
	InfiniMinTopUp      int     = 1   // 单币种模式兼容字段，多币种时不使用
	InfiniCurrency      string        // 单币种模式兼容字段，默认 USD
	// InfiniCurrencies 存储管理员配置的多币种列表（JSON 数组）
	// 设置后覆盖单币种模式的 InfiniCurrency/InfiniUnitPrice/InfiniMinTopUp
	InfiniCurrencies string
	// InfiniPayMethods 限定 Infini 托管结账页显示的支付方式（JSON 数组）
	// 1=加密货币, 2=银行卡, 3=Binance Pay, 5=Apple Pay, 6=Google Pay
	// 为空则使用商户在 Infini 控制台配置的默认值
	InfiniPayMethods string
	// InfiniUseRealtimeRate 控制到账折算用的 USD→CNY 汇率取值方式：
	// true=使用实时 USD/CNY 汇率（获取失败回退到手动汇率 InfiniExchangeRate）；false=始终使用手动汇率。
	InfiniUseRealtimeRate = true
	// InfiniExchangeRate 是「手动汇率（元/美金）」——USD→CNY：1 美元折算多少人民币。
	// 到账额度 = 实付美元 × 汇率 ÷ 系统充值比例(Price) × QuotaPerUnit。
	// 手动模式下直接使用它；实时模式下仅作为实时汇率获取失败时的兜底值。
	InfiniExchangeRate = 8.0
)

// InfiniSupportedCurrency 是 Infini 充值唯一受支持的结算币种。
//
// 到账换算公式只在实付金额为美元时成立（rawQuotaFromPayMoney 用 USD→CNY 汇率折算），
// 若放行其它币种，按该币种单价收款却按美元口径发放额度会造成成倍超发/少发，
// 且 webhook 只比对同币种金额、无法发现错额。因此币种在下单、报价与配置保存三处都限定为 USD。
const InfiniSupportedCurrency = "USD"

// IsInfiniCurrencySupported 判断币种代码是否受支持（大小写与空白不敏感）。
func IsInfiniCurrencySupported(currency string) bool {
	return strings.EqualFold(strings.TrimSpace(currency), InfiniSupportedCurrency)
}

// UnsupportedInfiniCurrency 校验单币种兼容字段，返回不受支持的币种代码；
// 返回空串表示配置合法（空值视为合法，运行时会退回默认的 USD）。
func UnsupportedInfiniCurrency(currency string) string {
	trimmed := strings.TrimSpace(currency)
	if trimmed == "" || IsInfiniCurrencySupported(trimmed) {
		return ""
	}
	return strings.ToUpper(trimmed)
}

// UnsupportedInfiniCurrencyInJSON 校验多币种 JSON 配置。
// 返回第一个不受支持的币种代码；JSON 无法解析时返回 error。
// 空串与空数组视为「未配置多币种」，合法。
func UnsupportedInfiniCurrencyInJSON(jsonStr string) (string, error) {
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" || trimmed == "[]" {
		return "", nil
	}
	var opts []constant.InfiniCurrencyOption
	if err := common.UnmarshalJsonStr(trimmed, &opts); err != nil {
		return "", err
	}
	for _, opt := range opts {
		if unsupported := UnsupportedInfiniCurrency(opt.Currency); unsupported != "" {
			return unsupported, nil
		}
	}
	return "", nil
}

// GetSupportedInfiniCurrencyOptions 返回只含受支持币种的配置列表，供对用户展示的接口使用，
// 避免把下单时必然被拒的币种暴露到充值页。
func GetSupportedInfiniCurrencyOptions() []constant.InfiniCurrencyOption {
	opts := GetInfiniCurrencyOptions()
	supported := make([]constant.InfiniCurrencyOption, 0, len(opts))
	for _, opt := range opts {
		if IsInfiniCurrencySupported(opt.Currency) {
			supported = append(supported, opt)
		}
	}
	return supported
}

func GetInfiniBaseUrl() string {
	if InfiniSandbox {
		return "https://openapi-sandbox.infini.money"
	}
	return "https://openapi.infini.money"
}

// GetInfiniCurrencyOptions 返回当前有效的多币种配置列表。
// 优先使用 InfiniCurrencies（JSON 数组）；未配置时退回到单币种兼容字段。
func GetInfiniCurrencyOptions() []constant.InfiniCurrencyOption {
	common.OptionMapRWMutex.RLock()
	jsonStr := common.OptionMap["InfiniCurrencies"]
	common.OptionMapRWMutex.RUnlock()

	if jsonStr != "" && jsonStr != "[]" {
		var opts []constant.InfiniCurrencyOption
		if err := common.UnmarshalJsonStr(jsonStr, &opts); err == nil && len(opts) > 0 {
			return opts
		}
	}

	// 退回单币种兼容模式
	currency := InfiniCurrency
	if currency == "" {
		currency = "USD"
	}
	unitPrice := InfiniUnitPrice
	if unitPrice <= 0 {
		unitPrice = 1.0
	}
	minTopUp := InfiniMinTopUp
	if minTopUp < 1 {
		minTopUp = 1
	}
	return []constant.InfiniCurrencyOption{
		{Currency: currency, UnitPrice: unitPrice, MinTopUp: minTopUp},
	}
}

// SetInfiniCurrencies 序列化并写入 OptionMap
func SetInfiniCurrencies(opts []constant.InfiniCurrencyOption) error {
	jsonBytes, err := common.Marshal(opts)
	if err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap["InfiniCurrencies"] = string(jsonBytes)
	common.OptionMapRWMutex.Unlock()
	return nil
}

// InfiniCurrencies2JsonString 供 InitOptionMap 使用
func InfiniCurrencies2JsonString() string {
	jsonBytes, err := common.Marshal(constant.DefaultInfiniCurrencyOptions)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}
