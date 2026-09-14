package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// terminalDeadlineWriter 模拟已有写入期限过期；只有续期后才能写，避免真实等待 30 秒。
type terminalDeadlineWriter struct {
	*httptest.ResponseRecorder                        // 下游已接受的字节。
	deadline                   time.Time              // 最近设置的连接期限，零值视为过期。
	sets                       int                    // 续期次数。
	control                    *streamTerminalControl // 非 nil 时模拟超时后仅专用回调开放写入。
}

// SetWriteDeadline 记录下一次写入的有界期限 deadline，不执行网络操作。
func (w *terminalDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.sets++
	return nil
}

// Write 仅在期限有效时交付 p，过期时返回确定的传输错误。
func (w *terminalDeadlineWriter) Write(p []byte) (int, error) {
	if w.control != nil && !w.control.allowed {
		return 0, context.DeadlineExceeded
	}
	if !w.deadline.After(time.Now()) {
		return 0, context.DeadlineExceeded
	}
	return w.ResponseRecorder.Write(p)
}

// streamTerminalControl 记录专用终止回调的临时写入范围；不改动真实超时中间件。
type streamTerminalControl struct {
	allowed bool // 当前是否位于专用终止写入回调内。
	calls   int  // 回调调用次数。
}

// RelayTimeoutKind 无参数，夹具固定声明已发生整体超时。
func (c *streamTerminalControl) RelayTimeoutKind() string { return "total" }

// WriteTerminalError 只在 write 回调执行期间开放写入，退出后恢复禁止状态。
func (c *streamTerminalControl) WriteTerminalError(write func()) {
	c.calls++
	c.allowed = true
	defer func() { c.allowed = false }()
	write()
}

// TestStreamTerminalDeadlineAfterManagedTimeout 验证续期不跳过专用通道，也不放开后续正文；t 为上下文。
func TestStreamTerminalDeadlineAfterManagedTimeout(t *testing.T) {
	control := &streamTerminalControl{}
	w := &terminalDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), control: control}
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	cancel()
	FinalizeStreamUsage(c, info, nil)
	require.Equal(t, 1, control.calls)
	require.Equal(t, 1, w.sets)
	require.Contains(t, w.Body.String(), "event: error")
	require.False(t, info.StreamResult.ClientGone)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.False(t, control.allowed)
	_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"late-body\"}}]}\n\n")
	require.Error(t, err)
	require.NotContains(t, w.Body.String(), "late-body")
}

// TestStreamTerminalRenewsDeadline 验证各 HTTP 终止格式通过旧透明包装续期；t 为测试上下文。
func TestStreamTerminalRenewsDeadline(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			w := &terminalDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			FinalizeStreamUsage(c, info, nil)
			require.Equal(t, 1, w.sets)
			require.WithinDuration(t, time.Now().Add(30*time.Second), w.deadline, time.Second)
			require.Contains(t, w.Body.String(), "event: error\n")
			require.True(t, w.Flushed)
			FinalizeStreamUsage(c, info, nil)
			require.Equal(t, 1, w.sets, "终止去重仍有效")
		})
	}
}
