package constant

// InfiniCurrencyOption 定义 Infini 支持的单个结算币种及其换算参数
type InfiniCurrencyOption struct {
	Currency  string  `json:"currency"`   // 大写币种代码，如 USD、EUR、JPY
	UnitPrice float64 `json:"unit_price"` // 每单位额度对应的该币种金额
	MinTopUp  int     `json:"min_topup"`  // 该币种下的最低充值量
}

// DefaultInfiniCurrencyOptions 未配置多币种时的默认值（等同于旧单币种行为）
var DefaultInfiniCurrencyOptions = []InfiniCurrencyOption{
	{Currency: "USD", UnitPrice: 1.0, MinTopUp: 1},
}

// InfiniZeroDecimalCurrencies Infini 支持的零小数位币种（金额须为整数）
var InfiniZeroDecimalCurrencies = map[string]bool{
	"JPY": true,
	"KRW": true,
}
