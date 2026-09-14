package helper

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// pingDeadlineWriter 在每次成功 Write 后模拟时间推进至过期，下一次写必须先续期。
type pingDeadlineWriter struct {
	*httptest.ResponseRecorder               // 已交付下游字节。
	deadline                   time.Time     // 有界写期限。
	sets, pings                int           // 所有者独占的续期/保活计数。
	ping                       chan struct{} // 通知上游夹具保活已经成功写出。
}

// SetWriteDeadline 保存 deadline，验证保活也使用连接期限。
func (w *pingDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.sets++
	return nil
}

// Write 交付 p 后立即让旧期限失效，避免等待真实 30 秒。
func (w *pingDeadlineWriter) Write(p []byte) (int, error) {
	if !w.deadline.After(time.Now()) {
		return 0, context.DeadlineExceeded
	}
	w.deadline = time.Time{}
	if bytes.Contains(p, []byte(": PING")) {
		w.pings++
		select {
		case w.ping <- struct{}{}:
		default:
		}
	}
	return w.ResponseRecorder.Write(p)
}

// TestManagedStreamPingRenewsDeadline 验证正常内容后只有保活时仍续期，不误标客户端离开；t 为上下文。
func TestManagedStreamPingRenewsDeadline(t *testing.T) {
	old := *operation_setting.GetGeneralSetting()
	setting := old
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	operation_setting.ReplaceGeneralSetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceGeneralSetting(old) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, upstream := io.Pipe()
	defer r.Close()
	defer upstream.Close()
	w := &pingDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), ping: make(chan struct{}, 1)}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
	info := &relaycommon.RelayInfo{IsStream: true, StreamSession: s, ChannelMeta: &relaycommon.ChannelMeta{}}
	s.BindContext(ctx)
	c.Writer = relaycommon.NewStreamWriter(c.Writer, s, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer upstream.Close()
		_, _ = io.WriteString(upstream, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		select {
		case <-w.ping:
		case <-ctx.Done():
			return
		}
		_, _ = io.WriteString(upstream, "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\n")
	}()
	StreamEventScannerHandler(c, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, info, func(_ string, data string, sr *StreamResult) {
		if err := StringData(c, data); err != nil {
			sr.Stop(err)
		}
	})
	cancel()
	<-done
	require.Equal(t, 1, w.pings)
	require.Equal(t, 3, w.sets)
	require.Nil(t, s.ClientError())
	require.True(t, s.ProtocolComplete())
}

// TestManagedStreamMixedFrameImmediate 验证不同换行的完整首帧无需等下一事件；t 为测试上下文。
func TestManagedStreamMixedFrameImmediate(t *testing.T) {
	for _, ending := range []string{"\n\r\n", "\r\r", "\r\n\n"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI), ChannelMeta: &relaycommon.ChannelMeta{}}
			first := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer w.Close()
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}"+ending)
				select {
				case <-first:
				case <-ctx.Done():
				}
			}()
			called := false
			StreamEventScannerHandler(c, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, info, func(_ string, data string, sr *StreamResult) { called = true; first <- struct{}{} })
			<-done
			require.True(t, called, "首帧应在上游继续或 EOF 之前交付")
			require.Equal(t, relaycommon.StreamEndReason("upstream_incomplete"), info.StreamSession.Snapshot().Reason, "内容交付后 EOF 仍缺少真实终态")
		})
	}
}
