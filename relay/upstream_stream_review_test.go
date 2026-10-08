package relay

// Regression tests for the 2026-09-24 external review of the adapted
// (non-stream-to-stream) path. Each was verified to fail on the unfixed code.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Audio output can neither be assembled nor safely streamed (streamed audio is
// pcm16-only), so any request that may produce it stays non-stream.
func TestShouldAdaptUpstreamStream_AudioOutputRejects(t *testing.T) {
	cases := []struct {
		name       string
		modalities string
		audio      string
		want       bool
	}{
		{"text and audio modalities", `["text","audio"]`, ``, false},
		{"audio parameter alone", ``, `{"voice":"alloy","format":"wav"}`, false},
		{"unreadable modalities treated as audio", `"audio"`, ``, false},
		{"text only stays eligible", `["text"]`, ``, true},
		{"explicit nulls stay eligible", `null`, `null`, true},
		{"absent stays eligible", ``, ``, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info, request := adaptCase(t)
			request.Modalities = json.RawMessage(tc.modalities)
			request.Audio = json.RawMessage(tc.audio)
			assert.Equal(t, tc.want, shouldAdaptUpstreamStream(c, info, request))
		})
	}
}

// The legacy functions API streams delta.function_call, which the stream DTO
// does not model: adapting it returned finish_reason "function_call" with no
// function in the body.
func TestShouldAdaptUpstreamStream_LegacyFunctionsRejects(t *testing.T) {
	c, info, request := adaptCase(t)
	request.Functions = json.RawMessage(`[{"name":"get_weather","parameters":{}}]`)
	assert.False(t, shouldAdaptUpstreamStream(c, info, request))

	c, info, request = adaptCase(t)
	request.FunctionCall = json.RawMessage(`"auto"`)
	assert.False(t, shouldAdaptUpstreamStream(c, info, request))

	c, info, request = adaptCase(t)
	request.Functions = json.RawMessage(`null`)
	assert.True(t, shouldAdaptUpstreamStream(c, info, request), "an explicit null is no request for functions")
}

// Chat Completions routed through the Responses API is answered by that path's
// own handler, which refunds on timeout: adapting it only rewrote the request.
func TestShouldAdaptUpstreamStream_ChatViaResponsesRejects(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	saved := settings.ChatCompletionsToResponsesPolicy
	defer func() { settings.ChatCompletionsToResponsesPolicy = saved }()

	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
		Enabled: true, AllChannels: true, ModelPatterns: []string{"^gpt-4o$"},
	}
	c, info, request := adaptCase(t)
	info.OriginModelName = "gpt-4o"
	assert.False(t, shouldAdaptUpstreamStream(c, info, request))

	// Control: a model the policy does not match is still adapted.
	c, info, request = adaptCase(t)
	info.OriginModelName = "gpt-4.1"
	assert.True(t, shouldAdaptUpstreamStream(c, info, request))
}

// A plain non-stream call forwards upstream's body, so these fields reached the
// client before adaptation; they must still reach it after.
func TestHandleAdaptedUpstreamStream_KeepsNonStreamFields(t *testing.T) {
	c, rec, info := bufferedCase(t)
	upstreamUsage := `{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"completion_tokens_details":{"reasoning_tokens":0,"accepted_prediction_tokens":0}}`
	body := strings.Join([]string{
		`data: {"id":"c1","created":7,"model":"gpt-4o","system_fingerprint":"fp_1","service_tier":"default","choices":[{"index":0,"delta":{"role":"assistant","content":null,"refusal":null}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"refusal":"I can't "}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"refusal":"help."}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"annotations":[{"type":"url_citation","url_citation":{"url":"https://a"}}]}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"annotations":[{"type":"url_citation","url_citation":{"url":"https://b"}}]},"finish_reason":"stop"}]}`,
		`data: {"id":"c1","choices":[],"usage":` + upstreamUsage + `}`,
		`data: [DONE]`,
		"",
	}, "\n")

	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	out := rec.Body.String()

	assert.Equal(t, "fp_1", gjson.Get(out, "system_fingerprint").String())
	assert.Equal(t, "default", gjson.Get(out, "service_tier").String())
	assert.Equal(t, "I can't help.", gjson.Get(out, "choices.0.message.refusal").String())
	assert.Equal(t, gjson.Null, gjson.Get(out, "choices.0.message.content").Type,
		"a declined structured output reports content null, as non-stream does")
	urls := gjson.Get(out, "choices.0.message.annotations.#.url_citation.url").Array()
	require.Len(t, urls, 2, "annotations from every frame are kept, in order")
	assert.Equal(t, "https://a", urls[0].String())
	assert.Equal(t, "https://b", urls[1].String())

	assert.JSONEq(t, upstreamUsage, gjson.Get(out, "usage").Raw,
		"usage is upstream's object verbatim, without internal dto.Usage fields")
	assert.NotContains(t, out, "claude_cache_creation")
}

// A plain non-stream response to a tool call has content null, not "". Agent
// frameworks branch on content being null to decide the turn is a tool call,
// so the re-assembled response must say the same (real upstream check: Azure
// gpt-4.1-nano via a new-api upstream).
func TestHandleAdaptedUpstreamStream_ToolCallContentShape(t *testing.T) {
	cases := []struct {
		name    string
		frames  []string
		want    gjson.Type
		wantStr string
	}{
		{"tool calls only → null", []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`,
		}, gjson.Null, ""},
		// Upstreams that convert Claude/Gemini tool calls answer content "" in
		// both modes (real upstream check); keep what the stream said.
		{"tool calls with content \"\" in the stream → \"\"", []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		}, gjson.String, ""},
		{"text plus tool calls → text kept", []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"Checking."}}]}`,
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		}, gjson.String, "Checking."},
		{"no text, no tool call → empty string, as non-stream", []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"length"}]}`,
		}, gjson.String, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			body := strings.Join(append(tc.frames, `data: [DONE]`, ""), "\n")
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			require.Nil(t, apiErr)
			content := gjson.Get(rec.Body.String(), "choices.0.message.content")
			require.True(t, content.Exists())
			assert.Equal(t, tc.want, content.Type)
			assert.Equal(t, tc.wantStr, content.String())
		})
	}
}

// Ordinary frames carry "refusal": null; that is not a refusal and must not
// null out real content.
func TestHandleAdaptedUpstreamStream_NullRefusalIsNotARefusal(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel","refusal":null}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"lo","refusal":null},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	out := rec.Body.String()
	assert.Equal(t, "Hello", gjson.Get(out, "choices.0.message.content").String())
	assert.False(t, gjson.Get(out, "choices.0.message.refusal").Exists())
	assert.False(t, gjson.Get(out, "choices.0.message.annotations").Exists())
	assert.False(t, gjson.Get(out, "service_tier").Exists())
}

// When upstream reported no usage the estimate is what gets billed, so it is
// what the client sees — the raw-usage substitution must not apply.
func TestHandleAdaptedUpstreamStream_EstimatedUsageIsReported(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"some words here"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0)
	assert.Equal(t, int64(usage.CompletionTokens), gjson.Get(rec.Body.String(), "usage.completion_tokens").Int())
}

func pricedToolStream(calls ...string) string {
	lines := make([]string, 0, len(calls)*2+2)
	for i, name := range calls {
		idx := string(rune('0' + i))
		// Name in the first fragment, arguments split over a second one: the
		// call must still be counted once.
		lines = append(lines,
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":`+idx+`,"id":"t`+idx+`","type":"function","function":{"name":"`+name+`","arguments":"{\"a\""}}]}}]}`,
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":`+idx+`,"function":{"arguments":":1}"}}]}}]}`,
		)
	}
	lines = append(lines, `data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	return strings.Join(lines, "\n") + "\n"
}

func toolCallCount(info *relaycommon.RelayInfo, name string) int {
	if info.ResponsesUsageInfo == nil {
		return 0
	}
	if tool := info.ResponsesUsageInfo.BuiltInTools[name]; tool != nil {
		return tool.CallCount
	}
	return 0
}

// This path bypasses the handlers that count per-call tool usage; without its
// own count a priced tool settled for nothing.
func TestHandleAdaptedUpstreamStream_CountsPricedToolCalls(t *testing.T) {
	operation_setting.SetToolPriceForTest("priced_fn", 0.01)
	defer operation_setting.SetToolPriceForTest("priced_fn", 0)

	c, _, info := bufferedCase(t)
	info.OriginModelName = "gpt-4o"
	body := pricedToolStream("priced_fn", "priced_fn", "free_fn") + "data: [DONE]\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)

	assert.Equal(t, 2, toolCallCount(info, "priced_fn"), "one count per call, not per fragment")
	assert.Equal(t, 0, toolCallCount(info, "free_fn"), "unpriced functions are not billed per call")
}

// On a timeout the calls that already arrived are settled too, and they must be
// counted before settlement reads them.
func TestHandleAdaptedUpstreamStream_TimeoutCountsPricedToolCalls(t *testing.T) {
	operation_setting.SetToolPriceForTest("priced_fn", 0.01)
	defer operation_setting.SetToolPriceForTest("priced_fn", 0)

	c, _, info := bufferedCase(t)
	info.OriginModelName = "gpt-4o"
	markTimedOut(c)

	countAtSettlement := -1
	restore := stubSettlement(func(_ *gin.Context, settledInfo *relaycommon.RelayInfo, _ *dto.Usage, _ []string) {
		countAtSettlement = toolCallCount(settledInfo, "priced_fn")
	})
	defer restore()

	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(pricedToolStream("priced_fn")))
	require.Nil(t, apiErr)
	assert.Equal(t, 1, countAtSettlement)
}

// releaseRecorder stands in for the timeout controller and records whether the
// handler ended the hold.
type releaseRecorder struct{ released int }

func (r *releaseRecorder) ReleaseRelayResponse() bool {
	r.released++
	return true
}

// A completed stream ends the hold, as a plain non-stream response's arrival
// stops the response timer; otherwise the timer runs on into assembly and
// settlement and can 504 an answer that was already billed.
func TestHandleAdaptedUpstreamStream_ReleasesTimerWhenComplete(t *testing.T) {
	c, _, info := bufferedCase(t)
	recorder := &releaseRecorder{}
	c.Set(string(constant.ContextKeyRelayTimeoutControl), recorder)
	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}` + "\n" + `data: [DONE]` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, 1, recorder.released)
}

// An upstream error frame is a failed attempt: the hold stays until the next
// attempt restarts the timer.
func TestHandleAdaptedUpstreamStream_KeepsTimerOnUpstreamError(t *testing.T) {
	c, _, info := bufferedCase(t)
	recorder := &releaseRecorder{}
	c.Set(string(constant.ContextKeyRelayTimeoutControl), recorder)
	body := `data: {"error":{"message":"boom","type":"server_error"}}` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.NotNil(t, apiErr)
	assert.Equal(t, 0, recorder.released)
}

// A malformed frame after real output delivers that output, marked incomplete,
// instead of failing: failing refunds the user and retries, and upstream bills
// the retry again while the aggregator already held the answer.
func TestHandleAdaptedUpstreamStream_MalformedTailDeliversPrefix(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":123}}]}`,
		"",
	}, "\n")
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	out := rec.Body.String()
	assert.Equal(t, "Hello", gjson.Get(out, "choices.0.message.content").String())
	assert.Equal(t, "length", gjson.Get(out, "choices.0.finish_reason").String(),
		"a delivered prefix is reported as incomplete")
}

// With nothing received yet there is no prefix to save: still an error.
func TestHandleAdaptedUpstreamStream_MalformedFirstFrameIsAnError(t *testing.T) {
	c, _, info := bufferedCase(t)
	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"content":123}}]}` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.NotNil(t, apiErr)
}

// An upstream that writes created as a float works over plain non-stream calls;
// adapting it must not fail every frame.
func TestHandleAdaptedUpstreamStream_FloatCreatedUpstream(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","created":1790000000.5,"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, "ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	assert.Equal(t, int64(1790000000), gjson.Get(rec.Body.String(), "created").Int())
}

// recordingAdaptor captures what is re-sent; only DoRequest is ever called.
type recordingAdaptor struct {
	channel.Adaptor
	sent   [][]byte
	status int
}

func (a *recordingAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (any, error) {
	raw, _ := io.ReadAll(body)
	a.sent = append(a.sent, raw)
	return &http.Response{StatusCode: a.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func rejected(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Your organization must be verified to stream this model."}}`))}
}

func adaptedStorage(t *testing.T) common.BodyStorage {
	t.Helper()
	storage, err := common.CreateBodyStorage([]byte(`{"model":"o3","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

// Upstream refusing to stream a request it would serve non-stream must not
// turn a working request into a failure: re-send it plain, once.
func TestFallBackFromRejectedAdaptation_ResendsPlainOn400(t *testing.T) {
	// OpenAI's "organization must be verified to stream this model".
	c, _, info := bufferedCase(t)
	adaptor := &recordingAdaptor{status: http.StatusOK}

	resp, _, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, rejected(http.StatusBadRequest), adaptedStorage(t))
	require.Nil(t, apiErr)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the plain re-send's response is what gets handled")
	require.Len(t, adaptor.sent, 1, "exactly one re-send")
	assert.False(t, gjson.GetBytes(adaptor.sent[0], "stream").Exists())
	assert.False(t, gjson.GetBytes(adaptor.sent[0], "stream_options").Exists())
	assert.Equal(t, "o3", gjson.GetBytes(adaptor.sent[0], "model").String(), "everything else is unchanged")
	assert.False(t, info.UpstreamStreamAdapted, "the re-sent request is handled as plain non-stream")
}

// The stream flag can change which code path an upstream takes, and so which
// error it returns: a new-api upstream answered an unreachable image URL with
// 400 (plain call, the provider rejects it) but 500 (stream call, its own
// token counting downloads the image and fails). Client errors and 500 are
// what the stream path may have caused; they get the plain re-send, so the user
// ends up with the outcome the unadapted request would have had.
func TestFallBackFromRejectedAdaptation_ResendsPlainOnAnyStreamPathFailure(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest, http.StatusNotFound, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity,
		http.StatusInternalServerError,
	} {
		c, _, info := bufferedCase(t)
		adaptor := &recordingAdaptor{status: http.StatusOK}
		resp, _, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, rejected(status), adaptedStorage(t))
		require.Nil(t, apiErr, "status %d", status)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "status %d: the plain re-send's response is handled", status)
		assert.Len(t, adaptor.sent, 1, "status %d: exactly one re-send", status)
		assert.False(t, info.UpstreamStreamAdapted, "status %d", status)
	}
}

// Auth, upstream balance, payload size, rate limiting, timeouts and upstream
// overload do not depend on the stream flag. A re-send would only add an upstream call outside the retry
// budget (and, for timeouts, double the wait): during an upstream outage every
// attempt would hit it twice. These go through the ordinary error path, where
// the retry loop counts them.
func TestFallBackFromRejectedAdaptation_StreamAgnosticStatusesUntouched(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusRequestTimeout,
		http.StatusRequestEntityTooLarge, http.StatusTooManyRequests, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		520, 524, 529,
	} {
		c, _, info := bufferedCase(t)
		adaptor := &recordingAdaptor{status: http.StatusOK}
		original := rejected(status)
		resp, _, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, original, adaptedStorage(t))
		require.Nil(t, apiErr)
		assert.Same(t, original, resp, "status %d", status)
		assert.Empty(t, adaptor.sent, "status %d", status)
		assert.True(t, info.UpstreamStreamAdapted)
	}
}

// A request that was never adapted has nothing to fall back to.
func TestFallBackFromRejectedAdaptation_NotAdapted(t *testing.T) {
	c, _, info := bufferedCase(t)
	info.UpstreamStreamAdapted = false
	adaptor := &recordingAdaptor{status: http.StatusOK}
	original := rejected(http.StatusBadRequest)
	resp, _, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, original, nil)
	require.Nil(t, apiErr)
	assert.Same(t, original, resp)
	assert.Empty(t, adaptor.sent)
}

// A 400 that had nothing to do with streaming comes back again from the plain
// re-send; that response is returned for the ordinary error path, so the user
// sees what the unadapted request would have shown. No second fallback.
func TestFallBackFromRejectedAdaptation_PlainAlso400(t *testing.T) {
	c, _, info := bufferedCase(t)
	adaptor := &recordingAdaptor{status: http.StatusBadRequest}
	resp, _, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, rejected(http.StatusBadRequest), adaptedStorage(t))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Len(t, adaptor.sent, 1)
}

// lazyAdaptor keeps the request body for reading after DoRequest returns, the
// way a real transport keeps writing it while the response is being read.
type lazyAdaptor struct {
	channel.Adaptor
	body io.Reader
}

func (a *lazyAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (any, error) {
	a.body = body
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

// The re-sent body must outlive the call that sends it: an upstream may answer
// before the body is fully sent (HTTP/2, early errors), and a disk-cached body
// closed at that point has its file deleted under the transport. The caller
// owns it until the response is done.
func TestFallBackFromRejectedAdaptation_PlainBodyOutlivesTheCall(t *testing.T) {
	c, _, info := bufferedCase(t)
	adaptor := &lazyAdaptor{}
	resp, plainBody, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, rejected(http.StatusBadRequest), adaptedStorage(t))
	require.Nil(t, apiErr)
	require.NotNil(t, resp)
	require.NotNil(t, plainBody, "the caller receives the re-sent body to close")
	sent, err := io.ReadAll(adaptor.body)
	require.NoError(t, err, "the body is still readable after the fallback returns")
	assert.Equal(t, "o3", gjson.GetBytes(sent, "model").String())
	require.NoError(t, plainBody.Close())
	_, err = io.ReadAll(adaptor.body)
	assert.Error(t, err, "closing it is the caller's job")
}

// relayInfo is reused across retry attempts. An earlier attempt may have been
// adapted on a channel that supports it; the next attempt's channel may not,
// and a leftover true would make the response side treat an unmodified request
// as SSE. Every text attempt starts unadapted and decides afresh.
func TestTextHelper_AttemptStartsUnadapted(t *testing.T) {
	c, _, info := bufferedCase(t)
	info.UpstreamStreamAdapted = true
	info.Request = nil // fails the request type check right after the reset
	apiErr := TextHelper(c, info)
	require.NotNil(t, apiErr)
	assert.False(t, info.UpstreamStreamAdapted)
}
