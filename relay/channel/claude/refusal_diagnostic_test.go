package claude

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// Completed refusals retain evidence without changing usage or emitting errors.
func TestClaudeRefusalDiagnostic(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, refusal := range []bool{false, true} {
			start := strings.Replace(strictStart, `"input_tokens":100`, `"input_tokens":0,"cache_creation_input_tokens":1200`, 1)
			stopReason := "end_turn"
			if refusal {
				stopReason = "refusal"
			}
			body := start + `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_details":{"type":"refusal","category":"reasoning_extraction"}},"usage":{"output_tokens":0}}

event: message_stop
data: {"type":"message_stop"}

`
			c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
			if !native {
				info.RelayFormat = types.RelayFormatOpenAI
			}
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(resp, nil)
			if native {
				usage, err := strictClaudeStream(c, resp, info)
				require.Nil(t, err)
				require.Zero(t, usage.CompletionTokens)
				require.Equal(t, 1200, usage.PromptTokensDetails.CachedCreationTokens)
				require.Equal(t, "upstream", info.StreamResult.UsageSource)
			} else {
				// Exercise the generic finalizer with the real Claude marker extraction.
				state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
				for _, frame := range strings.Split(body, "\n\n") {
					_, data, ok := strings.Cut(frame, "data: ")
					if ok {
						require.Nil(t, HandleStreamResponseData(c, info, state, data))
					}
				}
				info.StreamSession.Complete()
				info.StreamDiagnostic.Observe(resp)
				_, err := io.Copy(io.Discard, resp.Body)
				require.NoError(t, err)
				service.FinalizeStreamUsage(c, info, nil)
			}
			require.False(t, info.StreamResult.Failed)
			require.Equal(t, refusal, info.StreamResult.DiagnosticAvailable)
			require.NotContains(t, rec.Body.String(), "event: error")
			other := map[string]any{}
			service.AppendStreamLogInfo(info, other)
			require.Equal(t, refusal, other["stream_diagnostic_available"] == true)
			diagnostic := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			if refusal {
				require.Contains(t, string(diagnostic.BodyHead)+string(diagnostic.BodyTail), "reasoning_extraction")
				require.Equal(t, "claude_stop_reason=refusal", diagnostic.RejectReason)
				require.NotNil(t, diagnostic.DownstreamBodyBase64)
				require.Equal(t, base64.StdEncoding.EncodeToString(rec.Body.Bytes()), *diagnostic.DownstreamBodyBase64)
			} else {
				require.Empty(t, diagnostic.BodyHead)
				require.Nil(t, diagnostic.DownstreamBodyBase64)
			}
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamSession.Complete()
			service.FinalizeStreamUsage(c, info, nil)
			require.False(t, info.StreamResult.DiagnosticAvailable, "retry must not inherit the refusal")
		}
	}
}
