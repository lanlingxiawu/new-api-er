package logger

import (
	"bytes"
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamPrivateLogging 验证私有流错误/调试不泄露原文，非流式日志保留原行为。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamPrivateLogging(t *testing.T) {
	oldWriter, oldError, oldDebug := gin.DefaultWriter, gin.DefaultErrorWriter, common.DebugEnabled
	defer func() { gin.DefaultWriter, gin.DefaultErrorWriter, common.DebugEnabled = oldWriter, oldError, oldDebug }()
	var output bytes.Buffer
	gin.DefaultWriter = &output
	gin.DefaultErrorWriter = &output
	common.DebugEnabled = true
	ctx := context.WithValue(context.Background(), common.StreamPrivateContextKey, true)
	LogError(ctx, "private body")
	LogWarn(ctx, "private reason")
	LogDebug(ctx, "private request")
	require.NotContains(t, output.String(), "private body")
	require.NotContains(t, output.String(), "private reason")
	require.NotContains(t, output.String(), "private request")
	LogError(context.Background(), "legacy error")
	require.Contains(t, output.String(), "legacy error")
}

// legacyStreamObserver 模拟旧上下文接口，仅计数以确认日志不再驱动业务状态。
type legacyStreamObserver struct {
	calls int // 日志触发会话回调的次数，修复后应恒为零。
}

// RecordLegacyStreamError 接收旧桥传入的 err；仅用于检测不应发生的回调。
func (o *legacyStreamObserver) RecordLegacyStreamError(err error) { o.calls++ }

// TestLegacyStreamLogHasNoStateSideEffects 验证私有日志保持脱敏且不触发旧会话接口；t 为测试上下文。
func TestLegacyStreamLogHasNoStateSideEffects(t *testing.T) {
	old := gin.DefaultErrorWriter
	t.Cleanup(func() { gin.DefaultErrorWriter = old })
	var output bytes.Buffer
	gin.DefaultErrorWriter = &output
	observer := &legacyStreamObserver{}
	ctx := context.WithValue(context.Background(), common.StreamPrivateContextKey, true)
	ctx = context.WithValue(ctx, "unified_stream_session", observer)
	LogLegacyStreamError(ctx, "fixture private parse error")
	require.Zero(t, observer.calls)
	require.Contains(t, output.String(), "see private upstream diagnostics")
	require.NotContains(t, output.String(), "fixture private parse error")
}
