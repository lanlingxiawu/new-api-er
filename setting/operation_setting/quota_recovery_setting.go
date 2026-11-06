package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type QuotaRecoverySetting struct {
	Enabled bool `json:"enabled"`
}

var quotaRecoverySetting = QuotaRecoverySetting{Enabled: true}
var quotaRecoverySnapshot config.Snapshot[QuotaRecoverySetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("quota_recovery_setting", &quotaRecoverySetting, publishQuotaRecoverySetting)
}

func publishQuotaRecoverySetting() {
	quotaRecoverySnapshot.Publish(quotaRecoverySetting)
}

func QuotaRecoveryEnabled() bool {
	return quotaRecoverySnapshot.Load().Enabled
}
