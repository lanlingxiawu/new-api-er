package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// No provider or database: the real settlement/log boundary gets mock funding,
// disabled usage counters and an isolated no-op accounting continuation.
func forkZeroDiagnosticAccounting(t *testing.T) {
	t.Helper()
	model.DrainRelayLogsSync(time.Second)
	model.RegisterRelayLogAccountingHandler(func(model.RelayLogAccountingPayload, int) {})
	t.Cleanup(func() {
		model.DrainRelayLogsSync(time.Second)
		model.RegisterRelayLogAccountingHandler(func(p model.RelayLogAccountingPayload, logID int) {
			costCommissionSnapshot{UserID: p.UserID, ChannelID: p.ChannelID, ChannelName: p.ChannelName, UsingGroup: p.UsingGroup, OriginModelName: p.OriginModelName, GroupRatio: p.GroupRatio, Quota: p.Quota, BaseQuota: p.BaseQuota}.record(logID)
		})
	})
}

func forkZeroDiagnosticContext(t *testing.T, status int, capture bool) (*gin.Context, *relaycommon.RelayInfo, context.CancelFunc) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"}, UserQuota: 1_000_000_000}
	info.SetEstimatePromptTokens(375867)
	BeginStreamAttempt(c, info)
	if status == 0 {
		return c, info, cancel
	}
	// Padding demonstrates the existing bounded head/tail capture, without
	// interpreting or saving any real user's response.
	body := strings.Repeat("synthetic-private-head|", 80) + strings.Repeat("synthetic-private-tail|", 80)
	resp := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Test-Private": {"bounded-header"}}, Body: io.NopCloser(strings.NewReader(body))}
	info.StreamSession.ObserveTransport(resp, nil)
	if capture {
		info.StreamDiagnostic.Observe(resp)
		_, err := io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
	} else {
		info.StreamDiagnostic = nil
		c.Set(relaycommon.StreamResponseCaptureKey, (*relaycommon.StreamResponseCapture)(nil))
	}
	require.NoError(t, resp.Body.Close())
	return c, info, cancel
}

func TestForkZeroOutputDiagnosticSettlement(t *testing.T) {
	forkZeroDiagnosticAccounting(t)

	for _, kind := range []string{"client_missing", "client_explicit_zero", "normal_zero", "normal_positive", "content_zero", "timeout", "upstream_error", "local_error", "refund", "refund_positive", "capture_absent", "capture_absent_upstream"} {
		t.Run(kind, func(t *testing.T) {
			capture := !strings.HasPrefix(kind, "capture_absent")
			c, info, cancel := forkZeroDiagnosticContext(t, 200, capture)
			defer cancel()
			if kind != "client_missing" && kind != "local_error" {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.created","response":{"id":"resp_zero","status":"in_progress","usage":{"input_tokens":21,"output_tokens":0}}}`)))
			}
			switch kind {
			case "client_missing", "client_explicit_zero", "refund", "refund_positive":
				cancel()
			case "timeout":
				common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
				cancel()
			case "upstream_error", "capture_absent_upstream":
				info.StreamSession.Fail("upstream_error", errors.New("synthetic upstream error"))
			case "local_error":
				info.StreamSession.FailRelay("response_conversion_error", errors.New("synthetic conversion error"))
			default:
				info.StreamSession.Complete()
			}
			original := &dto.Usage{PromptTokens: 21}
			if kind == "normal_positive" {
				original.CompletionTokens = 7
			}
			usage := FinalizeStreamUsage(c, info, original)
			if kind == "content_zero" {
				// The log boundary must also retain a zero-token row with
				// effective non-token content (e.g. a completed media item).
				info.StreamResult.EffectiveContent = true
			}
			reason := info.StreamStatus.EndReason
			failed, clientGone, source := info.StreamResult.Failed, info.StreamResult.ClientGone, info.StreamResult.UsageSource
			beforeUsage, err := common.Marshal(usage)
			require.NoError(t, err)
			funding := &claudeSettlementFixture{}
			info.Billing = funding
			other := model.NewLogOther()
			quota := 27
			if kind == "refund" || kind == "refund_positive" || kind == "normal_zero" || kind == "timeout" {
				quota = 0
			}
			params := ConsumptionSettlementParams{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, Quota: quota, IsStream: true, Other: other}
			if kind == "refund_positive" {
				params.CompletionTokens = 7 // RecordErrorLog stores zero, regardless of this candidate.
			}
			FinalizeConsumptionSettlement(c, info, params)
			FinalizeConsumptionSettlement(c, info, params)
			require.Equal(t, 1, funding.calls)
			require.Equal(t, quota, funding.actual)
			require.Equal(t, reason, info.StreamStatus.EndReason)
			require.Equal(t, failed, info.StreamResult.Failed)
			require.Equal(t, clientGone, info.StreamResult.ClientGone)
			require.Equal(t, source, info.StreamResult.UsageSource)
			afterUsage, err := common.Marshal(usage)
			require.NoError(t, err)
			require.Equal(t, beforeUsage, afterUsage, "diagnostics must not change any usage")
			want := kind == "upstream_error" || kind == "capture_absent_upstream" || kind == "timeout" || kind != "normal_positive" && capture
			fields := other.Snapshot()
			require.Equal(t, want, fields["stream_diagnostic_available"] == true)
			diagnostic := fields["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			if want && capture {
				require.Len(t, diagnostic.BodyHead, 1024)
				require.Len(t, diagnostic.BodyTail, 1024)
				require.NotNil(t, diagnostic.DownstreamBodyBase64)
			} else {
				require.Empty(t, diagnostic.BodyHead)
				require.Empty(t, diagnostic.ResponseHeaders)
				require.Nil(t, diagnostic.DownstreamBodyBase64)
			}
			if kind == "client_missing" {
				require.Equal(t, relaycommon.StreamEndReasonClientGone, reason)
				require.False(t, info.StreamResult.EffectiveContent)
				require.Equal(t, 375867, usage.PromptTokens)
				require.Zero(t, usage.CompletionTokens)
				require.Zero(t, usage.PromptTokensDetails.CachedTokens)
				require.Empty(t, diagnostic.UsageEvidence)
				require.Equal(t, map[string]int{"input_tokens": 375867, "output_tokens": 0}, diagnostic.EstimatedUsage)
				require.False(t, diagnostic.UsageFinal)
				require.Equal(t, context.Canceled.Error(), diagnostic.Error)
			}
			if kind == "client_explicit_zero" {
				require.Equal(t, 0, diagnostic.UsageEvidence["output_tokens"])
				require.Contains(t, diagnostic.UsageEvidence, "output_tokens")
			}
			// The ordinary log/exports must never expose private data.
			row := model.Log{Other: other.JSONString()}
			require.NoError(t, row.AfterFind(nil))
			require.NotContains(t, row.Other, "synthetic-private")
			if len(diagnostic.BodyHead) > 0 {
				require.NotContains(t, row.Other, base64.StdEncoding.EncodeToString(diagnostic.BodyHead))
			}
			require.NotContains(t, row.Other, `"stream_diagnostic":`)
			if want {
				AppendStreamErrorDiagnostic(c, other, errors.New("later error attachment"))
				later := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
				require.True(t, other.Snapshot()["stream_diagnostic_available"] == true)
				require.Equal(t, diagnostic.UsageEvidence, later.UsageEvidence)
				require.Equal(t, diagnostic.EstimatedUsage, later.EstimatedUsage)
				require.Equal(t, diagnostic.UsageFinal, later.UsageFinal)
				require.Equal(t, diagnostic.Error, later.Error)
			}
		})
	}

}

func TestForkZeroOutputDiagnosticErrorBoundary(t *testing.T) {

	for _, kind := range []string{"client", "local", "nil_error", "capture_absent", "http_rejected", "pre_response"} {
		t.Run(kind, func(t *testing.T) {
			status := 200
			if kind == "http_rejected" {
				status = 503
			}
			if kind == "pre_response" {
				status = 0
			}
			c, info, cancel := forkZeroDiagnosticContext(t, status, kind != "capture_absent")
			defer cancel()
			var err error = errors.New("synthetic local failure")
			if kind == "client" {
				cancel()
				err = context.Canceled
			}
			if kind == "nil_error" {
				err = nil
			}
			other := model.NewLogOther()
			AppendStreamErrorDiagnostic(c, other, err)
			want := (kind == "client" || kind == "local")
			require.Equal(t, want, other.Snapshot()["stream_diagnostic_available"] == true)
			if kind == "http_rejected" || kind == "pre_response" {
				require.NotContains(t, other.Snapshot(), "stream_diagnostic")
				return
			}
			if want {
				diagnostic := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
				require.NotEmpty(t, diagnostic.BodyHead)
				require.Equal(t, err.Error(), diagnostic.Error)
			}
			require.Nil(t, info.StreamResult, "an error diagnostic must not activate settlement")
		})
	}

}

func TestForkZeroOutputDiagnosticFundingBoundary(t *testing.T) {
	forkZeroDiagnosticAccounting(t)

	for _, committed := range []bool{true, false} {
		t.Run(fmt.Sprintf("committed_%t", committed), func(t *testing.T) {
			c, info, cancel := forkZeroDiagnosticContext(t, 200, true)
			defer cancel()
			info.StreamSession.Complete()
			FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 21, CompletionTokens: 7})
			funding := &claudeSettlementFixture{err: errors.New("synthetic settlement failure"), committed: committed}
			info.Billing = funding
			other := model.NewLogOther()
			params := ConsumptionSettlementParams{PromptTokens: 21, CompletionTokens: 7, Quota: 27, IsStream: true, Other: other}
			FinalizeConsumptionSettlement(c, info, params)
			FinalizeConsumptionSettlement(c, info, params)
			require.Equal(t, 1, funding.calls)
			require.Equal(t, 27, funding.actual)
			state := "failed"
			if committed {
				state = "partial"
			}
			require.Equal(t, state, info.StreamResult.SettlementState)
			require.Equal(t, 27, info.StreamResult.IntendedQuota)
			// Committed positive-output consumption remains filtered. Only the
			// failed-funding error row stores zero output and gains eligibility.
			want := !committed
			require.Equal(t, want, other.Snapshot()["stream_diagnostic_available"] == true)
			diagnostic := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			require.Equal(t, want, len(diagnostic.BodyHead) > 0)
			require.Equal(t, funding.err.Error(), diagnostic.SettlementError)
		})
	}

}

func TestForkZeroOutputDiagnosticIgnoresPreviousAttempt(t *testing.T) {
	c, info, cancel := forkZeroDiagnosticContext(t, 200, true)
	defer cancel()
	other := model.NewLogOther()
	other.SetPublic("stream_result", &relaycommon.StreamOutcome{DiagnosticAvailable: true, Diagnostic: relaycommon.StreamDiagnostic{
		Attempt:       c.GetInt(relaycommon.StreamDiagnosticAttemptKey) - 1,
		UsageEvidence: map[string]int{"output_tokens": 123},
		Error:         "previous attempt cause",
	}})
	AppendStreamErrorDiagnostic(c, other, errors.New("current attempt cause"))
	diagnostic := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	require.Equal(t, c.GetInt(relaycommon.StreamDiagnosticAttemptKey), diagnostic.Attempt)
	require.Equal(t, "current attempt cause", diagnostic.Error)
	require.Empty(t, diagnostic.UsageEvidence)
	require.Nil(t, info.StreamResult)
}
