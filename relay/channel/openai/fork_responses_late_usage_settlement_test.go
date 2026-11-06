package openai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// The fork's managed delivery and usage-selection layer must be compared with
// the shared upstream handler separately. This fixture never commits funds.
func TestForkCustomToolIncidentSelection(t *testing.T) {
	for _, complete := range []bool{false, true} {
		for _, kind := range []string{"custom_partial", "custom_done", "function_partial", "function_done", "text", "created_only"} {
			t.Run(fmt.Sprintf("%s/complete=%t", kind, complete), func(t *testing.T) {
				c, info, response, body, recorder := forkCustomIncidentFixture(t, kind, complete)
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(response, nil)
				info.StreamDiagnostic.Observe(response)
				info.StreamSession.ObserveHTTP(response)
				usage, apiErr := OaiResponsesStreamHandler(c, info, response)
				require.Nil(t, apiErr)
				usage = service.FinalizeStreamUsage(c, info, usage)
				snapshot := info.StreamSession.Snapshot()
				require.False(t, info.StreamResult.SettlementAttempted)
				require.Contains(t, recorder.Body.String(), `"sequence_number":99`)
				require.Equal(t, complete, body.terminalRead.Load())
				require.Equal(t, complete, snapshot.Complete)
				require.Equal(t, !complete, info.StreamResult.Failed)
				require.Equal(t, !complete, info.StreamResult.ClientGone)
				require.Equal(t, kind == "custom_done" || kind == "function_done" || kind == "text", info.StreamResult.EffectiveContent)
				if complete {
					require.Equal(t, "upstream", info.StreamResult.UsageSource)
					require.Equal(t, 153261, usage.PromptTokens)
					require.Equal(t, 1098, usage.CompletionTokens)
					require.Equal(t, 152986, usage.PromptTokensDetails.CachedTokens)
				} else {
					require.Equal(t, "estimated", info.StreamResult.UsageSource)
					require.Equal(t, 150161, usage.PromptTokens)
					require.Zero(t, usage.PromptTokensDetails.CachedTokens)
					if kind == "created_only" {
						require.Zero(t, usage.CompletionTokens)
					} else {
						require.Positive(t, usage.CompletionTokens)
					}
				}
				if strings.HasPrefix(kind, "custom_") {
					require.Contains(t, recorder.Body.String(), "response.custom_tool_call_input.delta")
					require.Positive(t, snapshot.ReceivedOutput)
					if kind == "custom_done" {
						require.Positive(t, snapshot.EstimatedOutput)
					} else {
						require.Zero(t, snapshot.EstimatedOutput, "partial input is not effective delivery")
					}
				}
				t.Logf("prompt=%d output=%d cached=%d source=%s effective=%t received_estimate=%d written_estimate=%d terminal=%t", usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensDetails.CachedTokens, info.StreamResult.UsageSource, info.StreamResult.EffectiveContent, snapshot.ReceivedOutput, snapshot.EstimatedOutput, snapshot.Complete)
			})
		}
	}
}

type forkLateUsageTimeout string

func (s forkLateUsageTimeout) RelayTimeoutKind() string { return string(s) }

func TestForkResponsesLateUsageSelection(t *testing.T) {
	for _, mode := range []string{"complete", "client_gone", "total_timeout", "response_timeout_before_first_frame", "upstream_eof", "upstream_read_error"} {
		for _, policy := range []string{"refund", "input", "charge"} {
			t.Run(mode+"/"+policy, func(t *testing.T) {
				c, info, response, body := forkLateUsageFixture(t, mode)
				if mode == "total_timeout" || mode == "response_timeout_before_first_frame" {
					kind := "total_timeout"
					if mode == "response_timeout_before_first_frame" {
						kind = "response_timeout"
					}
					// Inject the middleware's timeout result, not a real timer.
					common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, forkLateUsageTimeout(kind))
				}
				common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, policy)
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(response, nil)
				info.StreamDiagnostic.Observe(response)
				info.StreamSession.ObserveHTTP(response)
				usage, apiErr := OaiResponsesStreamHandler(c, info, response)
				require.Nil(t, apiErr)
				usage = service.FinalizeStreamUsage(c, info, usage)
				require.False(t, info.StreamResult.SettlementAttempted, "selection fixture never changes funds")
				if mode == "complete" {
					require.True(t, body.terminalRead)
					require.False(t, info.StreamResult.Failed)
					require.True(t, info.StreamResult.ConfirmedUsage)
					require.Equal(t, 389301, usage.PromptTokens)
					require.Equal(t, 1542, usage.CompletionTokens)
					require.Equal(t, 389298, usage.PromptTokensDetails.CachedTokens)
				} else {
					require.False(t, body.terminalRead)
					require.True(t, info.StreamResult.Failed)
					require.False(t, info.StreamResult.ConfirmedUsage)
					require.False(t, info.StreamResult.EffectiveContent)
					require.Equal(t, mode == "client_gone", info.StreamResult.ClientGone)
					wantPrompt, source := 0, "none"
					if mode == "client_gone" || ((mode == "total_timeout" || mode == "response_timeout_before_first_frame") && policy != "refund") {
						wantPrompt, source = 375867, "estimated"
					}
					require.Equal(t, wantPrompt, usage.PromptTokens)
					require.Equal(t, source, info.StreamResult.UsageSource)
					require.Zero(t, usage.CompletionTokens)
					require.Zero(t, usage.PromptTokensDetails.CachedTokens)
					require.Empty(t, info.StreamResult.Diagnostic.UsageEvidence)
				}
				t.Logf("selected prompt=%d output=%d cached=%d source=%s client_gone=%t end=%s diagnostic=%t", usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensDetails.CachedTokens, info.StreamResult.UsageSource, info.StreamResult.ClientGone, info.StreamStatus.EndReason, info.StreamResult.DiagnosticAvailable)
			})
		}
	}
}
