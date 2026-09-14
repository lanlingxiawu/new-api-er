package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedOpenAIStreamFixtures 用真实渠道转换器验证正常/异常/原错误透传，不连接真实 AI。
// 参数 t：测试上下文；Begin/Finalize 之间只经过内存 HTTP 响应和原渠道处理器，不执行资金结算。
func TestUnifiedOpenAIStreamFixtures(t *testing.T) {
	text := "data: {\"id\":\"chatcmpl-test\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"
	stop := "data: {\"id\":\"chatcmpl-test\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name, body, source string
		failed             bool
	}{
		{"normal", text + stop, "upstream", false},
		{"truncated", text, "estimated", true},
		{"empty", "", "none", true},
		{"bad JSON", "data: {broken}\n\n", "none", true},
		{"error", "event: error\ndata: {\"error\":{\"message\":\"upstream-provided\",\"type\":\"server_error\"}}\n\n", "none", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, FinalRequestRelayFormat: types.RelayFormatOpenAI}
			info.SetEstimatePromptTokens(10)
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, err := OaiStreamHandler(c, info, resp)
			require.Nil(t, err)
			final := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, tc.failed, info.StreamResult.Failed)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			if tc.failed {
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.Contains(t, rec.Body.String(), `"error"`)
			} else {
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 2, final.CompletionTokens)
			}
			if tc.name == "truncated" {
				require.Contains(t, rec.Body.String(), "hello")
				require.Greater(t, final.TotalTokens, 0)
			}
			if tc.name == "error" {
				require.Equal(t, tc.body, rec.Body.String())
			}
		})
	}
}

// TestUnifiedResponsesPartialUsage 失败终态仍采用它携带的确认用量，而不是遗失该批字段。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedResponsesPartialUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	service.BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.StreamSession.CommitDelivery([]byte(`{"type":"response.output_text.delta","delta":"hello"}`))
	_ = info.StreamSession.ObserveEvent("", []byte(`{"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":12,"output_tokens":3},"error":{"message":"backend detail"}}}`))
	usage := service.FinalizeStreamUsage(c, info, &dto.Usage{})
	require.Equal(t, "upstream", info.StreamResult.UsageSource)
	require.Equal(t, 12, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
}

// TestUnifiedOpenAICandidateEndForwarding 验证原数据转发和强制格式化都保留先结束候选，后续正常/异常终止仍走原结算选择。
// 参数 t：测试上下文；逐帧调用实际渠道格式处理器，避免上游预读全部完成掩盖尾帧问题，不执行资金操作。
func TestUnifiedOpenAICandidateEndForwarding(t *testing.T) {
	for _, tc := range []struct {
		name        string // 输出路径与最终状态。
		forceFormat bool   // 是否经过既有 DTO 重新编码。
		failAfter   bool   // 是否在候选 0 完成后模拟上游读错误。
	}{
		{"raw normal", false, false}, {"formatted normal", true, false},
		{"raw failed", false, true}, {"formatted failed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true,"n":2}`))
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, FinalRequestRelayFormat: types.RelayFormatOpenAI}
			info.SetEstimatePromptTokens(10)
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			helper.SetEventStreamHeaders(c)
			frames := []string{
				`{"id":"chatcmpl-multi","model":"gpt-4o","choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}],"usage":{"prompt_tokens":10}}`,
				`{"id":"chatcmpl-multi","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"TAIL"},"finish_reason":"stop"}]}`,
			}
			for _, data := range frames {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(data)))
				require.NoError(t, HandleStreamFormat(c, info, data, tc.forceFormat, false))
			}
			require.False(t, info.StreamSession.Snapshot().Complete)
			require.Contains(t, rec.Body.String(), `"content":"TAIL"`)
			require.True(t, info.StreamSession.Snapshot().Effective)
			if !tc.forceFormat {
				require.Contains(t, rec.Body.String(), "data: "+frames[1]+"\n\n")
			}
			if tc.failAfter {
				info.StreamSession.EndRead(io.ErrUnexpectedEOF)
			} else {
				data := `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(data)))
				require.NoError(t, HandleStreamFormat(c, info, data, tc.forceFormat, false))
			}
			helper.Done(c)
			usage := service.FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12})
			require.Equal(t, tc.failAfter, info.StreamResult.Failed)
			require.Equal(t, tc.failAfter, info.StreamResult.DiagnosticAvailable)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, 10, usage.PromptTokens)
			if tc.failAfter {
				require.Equal(t, "mixed", info.StreamResult.UsageSource)
				require.Positive(t, usage.CompletionTokens, "已有交付内容时补估零输出")
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.Contains(t, rec.Body.String(), `"error"`)
			} else {
				require.Equal(t, "upstream", info.StreamResult.UsageSource)
				require.Equal(t, 2, usage.CompletionTokens)
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.NotContains(t, rec.Body.String(), `"error"`)
			}
		})
	}
}
