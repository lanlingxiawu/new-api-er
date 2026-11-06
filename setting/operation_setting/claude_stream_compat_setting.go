package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// ClaudeStreamCompatSetting controls the upstream-compatible tool and EOF
// handling without disabling stream failure detection or diagnostics.
type ClaudeStreamCompatSetting struct {
	Enabled bool `json:"enabled"`
}

var claudeStreamCompatSetting = ClaudeStreamCompatSetting{Enabled: true}
var claudeStreamCompatSnapshot config.Snapshot[ClaudeStreamCompatSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("claude_stream_compat_setting", &claudeStreamCompatSetting, publishClaudeStreamCompatSetting)
}

func publishClaudeStreamCompatSetting() {
	claudeStreamCompatSnapshot.Publish(claudeStreamCompatSetting)
}

// ClaudeStreamUpstreamCompatibilityEnabled returns a lock-free configuration
// snapshot. Each stream captures this value before processing any events.
func ClaudeStreamUpstreamCompatibilityEnabled() bool {
	return claudeStreamCompatSnapshot.Load().Enabled
}
