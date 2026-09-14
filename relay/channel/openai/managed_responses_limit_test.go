package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestManagedResponsesLimitLifecycle 验证限制终态经过原生/Chat/Claude 转换、保留真实用量且无异常诊断；t 管理内存响应。
func TestManagedResponsesLimitLifecycle(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatClaude} {
		for _, reason := range []string{"max_output_tokens", "content_filter"} {
			t.Run(string(format)+"/"+reason, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.SetEstimatePromptTokens(99)
				service.BeginStreamAttempt(c, info)
				ending := fmt.Sprintf(`{"type":"response.incomplete","response":{"id":"resp_fixture","model":"gpt-4o","object":"response","status":"incomplete","incomplete_details":{"reason":%q},"output":[],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`, reason)
				body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"model\":\"gpt-4o\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"output_index\":0,\"content_index\":0}\n\ndata: " + ending + "\n\n"
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				var usage *dto.Usage
				var apiErr *types.NewAPIError
				if format == types.RelayFormatOpenAIResponses {
					usage, apiErr = OaiResponsesStreamHandler(c, info, resp)
				} else {
					usage, apiErr = OaiResponsesToChatStreamHandler(c, info, resp)
				}
				require.Nil(t, apiErr)
				selected := service.FinalizeStreamUsage(c, info, usage)
				require.False(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.DiagnosticAvailable)
				require.Equal(t, "upstream", info.StreamResult.UsageSource)
				require.Equal(t, 5, selected.PromptTokens)
				require.Equal(t, 2, selected.CompletionTokens)
				require.Contains(t, rec.Body.String(), "hello")
				require.NotContains(t, rec.Body.String(), "upstream_stream_error")
				switch format {
				case types.RelayFormatOpenAIResponses:
					require.Contains(t, rec.Body.String(), ending)
				case types.RelayFormatOpenAI:
					finish := "length"
					if reason == "content_filter" {
						finish = "content_filter"
					}
					require.Contains(t, rec.Body.String(), `"finish_reason":"`+finish+`"`)
					require.Contains(t, rec.Body.String(), "[DONE]")
				case types.RelayFormatClaude:
					require.Contains(t, rec.Body.String(), "event: message_stop")
				}
				other := map[string]any{}
				service.AppendStreamLogInfo(info, other)
				require.NotContains(t, other, "stream_diagnostic_available")
			})
		}
	}
}

// TestManagedResponsesLimitZeroUsage 确认限制终态的显式零不被正常路径旧估算覆盖；t 不触发实际结算。
func TestManagedResponsesLimitZeroUsage(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatClaude} {
		for _, counts := range [][2]int{{0, 0}, {0, 2}, {5, 0}} {
			t.Run(fmt.Sprintf("%s/%v", format, counts), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.SetEstimatePromptTokens(99)
				service.BeginStreamAttempt(c, info)
				body := fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"output_index\":0,\"content_index\":0}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[],\"usage\":{\"input_tokens\":%d,\"output_tokens\":%d,\"total_tokens\":%d}}}\n\n", counts[0], counts[1], counts[0]+counts[1])
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamSession.ObserveHTTP(resp)
				var usage *dto.Usage
				var err *types.NewAPIError
				if format == types.RelayFormatOpenAIResponses {
					usage, err = OaiResponsesStreamHandler(c, info, resp)
				} else {
					usage, err = OaiResponsesToChatStreamHandler(c, info, resp)
				}
				require.Nil(t, err)
				selected := service.FinalizeStreamUsage(c, info, usage)
				require.False(t, info.StreamResult.Failed)
				require.Equal(t, counts[0], selected.PromptTokens)
				require.Equal(t, counts[1], selected.CompletionTokens)
			})
		}
	}
}
