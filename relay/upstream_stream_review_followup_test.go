package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Follow-ups to the branch-audit fixes of the upstream-stream adaptation
// (docs/design/non-stream-timeout-loss-prevention.md §21): M8 same-name
// index-less calls, M6 untyped and empty error fields, and Azure's per-choice
// content_filter_results. Each asserts what the plain non-stream path
// (OpenaiHandler) does with the same upstream answer.

// M8: two whole index-less, id-less calls to the same function are two calls
// with their own arguments, and per-call tool pricing counts both — as the
// plain path does for the same two calls.
func TestHandleAdaptedUpstreamStream_SameNameIndexlessCallsBilledPerCall(t *testing.T) {
	operation_setting.SetToolPriceForTest("audit_same_fn", 0.01)
	defer operation_setting.SetToolPriceForTest("audit_same_fn", 0)

	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"audit_same_fn","arguments":"{\"city\":\"A\"}"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"type":"function","function":{"name":"audit_same_fn","arguments":"{\"city\":\"B\"}"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	info.OriginModelName = "gpt-4o"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	calls := out.Choices[0].Message.ParseToolCalls()
	require.Len(t, calls, 2, "tool_calls: %s", gjson.Get(rec.Body.String(), "choices.0.message.tool_calls").Raw)
	assert.Equal(t, `{"city":"A"}`, calls[0].Function.Arguments)
	assert.Equal(t, `{"city":"B"}`, calls[1].Function.Arguments)
	assert.Equal(t, 2, toolCallCount(info, "audit_same_fn"), "priced once per call")
}

// jsonValueTracker decides completeness from structure fed in pieces; the
// split points below fall inside strings, escapes and nesting on purpose.
func TestJSONValueTracker(t *testing.T) {
	cases := []struct {
		name   string
		pieces []string
		want   bool
	}{
		{"nothing fed", nil, false},
		{"empty fragments", []string{"", ""}, false},
		{"empty object", []string{"{}"}, true},
		{"empty array", []string{"[]"}, true},
		{"object across fragments", []string{`{"a"`, `:1`, `}`}, true},
		{"open object", []string{`{"a":1`}, false},
		{"open nested", []string{`{"a":{}`}, false},
		{"brace inside a string", []string{`{"a":"}"`}, false},
		{"brace inside a string, closed", []string{`{"a":"}"}`}, true},
		{"escaped quote split at the backslash", []string{`{"a":"x\`, `"}`}, false},
		{"escaped quote split, closed", []string{`{"a":"x\`, `"}"}`}, true},
		{"escaped backslash before the quote", []string{`{"a":"x\\`, `"}`}, true},
		{"leading and trailing whitespace", []string{" \n{}", "\t\r\n "}, true},
		{"second value after the close", []string{"{}", "{}"}, false},
		{"junk after the close", []string{"{}x"}, false},
		{"close before open", []string{"}"}, false},
		{"extra close", []string{"{}}"}, false},
		{"top-level scalar", []string{"1"}, false},
		{"top-level string", []string{`"{}"`}, false},
		{"balanced but invalid", []string{`{"a":}`}, false},
		{"unicode", []string{`{"城市":"北京"}`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tracker jsonValueTracker
			var text strings.Builder
			for _, piece := range tc.pieces {
				text.WriteString(piece)
				tracker.feed(piece)
			}
			assert.Equal(t, tc.want, tracker.complete(text.String()))
			assert.Equal(t, tc.want, tracker.complete(text.String()), "asking again gives the same answer")
		})
	}
}

// jsonValueTracker validates at most once: once closed, only whitespace can be
// fed without breaking it, so the cached verdict stays right and repeated
// questions (one per same-name fragment) never rescan the arguments.
func TestJSONValueTracker_ValidatesOnce(t *testing.T) {
	var tracker jsonValueTracker
	tracker.feed(`{"a":1}`)
	require.True(t, tracker.complete(`{"a":1}`))
	// The verdict is cached, so a different text is not looked at again;
	// that is safe only because the tracker saw every byte written.
	assert.True(t, tracker.complete(`not json`))
	tracker.feed("  ")
	assert.True(t, tracker.complete(`{"a":1}  `))
	tracker.feed("x")
	assert.False(t, tracker.complete(`{"a":1}  x`), "anything after the close breaks it")
}

// M6: an untyped error object's message reaches err.Error(), the text
// service.ShouldDisableChannel runs the auto-disable keywords over. Reported
// as the generic "upstream stream error", it never matched.
func TestAdaptedStreamErrorFrame_UntypedMessageReachesAutoDisableMatching(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info,
		upstreamSSE(`data: {"error":{"message":"Your credit balance is too low","code":"billing"}}`+"\n"))
	require.NotNil(t, apiErr)
	got := apiErr.ToOpenAIError()
	assert.Equal(t, "Your credit balance is too low", got.Message)
	assert.Equal(t, "upstream_error", got.Type)
	assert.Equal(t, types.ErrorCode("billing"), apiErr.GetErrorCode())
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	hit, _ := service.AcSearch(strings.ToLower(apiErr.Error()), []string{"credit balance is too low"}, true)
	assert.True(t, hit, "auto-disable keyword matching sees %q", apiErr.Error())
}

// M6: normal chunks carrying an error field that holds nothing are content.
// These are stream chunks, and main's stream handler (OaiStreamHandler) never
// reads an error field. The plain body path agrees where its check is looser
// (null and objects without a type); on "", false, 0 and [] it would fail,
// but no upstream answers a non-stream call with those beside its content, so
// here they are judged as the stream frames they are.
func TestHandleAdaptedUpstreamStream_EmptyErrorFieldIsContent(t *testing.T) {
	plainAgrees := map[string]bool{"null": true, "empty object": true, "object of empty members": true}
	for name, field := range map[string]string{
		"null": `null`, "empty object": `{}`, "empty string": `""`, "false": `false`, "zero": `0`,
		"empty array": `[]`, "object of empty members": `{"code":0,"message":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if plainAgrees[name] {
				plainC, plainRec, plainInfo := bufferedCase(t)
				plainInfo.UpstreamStreamAdapted = false
				plainBody := `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","error":` + field +
					`,"choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],` +
					`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
				_, plainErr := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{}, Body: io.NopCloser(strings.NewReader(plainBody))})
				require.Nil(t, plainErr, "precondition: the plain path does not fail on it")
				require.Equal(t, "Hello", gjson.Get(plainRec.Body.String(), "choices.0.message.content").String())
			}

			body := `data: {"id":"c","created":1,"model":"gpt-4o","error":` + field + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}` + "\n" +
				`data: {"id":"c","error":` + field + `,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}` + "\n" +
				"data: [DONE]\n"
			c, rec, info := bufferedCase(t)
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "Hello", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
		})
	}
}

// L13 sibling: Azure's per-choice content_filter_results. The plain path
// forwards it on each choice; the stream repeats it on the choice of many
// frames, and the adapted body carries the last non-empty one.
func TestHandleAdaptedUpstreamStream_AzureContentFilterResults(t *testing.T) {
	const plain = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,` +
		`"content_filter_results":{"hate":{"filtered":false,"severity":"low"}},"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`
	plainC, plainRec, plainInfo := bufferedCase(t)
	plainInfo.UpstreamStreamAdapted = false
	_, plainErr := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{}, Body: io.NopCloser(strings.NewReader(plain))})
	require.Nil(t, plainErr)
	require.Equal(t, "low", gjson.Get(plainRec.Body.String(), "choices.0.content_filter_results.hate.severity").String(),
		"precondition: the plain path forwards it")

	stream := `data: {"choices":[],"prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}}}]}` + "\n" +
		`data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"content_filter_results":{},"delta":{"role":"assistant","content":""}}]}` + "\n" +
		`data: {"id":"c","choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}},"delta":{"content":"o"}}]}` + "\n" +
		`data: {"id":"c","choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"low"}},"delta":{"content":"k"}}]}` + "\n" +
		`data: {"id":"c","choices":[{"index":0,"content_filter_results":{},"delta":{},"finish_reason":"stop"}]}` + "\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}` + "\n" +
		"data: [DONE]\n"
	c, rec, info := bufferedCase(t)
	info.ChannelType = constant.ChannelTypeAzure
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(stream))
	require.Nil(t, apiErr)
	assert.Equal(t, gjson.Get(plainRec.Body.String(), "choices.0.content_filter_results").Raw,
		gjson.Get(rec.Body.String(), "choices.0.content_filter_results").Raw, "body: %s", rec.Body.String())
	assert.False(t, gjson.Get(rec.Body.String(), "choices.0.message.content_filter_results").Exists(), "on the choice, not the message")
	assert.Equal(t, "safe", gjson.Get(rec.Body.String(), "prompt_filter_results.0.content_filter_results.hate.severity").String(),
		"the prompt verdict's nested content_filter_results stays where it was")
}

// content_filter_results in the aggregator: replaced, not appended; charged to
// the budget only for growth; an empty object never replaces a verdict and
// alone yields "{}"; a frame without it adds nothing.
func TestAggregator_ContentFilterResults(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.AddFrameExtras(`{"choices":[{"index":0,"delta":{"content":"content_filter_results"}}]}`)
	assert.Empty(t, a.choices, "the word in content is not the field")
	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":null,"delta":{}}]}`)
	assert.Empty(t, a.choices, "only an object is a verdict")

	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":{ },"delta":{}}]}`)
	require.Contains(t, a.choices, 0)
	assert.Equal(t, "{}", a.choices[0].contentFilterResults, "only empty ones seen")

	const small = `{"hate":{"filtered":false}}`
	const large = `{"hate":{"filtered":false,"severity":"safe"}}`
	before := a.budget
	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":` + large + `}]}`)
	assert.Equal(t, large, a.choices[0].contentFilterResults)
	assert.Equal(t, before-len(large)+len("{}"), a.budget, "charged for the growth of what is held")

	before = a.budget
	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":` + large + `}]}`)
	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":` + small + `}]}`)
	assert.Equal(t, small, a.choices[0].contentFilterResults, "later wins")
	assert.Equal(t, before, a.budget, "a repeat or a smaller one is not charged again")
	a.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":{}}]}`)
	assert.Equal(t, small, a.choices[0].contentFilterResults, "an empty object does not replace a verdict")

	full := newChatStreamAggregator("m")
	full.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":` + small + `}]}`)
	full.budget = len(large) - len(small) - 1
	full.AddFrameExtras(`{"choices":[{"index":0,"content_filter_results":` + large + `}]}`)
	assert.Equal(t, small, full.choices[0].contentFilterResults, "kept when the budget cannot hold the growth")
	assert.True(t, full.OverBudget())
}
