package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedStreamLegacyFunctionSettlement 用 t 通过真实适配器验证已交付旧版调用的异常用量选择，不执行资金操作。
func TestUnifiedStreamLegacyFunctionSettlement(t *testing.T) {
	const requestBody = `{"model":"gpt-4o","stream":true,"n":2,"functions":[{"name":"lookup","parameters":{"type":"object"}}],"function_call":"auto"}`
	const start = "data: {\"choices\":[{\"index\":0,\"delta\":{\"function_call\":{\"name\":\"lookup\",\"arguments\":\"{\\\"x\\\":\"}}}]}\n\n"
	const end = "data: {\"choices\":[{\"index\":0,\"delta\":{\"function_call\":{\"arguments\":\"1}\"}},\"finish_reason\":\"function_call\"}]}\n\n"
	const upstreamError = "event: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"fixture failure\"}}\n\n"
	for _, suffix := range []string{"", upstreamError, "data: {broken}\n\n"} {
		for _, tc := range []struct {
			name       string // 用量证据与有效交付场景。
			usage      string // 上游确认用量 JSON；空表示没有报告。
			finished   bool   // 候选 0 是否已交付完整调用及结束帧。
			source     string // 预期异常结算来源。
			completion int    // 确认输出量；estimated 分支单独验证增量值。
		}{
			{"confirmed", `{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17}`, true, "upstream", 7},
			{"confirmed zero", `{"prompt_tokens":10,"completion_tokens":0,"total_tokens":10}`, true, "mixed", 0},
			{"estimated", "", true, "estimated", 0},
			{"unfinished confirmed", `{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17}`, false, "none", 0},
			{"unfinished no usage", "", false, "none", 0},
		} {
			t.Run(tc.name+"/"+suffix, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(requestBody))
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(requestBody, &request))
				require.NotEmpty(t, request.Functions)
				require.NotEmpty(t, request.FunctionCall)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, Request: &request, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.SetEstimatePromptTokens(10)
				service.BeginStreamAttempt(c, info)
				require.Equal(t, 2, info.StreamSession.ExpectedChoices, "入口 DTO 同样适用于跳过请求转换的透传路径")
				body := start
				if tc.finished {
					body += end
				}
				if tc.usage != "" {
					body += "data: {\"choices\":[],\"usage\":" + tc.usage + "}\n\n"
				}
				body += suffix
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				usage, err := OaiStreamHandler(c, info, resp)
				require.Nil(t, err)
				final := service.FinalizeStreamUsage(c, info, usage)
				require.True(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.ClientGone)
				require.Equal(t, tc.finished, info.StreamResult.EffectiveContent)
				require.Equal(t, tc.source, info.StreamResult.UsageSource)
				require.Contains(t, rec.Body.String(), start)
				if tc.finished {
					require.Contains(t, rec.Body.String(), end)
				}
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
				if suffix == upstreamError {
					require.Contains(t, rec.Body.String(), upstreamError)
				}
				if tc.source == "none" {
					require.Zero(t, final.TotalTokens)
				} else {
					require.Equal(t, 10, final.PromptTokens)
					if tc.source == "estimated" || tc.source == "mixed" {
						var estimator service.StreamTokenEstimator
						expected := estimator.Add("gpt-4o", `lookup{"x":1}`)
						require.Positive(t, expected)
						require.Equal(t, expected, final.CompletionTokens)
					} else {
						require.Equal(t, tc.completion, final.CompletionTokens)
					}
				}
				require.Same(t, final, service.FinalizeStreamUsage(c, info, usage), "终止结果冻结，不重复估算或补发")
			})
		}
	}
}
