package claude

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type parallelToolIncidentFixture struct {
	name         string
	events       []string
	wantFailure  bool
	wantFirstArg string
}

// These synthetic fixtures retain the incident's model, tool IDs and event order.
// Every fixture supplies both tool stops and the final message_stop, so failure
// cannot be explained by a missing upstream tail. No provider or DB is contacted.
func parallelToolIncidentFixtures() []parallelToolIncidentFixture {
	const start = `{"type":"message_start","message":{"type":"message","model":"us.moonshotai.kimi-k3","usage":{"input_tokens":15456,"output_tokens":0},"role":"assistant","id":"chatcmpl-parallel-incident","content":[]}}`
	const tool0 = `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"Bash_5","name":"Bash","input":{}}}`
	const tool1 = `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"Bash_6","name":"Bash","input":{}}}`
	const args0 = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"printf first\",\"description\":\"first tool\"}"}}`
	const args1 = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"printf second\",\"description\":\"second tool\"}"}}`
	const first0 = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"printf "}}`
	const last0 = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"first\",\"description\":\"first tool\"}"}}`
	const first1 = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"printf "}}`
	const last1 = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"second\",\"description\":\"second tool\"}"}}`
	const stop0 = `{"type":"content_block_stop","index":0}`
	const stop1 = `{"type":"content_block_stop","index":1}`
	const finish = `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":15456,"output_tokens":47}}`
	const stop = `{"type":"message_stop"}`
	return []parallelToolIncidentFixture{
		{"serial_control", []string{start, tool0, args0, stop0, tool1, args1, stop1, finish, stop}, false, `{"command":"printf first","description":"first tool"}`},
		{"overlapping_starts_complete_tail", []string{start, tool0, args0, tool1, args1, stop0, stop1, finish, stop}, true, `{"command":"printf first","description":"first tool"}`},
		{"interleaved_arguments_reverse_stops", []string{start, tool0, first0, tool1, first1, last0, last1, stop1, stop0, finish, stop}, true, `{"command":"printf `},
	}
}

func (f parallelToolIncidentFixture) body() string {
	var body strings.Builder
	for _, event := range f.events {
		body.WriteString("event: ")
		body.WriteString(gjson.Get(event, "type").String())
		body.WriteString("\ndata: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	return body.String()
}

// TestParallelToolIncidentReproduction exercises the real upstream observer,
// Claude handler, converter, downstream writer and terminal-error finalizer.
// Complete indexed tool streams must match the upstream/main behavior.
func TestParallelToolIncidentReproduction(t *testing.T) {
	previous := operation_setting.ClaudeStreamUpstreamCompatibilityEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(previous)}))
	})
	for _, compatibility := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(compatibility)}))
		for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
			for _, fixture := range parallelToolIncidentFixtures() {
				t.Run(fmt.Sprintf("enabled=%t/%s/%s", compatibility, format, fixture.name), func(t *testing.T) {
					body := fixture.body()
					require.True(t, strings.HasSuffix(body, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
					path := "/v1/chat/completions"
					if format == types.RelayFormatOpenAIResponses {
						path = "/v1/responses"
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, nil)
					info := &relaycommon.RelayInfo{
						RelayFormat: format,
						IsStream:    true,
						DisablePing: true,
						ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "us.moonshotai.kimi-k3"},
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
					service.BeginStreamAttempt(c, info)
					require.NotNil(t, info.StreamSession)
					info.StreamSession.ObserveTransport(resp, nil)
					info.StreamDiagnostic.Observe(resp)
					info.StreamSession.ObserveHTTP(resp)
					var usage *dto.Usage
					var apiErr *types.NewAPIError
					if format == types.RelayFormatOpenAIResponses {
						usage, apiErr = ClaudeResponsesStreamHandler(c, resp, info)
					} else {
						usage, apiErr = ClaudeStreamHandler(c, resp, info)
					}
					require.Nil(t, apiErr)
					service.FinalizeStreamUsage(c, info, usage)
					require.NotNil(t, info.StreamResult)
					require.Equal(t, http.StatusOK, rec.Code)

					chatIDs := make(map[int]string)
					chatArgs := make(map[int]string)
					responseIDs := make(map[string]string)
					responseArgs := make(map[string]string)
					var errorCount, finishCount int
					for frame := range strings.SplitSeq(rec.Body.String(), "\n\n") {
						_, data := relaycommon.StreamFramePayload([]byte(frame))
						value := gjson.ParseBytes(data)
						if value.Get("error").Exists() {
							errorCount++
							require.Equal(t, "upstream_stream_error", value.Get("error.code").String())
						} else if format == types.RelayFormatOpenAIResponses && value.Get("type").String() == "error" {
							errorCount++
							require.Equal(t, "upstream_stream_error", value.Get("code").String())
						}
						if format == types.RelayFormatOpenAIResponses {
							switch value.Get("type").String() {
							case "response.completed":
								finishCount++
								for _, item := range value.Get("response.output").Array() {
									if item.Get("type").String() != "function_call" {
										continue
									}
									id := item.Get("id").String()
									responseIDs[id] = item.Get("name").String()
									responseArgs[id] = item.Get("arguments").String()
								}
							case "response.output_item.added", "response.output_item.done":
								item := value.Get("item")
								if item.Get("type").String() != "function_call" {
									continue
								}
								id := item.Get("id").String()
								if id != "" && responseIDs[id] == "" {
									responseIDs[id] = item.Get("name").String()
								}
								if args := item.Get("arguments").String(); args != "" {
									responseArgs[id] = args
								}
							case "response.function_call_arguments.delta":
								id := value.Get("item_id").String()
								responseArgs[id] += value.Get("delta").String()
							}
							continue
						}
						if value.Get("choices.0.finish_reason").String() == "tool_calls" {
							finishCount++
						}
						for _, tool := range value.Get("choices.0.delta.tool_calls").Array() {
							index := int(tool.Get("index").Int())
							if id := tool.Get("id").String(); id != "" {
								require.NotContains(t, chatIDs, index, "each tool identity is emitted once")
								chatIDs[index] = id
							}
							chatArgs[index] += tool.Get("function.arguments").String()
						}
					}
					snapshot := info.StreamSession.Snapshot()
					wantFailure := !compatibility && fixture.wantFailure
					require.Equal(t, wantFailure, info.StreamResult.Failed)
					if wantFailure {
						require.Equal(t, relaycommon.StreamEndReason("upstream_protocol_error"), info.StreamStatus.EndReason)
						require.EqualError(t, snapshot.Err, "invalid content block sequence")
						require.False(t, snapshot.Complete)
						require.Equal(t, 1, errorCount)
						require.Zero(t, finishCount)
						require.Equal(t, 3, info.ReceivedResponseCount, "the second start is rejected before the converter sees it")
						if format == types.RelayFormatOpenAIResponses {
							require.Equal(t, map[string]string{"Bash_5": "Bash"}, responseIDs)
							require.Equal(t, fixture.wantFirstArg, responseArgs["Bash_5"])
							require.NotContains(t, rec.Body.String(), "response.completed")
						} else {
							require.Equal(t, fixture.wantFirstArg, chatArgs[0])
							require.Equal(t, map[int]string{0: "Bash_5"}, chatIDs)
							require.NotContains(t, rec.Body.String(), "[DONE]")
						}
					} else {
						require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
						require.NoError(t, snapshot.Err)
						require.True(t, snapshot.Complete)
						require.Zero(t, errorCount)
						require.Equal(t, 1, finishCount)
						require.Equal(t, len(fixture.events), info.ReceivedResponseCount)
						if format == types.RelayFormatOpenAIResponses {
							require.Equal(t, map[string]string{"Bash_5": "Bash", "Bash_6": "Bash"}, responseIDs)
							require.JSONEq(t, `{"command":"printf first","description":"first tool"}`, responseArgs["Bash_5"])
							require.JSONEq(t, `{"command":"printf second","description":"second tool"}`, responseArgs["Bash_6"])
							require.Contains(t, rec.Body.String(), "response.completed")
						} else {
							require.Equal(t, map[int]string{0: "Bash_5", 1: "Bash_6"}, chatIDs)
							require.JSONEq(t, `{"command":"printf first","description":"first tool"}`, chatArgs[0])
							require.JSONEq(t, `{"command":"printf second","description":"second tool"}`, chatArgs[1])
							require.Contains(t, rec.Body.String(), "[DONE]")
						}
					}
					t.Logf("fixture_sha256=%x upstream_events=%d provided_message_stop=true consumed_events=%d end_reason=%s complete=%t chat_ids=%v response_ids=%v error=%v", sha256.Sum256([]byte(body)), len(fixture.events), info.ReceivedResponseCount, info.StreamStatus.EndReason, snapshot.Complete, chatIDs, responseIDs, snapshot.Err)
					t.Logf("downstream SSE:\n%s", rec.Body.String())
				})
			}
		}
	}
}

// TestResponsesOverlappingToolsKeepFinishedUsage reproduces the production
// Responses→Claude stream: a closed text block, then a second tool_use starts
// while the first is still open. The finished message_delta usage must be kept.
func TestResponsesOverlappingToolsKeepFinishedUsage(t *testing.T) {
	previous := operation_setting.ClaudeStreamUpstreamCompatibilityEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(previous)}))
	})
	const start = `{"type":"message_start","message":{"type":"message","model":"us.moonshotai.kimi-k3","usage":{"input_tokens":86700,"output_tokens":0},"role":"assistant","id":"msg_responses_overlap","content":[]}}`
	events := []string{
		start,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"running commands"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"exec_command_69","name":"exec","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"first\"}"}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"exec_command_70","name":"exec","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"second\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":85650,"output_tokens":287,"cache_read_input_tokens":85409}}`,
		`{"type":"message_stop"}`,
	}
	var body strings.Builder
	for _, event := range events {
		body.WriteString("event: ")
		body.WriteString(gjson.Get(event, "type").String())
		body.WriteString("\ndata: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	for _, compatibility := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(compatibility)}))
		t.Run(fmt.Sprintf("enabled=%t", compatibility), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAIResponses,
				IsStream:    true,
				DisablePing: true,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "us.moonshotai.kimi-k3"},
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}
			service.BeginStreamAttempt(c, info)
			require.NotNil(t, info.StreamSession)
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := ClaudeResponsesStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			usage = service.FinalizeStreamUsage(c, info, usage)
			require.NotNil(t, usage)
			if usage.BillingUsage != nil {
				if canonical, ok := usage.BillingUsage.CanonicalUsage(); ok {
					usage = canonical
				}
			}
			snapshot := info.StreamSession.Snapshot()
			if !compatibility {
				require.True(t, info.StreamResult.Failed)
				require.Equal(t, relaycommon.StreamEndReason("upstream_protocol_error"), info.StreamStatus.EndReason)
				require.EqualError(t, snapshot.Err, "invalid content block sequence")
				require.Equal(t, 86700, usage.PromptTokens)
				require.NotEqual(t, 287, usage.CompletionTokens)
				require.Zero(t, usage.PromptTokensDetails.CachedTokens)
				require.NotContains(t, rec.Body.String(), "response.completed")
				require.NotContains(t, rec.Body.String(), "exec_command_70")
				return
			}
			require.False(t, info.StreamResult.Failed)
			require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
			require.NoError(t, snapshot.Err)
			require.True(t, snapshot.Complete)
			require.Equal(t, 85650, usage.PromptTokens)
			require.Equal(t, 287, usage.CompletionTokens)
			require.Equal(t, 85409, usage.PromptTokensDetails.CachedTokens)
			require.Contains(t, rec.Body.String(), "response.completed")
			require.Contains(t, rec.Body.String(), "exec_command_69")
			require.Contains(t, rec.Body.String(), "exec_command_70")
			require.NotContains(t, rec.Body.String(), `"code":"upstream_stream_error"`)
		})
	}
}

// TestParallelToolIncidentConverterIsolation sends the identical event sequences
// directly to the indexed converter to isolate validation from transformation.
func TestParallelToolIncidentConverterIsolation(t *testing.T) {
	for _, fixture := range parallelToolIncidentFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			state := relayconvert.NewClaudeToChatStreamState()
			ids := make(map[int]string)
			arguments := make(map[int]string)
			var finishCount int
			for _, event := range fixture.events {
				var upstream dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(event, &upstream))
				chunk, err := state.ConvertChunk(&upstream)
				require.NoError(t, err)
				if chunk == nil {
					continue
				}
				for _, choice := range chunk.Choices {
					if choice.FinishReason != nil {
						require.Equal(t, "tool_calls", *choice.FinishReason)
						finishCount++
					}
					for _, tool := range choice.Delta.ToolCalls {
						require.NotNil(t, tool.Index)
						if tool.ID != "" {
							require.NotContains(t, ids, *tool.Index)
							require.Equal(t, "Bash", tool.Function.Name)
							ids[*tool.Index] = tool.ID
						}
						arguments[*tool.Index] += tool.Function.Arguments
					}
				}
			}
			require.Equal(t, map[int]string{0: "Bash_5", 1: "Bash_6"}, ids)
			require.JSONEq(t, `{"command":"printf first","description":"first tool"}`, arguments[0])
			require.JSONEq(t, `{"command":"printf second","description":"second tool"}`, arguments[1])
			require.Equal(t, 1, finishCount)
			t.Logf("fixture_sha256=%x converter_tools=%v arguments=%v finish_reason=tool_calls", sha256.Sum256([]byte(fixture.body())), ids, arguments)
		})
	}
}
