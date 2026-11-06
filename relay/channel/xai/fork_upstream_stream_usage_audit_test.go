package xai

import (
	"crypto/sha256"
	"io"
	"maps"
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
)

// This fixture is copied byte-for-byte into the upstream worktree audit.
const forkXAIUsageStart = "data: {\"id\":\"audit\",\"model\":\"grok-audit\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"
const forkXAIUsageEnd = "data: {\"id\":\"audit\",\"model\":\"grok-audit\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,\"prompt_tokens_details\":{\"cached_tokens\":90},\"completion_tokens_details\":{\"reasoning_tokens\":7}}}\n\n"

// Characterize the current adapter and actual managed finalization; no balances
// are settled and no database or external provider is involved.
func TestForkUpstreamXAIUsageAudit(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name, body, wantCache, wantReasoning := "complete", forkXAIUsageStart+forkXAIUsageEnd+"data: [DONE]\n\n", 90, 7
		if !complete {
			name, body, wantCache, wantReasoning = "incomplete", forkXAIUsageStart+strings.Replace(forkXAIUsageEnd, `"finish_reason":"stop"`, `"finish_reason":null`, 1), 90, 7
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai, UpstreamModelName: "grok-audit"}}
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			adapterUsage, apiErr := xAIStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 90, adapterUsage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 7, adapterUsage.CompletionTokenDetails.ReasoningTokens)
			selected := service.FinalizeStreamUsage(c, info, adapterUsage)
			require.Equal(t, 100, selected.PromptTokens)
			require.Equal(t, 20, selected.CompletionTokens)
			require.Equal(t, wantCache, selected.PromptTokensDetails.CachedTokens)
			require.Equal(t, wantReasoning, selected.CompletionTokenDetails.ReasoningTokens)
			require.Equal(t, 90, info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"])
			require.Equal(t, 7, info.StreamResult.Diagnostic.UsageEvidence["reasoning_tokens"])
			require.Equal(t, !complete, info.StreamResult.Failed)
			require.Equal(t, "upstream", info.StreamResult.UsageSource)
			require.True(t, info.StreamResult.EffectiveContent)
			require.False(t, info.StreamResult.SettlementAttempted)
			params := service.BuildTieredTokenParams(selected, false, map[string]bool{"p": true, "c": true, "cr": true})
			require.Equal(t, float64(100-wantCache), params.P)
			require.Equal(t, float64(wantCache), params.CR)
			t.Logf("fixture_sha256=%x bytes=%d complete=%t adapter_cache=%d adapter_reasoning=%d selected_cache=%d selected_reasoning=%d evidence_cache=%d evidence_reasoning=%d p=%g c=%g cr=%g end_reason=%s source=%s", sha256.Sum256([]byte(body)), len(body), complete, adapterUsage.PromptTokensDetails.CachedTokens, adapterUsage.CompletionTokenDetails.ReasoningTokens, selected.PromptTokensDetails.CachedTokens, selected.CompletionTokenDetails.ReasoningTokens, info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"], info.StreamResult.Diagnostic.UsageEvidence["reasoning_tokens"], params.P, params.C, params.CR, info.StreamStatus.EndReason, info.StreamResult.UsageSource)
		})
	}
}

func TestForkXAIStreamPartialUsageDetails(t *testing.T) {
	const initialUsage = `{"prompt_tokens":1000,"completion_tokens":193,"total_tokens":1200,"prompt_tokens_details":{"cached_tokens":90,"cached_creation_tokens":10,"cache_write_tokens":20,"cache_creation_input_tokens":30,"text_tokens":800,"audio_tokens":100,"image_tokens":100,"cached_tokens_details":{"text_tokens":60,"audio_tokens":20,"image_tokens":10},"cache_write_tokens_details":{"ttl_30m_tokens":4,"ttl_12h_tokens":16}},"completion_tokens_details":{"text_tokens":150,"audio_tokens":20,"image_tokens":30,"reasoning_tokens":7}}`
	initialCounts := map[string]int{
		"cache": 90, "creation": 10, "write": 20, "creation_input": 30,
		"input_text": 800, "input_audio": 100, "input_image": 100,
		"cache_text": 60, "cache_audio": 20, "cache_image": 10, "ttl_30m": 4, "ttl_12h": 16,
		"output_text": 150, "output_audio": 20, "output_image": 30, "reasoning": 7,
	}

	for _, tc := range []struct {
		name, details string
		changes       map[string]int
	}{
		{"omitted", "", nil},
		{"null objects", `,"prompt_tokens_details":null,"completion_tokens_details":null`, nil},
		{"empty objects", `,"prompt_tokens_details":{},"completion_tokens_details":{}`, nil},
		{"partial scalar zero", `,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}`, map[string]int{"cache": 0, "reasoning": 0}},
		{"partial scalar update", `,"prompt_tokens_details":{"audio_tokens":40},"completion_tokens_details":{"text_tokens":140}`, map[string]int{"input_audio": 40, "output_text": 140}},
		{"partial nested zero and null", `,"prompt_tokens_details":{"cached_tokens_details":{"image_tokens":0,"audio_tokens":null},"cache_write_tokens_details":{"ttl_12h_tokens":0}}`, map[string]int{"cache_image": 0, "ttl_12h": 0}},
		{"null nested objects", `,"prompt_tokens_details":{"cached_tokens_details":null,"cache_write_tokens_details":null}`, nil},
		{"empty nested objects", `,"prompt_tokens_details":{"cached_tokens_details":{},"cache_write_tokens_details":{}}`, nil},
		{"all explicit zeros", `,"prompt_tokens_details":{"cached_tokens":0,"cached_creation_tokens":0,"cache_write_tokens":0,"cache_creation_input_tokens":0,"text_tokens":0,"audio_tokens":0,"image_tokens":0,"cached_tokens_details":{"text_tokens":0,"audio_tokens":0,"image_tokens":0},"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":0}},"completion_tokens_details":{"text_tokens":0,"audio_tokens":0,"image_tokens":0,"reasoning_tokens":0}`, map[string]int{"cache": 0, "creation": 0, "write": 0, "creation_input": 0, "input_text": 0, "input_audio": 0, "input_image": 0, "cache_text": 0, "cache_audio": 0, "cache_image": 0, "ttl_30m": 0, "ttl_12h": 0, "output_text": 0, "output_audio": 0, "output_image": 0, "reasoning": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai, UpstreamModelName: "grok-audit"}}
			service.BeginStreamAttempt(c, info)
			body := forkXAIUsageStart + `data: {"id":"audit","choices":[],"usage":` + initialUsage + "}\n\n" +
				`data: {"id":"audit","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":193,"total_tokens":1200` + tc.details + "}}\n\ndata: [DONE]\n\n"
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			adapterUsage, apiErr := xAIStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			selected := service.FinalizeStreamUsage(c, info, adapterUsage)
			require.False(t, info.StreamResult.Failed)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, 1000, selected.PromptTokens)
			require.Equal(t, 200, selected.CompletionTokens, "xAI output remains total minus prompt")
			require.Equal(t, 1200, selected.TotalTokens)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "[DONE]"))

			var lastUsage *dto.Usage
			for line := range strings.SplitSeq(rec.Body.String(), "\n") {
				payload, ok := strings.CutPrefix(line, "data: ")
				if !ok || strings.TrimSpace(payload) == "[DONE]" {
					continue
				}
				var chunk dto.ChatCompletionsStreamResponse
				require.NoError(t, common.UnmarshalJsonStr(payload, &chunk))
				if chunk.Usage != nil {
					lastUsage = chunk.Usage
				}
			}
			require.NotNil(t, lastUsage)
			require.Equal(t, selected.PromptTokensDetails, lastUsage.PromptTokensDetails, "client usage must match selected cumulative details")
			require.Equal(t, selected.CompletionTokenDetails, lastUsage.CompletionTokenDetails)
			input, output := selected.PromptTokensDetails, selected.CompletionTokenDetails
			require.NotNil(t, input.CachedTokensDetails)
			require.NotNil(t, input.CachedTokensDetails.TextTokens)
			require.NotNil(t, input.CachedTokensDetails.AudioTokens)
			require.NotNil(t, input.CachedTokensDetails.ImageTokens)
			require.NotNil(t, input.CacheWriteTokensDetails)
			got := map[string]int{
				"cache": input.CachedTokens, "creation": input.CachedCreationTokens, "write": input.CacheWriteTokens, "creation_input": input.CacheCreationInputTokens,
				"input_text": input.TextTokens, "input_audio": input.AudioTokens, "input_image": input.ImageTokens,
				"cache_text": *input.CachedTokensDetails.TextTokens, "cache_audio": *input.CachedTokensDetails.AudioTokens, "cache_image": *input.CachedTokensDetails.ImageTokens,
				"ttl_30m": input.CacheWriteTokensDetails.TTL30mTokens, "ttl_12h": input.CacheWriteTokensDetails.TTL12hTokens,
				"output_text": output.TextTokens, "output_audio": output.AudioTokens, "output_image": output.ImageTokens, "reasoning": output.ReasoningTokens,
			}
			want := maps.Clone(initialCounts)
			maps.Copy(want, tc.changes)
			require.Equal(t, want, got)
			require.Equal(t, want["cache"], info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"])
			require.Equal(t, want["reasoning"], info.StreamResult.Diagnostic.UsageEvidence["reasoning_tokens"])
			params := service.BuildTieredTokenParams(selected, false, map[string]bool{"p": true, "c": true, "cr": true})
			// Unpriced 12h writes retain the existing 2x short-write rate in p.
			require.Equal(t, float64(1000-want["cache"]+want["ttl_12h"]), params.P)
			require.Equal(t, float64(want["cache"]), params.CR)
		})
	}

}
