package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamZeroOutputEstimation(t *testing.T) {
	for _, mode := range []string{"client", "normal", "upstream", "timeout", "no-delivery", "empty", "nonzero"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":15,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":4}}}`)))
			data := []byte(`{"choices":[{"delta":{"content":"hello world"}}]}`)
			if mode != "empty" {
				require.NoError(t, info.StreamSession.ObserveEvent("", data))
				if mode != "client" && mode != "no-delivery" {
					info.StreamSession.CommitDelivery(data)
					info.StreamSession.AddEstimatedOutput(EstimateTokenByModel("gpt-4o", "hello world"))
				}
			}
			switch mode {
			case "normal":
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":15,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":4}}}`)))
			case "upstream", "no-delivery":
				info.StreamSession.Fail("upstream_error", io.ErrUnexpectedEOF)
			case "timeout":
				info.StreamSession.Fail(relaycommon.StreamEndReasonTimeout, context.DeadlineExceeded)
			default:
				if mode == "nonzero" {
					require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"completion_tokens":7}}`)))
				}
				cancel()
			}
			u := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999})
			if mode == "no-delivery" {
				require.Zero(t, u.TotalTokens)
				return
			}
			require.Equal(t, 15, u.PromptTokens)
			require.Equal(t, 4, u.PromptTokensDetails.CachedTokens)
			if mode == "empty" {
				require.Zero(t, u.CompletionTokens)
				return
			}
			if mode == "nonzero" {
				require.Equal(t, 7, u.CompletionTokens)
				return
			}
			require.Equal(t, EstimateTokenByModel("gpt-4o", "hello world"), u.CompletionTokens)
			require.Equal(t, "mixed", info.StreamResult.UsageSource)
			require.Zero(t, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			require.Same(t, u, FinalizeStreamUsage(c, info, nil))
		})
	}
}

func TestStreamZeroOutputPreservesNestedBilling(t *testing.T) {
	for _, format := range []string{"openai", "claude", "gemini"} {
		t.Run(format, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{StreamResult: &relaycommon.StreamOutcome{ConfirmedUsage: true, UsageSource: "upstream"}}
			u := &dto.Usage{PromptTokens: 15}
			u.PromptTokensDetails.CachedTokens = 4
			switch format {
			case "openai":
				u.PromptTokensDetails.AudioTokens = 3
				u.BillingUsage = dto.NewOpenAIChatBillingUsage(u)
			case "claude":
				u.BillingUsage = dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 15, CacheReadInputTokens: 4, CacheCreationInputTokens: 2})
			case "gemini":
				u.BillingUsage = dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 15, CachedContentTokenCount: 4, TotalTokenCount: 15, PromptTokensDetails: []dto.GeminiPromptTokensDetails{{Modality: "AUDIO", TokenCount: 3}}})
			}
			v := SupplementStreamZeroOutput(c, info, u, 8, 0)
			effective := effectiveBillingUsage(v)
			require.Equal(t, 15, effective.PromptTokens)
			require.Equal(t, 8, effective.CompletionTokens)
			require.Equal(t, 4, effective.PromptTokensDetails.CachedTokens)
			if format != "claude" {
				require.Equal(t, 3, effective.PromptTokensDetails.AudioTokens)
			}
			require.Zero(t, effectiveBillingUsage(u).CompletionTokens, "original evidence remains unchanged")
			require.True(t, v.BillingUsage.Estimated)
			require.Equal(t, "mixed", info.StreamResult.UsageSource)
			require.Same(t, v, SupplementStreamZeroOutput(c, info, v, 100, 0), "nonzero output is never overwritten")
		})
	}
}

func TestGeminiIntermediateZeroDisconnect(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":0,"cachedContentTokenCount":4}}`)))
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"candidates":[{"content":{"parts":[{"text":"hello world"}]}}]}`)))
	info.StreamSession.ClientFailed(io.ErrClosedPipe)
	u := FinalizeStreamUsage(c, info, nil)
	require.Equal(t, 15, u.PromptTokens)
	require.Equal(t, 4, u.PromptTokensDetails.CachedTokens)
	require.Equal(t, EstimateTokenByModel("gemini-2.5-flash", "hello world"), u.CompletionTokens)
	require.Equal(t, "mixed", info.StreamResult.UsageSource)
	require.False(t, info.StreamResult.EffectiveContent)
}

func TestSupplementStreamOutputGuardsAndAudio(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	u := &dto.Usage{PromptTokens: 15}
	info := &relaycommon.RelayInfo{}
	require.Same(t, u, SupplementStreamZeroOutput(c, info, u, 8, 0))
	info.StreamResult = &relaycommon.StreamOutcome{UsageSource: "none"}
	require.Same(t, u, SupplementStreamZeroOutput(c, info, u, 8, 0))
	info.StreamResult = &relaycommon.StreamOutcome{UsageSource: "upstream", ConfirmedUsage: true}
	require.Nil(t, SupplementStreamZeroOutput(c, info, nil, 8, 0))
	require.Same(t, u, SupplementStreamZeroOutput(c, info, u, 0, 0))
	u.BillingUsage = dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 15, CompletionTokens: 7})
	require.Same(t, u, SupplementStreamZeroOutput(c, info, u, 99, 0), "nested positive usage takes precedence")
	u.BillingUsage = nil
	audio := SupplementStreamZeroOutput(c, info, u, 0, 8)
	require.Equal(t, 8, audio.CompletionTokens)
	require.Equal(t, 8, audio.CompletionTokenDetails.AudioTokens)
	require.Zero(t, audio.CompletionTokenDetails.TextTokens)
	require.Equal(t, 15, audio.PromptTokens)
	require.Equal(t, "mixed", info.StreamResult.UsageSource)
}

func TestZeroOutputUsesEffectiveBillingInsteadOfStaleTopLevelEstimate(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{StreamResult: &relaycommon.StreamOutcome{UsageSource: "upstream", ConfirmedUsage: true}}
	u := &dto.Usage{PromptTokens: 999, CompletionTokens: 99, BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 15, CompletionTokens: 0})}
	v := SupplementStreamZeroOutput(c, info, u, 8, 0)
	require.Equal(t, 15, effectiveBillingUsage(v).PromptTokens)
	require.Equal(t, 8, effectiveBillingUsage(v).CompletionTokens)
	require.Equal(t, "mixed", info.StreamResult.UsageSource)
	require.Zero(t, effectiveBillingUsage(u).CompletionTokens)
}
