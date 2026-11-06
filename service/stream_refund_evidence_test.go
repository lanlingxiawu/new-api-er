package service

import (
	"context"
	"errors"
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

// Upstream usage is evidence, not proof of delivery or an upstream invoice.
func TestUpstreamFailureWithoutDeliveryRefundsConfirmedUsage(t *testing.T) {
	for _, reason := range []string{"upstream_incomplete", "upstream_error", "upstream_read_error", "upstream_json_error", "upstream_protocol_error"} {
		for _, lateCancel := range []bool{false, true} {
			t.Run(reason+map[bool]string{false: "", true: "/late_cancel"}[lateCancel], func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, UserQuota: 1_000_000}
				info.SetEstimatePromptTokens(999)
				info.PriceData.ModelRatio = 1
				info.PriceData.CompletionRatio = 1
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3}}`)))
				cause := errors.New("synthetic upstream failure")
				if reason == "upstream_incomplete" {
					info.StreamSession.EndRead(io.EOF)
				} else {
					info.StreamSession.Fail(relaycommon.StreamEndReason(reason), cause)
				}
				if lateCancel {
					cancel()
				}
				usage := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
				require.True(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.ClientGone)
				require.False(t, info.StreamResult.EffectiveContent)
				require.True(t, info.StreamResult.ConfirmedUsage)
				require.Equal(t, "none", info.StreamResult.UsageSource)
				require.Zero(t, usage.TotalTokens)
				require.Zero(t, usage.PromptTokens)
				require.Zero(t, usage.CompletionTokens)
				require.Equal(t, 20, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
				require.Equal(t, 3, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
				delivery := info.StreamResult.Diagnostic.DeliveryEvidence
				require.NotNil(t, delivery)
				require.True(t, delivery.Accepted)
				require.True(t, delivery.ReceivedResponse)
				require.False(t, delivery.ProtocolComplete)
				require.False(t, delivery.Effective)
				require.Zero(t, delivery.DeliveredOutputEstimate)
				require.Same(t, usage, FinalizeStreamUsage(c, info, nil))
				summary := calculateTextQuotaSummary(c, info, usage)
				require.Zero(t, summary.Quota)
				funding := &claudeSettlementFixture{}
				info.Billing = funding
				params := ConsumptionSettlementParams{Quota: summary.Quota}
				require.True(t, settleStreamQuota(c, info, &params))
				require.False(t, settleStreamQuota(c, info, &params))
				require.Equal(t, 1, funding.calls)
				require.Zero(t, funding.actual)
				require.Equal(t, "released", info.StreamResult.SettlementState)
				require.Equal(t, 100, info.StreamResult.ReservedQuota)
			})
		}
	}
}

// A length cutoff before any tool arguments is not a completed call. Confirmed
// usage stays as evidence and the reservation is released. A flushed, complete
// call remains billable after a later upstream failure.
func TestTruncatedToolNameDoesNotBecomeDelivery(t *testing.T) {
	const usage = `{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3}}`
	const nameOnly = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup"}}]}}]}`
	const lengthStop = `{"choices":[{"index":0,"finish_reason":"length"}]}`
	const completeCall = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`

	for _, tc := range []struct {
		name      string
		frames    []string
		effective bool
		source    string
	}{
		{"name then length", []string{nameOnly, lengthStop}, false, "none"},
		{"complete empty arguments", []string{completeCall}, true, "upstream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, UserQuota: 1_000_000}
			info.SetEstimatePromptTokens(999)
			info.PriceData.ModelRatio = 1
			info.PriceData.CompletionRatio = 1
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(usage)))
			info.StreamWriter.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range tc.frames {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
				_, err := info.StreamWriter.Write([]byte("data: " + frame + "\n\n"))
				require.NoError(t, err)
			}
			require.NoError(t, info.StreamWriter.FlushError())
			info.StreamSession.Fail("upstream_read_error", errors.New("fixture read failed"))

			usageResult := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
			require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
			require.True(t, info.StreamResult.ConfirmedUsage)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, 20, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
			require.Equal(t, 3, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			delivery := info.StreamResult.Diagnostic.DeliveryEvidence
			require.NotNil(t, delivery)
			require.Equal(t, tc.effective, delivery.Effective)
			require.Positive(t, delivery.ReceivedOutputEstimate)
			if tc.effective {
				require.Positive(t, usageResult.TotalTokens)
				require.Positive(t, delivery.DeliveredOutputEstimate)
			} else {
				require.Zero(t, usageResult.TotalTokens)
				require.Zero(t, delivery.DeliveredOutputEstimate)
			}
			summary := calculateTextQuotaSummary(c, info, usageResult)
			funding := &claudeSettlementFixture{}
			info.Billing = funding
			params := ConsumptionSettlementParams{Quota: summary.Quota}
			require.True(t, settleStreamQuota(c, info, &params))
			require.Equal(t, summary.Quota, info.StreamResult.IntendedQuota)
			require.Equal(t, 100, info.StreamResult.ReservedQuota)
			if tc.effective {
				require.Positive(t, funding.actual)
				require.Equal(t, "settled", info.StreamResult.SettlementState)
			} else {
				require.Zero(t, funding.actual)
				require.Equal(t, "released", info.StreamResult.SettlementState)
			}
		})
	}
}
