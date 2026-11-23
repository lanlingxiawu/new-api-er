package common

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestClaudeStreamValidationPrecedence(t *testing.T) {
	previous := operation_setting.ClaudeStreamValidationEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_validation_setting", map[string]string{"enabled": fmt.Sprint(previous)}))
	})
	on, off := true, false
	for _, global := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_validation_setting", map[string]string{"enabled": fmt.Sprint(global)}))
		for ci, channel := range []*bool{nil, &on, &off} {
			for ui, user := range []*bool{nil, &on, &off} {
				t.Run(fmt.Sprintf("global=%t/channel=%d/user=%d", global, ci, ui), func(t *testing.T) {
					info := &RelayInfo{UserSetting: dto.UserSetting{ClaudeStreamValidation: user}}
					info.FreezeClaudeStreamValidation(dto.ChannelSettings{ClaudeStreamValidation: channel})
					want, source := global, "global"
					if channel != nil {
						want, source = *channel, "channel"
					}
					if user != nil {
						want, source = *user, "user"
					}
					require.Equal(t, want, info.ClaudeStreamValidationEnabled())
					require.Equal(t, source, info.ClaudeStreamValidationSource)
				})
			}
		}
	}
}

func TestClaudeStreamValidationAttemptSnapshot(t *testing.T) {
	on, off := true, false
	info := &RelayInfo{}
	info.FreezeClaudeStreamValidation(dto.ChannelSettings{ClaudeStreamValidation: &off})
	off = true
	require.False(t, info.ClaudeStreamValidationEnabled(), "do not retain a mutable DTO pointer")
	info.FreezeClaudeStreamValidation(dto.ChannelSettings{ClaudeStreamValidation: &on})
	require.True(t, info.ClaudeStreamValidationEnabled(), "a new channel attempt resolves again")
}

func BenchmarkClaudeStreamValidation(b *testing.B) {
	on, off := true, false
	for _, user := range []*bool{nil, &on, &off} {
		b.Run(fmt.Sprint(user), func(b *testing.B) {
			info := &RelayInfo{UserSetting: dto.UserSetting{ClaudeStreamValidation: user}}
			channel := dto.ChannelSettings{ClaudeStreamValidation: &off}
			b.ReportAllocs()
			for b.Loop() {
				info.FreezeClaudeStreamValidation(channel)
				_ = info.ClaudeStreamValidationEnabled()
			}
		})
	}
}
