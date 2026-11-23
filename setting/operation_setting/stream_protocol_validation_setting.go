package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// A nil draft value preserves the scope of pre-existing Claude-only settings.
// Public writes accept booleans; absence means the new global default is true.
type StreamProtocolValidationSetting struct {
	Enabled *bool `json:"enabled"`
}

type StreamValidationDefaults struct {
	Enabled       bool
	Explicit      bool
	ClaudeEnabled bool
}

var streamProtocolValidationSetting StreamProtocolValidationSetting
var streamValidationDefaults config.Snapshot[StreamValidationDefaults]

func init() {
	config.GlobalConfig.RegisterSnapshot("stream_protocol_validation_setting", &streamProtocolValidationSetting, publishStreamValidationDefaults)
}

func publishStreamValidationDefaults() {
	value := StreamValidationDefaults{Enabled: true, ClaudeEnabled: claudeStreamValidationSetting.Enabled}
	if streamProtocolValidationSetting.Enabled != nil {
		value.Enabled = *streamProtocolValidationSetting.Enabled
		value.ClaudeEnabled = value.Enabled
		value.Explicit = true
	}
	streamValidationDefaults.Publish(value)
}

func GetStreamValidationDefaults() StreamValidationDefaults {
	return *streamValidationDefaults.Load()
}

// ReplaceStreamValidationDefaults publishes a complete database/config update
// under the draft lock. Readers see either complete policy, never a mixed pair.
func ReplaceStreamValidationDefaults(canonical *bool, legacy bool) {
	config.WithConfigDraft(func() {
		streamProtocolValidationSetting.Enabled = nil
		if canonical != nil {
			value := *canonical
			streamProtocolValidationSetting.Enabled = &value
		}
		claudeStreamValidationSetting.Enabled = legacy
		publishClaudeStreamValidationSetting()
	})
}
