package coze

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestStreamCozeChatFailedLogMessage(t *testing.T) {
	for _, prefix := range []string{"", "event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\n"} {
		t.Run(fmt.Sprintf("delivered=%t", prefix != ""), func(t *testing.T) {
			c, rec := newContext()
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
			service.BeginStreamAttempt(c, info)
			frame := "event: conversation.chat.failed\ndata: " + `{"status":"failed","last_error":{"code":123,"msg":"coze failure api_key:secret (request id: upstream)"}}` + "\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(prefix + frame))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := cozeChatStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			snapshot := info.StreamSession.Snapshot()
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, relaycommon.StreamEndReason("upstream_error"), snapshot.Reason)
			require.Equal(t, frame, string(snapshot.ErrorFrame))
			require.Equal(t, snapshot.Err.Error(), info.StreamResult.Diagnostic.Error)
			require.True(t, info.StreamResult.DiagnosticAvailable)
			require.Equal(t, prefix != "", info.StreamResult.EffectiveContent)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), "upstream_stream_error")
			require.NotContains(t, rec.Body.String(), "coze failure")
			require.NotContains(t, rec.Body.String(), "[DONE]")
			if prefix == "" {
				require.Equal(t, "none", info.StreamResult.UsageSource)
				require.Zero(t, selected.TotalTokens)
			} else {
				require.Equal(t, "estimated", info.StreamResult.UsageSource)
				require.Equal(t, service.EstimateTokenByModel("fixture", "hello"), selected.CompletionTokens)
			}
			require.Equal(t, "coze failure api_key:***", info.StreamResult.ErrorMessage)
		})
	}
}

// TestStreamCozeDTOErrorOrigin 验证独立扫描器的嵌套字段解析错误停流，不补成功尾帧；t 为上下文。
func TestStreamCozeDTOErrorOrigin(t *testing.T) {
	c, rec := newContext()
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
	service.BeginStreamAttempt(c, info)
	body := "event: conversation.message.delta\ndata: {\"content\":123}\n\nevent: conversation.chat.completed\ndata: {}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	usage, _ := cozeChatStreamHandler(c, info, resp)
	service.FinalizeStreamUsage(c, info, usage)
	require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), info.StreamSession.Snapshot().Reason)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.Equal(t, 1, info.ReceivedResponseCount)
	require.Equal(t, "none", info.StreamResult.UsageSource)
	require.Contains(t, rec.Body.String(), "upstream_stream_error")
	require.NotContains(t, rec.Body.String(), "[DONE]")
}

// cozeTerminalReadError 在已消费正常尾帧后模拟连接复位，不生成额外数据。
type cozeTerminalReadError struct{}

// Read 向 p 返回零字节和底层读取错误；用于验证完成后的读错不会重新进入失败兜底。
func (cozeTerminalReadError) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestStreamCozeCompletedReadError 验证受管完成流保留用量并正常收尾，旧路径仍返回原读取错误；t 为上下文。
func TestStreamCozeCompletedReadError(t *testing.T) {
	for _, managed := range []bool{false, true} {
		c, rec := newContext()
		info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
		if managed {
			service.BeginStreamAttempt(c, info)
		}
		body := "event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\nevent: conversation.chat.completed\ndata: {\"usage\":{\"input_count\":10,\"output_count\":2,\"token_count\":12}}\n\n"
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(body), cozeTerminalReadError{}))}
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamSession.ObserveHTTP(resp)
		usage, apiErr := cozeChatStreamHandler(c, info, resp)
		if !managed {
			require.NotNil(t, apiErr)
			continue
		}
		require.Nil(t, apiErr, "已完成流不应通过中转错误返回值再次进入异常兜底")
		selected := service.FinalizeStreamUsage(c, info, usage)
		require.False(t, info.StreamResult.Failed)
		require.False(t, info.StreamResult.DiagnosticAvailable)
		require.Equal(t, 12, selected.TotalTokens)
		require.Contains(t, rec.Body.String(), "[DONE]")
		require.NotContains(t, rec.Body.String(), "upstream_stream_error")
	}
}
