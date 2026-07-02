package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func isPaymentComplianceConfirmed() bool {
	return operation_setting.IsPaymentComplianceConfirmed()
}

func isStripeTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	// StripeEnabled 为管理员的独立显示/受理开关；关闭即隐藏 Stripe 支付选项。
	if !setting.StripeEnabled {
		return false
	}
	// 充值下单已改用动态 price_data（见 genStripeLink），不再依赖 StripePriceId；
	// 启用只需 API 密钥（拉起会话）+ Webhook 签名密钥（回调入账）。
	return strings.TrimSpace(setting.StripeApiSecret) != "" &&
		strings.TrimSpace(setting.StripeWebhookSecret) != ""
}

func isStripeWebhookConfigured() bool {
	return strings.TrimSpace(setting.StripeWebhookSecret) != ""
}

// isStripeWebhookEnabled 仅依赖 Webhook 签名密钥，与充值展示/合规/API 密钥等配置解耦。
// 原因：已创建并付款成功的 Checkout Session 必须能入账；若把回调入口和这些运行时可变的开关绑定，
// 管理员一旦改动配置就会让在途订单在回调处被 403 拒收，造成「用户已付款但系统不入账」。
// 真正的安全边界是下游的 Stripe 签名校验（依赖 StripeWebhookSecret）。
func isStripeWebhookEnabled() bool {
	return isStripeWebhookConfigured()
}

func isCreemTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	products := strings.TrimSpace(setting.CreemProducts)
	return strings.TrimSpace(setting.CreemApiKey) != "" &&
		products != "" &&
		products != "[]"
}

func isCreemWebhookConfigured() bool {
	return strings.TrimSpace(setting.CreemWebhookSecret) != ""
}

func isCreemWebhookEnabled() bool {
	return isCreemTopUpEnabled() && isCreemWebhookConfigured()
}

func isWaffoTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	if !setting.WaffoEnabled {
		return false
	}

	return isWaffoWebhookConfigured()
}

func isWaffoWebhookConfigured() bool {
	if setting.WaffoSandbox {
		return strings.TrimSpace(setting.WaffoSandboxApiKey) != "" &&
			strings.TrimSpace(setting.WaffoSandboxPrivateKey) != "" &&
			strings.TrimSpace(setting.WaffoSandboxPublicCert) != ""
	}

	return strings.TrimSpace(setting.WaffoApiKey) != "" &&
		strings.TrimSpace(setting.WaffoPrivateKey) != "" &&
		strings.TrimSpace(setting.WaffoPublicCert) != ""
}

func isWaffoWebhookEnabled() bool {
	return isWaffoTopUpEnabled()
}

func isWaffoPancakeTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	// Presence-of-credentials = enabled. Webhook public keys ship inside
	// the SDK; mode (test/prod) is read from each event.
	return strings.TrimSpace(setting.WaffoPancakeMerchantID) != "" &&
		strings.TrimSpace(setting.WaffoPancakePrivateKey) != "" &&
		strings.TrimSpace(setting.WaffoPancakeProductID) != ""
}

func isWaffoPancakeWebhookConfigured() bool {
	return isWaffoPancakeTopUpEnabled()
}

func isWaffoPancakeWebhookEnabled() bool {
	return isWaffoPancakeTopUpEnabled()
}

func isEpayTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	return isEpayWebhookConfigured() && len(operation_setting.PayMethods) > 0
}

func isAlipayTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	if !setting.AlipayEnabled {
		return false
	}
	return isAlipayWebhookConfigured()
}

func isAlipayWebhookConfigured() bool {
	return strings.TrimSpace(setting.AlipayAppId) != "" &&
		strings.TrimSpace(setting.AlipayPrivateKey) != "" &&
		strings.TrimSpace(setting.AlipayPublicKey) != ""
}

func isAlipayWebhookEnabled() bool {
	return isAlipayTopUpEnabled()
}

func isWechatTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	if !setting.WechatEnabled {
		return false
	}
	if strings.TrimSpace(setting.WechatAppId) == "" {
		return false
	}
	return isWechatWebhookConfigured()
}

func isWechatWebhookConfigured() bool {
	return strings.TrimSpace(setting.WechatMchId) != "" &&
		strings.TrimSpace(setting.WechatApiV3Key) != "" &&
		strings.TrimSpace(setting.WechatMchPrivateKey) != "" &&
		strings.TrimSpace(setting.WechatMchCertSerialNo) != ""
}

func isWechatWebhookEnabled() bool {
	return isWechatTopUpEnabled()
}

func isEpayWebhookConfigured() bool {
	return strings.TrimSpace(operation_setting.PayAddress) != "" &&
		strings.TrimSpace(operation_setting.EpayId) != "" &&
		strings.TrimSpace(operation_setting.EpayKey) != ""
}

func isEpayWebhookEnabled() bool {
	return isEpayTopUpEnabled()
}

func isInfiniTopUpEnabled() bool {
	if !isPaymentComplianceConfirmed() {
		return false
	}
	if !setting.InfiniEnabled {
		return false
	}
	return strings.TrimSpace(setting.InfiniApiKey) != "" &&
		strings.TrimSpace(setting.InfiniApiSecret) != "" &&
		strings.TrimSpace(setting.InfiniWebhookSecret) != ""
}

func isInfiniWebhookEnabled() bool {
	return isInfiniTopUpEnabled()
}
