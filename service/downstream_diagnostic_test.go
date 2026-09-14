package service

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamDiagnosticDownstreamBody 验证日志只在上游异常采集实际输出，且包含终止补发；t 为测试上下文。
func TestStreamDiagnosticDownstreamBody(t *testing.T) {
	for _, kind := range []string{"eof", "normal", "client", "capture_off", "nonstream"} {
		t.Run(kind, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: kind != "nonstream", RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			if kind == "capture_off" {
				info.StreamDiagnostic = nil
				c.Set(relaycommon.StreamResponseCaptureKey, (*relaycommon.StreamResponseCapture)(nil))
			}
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
			require.NoError(t, err)
			c.Writer.Flush()
			if info.StreamSession != nil {
				switch kind {
				case "normal":
					info.StreamSession.Complete()
				case "client":
					info.StreamSession.ClientFailed(io.ErrClosedPipe)
				default:
					info.StreamSession.EndRead(io.EOF)
				}
			}
			FinalizeStreamUsage(c, info, nil)
			other := map[string]any{}
			AppendStreamLogInfo(info, other)
			raw, err := common.Marshal(other)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, common.Unmarshal(raw, &fields))
			if kind == "eof" {
				require.Contains(t, rec.Body.String(), "error")
				diagnostic := fields["stream_diagnostic"].(map[string]any)
				require.Equal(t, base64.StdEncoding.EncodeToString(rec.Body.Bytes()), diagnostic["downstream_body_base64"])
			} else {
				require.NotContains(t, string(raw), "downstream_body_base64")
			}
			require.NotContains(t, string(raw), "claude_diagnostic")
			require.NotContains(t, string(raw), "claude_stream")
		})
	}
}

// TestStreamDiagnosticDownstreamRetry 验证空串有别于未采集，重试不叠加包装器、不继承正文；t 为测试上下文。
func TestStreamDiagnosticDownstreamRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	first := info.StreamDiagnostic
	_, err := c.Writer.WriteString("first")
	require.NoError(t, err)
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(nil, errors.New("connect"))
	other := map[string]any{}
	AppendStreamErrorDiagnostic(c, other, errors.New("connect"))
	require.Empty(t, other, "连接失败没有响应诊断")
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.StreamSession.EndRead(io.ErrUnexpectedEOF)
	AppendStreamErrorDiagnostic(c, other, io.ErrUnexpectedEOF)
	diag := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	require.NotNil(t, diag.DownstreamBodyBase64)
	require.Empty(t, *diag.DownstreamBodyBase64)
	raw, marshalErr := common.Marshal(other)
	require.NoError(t, marshalErr)
	require.Contains(t, string(raw), `"downstream_body_base64":""`)
	_, err = c.Writer.WriteString("second")
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("first")), *first.DownstreamBody())
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("second")), *info.StreamDiagnostic.DownstreamBody())
	require.Empty(t, *diag.DownstreamBodyBase64, "入队快照独立于之后的写出")
}
