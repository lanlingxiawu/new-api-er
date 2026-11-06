package common

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestForkResponsesRejectsForeignTerminators(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI} {
		for _, foreign := range []string{
			`{"type":"response.output_text.delta","delta":"hello","done":true}`,
			`{"type":"response.output_text.delta","delta":"hello","is_end":true}`,
			`{"type":"response.output_text.delta","delta":"hello","choices":[{"index":0,"finish_reason":"stop"}]}`,
			`{"type":"response.output_text.delta","delta":"hello","candidates":[{"index":0,"finishReason":"STOP"}]}`,
			`{"type":"response.output_text.delta","delta":"hello","promptFeedback":{"blockReason":"SAFETY"}}`,
			`{"type":"done"}`,
			`{"type":"image_generation.completed","b64_json":"eA=="}`,
		} {
			t.Run(fmt.Sprintf("%s/%s", format, foreign), func(t *testing.T) {
				s := NewStreamSession(format)
				require.NoError(t, s.ObserveEvent("", []byte(`{"type":"response.created","response":{"id":"resp_foreign","object":"response","status":"in_progress","output":[],"usage":null}}`)))
				require.NoError(t, s.ObserveEvent("", []byte(foreign)))
				require.Equal(t, false, s.ProtocolComplete(), "only the upstream Responses terminal may complete this stream")
				err := s.ObserveEvent("", []byte("[DONE]"))
				require.Error(t, err)
				require.False(t, s.ProtocolComplete())
				require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
			})
		}
	}
}
