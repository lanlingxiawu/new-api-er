package common

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamDiagnosticOrigin 验证协议异常与本地/下游错误独立分类；t 为测试上下文。
func TestStreamDiagnosticOrigin(t *testing.T) {
	for _, tc := range []struct {
		reason StreamEndReason
		want   bool
	}{
		{"upstream_incomplete", true}, {"upstream_read_error", true},
		{"upstream_json_error", true}, {"upstream_protocol_error", true},
		{"upstream_error", true}, {StreamEndReasonTimeout, true},
		{"response_conversion_error", false}, {"settlement_reservation_error", false},
		{StreamEndReasonPanic, false},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.Fail(tc.reason, errors.New("cause"))
			s.ClientFailed(io.ErrClosedPipe)
			require.Equal(t, tc.want, s.Snapshot().DiagnosticAvailable(false))
			require.Equal(t, tc.want, s.DiagnosticAvailable(false))
		})
	}
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.FailRelay("upstream_read_error", errors.New("local validation"))
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
	require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason, "billing reason is unchanged")
	s.EndRead(io.EOF)
	require.False(t, s.Snapshot().DiagnosticAvailable(false), "cleanup must not replace a local first cause")
	s = NewStreamSession(types.RelayFormatOpenAI)
	s.ClientFailed(io.ErrClosedPipe)
	s.EndRead(io.EOF)
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
}

// TestStreamDiagnosticTransport 验证无 body、采集关闭时的传输证据及 SDK 重试覆盖；t 为测试上下文。
func TestStreamDiagnosticTransport(t *testing.T) {
	var absent *StreamSession
	require.False(t, absent.DiagnosticAvailable(false))
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	s.ObserveTransport(nil, errors.New("connection reset"))
	require.False(t, s.DiagnosticAvailable(false))
	require.Empty(t, s.Snapshot().Reason, "diagnostic observation must not terminate SDK retries")
	s.ObserveTransport(&http.Response{StatusCode: 503}, nil)
	require.False(t, s.DiagnosticAvailable(false))
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
	s.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
	require.False(t, s.DiagnosticAvailable(false), "successful WS handshake is not an HTTP error")
	s.Complete()
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
	s = NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	s.ObserveTransport(&http.Response{StatusCode: 503}, nil)
	s.ClientFailed(io.ErrClosedPipe)
	require.False(t, s.DiagnosticAvailable(false), "非 200 后客户端关闭仍走原流程")
	s = NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	s.ObserveTransport(&http.Response{StatusCode: 503}, nil)
	s.FailRelay("upstream_read_error", errors.New("HTTP error"))
	require.False(t, s.DiagnosticAvailable(false))
	ctx, cancel := context.WithCancel(context.Background())
	s = NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	s.BindContext(ctx)
	cancel()
	s.ObserveTransport(nil, ctx.Err())
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
	require.False(t, s.DiagnosticAvailable(true), "响应头之前超时沿用原流程")
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.True(t, s.DiagnosticAvailable(true), "已取得 200 的受管超时才属于新流程")
	s = NewStreamSession(types.RelayFormatOpenAI)
	require.False(t, s.Snapshot().DiagnosticAvailable(true), "timeout before upstream dispatch has no upstream evidence")
	s.Disable()
	s.ObserveTransport(nil, errors.New("ignored"))
	require.False(t, s.DiagnosticAvailable(false))
	require.False(t, s.Snapshot().DiagnosticAvailable(false))
}
