package common

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamSettingsFreeze 验证入口模式冻结、协议兼容开关和全局采集关闭均可回退。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamSettingsFreeze(t *testing.T) {
	on, off := true, false
	info := &RelayInfo{RelayFormat: types.RelayFormatOpenAI}
	require.False(t, info.UseStreamErrors())
	require.Nil(t, info.streamErrorsEnabled) // 非流式不初始化配置或异常处理状态。
	info.IsStream = true
	require.False(t, info.UseStreamErrors())
	info = &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, streamErrorsEnabled: &off, clientStreamMode: &on}
	require.False(t, info.UseStrictClaudeStream())
	require.False(t, info.UseStreamErrors())
	info = &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, streamErrorsEnabled: &on, streamCaptureEnabled: &off, clientStreamMode: &on}
	require.True(t, info.UseStrictClaudeStream())
	require.False(t, info.CaptureClaudeResponse())
	require.False(t, info.CaptureStreamResponse())
	info = &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, claudeStreamStrict: &off}
	require.False(t, info.UseStreamErrors())
	info = &RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime}
	require.True(t, info.UseStreamErrors())
}
