package common

import (
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStreamSettingsAlwaysEnabled(t *testing.T) {
	for _, name := range []string{"stream_error_setting", "claude_stream_setting"} {
		require.Nil(t, config.GlobalConfig.Get(name), "retired settings must not be registered")
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"stream_error_setting.enabled": "false", "stream_error_setting.capture_response": "false",
		"claude_stream_setting.enabled": "false", "claude_stream_setting.capture_response": "false",
	}))
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIRealtime} {
		info := &RelayInfo{IsStream: true, RelayFormat: format}
		require.True(t, info.UseStreamErrors())
		require.True(t, info.CaptureStreamResponse())
		info.IsStream = false
		require.True(t, info.UseStreamErrors(), "retries retain the original streaming mode")
	}
	info := &RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime}
	require.True(t, info.UseStreamErrors())
	info = &RelayInfo{RelayFormat: types.RelayFormatClaude}
	require.False(t, info.UseStreamErrors())
	info.IsStream = true
	require.False(t, info.UseStreamErrors(), "upstream SSE cannot promote a non-stream request")
	require.False(t, info.UseStrictClaudeStream())
	require.False(t, info.CaptureStreamResponse())
	require.True(t, info.CaptureClaudeResponse(), "legacy Claude response capture stays enabled")
}
