package setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

var (
	InfiniEnabled       bool
	InfiniApiKey        string  // keyId，用于请求签名
	InfiniApiSecret     string  // HMAC-SHA256 请求签名密钥
	InfiniWebhookSecret string  // HMAC-SHA256 Webhook 验签密钥
	InfiniSandbox       bool
	InfiniNotifyUrl     string  // Webhook 回调地址（空则自动推导）
	InfiniReturnUrl     string  // 支付成功后跳转
	InfiniFailUrl       string  // 支付失败后跳转
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

)

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
