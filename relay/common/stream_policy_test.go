package common

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestStreamPolicyPrecedenceAndLegacyScope(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_protocol_validation_setting", map[string]string{"enabled": "null"}))
	})
	on, off := true, false
	for _, global := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_protocol_validation_setting", map[string]string{"enabled": fmt.Sprint(global)}))
		for ci, channel := range []*bool{nil, &on, &off} {
			for ui, user := range []*bool{nil, &on, &off} {
				t.Run(fmt.Sprintf("global=%t/channel=%d/user=%d", global, ci, ui), func(t *testing.T) {
					info := &RelayInfo{UserSetting: dto.UserSetting{StreamProtocolValidation: user}}
					info.FreezeStreamValidation(dto.ChannelSettings{StreamProtocolValidation: channel})
					want, source := global, "global"
					if channel != nil {
						want, source = *channel, "channel"
					}
					if user != nil {
						want, source = *user, "user"
					}
					require.Equal(t, want, info.StreamValidationPolicy.Enabled)
					require.Equal(t, source, info.StreamValidationPolicy.Source)
					require.Equal(t, want, info.ClaudeStreamValidationEnabled())
				})
			}
		}
	}
	info := &RelayInfo{UserSetting: dto.UserSetting{ClaudeStreamValidation: &on}}
	info.FreezeStreamValidation(dto.ChannelSettings{StreamProtocolValidation: &off})
	require.False(t, info.StreamValidationPolicy.Enabled, "legacy Claude user setting cannot enable unrelated protocols")
	require.True(t, info.ClaudeStreamValidationEnabled(), "legacy user override still wins within its original scope")
	info.UserSetting.StreamProtocolValidation = &off
	info.FreezeStreamValidation(dto.ChannelSettings{})
	require.False(t, info.ClaudeStreamValidationEnabled(), "canonical user value replaces legacy user override")
	off = true
	require.False(t, info.StreamValidationPolicy.Enabled, "attempt snapshot must copy the value")
}

func TestStreamPolicyReadOrderAndRetry(t *testing.T) {
	for _, claudeFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(claudeFirst), func(t *testing.T) {
			on, off := true, false
			info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelSetting: dto.ChannelSettings{
				StreamProtocolValidation: &off, ClaudeStreamValidation: &on,
			}}}
			if claudeFirst {
				require.False(t, info.ClaudeStreamValidationEnabled())
			}
			require.False(t, info.StreamValidationEnabled())
			require.True(t, info.MainStreamMode())
			off = true
			require.False(t, info.ClaudeStreamValidationEnabled(), "both readers share the attempt snapshot")
			info.FreezeStreamValidation(dto.ChannelSettings{StreamProtocolValidation: &on})
			require.True(t, info.ClaudeStreamValidationEnabled())
			require.False(t, info.MainStreamMode(), "retry resolves the next channel")
			info.StreamProcessingOwner = StreamOwnerNotApplicable
			info.ClaudeStreamMainMode = true
			require.False(t, info.MainStreamMode())
		})
	}
}
