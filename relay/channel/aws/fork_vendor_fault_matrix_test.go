package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/stretchr/testify/require"
)

// Local synthetic fixtures use the real SDK eventstream encoder/decoder and
// Anthropic schemas, not live provider calls or a mock of the Claude parser:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModelWithResponseStream.html
// https://platform.claude.com/docs/en/build-with-claude/streaming
// The fixed model ID is documented at:
// https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4-5.html
const forkAWSModel = "anthropic.claude-sonnet-4-5-20250929-v1:0"

func forkAWSStart(usage string) string {
	if usage != "" {
		usage = `,"usage":` + usage
	}
	return `{"type":"message_start","message":{"id":"msg_bedrock_matrix","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[],"stop_reason":null,"stop_sequence":null` + usage + `}}`
}

func forkAWSText(index int) []string {
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":"Hello, 世界 🌍"}}`, index),
	}
}

func forkAWSClose(index int) string {
	return fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)
}

func forkAWSTool(index int) []string {
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":"toolu_bedrock_%d","name":"get_weather","input":{}}}`, index, index),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`, index),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":"\"東京\"}"}}`, index),
		forkAWSClose(index),
	}
}

func forkAWSFinish(reason string) []string {
	return []string{
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":12}}`, reason),
		`{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":21,"outputTokenCount":12,"invocationLatency":30,"firstByteLatency":5}}`,
	}
}

func forkAWSEncode(t *testing.T, events []string) []byte {
	t.Helper()
	var wire bytes.Buffer
	for _, payload := range events {
		require.NoError(t, writeAwsStreamEvent(&wire, payload))
	}
	return wire.Bytes()
}

type forkAWSFragmentReader struct {
	io.Reader
	chunk int
}

func (r forkAWSFragmentReader) Read(p []byte) (int, error) {
	if r.chunk > 0 && len(p) > r.chunk {
		p = p[:r.chunk]
	}
	return r.Reader.Read(p)
}

func forkAWSRun(t *testing.T, wire []byte, chunk int, format types.RelayFormat) (*relaycommon.RelayInfo, *dto.Usage, string, *types.NewAPIError) {
	t.Helper()
	rec := httptest.NewRecorder()
	c := newAwsTestContext(rec, context.Background())
	if format == types.RelayFormatClaude {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	}
	info := newAwsTestRelayInfo()
	info.RelayFormat, info.OriginModelName, info.UpstreamModelName = format, forkAWSModel, forkAWSModel
	info.SetEstimatePromptTokens(50)
	service.BeginStreamAttempt(c, info)
	client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{
		Capture: info.StreamDiagnostic, Stream: info.StreamSession,
		Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			return newAwsStreamResponse(r, io.NopCloser(forkAWSFragmentReader{Reader: bytes.NewReader(wire), chunk: chunk})), nil
		}),
	})
	input := newAwsStreamInput()
	input.ModelId = new(string)
	*input.ModelId = forkAWSModel
	input.Body = []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":128,"messages":[{"role":"user","content":"Hello"}]}`)
	apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: input})
	usage = service.FinalizeStreamUsage(c, info, usage)
	return info, usage, rec.Body.String(), apiErr
}

func TestForkAWSVendorValidStreams(t *testing.T) {
	text := append(forkAWSText(0), forkAWSClose(0))
	thinking := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me check."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"c3ludGhldGljLXNpZ25hdHVyZQ=="}}`,
		forkAWSClose(0),
	}
	thinking = append(thinking, forkAWSText(1)...)
	thinking = append(thinking, forkAWSClose(1))
	for _, tc := range []struct {
		name   string
		blocks []string
		reason string
	}{
		{"text", text, "end_turn"},
		{"thinking_then_text", thinking, "end_turn"},
		{"two_fragmented_tools", append(forkAWSTool(0), forkAWSTool(1)...), "tool_use"},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
			for _, chunk := range []int{1, 11} {
				t.Run(fmt.Sprintf("%s/%s/read_%d", tc.name, format, chunk), func(t *testing.T) {
					events := append([]string{forkAWSStart(`{"input_tokens":21,"output_tokens":1}`)}, tc.blocks...)
					events = append(events, forkAWSFinish(tc.reason)...)
					info, usage, body, apiErr := forkAWSRun(t, forkAWSEncode(t, events), chunk, format)
					require.Nil(t, apiErr)
					require.False(t, info.StreamResult.Failed)
					require.True(t, info.StreamResult.EffectiveContent)
					require.Equal(t, "upstream", info.StreamResult.UsageSource)
					require.Equal(t, 21, usage.PromptTokens)
					require.Equal(t, 12, usage.CompletionTokens)
					require.NotContains(t, body, "upstream_stream_error")
					if tc.reason == "tool_use" {
						require.Contains(t, body, "toolu_bedrock_0")
						require.Contains(t, body, "toolu_bedrock_1")
						require.Contains(t, body, "get_weather")
						require.Contains(t, body, "東京")
					} else {
						require.Contains(t, body, "Hello, 世界 🌍")
					}
					if format == types.RelayFormatClaude {
						require.Equal(t, 1, strings.Count(body, "event: message_stop"))
						require.NotContains(t, body, "[DONE]")
						if tc.name == "thinking_then_text" {
							require.Contains(t, body, "c3ludGhldGljLXNpZ25hdHVyZQ==")
						}
					} else {
						require.Equal(t, 1, strings.Count(body, "data: [DONE]"))
					}
				})
			}
		}
	}
}

func TestForkAWSVendorFaultMatrix(t *testing.T) {
	start := forkAWSStart(`{"input_tokens":21,"output_tokens":0}`)
	text := append([]string{start}, forkAWSText(0)...)
	for _, tc := range []struct {
		name             string
		events           []string
		mutation, reason string
		effective        bool
	}{
		{"empty_transport", nil, "", "upstream_incomplete", false},
		{"start_only", []string{start}, "", "upstream_incomplete", false},
		{"text_then_eof", text, "", "upstream_incomplete", true},
		{"truncated_sdk_prelude", []string{start}, "prelude", "upstream_read_error", false},
		// Transport EOF is observed before the SDK reports its truncated payload.
		// The existing first-cause rule retains upstream_incomplete after a start.
		{"truncated_sdk_payload", text, "tail", "upstream_incomplete", false},
		{"bad_sdk_crc", []string{start}, "crc", "upstream_read_error", false},
		{"half_json_inside_complete_sdk_frame", []string{start, `{"type":"content_block_start"`}, "", "upstream_json_error", false},
		{"html_inside_complete_sdk_frame", []string{`<html>upstream unavailable</html>`}, "", "upstream_json_error", false},
		{"duplicate_start", []string{start, start}, "", "upstream_protocol_error", false},
		{"delta_without_block", []string{start, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`}, "", "upstream_protocol_error", false},
		{"terminal_with_open_block", append(append([]string(nil), text...), forkAWSFinish("end_turn")...), "", "upstream_protocol_error", true},
		{"terminal_without_stop_reason", append(append([]string(nil), text...), forkAWSClose(0), `{"type":"message_stop"}`), "", "upstream_protocol_error", true},
		{"negative_usage", []string{forkAWSStart(`{"input_tokens":-1,"output_tokens":0}`)}, "", "upstream_json_error", false},
		{"oversized_usage", []string{forkAWSStart(`{"input_tokens":1000000001,"output_tokens":0}`)}, "", "upstream_json_error", false},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
			t.Run(fmt.Sprintf("%s/%s", tc.name, format), func(t *testing.T) {
				wire := forkAWSEncode(t, tc.events)
				switch tc.mutation {
				case "prelude":
					wire = wire[:7]
				case "tail":
					wire = wire[:len(wire)-7]
				case "crc":
					wire[len(wire)-1] ^= 1
				}
				info, usage, body, _ := forkAWSRun(t, wire, 5, format)
				require.Equal(t, tc.reason, string(info.StreamSession.Snapshot().Reason))
				require.True(t, info.StreamResult.Failed)
				require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
				require.NotContains(t, body, "[DONE]")
				require.NotContains(t, body, "event: message_stop")
				require.GreaterOrEqual(t, usage.PromptTokens, 0)
				require.GreaterOrEqual(t, usage.CompletionTokens, 0)
				if tc.mutation == "tail" {
					t.Logf("truncated SDK payload: reason=%s diagnostic=%s", info.StreamSession.Snapshot().Reason, info.StreamResult.Diagnostic.Error)
				}
			})
		}
	}
}

func TestForkAWSVendorExceptionPriority(t *testing.T) {
	for _, exception := range []string{"modelStreamErrorException", "throttlingException", "validationException", "internalServerException", "modelTimeoutException"} {
		for _, delivered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delivered_%t", exception, delivered), func(t *testing.T) {
				events := []string{forkAWSStart(`{"input_tokens":21,"output_tokens":1}`)}
				if delivered {
					events = append(events, forkAWSText(0)...)
				}
				wire := bytes.NewBuffer(forkAWSEncode(t, events))
				require.NoError(t, eventstream.NewEncoder().Encode(wire, eventstream.Message{
					Headers: eventstream.Headers{
						{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.ExceptionMessageType)},
						{Name: eventstreamapi.ExceptionTypeHeader, Value: eventstream.StringValue(exception)},
						{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
					}, Payload: []byte(`{"message":"Vendor stream unavailable"}`),
				}))
				// A complete exception outranks malformed bytes following it.
				wire.Write([]byte{1, 2, 3})
				info, usage, body, apiErr := forkAWSRun(t, wire.Bytes(), 1, types.RelayFormatClaude)
				require.Nil(t, apiErr)
				require.Equal(t, "upstream_read_error", string(info.StreamSession.Snapshot().Reason))
				require.Contains(t, strings.ToLower(info.StreamResult.Diagnostic.Error), strings.ToLower(exception))
				require.Equal(t, "Vendor stream unavailable", info.StreamResult.ErrorMessage)
				require.Equal(t, delivered, info.StreamResult.EffectiveContent)
				if delivered {
					require.Equal(t, 21, usage.PromptTokens)
				} else {
					require.Equal(t, "none", info.StreamResult.UsageSource)
					require.Zero(t, usage.TotalTokens)
				}
				require.Equal(t, 21, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
				require.NotContains(t, body, "event: message_stop")
				require.Equal(t, 1, strings.Count(body, "event: error"))
			})
		}
	}
}

func TestForkAWSVendorUndeliveredUsage(t *testing.T) {
	for _, tc := range []struct {
		name, usage           string
		prompt, output, cache int
		confirmed             bool
	}{
		{"missing", "", 0, 0, 0, false},
		{"zero", `{"input_tokens":0,"output_tokens":0}`, 0, 0, 0, true},
		{"input_only", `{"input_tokens":21}`, 21, 0, 0, true},
		{"initial_output", `{"input_tokens":21,"output_tokens":3}`, 21, 3, 0, true},
		{"cache_only", `{"cache_read_input_tokens":20}`, 0, 0, 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := forkAWSEncode(t, []string{forkAWSStart(tc.usage), `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`})
			info, usage, body, apiErr := forkAWSRun(t, wire, 7, types.RelayFormatClaude)
			require.Nil(t, apiErr)
			require.True(t, info.StreamResult.Failed)
			require.False(t, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.confirmed, info.StreamResult.ConfirmedUsage)
			require.Equal(t, tc.prompt, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
			require.Equal(t, tc.output, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			require.Equal(t, tc.cache, info.StreamResult.Diagnostic.UsageEvidence["cache_read_input_tokens"])
			require.Equal(t, "none", info.StreamResult.UsageSource)
			require.Zero(t, usage.TotalTokens)
			require.Zero(t, usage.PromptTokensDetails.CachedTokens)
			require.NotContains(t, body, "event: message_stop")
			require.Equal(t, 1, strings.Count(body, "event: error"))
		})
	}
}

// Reject malformed vendor messages even when they contain superficially valid
// terminal events or fields belonging to a different provider's protocol.
func TestForkAWSVendorRejectProtocolCrossovers(t *testing.T) {
	start := forkAWSStart(`{"input_tokens":21,"output_tokens":1}`)
	text := append([]string{start}, forkAWSText(0)...)
	for _, tc := range []struct {
		name   string
		events []string
	}{
		{"thinking_delta_in_text_block", append(append(append([]string(nil), text...), `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"wrong block"}}`, forkAWSClose(0)), forkAWSFinish("end_turn")...)},
		{"signature_delta_in_text_block", append(append(append([]string(nil), text...), `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"wrong block"}}`, forkAWSClose(0)), forkAWSFinish("end_turn")...)},
		{"incomplete_tool_with_tool_use_terminal", append([]string{start, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_incomplete","name":"get_weather","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`, forkAWSClose(0)}, forkAWSFinish("tool_use")...)},
		{"conflicting_stop_reasons", append(append(append([]string(nil), text...), forkAWSClose(0), `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`), forkAWSFinish("max_tokens")...)},
		{"reuse_closed_block_index", append(append(append(append([]string(nil), text...), forkAWSClose(0)), forkAWSText(0)...), append([]string{forkAWSClose(0)}, forkAWSFinish("end_turn")...)...)},
		{"foreign_terminal_field_on_ping", []string{start, `{"type":"ping","done":true}`}},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
			t.Run(fmt.Sprintf("%s/%s", tc.name, format), func(t *testing.T) {
				info, _, body, apiErr := forkAWSRun(t, forkAWSEncode(t, tc.events), 11, format)
				require.False(t, info.StreamSession.ProtocolComplete())
				require.True(t, info.StreamResult.Failed)
				require.NotContains(t, body, "event: message_stop")
				require.NotContains(t, body, "[DONE]")
				if tc.name == "foreign_terminal_field_on_ping" {
					require.Nil(t, apiErr)
					require.Equal(t, "upstream_incomplete", string(info.StreamSession.Snapshot().Reason))
				} else {
					require.NotNil(t, apiErr)
					require.Equal(t, "upstream_protocol_error", string(info.StreamSession.Snapshot().Reason))
				}
			})
		}
	}
}

func TestForkAWSVendorFineGrainedMaxTokens(t *testing.T) {
	// Anthropic explicitly allows a partial tool JSON value at max_tokens:
	// https://platform.claude.com/docs/en/agents-and-tools/tool-use/fine-grained-tool-streaming
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/real_terminal_%t", format, terminal), func(t *testing.T) {
				events := []string{forkAWSStart(`{"input_tokens":21,"output_tokens":1}`), `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_partial","name":"get_weather","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`, forkAWSClose(0)}
				finish := forkAWSFinish("max_tokens")
				events = append(events, finish[0])
				if terminal {
					events = append(events, finish[1])
				}
				info, usage, body, apiErr := forkAWSRun(t, forkAWSEncode(t, events), 1, format)
				require.Nil(t, apiErr)
				require.Equal(t, terminal, info.StreamSession.ProtocolComplete())
				require.Equal(t, !terminal, info.StreamResult.Failed)
				require.False(t, info.StreamResult.EffectiveContent, "partial tool is not executable content")
				if terminal {
					require.Equal(t, 21, usage.PromptTokens)
					require.Equal(t, 12, usage.CompletionTokens)
				} else {
					require.Equal(t, "none", info.StreamResult.UsageSource)
					require.Zero(t, usage.TotalTokens)
					require.NotContains(t, body, "[DONE]")
					require.NotContains(t, body, "event: message_stop")
				}
			})
		}
	}
}
