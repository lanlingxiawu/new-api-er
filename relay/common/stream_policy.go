package common

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type StreamProcessingOwner string

const (
	StreamOwnerGeneral       StreamProcessingOwner = "general_strict"
	StreamOwnerClaude        StreamProcessingOwner = "claude_strict"
	StreamOwnerRealtime      StreamProcessingOwner = "realtime_strict"
	StreamOwnerMain          StreamProcessingOwner = "main"
	StreamOwnerNotApplicable StreamProcessingOwner = "not_applicable"
)

// StreamValidationPolicy is an immutable attempt value, not a session's state.
type StreamValidationPolicy struct {
	Enabled    bool   `json:"enabled"`
	Source     string `json:"source"`
	Origin     string `json:"config_origin"`
	Applicable *bool  `json:"applicable,omitempty"`
}

func (info *RelayInfo) FreezeStreamValidation(channel dto.ChannelSettings) {
	defaults := operation_setting.GetStreamValidationDefaults()
	origin := "default"
	if defaults.Explicit {
		origin = "canonical"
	}
	policy := StreamValidationPolicy{Enabled: defaults.Enabled, Source: "global", Origin: origin}
	claude := policy
	if !defaults.Explicit {
		claude.Enabled, claude.Origin = defaults.ClaudeEnabled, "legacy_claude"
	}
	if channel.StreamProtocolValidation != nil {
		policy = StreamValidationPolicy{Enabled: *channel.StreamProtocolValidation, Source: "channel", Origin: "canonical"}
		claude = policy
	} else if channel.ClaudeStreamValidation != nil {
		claude = StreamValidationPolicy{Enabled: *channel.ClaudeStreamValidation, Source: "channel", Origin: "legacy_claude"}
	}
	if info.UserSetting.StreamProtocolValidation != nil {
		policy = StreamValidationPolicy{Enabled: *info.UserSetting.StreamProtocolValidation, Source: "user", Origin: "canonical"}
		claude = policy
	} else if info.UserSetting.ClaudeStreamValidation != nil {
		claude = StreamValidationPolicy{Enabled: *info.UserSetting.ClaudeStreamValidation, Source: "user", Origin: "legacy_claude"}
	}
	info.StreamValidationPolicy, info.claudeValidationPolicy = policy, claude
	info.StreamProcessingOwner = StreamOwnerGeneral
	if !policy.Enabled {
		info.StreamProcessingOwner = StreamOwnerMain
	}
	info.streamPolicyFrozen = true
	info.ClaudeStreamValidationSource = claude.Source
	info.ClaudeStreamPolicyApplied, info.ClaudeStreamMainMode = false, false
}

// StreamValidationEnabled is usable before the upstream handshake, unlike Active.
func (info *RelayInfo) StreamValidationEnabled() bool {
	if !info.streamPolicyFrozen {
		channel := dto.ChannelSettings{}
		if info.ChannelMeta != nil {
			channel = info.ChannelSetting
		}
		info.FreezeStreamValidation(channel)
	}
	return info.StreamValidationPolicy.Enabled
}

// MainStreamMode identifies compatibility policy without mutating attempt state.
func (info *RelayInfo) MainStreamMode() bool {
	if info.StreamProcessingOwner == StreamOwnerNotApplicable {
		return false
	}
	return info.StreamProcessingOwner == StreamOwnerMain || info.ClaudeStreamMainMode
}

// NeedsSemanticWriter allows a historical Claude override to select its strict
// handler later. Fully disabled attempts install only the passive writer.
func (info *RelayInfo) NeedsSemanticWriter() bool {
	return info.StreamValidationPolicy.Enabled || info.claudeValidationPolicy.Enabled
}

// ApplyClaudeStreamPolicy is only for handlers covered by the historical key.
// SDK Claude events do not call it and therefore retain the global protocol policy.
func (info *RelayInfo) ApplyClaudeStreamPolicy() {
	_ = info.StreamValidationEnabled()
	info.StreamValidationPolicy = info.claudeValidationPolicy
	info.StreamProcessingOwner = StreamOwnerGeneral
	if !info.StreamValidationPolicy.Enabled {
		info.StreamProcessingOwner = StreamOwnerMain
	}
	info.StreamSession.SetValidationEnabled(info.StreamValidationPolicy.Enabled)
	info.StreamDiagnostic.SetValidationPolicy(info.StreamValidationPolicy)
	if info.StreamDiagnostic != nil {
		info.StreamDiagnostic.PassiveStatus = nil
		if !info.StreamValidationPolicy.Enabled {
			info.StreamDiagnostic.PassiveStatus = info.StreamStatus
		}
	}
}
