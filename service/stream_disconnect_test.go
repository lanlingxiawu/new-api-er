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

// Cancellation must retain upstream content even when no downstream flush succeeded.
func TestStreamDisconnectUsage(t *testing.T) {
	for _, tc := range []struct {
		name, evidence, source string
		response, writeFailure bool
		input, output          int
	}{
		{"empty", "", "none", false, false, 0, 0},
		{"received", "", "estimated", true, false, 23, -1},
		{"write failure", "", "estimated", true, true, 23, -1},
		{"confirmed", `{"usage":{"prompt_tokens":12,"completion_tokens":7}}`, "upstream", true, false, 12, 7},
		{"confirmed zero", `{"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "mixed", true, true, 0, -1},
		{"partial", `{"usage":{"prompt_tokens":12}}`, "mixed", true, true, 12, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			info.SetEstimatePromptTokens(23)
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			if tc.response {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"index":0,"delta":{"content":"hello world"}}]}`)))
			}
			if tc.evidence != "" {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(tc.evidence)))
			}
			if tc.writeFailure {
				info.StreamSession.ClientFailed(io.ErrClosedPipe)
			} else {
				cancel()
			}
			u := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.input, u.PromptTokens)
			if tc.output < 0 {
				require.Equal(t, EstimateTokenByModel("gpt-4o", "hello world"), u.CompletionTokens)
			} else {
				require.Equal(t, tc.output, u.CompletionTokens)
			}
			require.False(t, info.StreamResult.EffectiveContent)
			require.Same(t, u, FinalizeStreamUsage(c, info, nil))
		})
	}
}

func TestStreamDisconnectPingAndUpstreamFailure(t *testing.T) {
	for _, client := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		info.SetEstimatePromptTokens(23)
		BeginStreamAttempt(c, info)
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"ping"}`)))
		if client {
			cancel()
		} else {
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"received but not delivered"}}]}`)))
			info.StreamSession.Fail("upstream_error", io.ErrUnexpectedEOF)
		}
		u := FinalizeStreamUsage(c, info, nil)
		require.Zero(t, u.TotalTokens)
		require.Equal(t, "none", info.StreamResult.UsageSource)
	}
}

func TestDisconnectEstimateSettlesOnceWithCostQuota(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	funding := &claudeSettlementFixture{}
	info := &relaycommon.RelayInfo{Billing: funding, IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.PriceData.ModelRatio = 1
	info.PriceData.CompletionRatio = 1
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	info.SetEstimatePromptTokens(23)
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"hello"}}]}`)))
	info.StreamSession.ClientFailed(io.ErrClosedPipe)
	u := FinalizeStreamUsage(c, info, nil)
	summary := calculateTextQuotaSummary(c, info, u)
	require.Positive(t, summary.Quota)
	require.Positive(t, summary.LedgerQuota)
	params := ConsumptionSettlementParams{Quota: summary.Quota, LedgerQuota: summary.LedgerQuota, CountUsage: summary.hasBillableUsage()}
	require.True(t, settleStreamQuota(c, info, &params))
	require.False(t, settleStreamQuota(c, info, &params))
	require.Equal(t, 1, funding.calls)
	require.Equal(t, summary.Quota, funding.actual)
	require.Equal(t, summary.LedgerQuota, params.LedgerQuota)
}
