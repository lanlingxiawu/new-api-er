package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Findings of the independent review of the upstream-stream adaptation
// (docs/design/non-stream-timeout-loss-prevention.md §22): M2 untyped error
// objects on content frames, and L8 tool-call placement and Azure verdicts.

// M2: an error object without a type on a frame that carries output is not an
// error. The plain path (OpenaiHandler) fails only on a typed error, so it
// returns this answer; the adapted path discarded it, refunded and retried —
// buying the answer again — and fed channel auto-disable.
func TestAdaptedStreamErrorFrame_UntypedErrorOnContentFrame(t *testing.T) {
	cases := []struct {
		name    string
		frame   string
		wantErr bool
	}{
		{"code only beside content", `{"error":{"code":200},"choices":[{"index":0,"delta":{"content":"hi"}}]}`, false},
		{"success message beside content", `{"error":{"message":"success","code":0},"choices":[{"index":0,"delta":{"content":"hi"}}]}`, false},
		{"beside a role delta", `{"error":{"code":200},"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`, false},
		{"beside a tool call", `{"error":{"code":200},"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`, false},
		{"beside a finish reason", `{"error":{"message":"ok"},"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, false},
		{"beside usage", `{"error":{"code":200},"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`, false},
		// Frames that carry nothing but the error stay errors.
		{"no choices", `{"error":{"code":200}}`, true},
		{"empty choices", `{"error":{"message":"overloaded"},"choices":[]}`, true},
		{"empty delta", `{"error":{"message":"overloaded"},"choices":[{"index":0,"delta":{}}]}`, true},
		{"delta of empty members", `{"error":{"message":"overloaded"},"choices":[{"index":0,"delta":{"content":"","tool_calls":[],"refusal":null},"finish_reason":null}]}`, true},
		{"empty usage", `{"error":{"message":"overloaded"},"choices":[],"usage":{"prompt_tokens":0}}`, true},
		// A typed error fails even beside content, as on the plain path.
		{"typed beside content", `{"error":{"message":"boom","type":"server_error"},"choices":[{"index":0,"delta":{"content":"hi"}}]}`, true},
		// A string error is typed "error" by GetOpenAIError: fatal on the plain path too.
		{"string beside content", `{"error":"boom","choices":[{"index":0,"delta":{"content":"hi"}}]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := adaptedStreamErrorFrame(tc.frame)
			if tc.wantErr {
				assert.NotNil(t, got)
			} else {
				assert.Nil(t, got, "error: %v", got)
			}
		})
	}
}

// M2 end to end: an upstream that stamps an untyped error object on every
// chunk — the usage frame included — returns the answer with HTTP 200 and
// settles on upstream's usage, as the plain path does with the same body.
func TestHandleAdaptedUpstreamStream_UntypedErrorOnEveryChunkIsContent(t *testing.T) {
	for name, field := range map[string]string{
		"code 200":        `{"code":200}`,
		"success message": `{"message":"success","code":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			plainC, plainRec, plainInfo := bufferedCase(t)
			plainInfo.UpstreamStreamAdapted = false
			plainBody := `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","error":` + field +
				`,"choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
			_, plainErr := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{}, Body: io.NopCloser(strings.NewReader(plainBody))})
			require.Nil(t, plainErr, "precondition: the plain path does not fail on it")
			require.Equal(t, "Hello", gjson.Get(plainRec.Body.String(), "choices.0.message.content").String())

			body := strings.Join([]string{
				`data: {"id":"c","created":1,"model":"gpt-4o","error":` + field + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
				`data: {"id":"c","error":` + field + `,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
				`data: {"id":"c","error":` + field + `,"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
				"data: [DONE]",
			}, "\n") + "\n"
			c, rec, info := bufferedCase(t)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "Hello", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
			require.NotNil(t, usage)
			assert.Equal(t, 3, usage.PromptTokens)
			assert.Equal(t, 1, usage.CompletionTokens)
		})
	}
}

// M2, the other side: an untyped error with a message on a frame that is only
// the error still fails the request, and its message still reaches the text
// channel auto-disable matching reads.
func TestHandleAdaptedUpstreamStream_UntypedErrorFrameAfterContentStillFails(t *testing.T) {
	body := `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":"part"}}]}` + "\n" +
		`data: {"error":{"message":"Your credit balance is too low"},"choices":[]}` + "\n"
	c, rec, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.NotNil(t, apiErr)
	assert.Equal(t, "Your credit balance is too low", apiErr.ToOpenAIError().Message)
	assert.Equal(t, "upstream_error", apiErr.ToOpenAIError().Type)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Empty(t, rec.Body.String())
}

type wantToolCall struct{ id, name, args string }

// assembleToolCalls feeds each delta as the only tool call of choice 0 and
// returns the assembled calls.
func assembleToolCalls(t *testing.T, deltas []string) (*chatStreamAggregator, []dto.ToolCallRequest) {
	t.Helper()
	a := newChatStreamAggregator("m")
	for _, delta := range deltas {
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, decodeStreamChunk(`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[`+delta+`]}}]}`, &chunk))
		a.AddChunk(&chunk)
	}
	return a, a.Snapshot("fallback", false).Choices[0].Message.ParseToolCalls()
}

func assertToolCalls(t *testing.T, a *chatStreamAggregator, calls []dto.ToolCallRequest, want []wantToolCall) {
	t.Helper()
	require.Len(t, calls, len(want), "calls: %+v", calls)
	for i, w := range want {
		assert.Equal(t, w.id, calls[i].ID, "call %d id", i)
		assert.Equal(t, w.name, calls[i].Function.Name, "call %d name", i)
		assert.Equal(t, w.args, calls[i].Function.Arguments, "call %d arguments", i)
	}
	assert.Equal(t, len(want), a.ToolCallCount())
}

// L8: same-name index-less, id-less calls whose arguments are empty or blank,
// and a name repeated after the arguments finished.
//
// The rule: a same-name fragment starts a new call only when the current
// call's arguments are complete JSON and the fragment brings non-blank
// arguments, a call header (type) or an id the current call does not have; or
// when the current call has no arguments yet and the fragment is a header with
// blank arguments too. A name-only repeat never starts a call.
func TestAggregator_IndexlessSameNameBlankArguments(t *testing.T) {
	cases := []struct {
		name   string
		deltas []string
		want   []wantToolCall
	}{
		{
			name: "two headers with empty arguments are two calls",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"type":"function","function":{"name":"f","arguments":""}}`,
			},
			want: []wantToolCall{{"", "f", ""}, {"", "f", ""}},
		},
		{
			name: "two headers with blank arguments are two calls",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":" "}}`,
				`{"type":"function","function":{"name":"f","arguments":"\n"}}`,
			},
			want: []wantToolCall{{"", "f", " "}, {"", "f", "\n"}},
		},
		{
			name: "two headers with empty arguments then arguments go to the second",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{}"}}`,
			},
			want: []wantToolCall{{"", "f", ""}, {"", "f", `{}`}},
		},
		{
			// Providers that re-send the header on every fragment open the call
			// with empty arguments; the header bringing arguments continues it.
			name: "a header re-sent on every fragment continues the call",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"type":"function","function":{"name":"f","arguments":"{\"a\""}}`,
				`{"type":"function","function":{"name":"f","arguments":":1}"}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}},
		},
		{
			name: "a name repeated with empty arguments after they finished is no new call",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{\"a\":1}"}}`,
				`{"function":{"name":"f","arguments":""}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}},
		},
		{
			name: "a bare name repeat after the arguments finished is no new call",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}`,
				`{"function":{"name":"f"}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}},
		},
		{
			name: "a name repeat before any arguments continues the call",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{}"}}`,
			},
			want: []wantToolCall{{"", "f", `{}`}},
		},
		{
			name: "a header after complete arguments starts a call even with empty arguments",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}`,
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{\"a\":2}"}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}, {"", "f", `{"a":2}`}},
		},
		{
			name: "a new id after complete arguments starts a call even with empty arguments",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}`,
				`{"id":"call_2","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{\"a\":2}"}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}, {"call_2", "f", `{"a":2}`}},
		},
		{
			name: "a late id with a name repeat before any arguments continues the call",
			deltas: []string{
				`{"function":{"name":"f","arguments":""}}`,
				`{"id":"call_1","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"{}"}}`,
			},
			want: []wantToolCall{{"call_1", "f", `{}`}},
		},
		{
			name: "a header while the arguments are open continues the call",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":"{\"a\":"}}`,
				`{"type":"function","function":{"name":"f","arguments":""}}`,
				`{"function":{"arguments":"1}"}}`,
			},
			want: []wantToolCall{{"", "f", `{"a":1}`}},
		},
		{
			// Known limit: function arguments are a JSON object per the API, so a
			// bare scalar never reads as complete and a second same-name call
			// without index or id folds into it.
			name: "bare scalar arguments never complete",
			deltas: []string{
				`{"type":"function","function":{"name":"f","arguments":"1"}}`,
				`{"type":"function","function":{"name":"f","arguments":"2"}}`,
			},
			want: []wantToolCall{{"", "f", "12"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, calls := assembleToolCalls(t, tc.deltas)
			assertToolCalls(t, a, calls, tc.want)
		})
	}
}

// L8 billing: two same-name, id-less, index-less calls with empty arguments
// are priced as two calls.
func TestHandleAdaptedUpstreamStream_SameNameEmptyArgumentCallsBilledPerCall(t *testing.T) {
	operation_setting.SetToolPriceForTest("audit_empty_fn", 0.01)
	defer operation_setting.SetToolPriceForTest("audit_empty_fn", 0)

	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"audit_empty_fn","arguments":""}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"type":"function","function":{"name":"audit_empty_fn","arguments":""}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	info.OriginModelName = "gpt-4o"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Choices[0].Message.ParseToolCalls(), 2)
	assert.Equal(t, 2, toolCallCount(info, "audit_empty_fn"), "priced once per call")
}

// L8: index-less calls never share a slot with an indexed call, whichever
// arrives first — an indexed call used to land on the slot an earlier
// index-less call had taken (the highest index + 1, or 0 for the first call).
func TestAggregator_IndexlessAndIndexedCallsNeverCollide(t *testing.T) {
	cases := []struct {
		name   string
		deltas []string
		want   []wantToolCall
	}{
		{
			name: "indexed call on the slot after an index-less one",
			deltas: []string{
				`{"index":0,"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{}"}}`,
				`{"id":"call_b","type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
				`{"index":1,"id":"call_c","type":"function","function":{"name":"fn_c","arguments":"{}"}}`,
			},
			want: []wantToolCall{{"call_a", "fn_a", `{}`}, {"call_b", "fn_b", `{}`}, {"call_c", "fn_c", `{}`}},
		},
		{
			name: "indexed call 0 after a first index-less call",
			deltas: []string{
				`{"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{}"}}`,
				`{"index":0,"id":"call_b","type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
			},
			want: []wantToolCall{{"call_a", "fn_a", `{}`}, {"call_b", "fn_b", `{}`}},
		},
		{
			name: "index-less fragments continue the most recent indexed call",
			deltas: []string{
				`{"index":2,"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{\"x\""}}`,
				`{"function":{"arguments":":1}"}}`,
				`{"id":"call_b","type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
				`{"index":3,"id":"call_c","type":"function","function":{"name":"fn_c","arguments":"{}"}}`,
			},
			want: []wantToolCall{{"call_a", "fn_a", `{"x":1}`}, {"call_b", "fn_b", `{}`}, {"call_c", "fn_c", `{}`}},
		},
		{
			name: "a negative upstream index is clamped to 0, never into the index-less slots",
			deltas: []string{
				`{"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{}"}}`,
				`{"index":-1,"id":"call_b","type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
			},
			want: []wantToolCall{{"call_a", "fn_a", `{}`}, {"call_b", "fn_b", `{}`}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, calls := assembleToolCalls(t, tc.deltas)
			assertToolCalls(t, a, calls, tc.want)
		})
	}
}

// L8: index-less calls of several choices are placed per choice — a call on
// one choice never continues or splits another choice's call.
func TestAggregator_IndexlessToolCallsMultiChoice(t *testing.T) {
	frames := []string{
		// Both choices open a same-name call in one frame.
		`{"id":"c","choices":[` +
			`{"index":0,"delta":{"tool_calls":[{"type":"function","function":{"name":"f","arguments":"{\"c\":0"}}]}},` +
			`{"index":1,"delta":{"tool_calls":[{"type":"function","function":{"name":"f","arguments":"{\"c\":1"}}]}}]}`,
		// Continuations arrive in the other order.
		`{"id":"c","choices":[` +
			`{"index":1,"delta":{"tool_calls":[{"function":{"arguments":"}"}}]}},` +
			`{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"}"}}]}}]}`,
		// Choice 1 makes a second whole call; choice 0 repeats its name only.
		`{"id":"c","choices":[{"index":1,"delta":{"tool_calls":[{"type":"function","function":{"name":"f","arguments":"{\"c\":11}"}}]}}]}`,
		`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"name":"f","arguments":""}}]}}]}`,
		// Two whole calls of choice 0 in one frame, the second to another function.
		`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[` +
			`{"type":"function","function":{"name":"f","arguments":"{\"c\":2}"}},` +
			`{"type":"function","function":{"name":"g","arguments":"{}"}}]}}]}`,
	}
	a := newChatStreamAggregator("m")
	for _, frame := range frames {
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, decodeStreamChunk(frame, &chunk))
		a.AddChunk(&chunk)
	}
	snapshot := a.Snapshot("fallback", false)
	require.Len(t, snapshot.Choices, 2)
	calls0 := snapshot.Choices[0].Message.ParseToolCalls()
	calls1 := snapshot.Choices[1].Message.ParseToolCalls()
	require.Len(t, calls0, 3, "choice 0: %+v", calls0)
	require.Len(t, calls1, 2, "choice 1: %+v", calls1)
	assert.Equal(t, `{"c":0}`, calls0[0].Function.Arguments)
	assert.Equal(t, `{"c":2}`, calls0[1].Function.Arguments)
	assert.Equal(t, "g", calls0[2].Function.Name)
	assert.Equal(t, `{"c":1}`, calls1[0].Function.Arguments)
	assert.Equal(t, `{"c":11}`, calls1[1].Function.Arguments)
	assert.Equal(t, 5, a.ToolCallCount())
}

// L8: a category Azure once flagged (filtered: true) stays flagged: a later
// verdict that reports it unfiltered, or omits it, does not clear it. Other
// categories follow the latest verdict.
func TestAggregator_ContentFilterResultsKeepFlaggedCategories(t *testing.T) {
	frame := func(verdict string) string {
		return `{"choices":[{"index":0,"content_filter_results":` + verdict + `,"delta":{}}]}`
	}
	a := newChatStreamAggregator("m")
	a.AddFrameExtras(frame(`{"hate":{"filtered":false,"severity":"safe"},"violence":{"filtered":false,"severity":"safe"}}`))
	a.AddFrameExtras(frame(`{"hate":{"filtered":true,"severity":"high"},"violence":{"filtered":false,"severity":"safe"}}`))
	a.AddFrameExtras(frame(`{"hate":{"filtered":false,"severity":"safe"},"violence":{"filtered":false,"severity":"low"}}`))
	got := a.choices[0].contentFilterResults
	assert.True(t, gjson.Get(got, "hate.filtered").Bool(), "verdict: %s", got)
	assert.Equal(t, "high", gjson.Get(got, "hate.severity").String())
	assert.Equal(t, "low", gjson.Get(got, "violence.severity").String(), "other categories follow the latest")

	a.AddFrameExtras(frame(`{"violence":{"filtered":false,"severity":"medium"}}`))
	got = a.choices[0].contentFilterResults
	assert.True(t, gjson.Get(got, "hate.filtered").Bool(), "a verdict omitting it does not clear it: %s", got)
	assert.Equal(t, "medium", gjson.Get(got, "violence.severity").String())
	assert.True(t, gjson.Valid(got))

	// Repeating the same verdict is a no-op for the budget.
	before := a.budget
	a.AddFrameExtras(frame(`{"violence":{"filtered":false,"severity":"medium"}}`))
	assert.Equal(t, got, a.choices[0].contentFilterResults)
	assert.Equal(t, before, a.budget)

	// A flag on a later verdict is kept alongside the earlier one.
	a.AddFrameExtras(frame(`{"violence":{"filtered":true,"severity":"high"}}`))
	got = a.choices[0].contentFilterResults
	assert.True(t, gjson.Get(got, "hate.filtered").Bool(), "verdict: %s", got)
	assert.True(t, gjson.Get(got, "violence.filtered").Bool(), "verdict: %s", got)
}
