package gemini

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Both worktrees use this exact metadata and response body; no provider call.
const forkGeminiUsageFrame = "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":100,\"toolUsePromptTokenCount\":30,\"candidatesTokenCount\":20,\"thoughtsTokenCount\":7,\"totalTokenCount\":157,\"cachedContentTokenCount\":40,\"promptTokensDetails\":[{\"modality\":\"TEXT\",\"tokenCount\":60},{\"modality\":\"AUDIO\",\"tokenCount\":40}],\"toolUsePromptTokensDetails\":[{\"modality\":\"TEXT\",\"tokenCount\":30}],\"candidatesTokensDetails\":[{\"modality\":\"TEXT\",\"tokenCount\":12},{\"modality\":\"AUDIO\",\"tokenCount\":8}]}}\n\n"

func TestForkUpstreamGeminiUsageAudit(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name, body, wantPrompt, wantAI, wantAO := "complete", forkGeminiUsageFrame, 130, 40, 8
		if !complete {
			name, body = "incomplete", strings.Replace(forkGeminiUsageFrame, `,"finishReason":"STOP"`, "", 1)
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini:streamGenerateContent", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatGemini, FinalRequestRelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-audit"}}
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			adapterUsage, apiErr := GeminiTextGenerationStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 130, adapterUsage.PromptTokens)
			require.Equal(t, 40, adapterUsage.PromptTokensDetails.AudioTokens)
			require.Equal(t, 8, adapterUsage.CompletionTokenDetails.AudioTokens)
			selected := service.FinalizeStreamUsage(c, info, adapterUsage)
			if selected.BillingUsage != nil {
				var ok bool
				selected, ok = selected.BillingUsage.CanonicalUsage()
				require.True(t, ok)
			}
			require.Equal(t, wantPrompt, selected.PromptTokens)
			require.Equal(t, 27, selected.CompletionTokens)
			require.Equal(t, 40, selected.PromptTokensDetails.CachedTokens)
			require.Equal(t, 7, selected.CompletionTokenDetails.ReasoningTokens)
			require.Equal(t, wantAI, selected.PromptTokensDetails.AudioTokens)
			require.Equal(t, wantAO, selected.CompletionTokenDetails.AudioTokens)
			require.Equal(t, !complete, info.StreamResult.Failed)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, "upstream", info.StreamResult.UsageSource)
			require.False(t, info.StreamResult.SettlementAttempted)
			params := service.BuildTieredTokenParams(selected, false, map[string]bool{"p": true, "c": true, "cr": true, "ai": true, "ao": true})
			require.Equal(t, float64(wantPrompt-40-wantAI), params.P)
			require.Equal(t, float64(wantAI), params.AI)
			require.Equal(t, float64(wantAO), params.AO)
			t.Logf("fixture_sha256=%x bytes=%d complete=%t adapter_input=%d selected_input=%d output=%d cache=%d reasoning=%d audio_input=%d audio_output=%d p=%g c=%g cr=%g ai=%g ao=%g end_reason=%s", sha256.Sum256([]byte(body)), len(body), complete, adapterUsage.PromptTokens, selected.PromptTokens, selected.CompletionTokens, selected.PromptTokensDetails.CachedTokens, selected.CompletionTokenDetails.ReasoningTokens, selected.PromptTokensDetails.AudioTokens, selected.CompletionTokenDetails.AudioTokens, params.P, params.C, params.CR, params.AI, params.AO, info.StreamStatus.EndReason)
		})
	}
}
