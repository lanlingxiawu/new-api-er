package xai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Vendor wire reference: https://docs.x.ai/developers/model-capabilities/text/streaming
func forkXAIFaultStream(t *testing.T, body string, fragmented, liveHTTP bool) (*relaycommon.RelayInfo, *dto.Usage, string) {
	t.Helper()
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai, UpstreamModelName: "grok-fault-fixture"}}
	info.SetEstimatePromptTokens(999)
	service.BeginStreamAttempt(c, info)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	if liveHTTP {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{body[:len(body)/2], body[len(body)/2:]} {
				_, _ = io.WriteString(w, part)
				w.(http.Flusher).Flush()
			}
		}))
		t.Cleanup(server.Close)
		var err error
		resp, err = server.Client().Get(server.URL)
		require.NoError(t, err)
	}
	if fragmented {
		original := resp.Body
		t.Cleanup(func() { _ = original.Close() })
		resp.Body = io.NopCloser(iotest.OneByteReader(original))
	}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	usage, apiErr := xAIStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	selected := service.FinalizeStreamUsage(c, info, usage)
	require.NotNil(t, selected)
	require.False(t, info.StreamResult.SettlementAttempted)
	require.Same(t, selected, service.FinalizeStreamUsage(c, info, nil))
	return info, selected, rec.Body.String()
}

func TestForkXAIVendorDeliveryUsageMatrix(t *testing.T) {

	for _, terminal := range []bool{false, true} {
		for _, content := range []bool{false, true} {
			for _, confirmed := range []bool{false, true} {
				t.Run(fmt.Sprintf("terminal=%t/content=%t/usage=%t", terminal, content, confirmed), func(t *testing.T) {
					delta := map[string]any{}
					if content {
						delta["content"] = "hello"
					}
					choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
					if terminal {
						choice["finish_reason"] = "stop"
					}
					frame := map[string]any{"id": "fixture", "model": "grok", "choices": []any{choice}}
					if confirmed {
						frame["usage"] = map[string]int{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
					}
					encoded, err := common.Marshal(frame)
					require.NoError(t, err)
					body := "data: " + string(encoded) + "\n\n"
					if terminal {
						body += "data: [DONE]\n\n"
					}
					info, usage, downstream := forkXAIFaultStream(t, body, false, false)
					wantSource := "estimated"
					if confirmed {
						wantSource = "upstream"
					}
					if !terminal && !content {
						wantSource = "none"
					}
					require.Equal(t, !terminal, info.StreamResult.Failed)
					require.Equal(t, content, info.StreamResult.EffectiveContent)
					require.Equal(t, wantSource, info.StreamResult.UsageSource)
					require.Equal(t, terminal, info.StreamResult.Diagnostic.UsageFinal)
					if wantSource == "upstream" {
						require.Equal(t, 100, usage.PromptTokens)
						require.Equal(t, 20, usage.CompletionTokens)
					}
					if wantSource == "none" {
						require.Zero(t, usage.TotalTokens)
					}
					require.GreaterOrEqual(t, usage.PromptTokens, 0)
					require.GreaterOrEqual(t, usage.CompletionTokens, 0)
					if !terminal {
						require.Equal(t, 1, strings.Count(downstream, "event: error"))
						require.NotContains(t, downstream, "[DONE]")
					}
				})
			}
		}
	}

}

func TestForkXAIVendorFaultShapes(t *testing.T) {
	const usagePrefix = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":0,\"total_tokens\":5}}\n\n"
	for _, tc := range []struct {
		name, body                              string
		failed, effective, fragmented, liveHTTP bool
		input                                   int
	}{
		{"normal cache reasoning", forkXAIUsageStart + forkXAIUsageEnd + "data: [DONE]\n\n", false, true, false, false, 100},
		{"length finish", forkXAIUsageStart + strings.Replace(forkXAIUsageEnd, `"stop"`, `"length"`, 1) + "data: [DONE]\n\n", false, true, false, false, 100},
		{"content filter", strings.Replace(forkXAIUsageEnd, `"stop"`, `"content_filter"`, 1) + "data: [DONE]\n\n", false, false, false, false, 100},
		{"usage only EOF", usagePrefix, true, false, false, false, 0},
		{"negative usage", usagePrefix + "data: {\"usage\":{\"prompt_tokens\":-1}}\n\n", true, false, false, false, 0},
		{"usage array", usagePrefix + "data: {\"usage\":[]}\n\n", true, false, false, false, 0},
		{"negative detail batch", usagePrefix + "data: {\"usage\":{\"prompt_tokens\":999,\"prompt_tokens_details\":{\"cached_tokens\":-1}}}\n\n", true, false, false, false, 0},
		{"bad JSON", usagePrefix + "data: {broken}\n\n", true, false, false, false, 0},
		{"upstream error", usagePrefix + "data: {\"error\":{\"message\":\"fixture unavailable\",\"type\":\"server_error\"}}\n\n", true, false, false, false, 0},
		{"truncated SSE", usagePrefix + "data: {\"choices\":[", true, false, false, false, 0},
		{"HTTP byte split UTF8", strings.Replace(forkXAIUsageStart, "hello", "你好🌐", 1) + forkXAIUsageEnd + "data: [DONE]\n\n", false, true, true, true, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, usage, downstream := forkXAIFaultStream(t, tc.body, tc.fragmented, tc.liveHTTP)
			require.Equal(t, tc.failed, info.StreamResult.Failed)
			require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.input, usage.PromptTokens)
			if tc.failed {
				require.Equal(t, 1, strings.Count(downstream, `"error"`))
				require.NotContains(t, downstream, "[DONE]")
			}
			if tc.fragmented {
				require.Contains(t, downstream, "你好🌐")
			}
			if tc.input == 100 {
				require.Equal(t, 20, usage.CompletionTokens)
				require.Equal(t, 90, usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 7, usage.CompletionTokenDetails.ReasoningTokens)
			}
		})
	}
}

func TestForkXAIVendorExplicitZeroInputs(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		for _, explicitZero := range []bool{false, true} {
			t.Run(fmt.Sprintf("terminal=%t/explicit_zero=%t", terminal, explicitZero), func(t *testing.T) {
				frame := `{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":`
				if terminal {
					frame += `"stop"`
				} else {
					frame += `null`
				}
				frame += `}],"usage":{"completion_tokens":20,"total_tokens":20`
				if explicitZero {
					frame += `,"prompt_tokens":0`
				}
				frame += `}}`
				body := "data: " + frame + "\n\n"
				if terminal {
					body += "data: [DONE]\n\n"
				}
				info, usage, _ := forkXAIFaultStream(t, body, false, false)
				require.Equal(t, !terminal, info.StreamResult.Failed)
				wantInput := 0 // The xAI adapter retains its upstream arithmetic on normal completion.
				if !terminal && !explicitZero {
					wantInput = 999
				}
				require.Equal(t, wantInput, usage.PromptTokens)
				_, present := info.StreamResult.Diagnostic.UsageEvidence["input_tokens"]
				require.Equal(t, explicitZero, present)
			})
		}
	}
}
