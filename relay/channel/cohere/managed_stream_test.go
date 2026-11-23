package cohere

import (
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

// TestUnifiedCohereNDJSON 验证独立 NDJSON 路径的真实完成与截断，错误 Content-Type 不改变行语义。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedCohereNDJSON(t *testing.T) {
	for _, complete := range []bool{true, false} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command"}}
		service.BeginStreamAttempt(c, info)
		body := "{\"event_type\":\"text-generation\",\"text\":\"hi\"}\n"
		if complete {
			body += "{\"event_type\":\"stream-end\",\"is_finished\":true,\"finish_reason\":\"COMPLETE\",\"response\":{\"meta\":{\"billed_units\":{\"input_tokens\":10,\"output_tokens\":2}}}}\n"
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		info.StreamSession.ObserveTransport(resp, nil)
		u, err := cohereStreamHandler(c, info, resp)
		require.Nil(t, err)
		service.FinalizeStreamUsage(c, info, u)
		require.Equal(t, !complete, info.StreamResult.Failed)
		require.True(t, info.StreamResult.EffectiveContent)
		if complete {
			require.Equal(t, 2, info.StreamFinalUsage.CompletionTokens)
			require.Contains(t, rec.Body.String(), "[DONE]")
		} else {
			require.Contains(t, rec.Body.String(), "event: error")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		}
	}
}

func TestCohereMainPolicyKeepsNDJSONFallbackAndEnding(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			rec := newCloseNotifyRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			disabled := false
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &disabled},
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command"}}
			info.SetEstimatePromptTokens(7)
			service.BeginStreamAttempt(c, info)
			body := "{\"event_type\":\"text-generation\",\"text\":\"hi\"}\n"
			if malformed {
				body = "{invalid}\n" + body
			}
			if complete {
				body += "{\"event_type\":\"stream-end\",\"is_finished\":true,\"finish_reason\":\"COMPLETE\",\"response\":{\"meta\":{\"billed_units\":{\"input_tokens\":10,\"output_tokens\":2}}}}\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-ndjson"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			u, err := cohereStreamHandler(c, info, resp)
			require.Nil(t, err)
			final := service.FinalizeStreamUsage(c, info, u)
			require.Same(t, u, final)
			require.Nil(t, info.StreamResult)
			require.Equal(t, malformed, info.StreamStatus.HasErrors())
			if malformed {
				require.NotEmpty(t, info.StreamDiagnostic.Snapshot().Error)
			}
			require.Contains(t, rec.Body.String(), "[DONE]")
			require.NotContains(t, rec.Body.String(), "event: error")
			if complete {
				require.Equal(t, 10, u.PromptTokens)
				require.Equal(t, 2, u.CompletionTokens)
			} else {
				require.Equal(t, 7, u.PromptTokens)
			}
		}
	}
}
