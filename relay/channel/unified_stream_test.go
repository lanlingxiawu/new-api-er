package channel

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedStreamPassthroughHTTP 使用本地上游验证请求字节透传不绕过原始响应观察，且诊断没有请求数据。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamPassthroughHTTP(t *testing.T) {
	requestBody := "{ \"stream\":true, \"unknown_fixture\":\"request-only-marker\" }"
	responseBody := "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n"
	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream-Fixture", "response-only")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer srv.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(requestBody))
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o", ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}}}
	service.BeginStreamAttempt(c, info)
	req, err := http.NewRequest("POST", srv.URL, strings.NewReader(requestBody))
	require.NoError(t, err)
	req.Header.Set("Authorization", "fixture-request-credential")
	resp, err := DoRequest(c, req, info)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, responseBody, string(body))
	require.Equal(t, requestBody, <-received)
	require.True(t, info.StreamSession.Snapshot().Complete)
	require.False(t, info.StreamSession.Snapshot().DiagnosticAvailable(false))
	require.Contains(t, info.StreamSession.Snapshot().Evidence, "output_tokens")
	diag := info.StreamDiagnostic.Snapshot()
	require.Equal(t, responseBody, string(append(diag.BodyHead, diag.BodyTail...)))
	require.Equal(t, "response-only", diag.ResponseHeaders.Get("X-Upstream-Fixture"))
	require.Empty(t, diag.ResponseHeaders.Get("Authorization"))
	// 正常透传仍在内存观察响应，但消费日志不持久化原始头/body。
	service.FinalizeStreamUsage(c, info, nil)
	otherLog := model.NewLogOther()
	service.AppendStreamLogInfo(info, otherLog)
	other := otherLog.Snapshot()
	logged := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	require.Empty(t, logged.ResponseHeaders)
	require.Empty(t, logged.BodyHead)
	require.Empty(t, logged.BodyTail)
	require.Equal(t, diag, info.StreamDiagnostic.Snapshot())
}

// TestStreamDiagnosticPassthroughFailure 验证实际透传请求的异常响应进入新诊断标记；t 为测试上下文。
func TestStreamDiagnosticPassthroughFailure(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", 200},
		{"data: {broken}\n\n", 200},
		{`{"error":{"message":"upstream-error"}}`, 503},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}}}
		service.BeginStreamAttempt(c, info)
		req, err := http.NewRequest("POST", srv.URL, strings.NewReader(`{"stream":true}`))
		require.NoError(t, err)
		resp, err := DoRequest(c, req, info)
		require.NoError(t, err)
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		srv.Close()
		service.FinalizeStreamUsage(c, info, nil)
		otherLog := model.NewLogOther()
		service.AppendStreamLogInfo(info, otherLog)
		other := otherLog.Snapshot()
		if tc.status != http.StatusOK {
			require.Empty(t, other)
			require.Nil(t, info.StreamResult)
			require.Empty(t, recorder.Body.String())
			continue
		}
		require.Equal(t, true, other["stream_diagnostic_available"])
		diagnostic := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
		require.Equal(t, tc.body, string(diagnostic.BodyHead)+string(diagnostic.BodyTail))
		require.NotNil(t, diagnostic.DownstreamBodyBase64)
		require.Contains(t, recorder.Body.String(), "error")
		require.Equal(t, base64.StdEncoding.EncodeToString(recorder.Body.Bytes()), *diagnostic.DownstreamBodyBase64)
	}
}
