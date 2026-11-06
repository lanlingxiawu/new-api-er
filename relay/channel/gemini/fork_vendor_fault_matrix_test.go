package gemini

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

// Vendor wire reference: https://aistudio.google.com/api-reference/generate-content
// forkGeminiFaultStream drives the real native adapter and managed accounting
// selection with an in-memory or loopback HTTP vendor response. It never settles.
func forkGeminiFaultStream(t *testing.T, body string, fragmented, liveHTTP, converted bool) (*relaycommon.RelayInfo, *dto.Usage, string) {
	t.Helper()
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini:streamGenerateContent", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatGemini, FinalRequestRelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-fault-fixture"}}
	if converted {
		info.RelayFormat = types.RelayFormatOpenAI
	}
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
	var usage *dto.Usage
	if converted {
		usage, _ = GeminiChatStreamHandler(c, info, resp)
	} else {
		usage, _ = GeminiTextGenerationStreamHandler(c, info, resp)
	}
	selected := service.FinalizeStreamUsage(c, info, usage)
	require.NotNil(t, selected)
	require.False(t, info.StreamResult.SettlementAttempted)
	require.Same(t, selected, service.FinalizeStreamUsage(c, info, nil))
	return info, selected, rec.Body.String()
}

func TestForkGeminiVendorDeliveryUsageMatrix(t *testing.T) {

	for _, terminal := range []bool{false, true} {
		for _, content := range []bool{false, true} {
			for _, confirmed := range []bool{false, true} {
				t.Run(fmt.Sprintf("terminal=%t/content=%t/usage=%t", terminal, content, confirmed), func(t *testing.T) {
					candidate := map[string]any{"index": 0}
					if content {
						candidate["content"] = map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}}
					}
					if terminal {
						candidate["finishReason"] = "STOP"
					}
					frame := map[string]any{"candidates": []any{candidate}}
					if confirmed {
						frame["usageMetadata"] = map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 20, "totalTokenCount": 120}
					}
					encoded, err := common.Marshal(frame)
					require.NoError(t, err)
					info, usage, downstream := forkGeminiFaultStream(t, "data: "+string(encoded)+"\n\n", false, false, false)
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
					}
				})
			}
		}
	}

}

func TestForkGeminiVendorFaultShapes(t *testing.T) {
	const usagePrefix = "data: {\"usageMetadata\":{\"promptTokenCount\":5}}\n\n"
	for _, tc := range []struct {
		name, body                              string
		failed, effective, fragmented, liveHTTP bool
		input                                   int
	}{
		{"stop", forkGeminiUsageFrame, false, true, false, false, 130},
		{"max tokens", strings.Replace(forkGeminiUsageFrame, `"STOP"`, `"MAX_TOKENS"`, 1), false, true, false, false, 130},
		{"safety", "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"SAFETY\"}],\"usageMetadata\":{\"promptTokenCount\":5}}\n\n", false, false, false, false, 5},
		{"prompt blocked", "data: {\"candidates\":[],\"promptFeedback\":{\"blockReason\":\"SAFETY\"},\"usageMetadata\":{\"promptTokenCount\":5}}\n\n", false, false, false, false, 5},
		{"empty candidates EOF", usagePrefix + "data: {\"candidates\":[]}\n\n", true, false, false, false, 0},
		{"negative usage", usagePrefix + "data: {\"usageMetadata\":{\"promptTokenCount\":-1}}\n\n", true, false, false, false, 0},
		{"usage array", usagePrefix + "data: {\"usageMetadata\":[]}\n\n", true, false, false, false, 0},
		{"modality object", usagePrefix + "data: {\"usageMetadata\":{\"promptTokenCount\":999,\"promptTokensDetails\":{}}}\n\n", true, false, false, false, 0},
		{"bad JSON", usagePrefix + "data: {broken}\n\n", true, false, false, false, 0},
		{"upstream error", usagePrefix + "data: {\"error\":{\"code\":503,\"message\":\"fixture unavailable\",\"status\":\"UNAVAILABLE\"}}\n\n", true, false, false, false, 0},
		{"truncated SSE", usagePrefix + "data: {\"candidates\":[", true, false, false, false, 0},
		{"HTTP byte split UTF8", strings.Replace(forkGeminiUsageFrame, "hello", "你好🌐", 1), false, true, true, true, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, usage, downstream := forkGeminiFaultStream(t, tc.body, tc.fragmented, tc.liveHTTP, false)
			require.Equal(t, tc.failed, info.StreamResult.Failed)
			require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.name == "safety" || tc.name == "prompt blocked", info.PerformanceBusinessRejection)
			require.Equal(t, tc.input, usage.PromptTokens)
			if tc.failed {
				require.Equal(t, 1, strings.Count(downstream, `"error"`))
				require.False(t, info.StreamResult.Diagnostic.UsageFinal)
			}
			if tc.fragmented {
				require.Contains(t, downstream, "你好🌐")
			}
			if tc.input == 130 {
				require.Equal(t, 27, usage.CompletionTokens)
				require.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 40, usage.PromptTokensDetails.AudioTokens)
				require.Equal(t, 8, usage.CompletionTokenDetails.AudioTokens)
				require.Equal(t, 7, usage.CompletionTokenDetails.ReasoningTokens)
			}
		})
	}
}

func TestForkGeminiVendorZeroPromptDoesNotInventOutput(t *testing.T) {
	for _, converted := range []bool{false, true} {
		for _, explicitTotal := range []bool{false, true} {
			t.Run(fmt.Sprintf("converted=%t/explicit_total=%t", converted, explicitTotal), func(t *testing.T) {
				body := "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n\n"
				frame := `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0`
				if explicitTotal {
					frame += `,"totalTokenCount":0`
				}
				body += "data: " + frame + "}}\n\n"
				info, usage, _ := forkGeminiFaultStream(t, body, false, false, converted)
				require.False(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.EffectiveContent)
				require.Zero(t, usage.PromptTokens)
				require.Zero(t, usage.CompletionTokens)
				require.Zero(t, usage.TotalTokens)
				if billing, ok := usage.BillingUsage.CanonicalUsage(); ok {
					require.Zero(t, billing.PromptTokens)
					require.Zero(t, billing.CompletionTokens)
				}
			})
		}
	}
}

func TestForkGeminiVendorExplicitZeroInputs(t *testing.T) {

	for _, converted := range []bool{false, true} {
		for _, terminal := range []bool{false, true} {
			for _, tc := range []struct {
				name, prefix, finalInput string
				input, legacy            int
				present                  bool
			}{
				{"explicit zero", "", `,"promptTokenCount":0`, 0, 999, true},
				{"zero overwrites positive", "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n\n", `,"promptTokenCount":0`, 0, 100, true},
				{"omission preserves zero", "data: {\"usageMetadata\":{\"promptTokenCount\":0}}\n\n", "", 0, 999, true},
				{"omission preserves positive", "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n\n", "", 100, 100, true},
				{"missing fallback", "", "", 999, 999, false},
			} {
				t.Run(fmt.Sprintf("converted=%t/terminal=%t/%s", converted, terminal, tc.name), func(t *testing.T) {
					frame := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]}`
					if terminal {
						frame += `,"finishReason":"STOP"`
					}
					frame += `}],"usageMetadata":{"candidatesTokenCount":20` + tc.finalInput + `}}`
					info, usage, _ := forkGeminiFaultStream(t, tc.prefix+"data: "+frame+"\n\n", false, false, converted)
					require.Equal(t, !terminal, info.StreamResult.Failed)
					wantInput := tc.input

					require.Equal(t, wantInput, usage.PromptTokens)
					_, present := info.StreamResult.Diagnostic.UsageEvidence["input_tokens"]
					require.Equal(t, tc.present, present)
				})
			}
		}
	}

}
