package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type PaymentSetting struct {
	AmountOptions  []int           `json:"amount_options"`
	AmountDiscount map[int]float64 `json:"amount_discount"` // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠

	// UserExportMaxRows 普通用户单次导出充值账单的最大行数（管理员导出不受此限制）。
	// <=0 时回退到 DefaultUserExportMaxRows。
	UserExportMaxRows int `json:"user_export_max_rows"`

	ComplianceConfirmed    bool   `json:"compliance_confirmed"`
	ComplianceTermsVersion string `json:"compliance_terms_version"`
	ComplianceConfirmedAt  int64  `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy  int    `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP  string `json:"compliance_confirmed_ip"`
}

const CurrentComplianceTermsVersion = "v1"

// DefaultUserExportMaxRows 普通用户导出充值账单的默认最大行数。
const DefaultUserExportMaxRows = 10000

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:     []int{10, 20, 50, 100, 200, 500},
	AmountDiscount:    map[int]float64{},
	UserExportMaxRows: DefaultUserExportMaxRows,
}

// GetUserExportMaxRows 返回普通用户导出最大行数，非法值回退默认。
func (s *PaymentSetting) GetUserExportMaxRows() int {
	if s.UserExportMaxRows <= 0 {
		return DefaultUserExportMaxRows
	}
	return s.UserExportMaxRows
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}
