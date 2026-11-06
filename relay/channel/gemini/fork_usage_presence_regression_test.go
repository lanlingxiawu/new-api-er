package gemini

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForkGeminiUsagePresenceRegression(t *testing.T) {
	mode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(mode) })
	const prefix = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":20,\"cachedContentTokenCount\":40,\"totalTokenCount\":120}}\n\n"
	const stop = "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}]}\n\n"

	for _, format := range []types.RelayFormat{types.RelayFormatGemini, types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, tc := range []struct {
			name, wire    string
			prompt, cache int
		}{
			{"cache_zero", prefix + "data: {\"usageMetadata\":{\"cachedContentTokenCount\":0}}\n\n" + stop, 100, 0},
			{"cache_absent", prefix + stop, 100, 40},
			{"all_zero", "data: {\"usageMetadata\":{\"promptTokenCount\":0,\"candidatesTokenCount\":0,\"cachedContentTokenCount\":0,\"totalTokenCount\":0}}\n\n" + stop, 0, 0},
			{"missing_usage", stop, 999, 0},
			{"cache_only_zero", "data: {\"usageMetadata\":{\"cachedContentTokenCount\":0}}\n\n" + stop, 999, 0},
		} {
			t.Run(fmt.Sprintf("%s/%s", format, tc.name), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"}}
				info.SetEstimatePromptTokens(999)
				service.BeginStreamAttempt(c, info)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.wire))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				handler := GeminiChatStreamHandler
				if format == types.RelayFormatGemini {
					handler = GeminiTextGenerationStreamHandler
				} else if format == types.RelayFormatOpenAIResponses {
					handler = GeminiResponsesStreamHandler
				}
				u, err := handler(c, info, resp)
				require.Nil(t, err)
				require.NotNil(t, u)
				adapterPrompt := u.PromptTokens
				final := service.FinalizeStreamUsage(c, info, u)
				require.NotNil(t, final)
				require.False(t, info.StreamResult.Failed)
				if canonical, ok := final.BillingUsage.CanonicalUsage(); ok {
					final = canonical
				}
				wantPrompt, wantCache := tc.prompt, tc.cache

				t.Logf("adapter_input=%d selected_input=%d cache=%d confirmed=%v", adapterPrompt, final.PromptTokens, final.PromptTokensDetails.CachedTokens, info.StreamResult.Diagnostic.UsageEvidence)
				require.Equal(t, wantPrompt, final.PromptTokens)
				require.Equal(t, wantCache, final.PromptTokensDetails.CachedTokens)
				if tc.name == "all_zero" {
					require.Zero(t, final.CompletionTokens)
					require.Equal(t, 0, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
				}
				if tc.name == "cache_only_zero" {
					require.Equal(t, 999, final.TotalTokens)
					require.NotNil(t, final.BillingUsage)
					require.True(t, final.BillingUsage.Estimated, "missing input is an estimate, not confirmed by the zero cache counter")
					require.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
					_, reported := info.StreamResult.Diagnostic.UsageEvidence["input_tokens"]
					require.False(t, reported)
					require.Equal(t, 0, info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"])
				}
				if format == types.RelayFormatOpenAIResponses {
					var wireUsage gjson.Result
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if data, ok := strings.CutPrefix(line, "data: "); ok && gjson.Get(data, "type").String() == "response.completed" {
							wireUsage = gjson.Get(data, "response.usage")
						}
					}
					require.True(t, wireUsage.IsObject(), "successful Responses terminal carries usage")
					require.Equal(t, int64(wantPrompt), wireUsage.Get("input_tokens").Int(), "wire input agrees with settlement")
					require.Equal(t, int64(wantCache), wireUsage.Get("input_tokens_details.cached_tokens").Int(), "wire cache agrees with settlement")
				}
			})
		}
	}

}

func TestForkGeminiCumulativeUsagePresence(t *testing.T) {
	mode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(mode) })
	const initial = `{"promptTokenCount":100,"toolUsePromptTokenCount":30,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":157,"promptTokensDetails":[{"modality":"TEXT","tokenCount":60},{"modality":"AUDIO","tokenCount":40}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":20},{"modality":"AUDIO","tokenCount":10}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":12},{"modality":"AUDIO","tokenCount":8}]}`
	const zeroOutput = `{"candidatesTokenCount":0,"thoughtsTokenCount":0}`
	const zeroToolAndAudio = `{"toolUsePromptTokenCount":0,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":0}],"toolUsePromptTokensDetails":[{"modality":"AUDIO","tokenCount":0}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":0}]}`
	for _, format := range []types.RelayFormat{types.RelayFormatGemini, types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, tc := range []struct {
			name, correction, after, initial string
			failed, content, sidecar         bool
			wantTotal                        int
			// Input, output, reasoning, input/output audio, input text, input/output image.
			want [8]int
		}{
			{name: "zero_tool_and_modalities", correction: zeroToolAndAudio, want: [8]int{100, 27, 7, 0, 0, 80, 0, 0}},
			{name: "zero_tool_and_modalities_failure", correction: zeroToolAndAudio, failed: true, want: [8]int{100, 27, 7, 0, 0, 80, 0, 0}},
			{name: "omitted_thoughts_and_modalities", correction: `{"candidatesTokenCount":25}`, want: [8]int{130, 32, 7, 50, 8, 80, 0, 0}},
			{name: "zero_thoughts_only", correction: `{"thoughtsTokenCount":0}`, want: [8]int{130, 20, 0, 50, 8, 80, 0, 0}},
			{name: "zero_output_without_total", correction: zeroOutput, want: [8]int{130, 0, 0, 50, 8, 80, 0, 0}},
			{name: "zero_output_with_stale_total", correction: `{"candidatesTokenCount":0,"thoughtsTokenCount":0,"totalTokenCount":157}`, want: [8]int{130, 0, 0, 50, 8, 80, 0, 0}},
			{name: "zero_output_followed_by_total", correction: zeroOutput, after: `{"totalTokenCount":999}`, want: [8]int{130, 0, 0, 50, 8, 80, 0, 0}},
			{name: "zero_output_followed_by_empty", correction: zeroOutput, after: `{}`, want: [8]int{130, 0, 0, 50, 8, 80, 0, 0}},
			{name: "unreported_output_total_fallback", initial: `{"promptTokenCount":100,"totalTokenCount":120}`, correction: `{"totalTokenCount":125}`, want: [8]int{100, 25, 0, 0, 0, 100, 0, 0}},
			{name: "thoughts_zero_with_unreported_candidates", initial: `{"promptTokenCount":100,"totalTokenCount":120,"thoughtsTokenCount":0}`, correction: `{}`, want: [8]int{100, 20, 0, 0, 0, 100, 0, 0}, wantTotal: 120},
			{name: "total_before_candidate_count", initial: `{"promptTokenCount":100,"totalTokenCount":120}`, correction: `{"candidatesTokenCount":20}`, want: [8]int{100, 20, 0, 0, 0, 100, 0, 0}, wantTotal: 120},
			{name: "zero_output_failure", correction: zeroOutput, failed: true, want: [8]int{130, 0, 0, 50, 8, 80, 0, 0}},
			{name: "zero_output_still_estimates_content", correction: zeroOutput, content: true, want: [8]int{130, 1, 0, 50, 8, 80, 0, 0}},
			{name: "partial_modalities", correction: `{"promptTokensDetails":[{"modality":" audio ","tokenCount":0},{"modality":"IMAGE","tokenCount":5}],"toolUsePromptTokensDetails":[{"modality":"AUDIO","tokenCount":0}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":0},{"modality":"IMAGE","tokenCount":4}]}`, want: [8]int{130, 27, 7, 0, 0, 80, 5, 4}},
			{name: "duplicate_modalities", correction: `{"promptTokensDetails":[{"modality":"audio","tokenCount":0},{"modality":"AUDIO","tokenCount":2}],"toolUsePromptTokensDetails":null,"candidatesTokensDetails":[]}`, want: [8]int{130, 27, 7, 12, 8, 80, 0, 0}},
			{name: "null_metadata", correction: `null`, want: [8]int{130, 27, 7, 50, 8, 80, 0, 0}},
			{name: "original_billing_sidecar", correction: zeroToolAndAudio, sidecar: true, want: [8]int{55, 4, 0, 0, 0, 55, 0, 0}},
		} {
			t.Run(fmt.Sprintf("%s/%s", format, tc.name), func(t *testing.T) {
				metadata := initial
				if tc.initial != "" {
					metadata = tc.initial
				}
				if tc.sidecar {
					metadata = strings.TrimSuffix(metadata, "}") + `,"billing_usage":{"source":"gemini_chat","semantic":"gemini","gemini_usage_metadata":{"promptTokenCount":55,"candidatesTokenCount":4,"totalTokenCount":59}}}`
				}
				body := "data: {\"usageMetadata\":" + metadata + "}\n\n"
				if tc.content {
					body += "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
				}
				body += "data: {\"usageMetadata\":" + tc.correction + "}\n\n"
				if tc.after != "" {
					body += "data: {\"usageMetadata\":" + tc.after + "}\n\n"
				}
				if !tc.failed {
					body += "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}]}\n\n"
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"}}
				service.BeginStreamAttempt(c, info)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				handler := GeminiChatStreamHandler
				if format == types.RelayFormatGemini {
					handler = GeminiTextGenerationStreamHandler
				} else if format == types.RelayFormatOpenAIResponses {
					handler = GeminiResponsesStreamHandler
				}
				usage, err := handler(c, info, resp)
				require.Nil(t, err)
				final := service.FinalizeStreamUsage(c, info, usage)
				require.Equal(t, tc.failed, info.StreamResult.Failed)
				for index, selected := range []*dto.Usage{usage, final} {
					if tc.failed && index == 1 {
						require.Zero(t, selected.TotalTokens)
						require.Equal(t, "none", info.StreamResult.UsageSource)
						require.False(t, info.StreamResult.EffectiveContent)
						continue
					}
					if canonical, ok := selected.BillingUsage.CanonicalUsage(); ok {
						selected = canonical
					}
					if tc.content {
						require.Equal(t, 130, selected.PromptTokens)
						require.Positive(t, selected.CompletionTokens, "received text still uses the existing output estimate")
						require.Zero(t, selected.CompletionTokenDetails.ReasoningTokens)
						require.True(t, selected.BillingUsage.Estimated)
						continue
					}
					got := [8]int{selected.PromptTokens, selected.CompletionTokens, selected.CompletionTokenDetails.ReasoningTokens, selected.PromptTokensDetails.AudioTokens, selected.CompletionTokenDetails.AudioTokens, selected.PromptTokensDetails.TextTokens, selected.PromptTokensDetails.ImageTokens, selected.CompletionTokenDetails.ImageTokens}
					require.Equal(t, tc.want, got)
					if tc.wantTotal > 0 {
						require.Equal(t, tc.wantTotal, selected.TotalTokens)
					}
					if strings.HasPrefix(tc.name, "zero_output_") && !tc.content {
						require.Equal(t, 130, selected.TotalTokens, "a stale total must not revive explicitly cleared output")
					}
				}
				if format == types.RelayFormatOpenAIResponses && !tc.failed && !tc.sidecar {
					var terminal gjson.Result
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if data, ok := strings.CutPrefix(line, "data: "); ok && gjson.Get(data, "type").String() == "response.completed" {
							terminal = gjson.Get(data, "response.usage")
						}
					}
					require.True(t, terminal.IsObject())
					require.EqualValues(t, tc.want[0], terminal.Get("input_tokens").Int())
					if tc.content {
						require.Positive(t, terminal.Get("output_tokens").Int())
					} else {
						require.EqualValues(t, tc.want[1], terminal.Get("output_tokens").Int())
					}
				}
			})
		}
	}
}
