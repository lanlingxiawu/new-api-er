package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type ClaudeStreamValidationSetting struct {
	Enabled bool `json:"enabled"`
}

var claudeStreamValidationSetting = ClaudeStreamValidationSetting{Enabled: true}
var claudeStreamValidationSnapshot config.Snapshot[ClaudeStreamValidationSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("claude_stream_validation_setting", &claudeStreamValidationSetting, publishClaudeStreamValidationSetting)
}

func publishClaudeStreamValidationSetting() {
	claudeStreamValidationSnapshot.Publish(claudeStreamValidationSetting)
	publishStreamValidationDefaults()
}

func ClaudeStreamValidationEnabled() bool {
	return claudeStreamValidationSnapshot.Load().Enabled
}
