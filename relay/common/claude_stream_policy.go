package common

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// FreezeClaudeStreamValidation resolves only cached settings, once per attempt.
// Copy the boolean rather than retaining a pointer owned by a shared cache.
func (info *RelayInfo) FreezeClaudeStreamValidation(channel dto.ChannelSettings) {
	info.FreezeStreamValidation(channel)
}

func (info *RelayInfo) ClaudeStreamValidationEnabled() bool {
	_ = info.StreamValidationEnabled()
	return info.claudeValidationPolicy.Enabled
}
