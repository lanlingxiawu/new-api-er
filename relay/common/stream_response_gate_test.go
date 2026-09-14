package common

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// streamGateHTTPFixture 原样返回预设交换；resp/err 表示真实客户端结果，不进行网络访问。
type streamGateHTTPFixture struct {
	resp *http.Response
	err  error
}

// Do 的 req 仅满足 HTTP 客户端接口；夹具不采集请求信息。
func (f streamGateHTTPFixture) Do(req *http.Request) (*http.Response, error) {
	return f.resp, f.err
}

// TestStreamResponseGateHTTP 覆盖状态边界、无响应和响应伴随错误，拒绝观察器提前读取或改写旧错误正文。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateHTTP(t *testing.T) {
	for _, status := range []int{0, 101, 199, 200, 201, 204, 299, 301, 400, 401, 429, 500, 503} {
		for _, failed := range []bool{false, true} {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ResponseGate = &StreamResponseGate{}
			capture := NewStreamResponseCapture(1)
			capture.ResponseGate = s.ResponseGate
			require.False(t, s.Active(), "尚未收到真实成功响应")
			raw := io.NopCloser(strings.NewReader("data: {broken}\n\n"))
			var resp *http.Response
			if status != 0 {
				resp = &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Fixture": {"raw-header"}}, Body: raw}
			}
			var transportErr error
			if failed {
				transportErr = errors.New("transport failure")
			}
			client := StreamDiagnosticHTTPClient{Client: streamGateHTTPFixture{resp, transportErr}, Capture: capture, Stream: s}
			got, err := client.Do(nil)
			require.Same(t, resp, got)
			require.Equal(t, transportErr, err)
			allowed := status == http.StatusOK && !failed
			require.Equal(t, allowed, s.Active(), "status=%d failed=%v", status, failed)
			if !allowed && resp != nil {
				require.Equal(t, raw, resp.Body, "旧错误 body 保持原对象、未被读取")
			}
			if resp != nil {
				body, _ := io.ReadAll(resp.Body)
				require.Equal(t, "data: {broken}\n\n", string(body))
				require.NoError(t, resp.Body.Close())
			}
			capture.SetError(errors.New("private error"))
			capture.WriteStreamPayload([]byte("sdk payload"))
			capture.WriteDownstreamPayload([]byte("downstream body"))
			if allowed {
				require.True(t, s.DiagnosticAvailable(false))
				require.Equal(t, "raw-header", capture.Snapshot().ResponseHeaders.Get("X-Fixture"))
				require.NotNil(t, capture.DownstreamBody())
			} else {
				require.False(t, s.DiagnosticAvailable(true), "响应头之前超时也不接管")
				require.Equal(t, StreamDiagnostic{}, capture.Snapshot())
				require.Nil(t, capture.DownstreamBody())
				require.Empty(t, capture.responses)
				require.Nil(t, capture.downstream)
			}
		}
	}
}

// TestStreamResponseGateSDKRetry 验证 SDK 失败后成功可激活，最终失败关闭资格且不暴露之前证据。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateSDKRetry(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	capture := NewStreamResponseCapture(2)
	capture.ResponseGate = s.ResponseGate
	for _, status := range []int{503, 200, 429, 200} {
		resp := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("body"))}
		client := StreamDiagnosticHTTPClient{Client: streamGateHTTPFixture{resp: resp}, Capture: capture, Stream: s}
		_, err := client.Do(nil)
		require.NoError(t, err)
		_, _ = io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, status == 200, s.Active())
		if status != 200 {
			require.Equal(t, StreamDiagnostic{}, capture.Snapshot())
			require.Nil(t, capture.DownstreamBody())
		} else {
			for _, previous := range capture.Snapshot().PreviousResponses {
				require.Equal(t, 200, previous.StatusCode)
			}
		}
	}
	s.ObserveTransport(nil, errors.New("final dial failed"))
	require.False(t, s.Active())
	require.Equal(t, StreamDiagnostic{}, capture.Snapshot())
}

// TestStreamResponseGateWebSocket 只有真实成功的 101 握手激活；普通 HTTP 的 101/WS 的 200 均不激活。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateWebSocket(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIRealtime)
	s.ResponseGate = &StreamResponseGate{}
	for _, status := range []int{200, 400, 101, 503} {
		s.ObserveWebSocketHandshake(&http.Response{StatusCode: status}, nil)
		require.Equal(t, status == 101, s.Active())
	}
	s.ObserveWebSocketHandshake(&http.Response{StatusCode: 101}, errors.New("handshake invalid"))
	require.False(t, s.Active())
	s.ObserveWebSocketHandshake(nil, errors.New("dial failed"))
	require.False(t, s.Active())
	s.ObserveTransport(&http.Response{StatusCode: 101}, nil)
	require.False(t, s.Active())
}

// TestStreamResponseGatePlaceholder 验证适配器构造的占位响应/事件不绕过真实交换门控。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGatePlaceholder(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.ResponseGate = &StreamResponseGate{}
	raw := io.NopCloser(strings.NewReader("data: [DONE]\n\n"))
	resp := &http.Response{StatusCode: 200, Body: raw}
	s.ObserveHTTP(resp)
	require.Equal(t, raw, resp.Body)
	require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"input_tokens":5,"output_tokens":2}}`)))
	s.Complete()
	require.False(t, s.Active())
	require.Empty(t, s.Snapshot().Evidence)
	require.False(t, s.Snapshot().Complete)
}
