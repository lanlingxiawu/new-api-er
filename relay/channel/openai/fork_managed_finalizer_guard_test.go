package openai

import (
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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func forkManagedFinalizerOpenAI(t *testing.T, upstream, client types.RelayFormat, body string) (*relaycommon.RelayInfo, *dto.Usage, []gjson.Result) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: client, FinalRequestRelayFormat: upstream, OriginModelName: "gpt-4o", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.SetEstimatePromptTokens(91)
	service.BeginStreamAttempt(c, info)
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	info.StreamSession.ObserveTransport(response, nil)
	info.StreamDiagnostic.Observe(response)
	info.StreamSession.ObserveHTTP(response)
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	if upstream == types.RelayFormatOpenAI {
		usage, apiErr = OaiChatToResponsesStreamHandler(c, info, response)
	} else {
		usage, apiErr = OaiResponsesToChatStreamHandler(c, info, response)
	}
	require.Nil(t, apiErr)
	usage = service.FinalizeStreamUsage(c, info, usage)
	require.False(t, info.StreamResult.SettlementAttempted)
	var events []gjson.Result
	for frame := range strings.SplitSeq(recorder.Body.String(), "\n\n") {
		_, data := relaycommon.StreamFramePayload([]byte(frame))
		if len(data) == 0 || string(data) == "[DONE]" {
			continue
		}
		require.True(t, gjson.ValidBytes(data))
		events = append(events, gjson.ParseBytes(data))
	}
	return info, usage, events
}

// A parsable tool argument delta is still pending until the upstream protocol
// closes that tool. Scanner-owned errors must not invoke a successful finalizer.
func TestForkManagedOpenAIFinalizerRejectsUnfinishedTool(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	chatStart := `{"id":"chat_fixture","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"lookup_item","arguments":"{\"item\":\"alpha\"}"}}]},"finish_reason":null}],"usage":{"prompt_tokens":91,"completion_tokens":0,"total_tokens":91}}`
	chatFinish := `{"id":"chat_fixture","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":91,"completion_tokens":17,"total_tokens":108}}`
	responsesStart := `{"type":"response.created","response":{"id":"resp_fixture","model":"gpt-4o","status":"in_progress","output":[],"usage":{"input_tokens":91,"output_tokens":0,"total_tokens":91}}}`
	responsesTool := `{"type":"response.output_item.added","output_index":0,"item":{"id":"item_fixture","type":"function_call","call_id":"call_fixture","name":"lookup_item","arguments":"","status":"in_progress"}}`
	responsesArgs := `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_fixture","delta":"{\"item\":\"alpha\"}"}`
	responsesDone := `{"type":"response.output_item.done","output_index":0,"item":{"id":"item_fixture","type":"function_call","call_id":"call_fixture","name":"lookup_item","arguments":"{\"item\":\"alpha\"}","status":"completed"}}`
	responsesFinish := `{"type":"response.completed","response":{"id":"resp_fixture","model":"gpt-4o","status":"completed","output":[{"id":"item_fixture","type":"function_call","call_id":"call_fixture","name":"lookup_item","arguments":"{\"item\":\"alpha\"}","status":"completed"}],"usage":{"input_tokens":91,"output_tokens":17,"total_tokens":108}}}`
	for _, path := range []struct {
		name             string
		upstream, client types.RelayFormat
		start, finish    string
	}{
		{"chat_to_responses", types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, forkVendorSSE(chatStart), forkVendorSSE(chatFinish) + "data: [DONE]\n\n"},
		{"responses_to_chat", types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, forkVendorSSE(responsesStart) + forkVendorSSE(responsesTool) + forkVendorSSE(responsesArgs), forkVendorSSE(responsesDone) + forkVendorSSE(responsesFinish)},
		{"responses_to_claude", types.RelayFormatOpenAIResponses, types.RelayFormatClaude, forkVendorSSE(responsesStart) + forkVendorSSE(responsesTool) + forkVendorSSE(responsesArgs), forkVendorSSE(responsesDone) + forkVendorSSE(responsesFinish)},
		{"responses_to_gemini", types.RelayFormatOpenAIResponses, types.RelayFormatGemini, forkVendorSSE(responsesStart) + forkVendorSSE(responsesTool) + forkVendorSSE(responsesArgs), forkVendorSSE(responsesDone) + forkVendorSSE(responsesFinish)},
	} {
		for _, fault := range []struct {
			name, tail string
			failed     bool
		}{
			{"complete", path.finish, false},
			{"upstream_error", `data: {"error":{"type":"upstream_error","message":"synthetic fixture error"}}` + "\n\n", true},
			{"eof", "", true},
			{"malformed_json", "data: {oops}\n\n", true},
		} {
			t.Run(fmt.Sprintf("%s/%s", path.name, fault.name), func(t *testing.T) {
				info, usage, events := forkManagedFinalizerOpenAI(t, path.upstream, path.client, path.start+fault.tail)
				require.Equal(t, fault.failed, info.StreamResult.Failed)
				require.Equal(t, !fault.failed, info.StreamSession.ProtocolComplete())
				doneTools, errors, success := 0, 0, 0
				for _, event := range events {
					if relaycommon.IsStreamErrorEvent("", []byte(event.Raw)) {
						errors++
					}
					switch event.Get("type").String() {
					case "response.function_call_arguments.done":
						doneTools++
					case "response.output_item.done":
						if event.Get("item.type").String() == "function_call" {
							doneTools++
						}
					case "content_block_stop":
						doneTools++
					case "response.completed", "message_stop":
						success++
					}
					if event.Get("choices.0.finish_reason").String() != "" || event.Get("candidates.0.finishReason").String() != "" {
						success++
					}
				}
				if fault.failed {
					require.Equal(t, 1, errors)
					require.Zero(t, success, "must not synthesize a successful terminal")
					require.Zero(t, doneTools, "unfinished tools must not gain a successful closure")
					require.False(t, info.StreamResult.EffectiveContent, "partial tools alone cannot become effective delivery")
					require.Zero(t, usage.CompletionTokens, "explicit zero output cannot become an unfinished-tool estimate")
				} else {
					require.Positive(t, success)
					require.Equal(t, 17, usage.CompletionTokens)
				}
				if fault.failed {
					// Failed streams release all usage without effective delivery.
					require.Zero(t, usage.PromptTokens)
				} else {
					require.Equal(t, 91, usage.PromptTokens)
				}
			})
		}
	}
}
