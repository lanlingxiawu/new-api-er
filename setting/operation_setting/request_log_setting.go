package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type RequestLogSetting struct {
	ClearOnRestart bool `json:"clear_on_restart"`
}

var requestLogSetting RequestLogSetting

func init() {
	config.GlobalConfig.Register("request_log_setting", &requestLogSetting)
}

func GetRequestLogSetting() RequestLogSetting {
	var snapshot RequestLogSetting
	config.WithConfigDraft(func() { snapshot = requestLogSetting })
	return snapshot
}
