package gemini

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

// Gemini emits complete functionCall objects, but a Responses item must not be
// finalized successfully when the enclosing Gemini generation then fails.
func TestForkManagedGeminiFinalizerRejectsUnfinishedGeneration(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	start := "data: " + `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"name":"lookup_item","args":{"item":"alpha"}}}]}}],"usageMetadata":{"promptTokenCount":91,"candidatesTokenCount":0,"totalTokenCount":91}}` + "\n\n"
	finish := "data: " + `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":91,"candidatesTokenCount":17,"totalTokenCount":108}}` + "\n\n"
	for _, fault := range []struct {
		name, tail string
		failed     bool
	}{
		{"complete", finish, false},
		{"upstream_error", "data: " + `{"error":{"code":503,"message":"synthetic fixture error","status":"UNAVAILABLE"}}` + "\n\n", true},
		{"eof", "", true},
		{"malformed_json", "data: {oops}\n\n", true},
	} {

		t.Run(fmt.Sprintf("%s", fault.name), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatGemini, OriginModelName: "gemini-fixture", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-fixture"}}
			info.SetEstimatePromptTokens(91)
			service.BeginStreamAttempt(c, info)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(start + fault.tail))}
			info.StreamSession.ObserveTransport(response, nil)
			info.StreamDiagnostic.Observe(response)
			info.StreamSession.ObserveHTTP(response)
			usage, apiErr := GeminiResponsesStreamHandler(c, info, response)
			require.Nil(t, apiErr)
			usage = service.FinalizeStreamUsage(c, info, usage)
			require.False(t, info.StreamResult.SettlementAttempted)
			require.Equal(t, fault.failed, info.StreamResult.Failed)
			require.Equal(t, !fault.failed, info.StreamSession.ProtocolComplete())
			doneTools, errors, success := 0, 0, 0
			for frame := range strings.SplitSeq(recorder.Body.String(), "\n\n") {
				_, data := relaycommon.StreamFramePayload([]byte(frame))
				if len(data) == 0 {
					continue
				}
				require.True(t, gjson.ValidBytes(data))
				event := gjson.ParseBytes(data)
				if relaycommon.IsStreamErrorEvent("", data) {
					errors++
				}
				if event.Get("type").String() == "response.function_call_arguments.done" || event.Get("type").String() == "response.output_item.done" && event.Get("item.type").String() == "function_call" {
					doneTools++
				}
				if event.Get("type").String() == "response.completed" {
					success++
				}
			}
			if fault.failed {
				require.Equal(t, 1, errors)
				require.Zero(t, success)

				require.Zero(t, doneTools, "no success finalizer after a scanner-owned failure")

			} else {
				require.Equal(t, 1, success)
				require.Equal(t, 2, doneTools)
				require.Equal(t, 17, usage.CompletionTokens)
			}
			if fault.failed {
				require.Zero(t, usage.TotalTokens)
				require.False(t, info.StreamResult.EffectiveContent)
				require.Equal(t, "none", info.StreamResult.UsageSource)
			} else {
				require.Equal(t, 91, usage.PromptTokens)
			}
		})

	}
}

// Grounding uses a separate hosted bridge before the ordinary finalizer. Its
// delayed synthetic search item needs the same completion gate as client tools.
func TestForkManagedGeminiHostedFinalizerGuard(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.SetMode(oldMode)
	})
	start := "data: " + `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Synthetic answer"}]},"groundingMetadata":{"webSearchQueries":["fixture alpha","fixture beta","fixture alpha"]}}],"usageMetadata":{"promptTokenCount":91,"candidatesTokenCount":1,"totalTokenCount":92}}` + "\n\n"
	finish := "data: " + `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":91,"candidatesTokenCount":17,"totalTokenCount":108}}` + "\n\n"
	for _, fault := range []struct {
		name, tail string
		failed     bool
	}{
		{"complete", finish, false},
		{"upstream_error", "data: " + `{"error":{"code":503,"message":"synthetic fixture error","status":"UNAVAILABLE"}}` + "\n\n", true},
		{"eof", "", true},
		{"malformed_json", "data: {oops}\n\n", true},
	} {

		t.Run(fmt.Sprintf("%s", fault.name), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatGemini, OriginModelName: "gemini-fixture", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-fixture"}}
			info.SetEstimatePromptTokens(91)
			service.BeginStreamAttempt(c, info)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(start + fault.tail))}
			info.StreamSession.ObserveTransport(response, nil)
			info.StreamDiagnostic.Observe(response)
			info.StreamSession.ObserveHTTP(response)
			usage, apiErr := GeminiResponsesStreamHandler(c, info, response)
			require.Nil(t, apiErr)
			usage = service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, fault.failed, info.StreamResult.Failed)
			require.False(t, info.StreamResult.SettlementAttempted)
			require.NotNil(t, info.ResponsesUsageInfo)
			require.Contains(t, info.ResponsesUsageInfo.BuiltInTools, dto.BuildInToolGoogleSearch)
			require.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolGoogleSearch].CallCount, "actual upstream search evidence remains counted")
			searchDone, searchAdded, errors, success := 0, 0, 0, 0
			for frame := range strings.SplitSeq(recorder.Body.String(), "\n\n") {
				_, data := relaycommon.StreamFramePayload([]byte(frame))
				if len(data) == 0 {
					continue
				}
				require.True(t, gjson.ValidBytes(data))
				event := gjson.ParseBytes(data)
				if relaycommon.IsStreamErrorEvent("", data) {
					errors++
				}
				if event.Get("type").String() == "response.web_search_call.completed" {
					searchDone++
				}
				if event.Get("item.type").String() == "web_search_call" {
					if event.Get("type").String() == "response.output_item.added" {
						searchAdded++
					}
					if event.Get("type").String() == "response.output_item.done" {
						searchDone++
						require.Equal(t, "completed", event.Get("item.status").String())
						require.Len(t, event.Get("item.action.queries").Array(), 2)
					}
				}
				if event.Get("type").String() == "response.completed" {
					success++
				}
			}
			if fault.failed {
				require.Equal(t, 1, errors)
				require.Zero(t, success)

				require.Zero(t, searchAdded)
				require.Zero(t, searchDone)

			} else {
				require.Zero(t, errors)
				require.Equal(t, 1, success)
				require.Equal(t, 1, searchAdded)
				require.Equal(t, 2, searchDone)
				require.Equal(t, 17, usage.CompletionTokens)
			}
			require.Equal(t, 91, usage.PromptTokens)
		})

	}
}
