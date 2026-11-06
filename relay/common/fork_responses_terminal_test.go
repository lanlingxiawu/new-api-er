package common

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// Responses terminates with response.completed (or a supported limit outcome),
// not Chat's [DONE]. The upstream dialect can differ from the client format.
func TestForkResponsesRequiresProtocolTerminal(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI} {
		for _, terminal := range []string{"", "completed", "incomplete"} {
			t.Run(string(format)+"/"+terminal, func(t *testing.T) {
				s := NewStreamSession(format)
				s.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
				require.NoError(t, s.ObserveEvent("response.created", []byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","object":"response","status":"in_progress","output":[],"usage":null}}`)))
				if terminal == "completed" {
					require.NoError(t, s.ObserveEvent("", []byte(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22}}}`)))
				} else if terminal == "incomplete" {
					require.NoError(t, s.ObserveEvent("", []byte(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22}}}`)))
				}
				err := s.ObserveEvent("", []byte("[DONE]"))
				if terminal == "" {
					require.Error(t, err)
					require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
					require.False(t, s.Snapshot().Complete)
				} else {
					require.NoError(t, err)
					require.True(t, s.Snapshot().Complete)
				}
			})
		}
	}
}

func TestForkResponsesTerminalObject(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.completed"}`,
		`{"type":"response.completed","response":null}`,
		`{"type":"response.completed","response":[]}`,
		`{"type":"response.completed","response":{}}`,
		`{"type":"response.completed","response":{"status":42}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIResponses)
			s.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			require.Error(t, s.ObserveEvent("", []byte(payload)))
			require.False(t, s.Snapshot().Complete)
			require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
		})
	}
}
