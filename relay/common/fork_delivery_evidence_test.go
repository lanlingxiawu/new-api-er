package common

import (
	"errors"
	"net/http/httptest"
	"testing"

	basecommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForkDeliveryEvidenceSnapshot(t *testing.T) {
	s := StreamSnapshot{
		Accepted: true, ReceivedResponse: true, Complete: true, Effective: true,
		ErrorDelivered: true, ReceivedOutput: 11, EstimatedOutput: 7,
		ReceivedAudioOutput: 5, MediaBytes: 1234,
	}
	e := s.DeliveryEvidence()
	s.Effective = false
	s.EstimatedOutput = 0
	require.Equal(t, &StreamDeliveryEvidence{
		Accepted: true, ReceivedResponse: true, ProtocolComplete: true, Effective: true,
		ErrorDelivered: true, ReceivedOutputEstimate: 11, DeliveredOutputEstimate: 7,
		ReceivedAudioOutputEstimate: 5, DeliveredMediaBytes: 1234,
	}, e)
	raw, err := basecommon.Marshal(StreamDiagnostic{DeliveryEvidence: e})
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, basecommon.Unmarshal(raw, &fields))
	require.Contains(t, fields, "delivery_evidence")
	require.Equal(t, map[string]any{
		"accepted": true, "received_response": true, "protocol_complete": true, "effective": true,
		"error_delivered": true, "received_output_estimate": float64(11), "delivered_output_estimate": float64(7),
		"received_audio_output_estimate": float64(5), "delivered_media_bytes": float64(1234),
	}, fields["delivery_evidence"])
	zero := (StreamSnapshot{}).DeliveryEvidence()
	require.Equal(t, &StreamDeliveryEvidence{}, zero, "observed false remains distinct from absent legacy diagnostics")
	raw, err = basecommon.Marshal(StreamDiagnostic{})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "delivery_evidence")
}

func TestForkDeliveryRejectsTruncatedEmptyTool(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, reason := range []string{"length", "content_filter", "tool_calls", "function_call", "stop"} {
			t.Run(reason+map[bool]string{true: "/legacy", false: "/tools"}[legacy], func(t *testing.T) {
				s := NewStreamSession(types.RelayFormatOpenAI)
				frame := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup"}}]}}]}`
				if legacy {
					frame = `{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup"}}}]}`
				}
				s.CommitDelivery([]byte(frame))
				require.False(t, s.Snapshot().Effective)
				s.CommitDelivery([]byte(`{"choices":[{"index":0,"finish_reason":"` + reason + `"}]}`))
				complete := reason != "length" && reason != "content_filter"
				require.Equal(t, complete, s.Snapshot().Effective)
				if complete {
					require.Equal(t, "lookup{}", s.TakeDeliveredText())
				} else {
					require.Empty(t, s.TakeDeliveredText())
				}
				require.Empty(t, s.tools)
				require.Zero(t, s.toolBytes)
			})
		}
	}
}

func TestForkDeliveryIgnoresMetadataAndMalformedContent(t *testing.T) {
	for _, frame := range []string{
		`{"choices":[{"delta":{"role":"assistant"}}],"usage":{"prompt_tokens":50}}`,
		`{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"abc"}}`,
		`{"type":"response.output_text.delta","delta":42}`,
		`{"type":"response.reasoning_text.delta","delta":{"text":"not a string"}}`,
		`{"candidates":[{"content":{"parts":[{"text":true}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"inlineData":{"data":42}}]}}]}`,
		`{"choices":[{"delta":{"audio":{"data":42}}}]}`,
		`{"type":"response.audio.delta","delta":42}`,
		`{"type":"image_generation.partial_image","b64_json":42}`,
		`{"data":[{"url":"  "},{"b64_json":42}]}`,
	} {
		t.Run(frame, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.CommitDelivery([]byte(frame))
			require.False(t, s.Snapshot().Effective)
			require.Empty(t, s.TakeDeliveredText())
			require.Zero(t, s.TakeDeliveredMedia())
		})
	}
}

func TestForkDeliveryPreservesFlushedContentAfterFailure(t *testing.T) {
	for _, frame := range []string{
		`{"choices":[{"delta":{"content":"text"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"candidates":[{"content":{"parts":[{"inlineData":{"data":"QQ=="}}]}}]}`,
	} {
		t.Run(frame, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			s := NewStreamSession(types.RelayFormatOpenAI)
			w := NewStreamWriter(c.Writer, s, func(text string, media int) int { return len(text) + media })
			w.Header().Set("Content-Type", "text/event-stream")
			_, err := w.Write([]byte("data: " + frame + "\n\n"))
			require.NoError(t, err)
			require.False(t, s.Snapshot().Effective)
			require.NoError(t, w.FlushError())
			require.True(t, s.Snapshot().Effective)
			s.Fail("upstream_read_error", errors.New("fixture read failed"))
			s.ClientFailed(errors.New("fixture cleanup failed"))
			require.True(t, s.Snapshot().Effective)
			require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
		})
	}
}
