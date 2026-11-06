package openai

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const forkVendorText = "MATRIX_PAYLOAD"

// These are synthetic protocol-shaped vendor fixtures, not captured customer
// traffic. Usage on in-progress Responses objects exercises cumulative reports.
const forkVendorChatStart = `{"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
const forkVendorChatText = `{"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"MATRIX_PAYLOAD"},"finish_reason":null}]}`
const forkVendorChatFinish = `{"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
const forkVendorResponsesStart = `{"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","object":"response","created_at":1700000000,"status":"in_progress","model":"gpt-4o","error":null,"incomplete_details":null,"output":[],"usage":null}}`
const forkVendorResponsesItem = `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_fixture","type":"message","status":"in_progress","role":"assistant","content":[]}}`
const forkVendorResponsesPart = `{"type":"response.content_part.added","sequence_number":2,"item_id":"msg_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`
const forkVendorResponsesText = `{"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_fixture","output_index":0,"content_index":0,"delta":"MATRIX_PAYLOAD","logprobs":[]}`
const forkVendorResponsesTextDone = `{"type":"response.output_text.done","sequence_number":4,"item_id":"msg_fixture","output_index":0,"content_index":0,"text":"MATRIX_PAYLOAD","logprobs":[]}`
const forkVendorResponsesPartDone = `{"type":"response.content_part.done","sequence_number":5,"item_id":"msg_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"MATRIX_PAYLOAD","annotations":[]}}`
const forkVendorResponsesItemDone = `{"type":"response.output_item.done","sequence_number":6,"output_index":0,"item":{"id":"msg_fixture","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"MATRIX_PAYLOAD","annotations":[]}]}}`

func forkVendorSSE(payload string) string {
	if event := gjson.Get(payload, "type").String(); event != "" {
		return "event: " + event + "\ndata: " + payload + "\n\n"
	}
	return "data: " + payload + "\n\n"
}

func forkVendorResponsesTerminal(kind, status, usage string) string {
	return fmt.Sprintf(`{"type":%q,"sequence_number":7,"response":{"id":"resp_fixture","object":"response","created_at":1700000000,"status":%q,"model":"gpt-4o","error":null,"incomplete_details":null,"output":[{"id":"msg_fixture","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"MATRIX_PAYLOAD","annotations":[]}]}],"usage":%s}}`, kind, status, usage)
}

type forkVendorResult struct {
	info  *relaycommon.RelayInfo
	usage *dto.Usage
	body  string
}

func forkVendorRun(t *testing.T, format types.RelayFormat, resp *http.Response, images bool) forkVendorResult {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: format, OriginModelName: "gpt-4o", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.SetEstimatePromptTokens(11)
	if images {
		n := uint(3)
		info.Request = &dto.ImageRequest{N: &n}
		info.RelayMode = relayconstant.RelayModeImagesGenerations
		info.ImageRequestCount = 3
		info.PriceData.UsePrice = true
		info.PriceData.ModelPrice = 0.1
		info.PriceData.AddOtherRatio("n", 3)
	}
	service.BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	switch {
	case images:
		usage, apiErr = OpenaiImageStreamHandler(c, info, resp)
	case format == types.RelayFormatOpenAIResponses:
		usage, apiErr = OaiResponsesStreamHandler(c, info, resp)
	default:
		usage, apiErr = OaiStreamHandler(c, info, resp)
	}
	require.Nil(t, apiErr)
	selected := service.FinalizeStreamUsage(c, info, usage)
	body := recorder.Body.String()
	require.Same(t, selected, service.FinalizeStreamUsage(c, info, usage))
	require.Equal(t, body, recorder.Body.String(), "finalization must not write a second terminal event")
	require.False(t, info.StreamResult.SettlementAttempted, "fixtures select usage without touching funds")
	return forkVendorResult{info: info, usage: selected, body: body}
}

func forkVendorMemoryResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func forkVendorEventCounts(body string) map[string]int {
	counts := make(map[string]int)
	for _, raw := range strings.Split(body, "\n\n") {
		event, payload := relaycommon.StreamFramePayload([]byte(raw))
		if string(payload) == "[DONE]" {
			counts["DONE"]++
			continue
		}
		kind := gjson.GetBytes(payload, "type").String()
		if kind == "" {
			kind = event
		}
		if kind == "" && gjson.GetBytes(payload, "error").IsObject() {
			kind = "error"
		}
		counts[kind]++
		if gjson.GetBytes(payload, "choices.0.delta.content").String() == forkVendorText || kind == "response.output_text.delta" && gjson.GetBytes(payload, "delta").String() == forkVendorText {
			counts["text_delta"]++
		}
		if gjson.GetBytes(payload, "choices.0.delta.tool_calls.0.function.arguments").String() != "" || kind == "response.function_call_arguments.delta" {
			counts["tool_fragment"]++
		}
	}
	return counts
}

// 60 combinations: two protocols, two delivery stages, three
// usage states and five terminal faults. Invalid later usage cannot erase an
// earlier validated report; failures must never gain a synthetic success event.
func TestForkVendorChatResponsesFaultMatrix(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, delivered := range []bool{false, true} {
			for _, usageState := range []string{"absent", "zero", "confirmed"} {
				for _, fault := range []string{"EOF", "vendor_error", "invalid_json", "invalid_event_type", "invalid_usage"} {
					t.Run(fmt.Sprintf("%s/delivered=%t/usage=%s/%s", format, delivered, usageState, fault), func(t *testing.T) {
						input, output := 20, 8
						if usageState == "zero" {
							input, output = 0, 0
						}
						body := forkVendorSSE(forkVendorChatStart)
						if format == types.RelayFormatOpenAIResponses {
							body = forkVendorSSE(forkVendorResponsesStart)
						}
						if delivered {
							if format == types.RelayFormatOpenAIResponses {
								body += forkVendorSSE(forkVendorResponsesItem) + forkVendorSSE(forkVendorResponsesPart) + forkVendorSSE(forkVendorResponsesText)
							} else {
								body += forkVendorSSE(forkVendorChatText)
							}
						}
						if usageState != "absent" {
							if format == types.RelayFormatOpenAIResponses {
								body += forkVendorSSE(fmt.Sprintf(`{"type":"response.in_progress","sequence_number":4,"response":{"id":"resp_fixture","object":"response","status":"in_progress","output":[],"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}`, input, output, input+output))
							} else {
								body += forkVendorSSE(fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, input, output, input+output))
							}
						}
						reason := "upstream_incomplete"
						switch fault {
						case "vendor_error":
							reason = "upstream_error"
							if format == types.RelayFormatOpenAIResponses {
								body += forkVendorSSE(`{"type":"error","sequence_number":5,"code":"server_error","message":"VENDOR_MATRIX_FAILURE","param":null}`)
							} else {
								body += forkVendorSSE(`{"error":{"message":"VENDOR_MATRIX_FAILURE","type":"server_error","param":null,"code":"server_error"}}`)
							}
						case "invalid_json":
							reason = "upstream_json_error"
							body += "data: {broken}\n\n"
						case "invalid_event_type":
							reason = "upstream_protocol_error"
							body += "data: [1,2,3]\n\n"
						case "invalid_usage":
							reason = "upstream_json_error"
							body += "data: {\"usage\":{\"input_tokens\":999,\"output_tokens\":-1}}\n\n"
						}
						result := forkVendorRun(t, format, forkVendorMemoryResponse(body), false)
						require.True(t, result.info.StreamResult.Failed)
						require.Equal(t, reason, string(result.info.StreamStatus.EndReason))
						require.Equal(t, delivered, result.info.StreamResult.EffectiveContent)
						require.False(t, result.info.StreamResult.Diagnostic.UsageFinal)
						counts := forkVendorEventCounts(result.body)
						require.Zero(t, counts["DONE"])
						require.Zero(t, counts["response.completed"])
						require.Equal(t, 1, counts["error"])
						require.Equal(t, map[bool]int{false: 0, true: 1}[delivered], counts["text_delta"])
						if fault == "vendor_error" {
							require.Equal(t, 1, strings.Count(result.body, "VENDOR_MATRIX_FAILURE"))
						}
						if !delivered {
							require.Equal(t, "none", result.info.StreamResult.UsageSource)
							require.Zero(t, result.usage.TotalTokens)
						} else if usageState == "absent" {
							require.Equal(t, "estimated", result.info.StreamResult.UsageSource)
							require.Equal(t, 11, result.usage.PromptTokens)
							require.Positive(t, result.usage.CompletionTokens)
						} else {
							require.Equal(t, input, result.usage.PromptTokens)
							require.Equal(t, input, result.info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
							require.Equal(t, output, result.info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
							if delivered && output == 0 {
								require.Equal(t, "mixed", result.info.StreamResult.UsageSource)
								require.Positive(t, result.usage.CompletionTokens)
							} else {
								require.Equal(t, "upstream", result.info.StreamResult.UsageSource)
								require.Equal(t, output, result.usage.CompletionTokens)
							}
						}
					})
				}
			}
		}
	}
}

func TestForkVendorTerminalPlacement(t *testing.T) {
	responsesUsage := `{"input_tokens":20,"output_tokens":8,"total_tokens":28}`
	chatPrefix := forkVendorSSE(forkVendorChatStart) + forkVendorSSE(forkVendorChatText)
	responsesPrefix := forkVendorSSE(forkVendorResponsesStart) + forkVendorSSE(forkVendorResponsesItem) + forkVendorSSE(forkVendorResponsesPart) + forkVendorSSE(forkVendorResponsesText)
	completed := forkVendorSSE(forkVendorResponsesTextDone) + forkVendorSSE(forkVendorResponsesPartDone) + forkVendorSSE(forkVendorResponsesItemDone) + forkVendorSSE(forkVendorResponsesTerminal("response.completed", "completed", responsesUsage))
	for _, tc := range []struct {
		name     string
		format   types.RelayFormat
		body     string
		failed   bool
		terminal string
	}{
		{"chat finish then DONE", types.RelayFormatOpenAI, chatPrefix + forkVendorSSE(forkVendorChatFinish) + "data: [DONE]\n\n", false, "DONE"},
		{"chat finish then EOF", types.RelayFormatOpenAI, chatPrefix + forkVendorSSE(forkVendorChatFinish), false, "DONE"},
		{"chat DONE before finish", types.RelayFormatOpenAI, chatPrefix + "data: [DONE]\n\n", true, ""},
		{"chat duplicate DONE", types.RelayFormatOpenAI, chatPrefix + forkVendorSSE(forkVendorChatFinish) + "data: [DONE]\n\ndata: [DONE]\n\n", false, "DONE"},
		{"responses completed", types.RelayFormatOpenAIResponses, responsesPrefix + completed, false, "response.completed"},
		{"responses duplicate completed", types.RelayFormatOpenAIResponses, responsesPrefix + completed + completed, false, "response.completed"},
		{"responses max output tokens", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.incomplete","sequence_number":7,"response":{"id":"resp_fixture","object":"response","created_at":1700000000,"model":"gpt-4o","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}}`), false, "response.incomplete"},
		{"responses DONE without completed", types.RelayFormatOpenAIResponses, responsesPrefix + "data: [DONE]\n\n", true, ""},
		{"responses malformed terminal object", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":"invalid"}`), true, ""},
		{"responses missing terminal object", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7}`), true, ""},
		{"responses null terminal object", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":null}`), true, ""},
		{"responses array terminal object", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":[]}`), true, ""},
		{"responses empty terminal object", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":{}}`), true, ""},
		{"responses terminal missing status", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":{"id":"resp_fixture","object":"response","output":[]}}`), true, ""},
		{"responses terminal numeric status", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":{"id":"resp_fixture","status":7}}`), true, ""},
		{"responses terminal in progress", types.RelayFormatOpenAIResponses, responsesPrefix + forkVendorSSE(`{"type":"response.completed","sequence_number":7,"response":{"id":"resp_fixture","status":"in_progress"}}`), true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := forkVendorRun(t, tc.format, forkVendorMemoryResponse(tc.body), false)
			require.Equal(t, tc.failed, result.info.StreamResult.Failed)
			counts := forkVendorEventCounts(result.body)
			require.Equal(t, 1, counts["text_delta"])
			if tc.failed {
				require.Equal(t, 1, counts["error"])
				require.Zero(t, counts["DONE"])
				require.Zero(t, counts["response.completed"])
			} else if tc.format == types.RelayFormatOpenAIResponses {
				require.Equal(t, 1, counts[tc.terminal])
				require.Zero(t, counts["error"])
				require.Equal(t, 20, result.usage.PromptTokens)
				require.Equal(t, 8, result.usage.CompletionTokens)
			} else {
				require.Equal(t, 1, counts["DONE"])
				require.Zero(t, counts["error"])
			}
		})
	}
}

func TestForkVendorToolFragments(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, complete := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/complete=%t", format, complete), func(t *testing.T) {
				body := forkVendorSSE(forkVendorChatStart)
				if format == types.RelayFormatOpenAI {
					body += forkVendorSSE(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"lookup","arguments":"{\"city\":"}}]},"finish_reason":null}]}`)
					if complete {
						body += forkVendorSSE(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`)
					}
					body += forkVendorSSE(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`)
					if complete {
						body += "data: [DONE]\n\n"
					}
				} else {
					body = forkVendorSSE(forkVendorResponsesStart)
					body += forkVendorSSE(`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_fixture","type":"function_call","status":"in_progress","call_id":"call_fixture","name":"lookup","arguments":""}}`)
					body += forkVendorSSE(`{"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"fc_fixture","output_index":0,"delta":"{\"city\":"}`)
					if complete {
						body += forkVendorSSE(`{"type":"response.function_call_arguments.delta","sequence_number":3,"item_id":"fc_fixture","output_index":0,"delta":"\"Paris\"}"}`)
						body += forkVendorSSE(`{"type":"response.function_call_arguments.done","sequence_number":4,"item_id":"fc_fixture","output_index":0,"arguments":"{\"city\":\"Paris\"}"}`)
						body += forkVendorSSE(`{"type":"response.output_item.done","sequence_number":5,"output_index":0,"item":{"id":"fc_fixture","type":"function_call","status":"completed","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}"}}`)
						body += forkVendorSSE(`{"type":"response.completed","sequence_number":6,"response":{"id":"resp_fixture","object":"response","status":"completed","output":[{"id":"fc_fixture","type":"function_call","status":"completed","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}}`)
					} else {
						body += forkVendorSSE(`{"type":"response.in_progress","sequence_number":3,"response":{"id":"resp_fixture","object":"response","status":"in_progress","output":[],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}}`)
					}
				}
				result := forkVendorRun(t, format, forkVendorMemoryResponse(body), false)
				require.Equal(t, !complete, result.info.StreamResult.Failed)
				require.Equal(t, complete, result.info.StreamResult.EffectiveContent, "partial JSON alone is not an executable delivered tool")
				counts := forkVendorEventCounts(result.body)
				require.Equal(t, map[bool]int{true: 2, false: 1}[complete], counts["tool_fragment"])
				require.Equal(t, map[bool]int{true: 0, false: 1}[complete], counts["error"])
				if complete {
					require.Equal(t, 20, result.usage.PromptTokens)
					require.Equal(t, 8, result.usage.CompletionTokens)
				} else {
					require.Zero(t, result.usage.TotalTokens)
				}
				if !complete {
					require.Zero(t, counts["DONE"])
					require.Zero(t, counts["response.completed"])
				}
			})
		}
	}
}

func TestForkVendorImageFaultMatrix(t *testing.T) {
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	imageEvent := forkVendorSSE(fmt.Sprintf(`{"type":"image_generation.completed","created_at":1700000000,"b64_json":%q,"background":"transparent","size":"1024x1024","quality":"low","output_format":"png","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, encoded))
	preview := forkVendorSSE(fmt.Sprintf(`{"type":"image_generation.partial_image","created_at":1700000000,"partial_image_index":0,"b64_json":%q,"background":"transparent","size":"1024x1024","quality":"low","output_format":"png"}`, encoded))
	for _, tc := range []struct {
		name, body string
		completed  int
		failure    bool
		effective  bool
	}{
		{"full batch EOF", strings.Repeat(imageEvent, 3), 3, false, true},
		{"full batch DONE", strings.Repeat(imageEvent, 3) + "data: [DONE]\n\n", 3, false, true},
		{"fewer valid images DONE", imageEvent + "data: [DONE]\n\n", 1, false, true},
		{"partial batch EOF", imageEvent, 1, true, true},
		{"partial batch vendor error", imageEvent + forkVendorSSE(`{"type":"error","code":"server_error","message":"IMAGE_VENDOR_FAILURE"}`), 1, true, true},
		{"partial batch invalid JSON", imageEvent + "data: {broken}\n\n", 1, true, true},
		{"partial batch invalid usage", imageEvent + forkVendorSSE(`{"type":"image_generation.completed","usage":{"input_tokens":-1}}`), 1, true, true},
		{"preview cannot complete", preview + "data: [DONE]\n\n", 0, true, true},
		{"empty completion cannot complete", forkVendorSSE(`{"type":"image_generation.completed"}`) + "data: [DONE]\n\n", 0, true, false},
		{"empty stream", "", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := forkVendorRun(t, types.RelayFormatOpenAI, forkVendorMemoryResponse(tc.body), true)
			require.Equal(t, tc.failure, result.info.StreamResult.Failed)
			require.Equal(t, tc.effective, result.info.StreamResult.EffectiveContent)
			counts := forkVendorEventCounts(result.body)
			require.Equal(t, map[bool]int{true: 1, false: 0}[tc.failure], counts["error"])
			if tc.failure {
				require.Zero(t, counts["DONE"])
			} else if strings.Contains(tc.body, "[DONE]") {
				require.Equal(t, 1, counts["DONE"])
			} else {
				require.Zero(t, counts["DONE"], "all completed images suffice without inventing a vendor DONE")
			}
			if tc.completed > 0 {
				require.Equal(t, tc.completed, counts["image_generation.completed"])
				require.Equal(t, 2, result.usage.PromptTokens)
				require.Equal(t, 3, result.usage.CompletionTokens)
				expectedCount := 3
				if !tc.failure {
					expectedCount = tc.completed
				}
				require.Equal(t, float64(expectedCount), result.info.PriceData.OtherRatios()["n"])
			}
		})
	}
}

func TestForkVendorLoopbackChunkedStream(t *testing.T) {
	for _, mode := range []string{"complete", "clean_EOF", "truncated_HTTP"} {
		t.Run(mode, func(t *testing.T) {
			complete := mode == "complete"
			body := forkVendorSSE(forkVendorChatStart) + forkVendorSSE(forkVendorChatText)
			if complete {
				body += forkVendorSSE(forkVendorChatFinish) + "data: [DONE]\n\n"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "truncated_HTTP" {
					w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
				}
				// Prime-sized HTTP writes split field names, JSON strings and SSE
				// delimiters. Returning without a terminal frame is a clean EOF.
				for start := 0; start < len(body); start += 7 {
					_, _ = io.WriteString(w, body[start:min(start+7, len(body))])
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL)
			require.NoError(t, err)
			result := forkVendorRun(t, types.RelayFormatOpenAI, resp, false)
			require.Equal(t, !complete, result.info.StreamResult.Failed)
			counts := forkVendorEventCounts(result.body)
			require.Equal(t, 1, counts["text_delta"])
			if complete {
				require.Equal(t, 1, counts["DONE"])
				require.Zero(t, counts["error"])
			} else {
				require.Zero(t, counts["DONE"])
				require.Equal(t, 1, counts["error"])
				wantReason := "upstream_incomplete"
				if mode == "truncated_HTTP" {
					wantReason = "upstream_read_error"
				}
				require.Equal(t, wantReason, string(result.info.StreamStatus.EndReason))
			}
		})
	}
}
