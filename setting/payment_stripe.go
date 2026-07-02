package setting

// StripeEnabled 控制是否在充值页展示并受理 Stripe 支付选项（独立开关）。
// 默认 true 以兼容既有部署（此前只要配置了密钥即展示）；关闭后前端隐藏 Stripe、后端拒绝新的下单，
// 但已创建并付款的订单仍能正常入账——webhook 只依赖签名密钥（见 isStripeWebhookEnabled），不受此开关影响。
var StripeEnabled = true

var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""

// StripeUnitPrice 是「手动汇率（元/美金）」——USD→CNY 汇率：1 美元折算多少人民币。
// 注意它不是"充值价格"，与系统充值比例 operation_setting.Price 是两回事（后者才是到账公式的分母）。
// 当 StripeUseRealtimeRate=false（手动模式）时直接使用它；
// 当 StripeUseRealtimeRate=true（实时模式）时，它仅作为实时汇率获取失败时的兜底值。
var StripeUnitPrice = 8.0

// StripeUseRealtimeRate 控制到账折算用的 USD→CNY 汇率取值方式：
// true=使用实时 USD/CNY 汇率（获取失败回退到手动汇率 StripeUnitPrice）；false=始终使用手动汇率 StripeUnitPrice。
var StripeUseRealtimeRate = true

var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false
