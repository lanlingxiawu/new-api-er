package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Regression tests for the upstream-stream adaptation findings of the branch
// audit (docs/design/branch-audit-vs-main.md M6, M7, M8, L11, L12, L13). Each
// asserts what the plain non-stream path (OpenaiHandler) does with the same
// upstream answer.

var decimalByteList = regexp.MustCompile(`^\[\d+( \d+)*\]$`)

// M6: an in-band error frame is decoded as OpenaiHandler decodes a plain
// body's error field. Handing dto.GetOpenAIError a json.RawMessage lands in its
// catch-all: message "[123 34 109 ...]" (the frame's bytes), type and code
// "unknown_error", and channel auto-disable keyword matching sees garbage.
func TestBranchAuditRegression_ErrorFrameMessageIsByteList(t *testing.T) {
	const upstreamError = `{"error":{"message":"quota gone","type":"insufficient_quota","code":"insufficient_quota"}}`

	// Reference: the plain non-stream handler on the same error object.
	plainC, _, plainInfo := bufferedCase(t)
	plainInfo.UpstreamStreamAdapted = false
	plainErr := func() *types.NewAPIError {
		_, err := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
			Header: http.Header{}, Body: io.NopCloser(strings.NewReader(upstreamError))})
		return err
	}()
	require.NotNil(t, plainErr)
	require.Equal(t, "quota gone", plainErr.ToOpenAIError().Message)

	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE("data: "+upstreamError+"\n"))
	require.NotNil(t, apiErr)
	got := apiErr.ToOpenAIError()
	assert.Equal(t, "quota gone", got.Message, "client-visible message")
	assert.Equal(t, "insufficient_quota", got.Type)
	assert.Equal(t, types.ErrorCode("insufficient_quota"), apiErr.GetErrorCode())

	for name, frame := range map[string]string{
		"untyped object": `{"error":{"message":"rate limited"}}`,
		"string":         `{"error":"internal-host:8080 exploded"}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _, info := bufferedCase(t)
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE("data: "+frame+"\n"))
			require.NotNil(t, apiErr)
			msg := apiErr.ToOpenAIError().Message
			assert.False(t, decimalByteList.MatchString(msg), "message is the raw frame printed as bytes: %q", msg)
		})
	}
}

// M6, every shape of the error field: a typed object, an untyped object with a
// message (typed upstream_error) and a non-empty string surface as upstream's
// error; a field that carries nothing (null, {}, [], "", false, 0, an object of
// only such members) is no error, as OpenaiHandler does not fail on it; any
// other shape (a non-zero number, true, a non-empty array, an object with
// neither type nor message) is a generic upstream stream error that carries
// none of the frame.
func TestAdaptedStreamErrorFrame_ErrorFieldShapes(t *testing.T) {
	cases := []struct {
		name        string
		frame       string
		wantNil     bool
		wantMessage string
		wantType    string // "" for the generic upstream stream error
	}{
		{"typed object", `{"error":{"message":"boom","type":"server_error","code":"c1"}}`, false, "boom", "server_error"},
		{"typed object without message", `{"error":{"type":"server_error"}}`, false, "openai_error", "server_error"}, // ToOpenAIError fills an empty message
		{"string", `{"error":"upstream said no"}`, false, "upstream said no", "error"},
		{"untyped object with message", `{"error":{"message":"rate limited"}}`, false, "rate limited", "upstream_error"},
		{"untyped object with message and code", `{"error":{"message":"rate limited","code":429}}`, false, "rate limited", "upstream_error"},
		{"object with only a code", `{"error":{"code":"quota"}}`, false, "upstream stream error", ""},
		{"object with a non-string message", `{"error":{"message":42}}`, false, "upstream stream error", ""},
		{"number", `{"error":500}`, false, "upstream stream error", ""},
		{"true", `{"error":true}`, false, "upstream stream error", ""},
		{"array", `{"error":[123,34]}`, false, "upstream stream error", ""},
		{"null", `{"error":null}`, true, "", ""},
		{"empty object", `{"error":{}}`, true, "", ""},
		{"object of empty members", `{"error":{"code":0,"message":"","type":"","param":null,"details":[]}}`, true, "", ""},
		{"empty array", `{"error":[]}`, true, "", ""},
		{"empty string", `{"error":""}`, true, "", ""},
		{"false", `{"error":false}`, true, "", ""},
		{"zero", `{"error":0}`, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := adaptedStreamErrorFrame(tc.frame)
			if tc.wantNil {
				assert.Nil(t, apiErr)
				return
			}
			require.NotNil(t, apiErr)
			got := apiErr.ToOpenAIError()
			assert.Equal(t, tc.wantMessage, got.Message)
			if tc.wantType == "" {
				assert.Equal(t, types.ErrorCodeBadResponse, apiErr.GetErrorCode(), "generic upstream stream error")
			} else {
				assert.Equal(t, tc.wantType, got.Type)
			}
			assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode,
				"an error frame is a channel failure for the retry path")
		})
	}
}

// M7: a stream that closes cleanly (no [DONE], no read error, no finish_reason)
// after only a role frame — or after a frame that decodes to nothing, e.g. an
// "event: error" whose data has no "error" key — has no answer. It takes the
// retry/refund path, like the same prefix followed by a malformed frame or a
// read error (design §18 #3), instead of HTTP 200 with an empty message billed
// for the estimated input.
func TestBranchAuditRegression_CleanEOFWithoutOutputBilledAsSuccess(t *testing.T) {
	for name, body := range map[string]string{
		"role only":            `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n",
		"role then empty text": `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n\n",
		"event error without error key": `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
			"event: error\ndata: {\"message\":\"overloaded\"}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			info.SetEstimatePromptTokens(500)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			assert.NotNil(t, apiErr, "no usable output and no [DONE]: must go to the retry/refund path")
			assert.Nil(t, usage, "billed usage: %+v", usage)
			assert.Empty(t, rec.Body.String(), "HTTP 200 with an empty answer was written")
		})
	}
}

// M7, the other side of the line: upstream said it was done — [DONE] or a
// finish_reason — with an empty answer. The plain call returns that empty
// completion with HTTP 200 and bills it, so the adapted one does too rather
// than buying the same answer again on a retry.
func TestHandleAdaptedUpstreamStream_EmptyButFinishedAnswerIsDelivered(t *testing.T) {
	const usageFrame = `data: {"id":"c","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":0,"total_tokens":7}}` + "\n"
	for name, tc := range map[string]struct {
		body       string
		wantFinish string
	}{
		"[DONE] after a role frame": {
			body:       `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n" + usageFrame + "data: [DONE]\n",
			wantFinish: "",
		},
		"finish_reason without [DONE]": {
			body:       `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop"}]}` + "\n" + usageFrame,
			wantFinish: "stop",
		},
		"content_filter without [DONE]": {
			body:       `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"content_filter"}]}` + "\n" + usageFrame,
			wantFinish: "content_filter",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 7, usage.PromptTokens, "upstream's usage is billed")
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
			assert.Equal(t, tc.wantFinish, gjson.Get(rec.Body.String(), "choices.0.finish_reason").String())
		})
	}
}

// M7 decision table: the no-output branch of adaptedStreamAnswered, with each
// of its three escapes on its own.
func TestAdaptedStreamAnswered(t *testing.T) {
	feed := func(frames ...string) *chatStreamAggregator {
		a := newChatStreamAggregator("m")
		for _, frame := range frames {
			var chunk dto.ChatCompletionsStreamResponse
			require.NoError(t, decodeStreamChunk(frame, &chunk))
			a.AddChunk(&chunk)
		}
		return a
	}
	const role = `{"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	assert.False(t, adaptedStreamAnswered(feed(), true), "[DONE] alone: nothing received")
	assert.False(t, adaptedStreamAnswered(feed(role), false), "role only, no end signal")
	assert.True(t, adaptedStreamAnswered(feed(role), true), "role only, [DONE]")
	assert.True(t, adaptedStreamAnswered(feed(`{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`), false),
		"finish_reason is an end signal")
	assert.True(t, adaptedStreamAnswered(feed(`{"id":"c","choices":[{"index":0,"delta":{"content":"x"}}]}`), false),
		"output is always an answer")
}

// M8: tool-call deltas without "index" that carry a new id (or, without ids, a
// new function name) are a new call, not a continuation of the previous one.
// Merging them returned one call with the last name/id and arguments
// "{..}{..}" — invalid JSON for the wrong function — and billed per-call tool
// pricing once instead of twice.
func TestBranchAuditRegression_IndexlessToolCallsAreMerged(t *testing.T) {
	operation_setting.SetToolPriceForTest("audit_fn_a", 0.01)
	operation_setting.SetToolPriceForTest("audit_fn_b", 0.01)
	defer operation_setting.SetToolPriceForTest("audit_fn_a", 0)
	defer operation_setting.SetToolPriceForTest("audit_fn_b", 0)

	for name, frames := range map[string][]string{
		"separate frames": {
			`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"audit_fn_a","arguments":"{\"x\":1}"}}]}}]}`,
			`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_b","type":"function","function":{"name":"audit_fn_b","arguments":"{\"y\":2}"}}]}}]}`,
		},
		"same frame": {
			`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"audit_fn_a","arguments":"{\"x\":1}"}},{"id":"call_b","type":"function","function":{"name":"audit_fn_b","arguments":"{\"y\":2}"}}]}}]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := strings.Join(append(frames,
				`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: {"id":"c","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
				"data: [DONE]"), "\n") + "\n"
			c, rec, info := bufferedCase(t)
			info.OriginModelName = "gpt-4o"
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			require.Nil(t, apiErr)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
			calls := out.Choices[0].Message.ParseToolCalls()
			if assert.Len(t, calls, 2, "tool_calls: %s", gjson.Get(rec.Body.String(), "choices.0.message.tool_calls").Raw) {
				assert.Equal(t, "call_a", calls[0].ID)
				assert.Equal(t, "audit_fn_a", calls[0].Function.Name)
				assert.Equal(t, `{"x":1}`, calls[0].Function.Arguments)
				assert.Equal(t, "call_b", calls[1].ID)
				assert.Equal(t, `{"y":2}`, calls[1].Function.Arguments)
			}
			assert.Equal(t, 1, toolCallCount(info, "audit_fn_a"), "priced call a")
			assert.Equal(t, 1, toolCallCount(info, "audit_fn_b"), "priced call b")
		})
	}
}

// M8, the continuation side and the placement rule: index-less fragments that
// do not name a different call keep appending to the most recent one.
func TestAggregator_IndexlessToolCallPlacement(t *testing.T) {
	type wantCall struct{ id, name, args string }
	cases := []struct {
		name   string
		deltas []string
		want   []wantCall
	}{
		{
			name: "fragments without id continue the call",
			deltas: []string{
				`{"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{\"x\""}}`,
				`{"function":{"arguments":":1}"}}`,
			},
			want: []wantCall{{"call_a", "fn_a", `{"x":1}`}},
		},
		{
			name: "repeated id and name continue the call",
			deltas: []string{
				`{"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{\"x\""}}`,
				`{"id":"call_a","function":{"name":"fn_a","arguments":":1}"}}`,
			},
			want: []wantCall{{"call_a", "fn_a", `{"x":1}`}},
		},
		{
			name: "id arriving after a name-only fragment continues the call",
			deltas: []string{
				`{"type":"function","function":{"name":"fn_a","arguments":""}}`,
				`{"id":"call_a","function":{"arguments":"{}"}}`,
			},
			want: []wantCall{{"call_a", "fn_a", `{}`}},
		},
		{
			name: "without ids a different function name is a new call",
			deltas: []string{
				`{"type":"function","function":{"name":"fn_a","arguments":"{}"}}`,
				`{"type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
			},
			want: []wantCall{{"", "fn_a", `{}`}, {"", "fn_b", `{}`}},
		},
		{
			name: "two whole calls to the same function without id are two calls",
			deltas: []string{
				`{"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"A\"}"}}`,
				`{"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"B\"}"}}`,
			},
			want: []wantCall{{"", "get_weather", `{"city":"A"}`}, {"", "get_weather", `{"city":"B"}`}},
		},
		{
			name: "a call with an id after one without is compared by name",
			deltas: []string{
				`{"type":"function","function":{"name":"f1","arguments":"{}"}}`,
				`{"id":"call_2","type":"function","function":{"name":"f2","arguments":"{}"}}`,
			},
			want: []wantCall{{"", "f1", `{}`}, {"call_2", "f2", `{}`}},
		},
		{
			name: "a call with an id after a whole same-name call without one is a new call",
			deltas: []string{
				`{"type":"function","function":{"name":"f1","arguments":"{\"a\":1}"}}`,
				`{"id":"call_2","type":"function","function":{"name":"f1","arguments":"{\"a\":2}"}}`,
			},
			want: []wantCall{{"", "f1", `{"a":1}`}, {"call_2", "f1", `{"a":2}`}},
		},
		{
			name: "a whole same-name call without id after one with an id is a new call",
			deltas: []string{
				`{"id":"call_1","type":"function","function":{"name":"f1","arguments":"{\"a\":1}"}}`,
				`{"type":"function","function":{"name":"f1","arguments":"{\"a\":2}"}}`,
			},
			want: []wantCall{{"call_1", "f1", `{"a":1}`}, {"", "f1", `{"a":2}`}},
		},
		{
			name: "the name repeated on every fragment continues while the arguments are open",
			deltas: []string{
				`{"type":"function","function":{"name":"fn_a","arguments":"{\"x\":{\"y\":\"}\""}}`,
				`{"function":{"name":"fn_a","arguments":"}"}}`,
				`{"function":{"name":"fn_a","arguments":",\"z\":[1"}}`,
				`{"function":{"name":"fn_a","arguments":"]}"}}`,
			},
			want: []wantCall{{"", "fn_a", `{"x":{"y":"}"},"z":[1]}`}},
		},
		{
			name: "same-name headers followed by argument fragments are separate calls",
			deltas: []string{
				`{"type":"function","function":{"name":"fn","arguments":""}}`,
				`{"function":{"arguments":"{\"a\":"}}`,
				`{"function":{"arguments":"1}"}}`,
				`{"type":"function","function":{"name":"fn","arguments":""}}`,
				`{"function":{"arguments":"{\"a\":2}"}}`,
			},
			want: []wantCall{{"", "fn", `{"a":1}`}, {"", "fn", `{"a":2}`}},
		},
		{
			name: "a fragment naming no function continues even a complete call",
			deltas: []string{
				`{"type":"function","function":{"name":"fn","arguments":"{}"}}`,
				`{"function":{"arguments":" "}}`,
				`{"id":"call_late","function":{"arguments":""}}`,
			},
			want: []wantCall{{"call_late", "fn", `{} `}},
		},
		{
			name: "same name after balanced but invalid arguments continues the call",
			deltas: []string{
				`{"type":"function","function":{"name":"fn","arguments":"{\"a\":}"}}`,
				`{"type":"function","function":{"name":"fn","arguments":"{}"}}`,
			},
			want: []wantCall{{"", "fn", `{"a":}{}`}},
		},
		{
			name: "a new index-less call does not land on an indexed one",
			deltas: []string{
				`{"index":0,"id":"call_a","type":"function","function":{"name":"fn_a","arguments":"{}"}}`,
				`{"index":3,"id":"call_b","type":"function","function":{"name":"fn_b","arguments":"{}"}}`,
				`{"index":1,"id":"call_c","type":"function","function":{"name":"fn_c","arguments":"{}"}}`,
				`{"id":"call_d","type":"function","function":{"name":"fn_d","arguments":"{}"}}`,
			},
			want: []wantCall{{"call_a", "fn_a", `{}`}, {"call_b", "fn_b", `{}`}, {"call_c", "fn_c", `{}`}, {"call_d", "fn_d", `{}`}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newChatStreamAggregator("m")
			for _, delta := range tc.deltas {
				var chunk dto.ChatCompletionsStreamResponse
				require.NoError(t, decodeStreamChunk(`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[`+delta+`]}}]}`, &chunk))
				a.AddChunk(&chunk)
			}
			calls := a.Snapshot("fallback", false).Choices[0].Message.ParseToolCalls()
			require.Len(t, calls, len(tc.want))
			for i, want := range tc.want {
				assert.Equal(t, want.id, calls[i].ID, "call %d id", i)
				assert.Equal(t, want.name, calls[i].Function.Name, "call %d name", i)
				assert.Equal(t, want.args, calls[i].Function.Arguments, "call %d arguments", i)
			}
			assert.Equal(t, len(tc.want), a.ToolCallCount())
		})
	}
}

// L11: a request the timeout subsystem owns but that has no deadline (the user
// set both non-stream limits to -1) is not adapted: it can never time out, so
// changing how it talks to upstream is pure risk.
func TestBranchAuditRegression_UnlimitedTimeoutStillAdapts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting,
		&operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 0, TotalTimeoutSeconds: 0})
	common.SetContextKey(c, constant.ContextKeyUserNonStreamResponseTimeout, service.RelayTimeoutUnlimited)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, service.RelayTimeoutUnlimited)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "charge")
	middleware.StartRelayRequestTimeout(c, false)
	require.True(t, service.IsRelayTimeoutManaged(c), "precondition: -1/-1 installs the controller")
	_, hasDeadline := service.RelayRequestDeadline(c)
	require.False(t, hasDeadline, "precondition: no limit applies")

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{SupportStreamOptions: true, ApiType: constant.APITypeOpenAI},
	}
	assert.False(t, shouldAdaptUpstreamStream(c, info, &dto.GeneralOpenAIRequest{Model: "gpt-4o"}),
		"a request with no time limit gains nothing from the adaptation")
}

// L11, the other side against the real controller: either non-stream limit on
// its own is a deadline, including one limit set while the other is -1.
func TestShouldAdaptUpstreamStream_RealControllerWithALimitAdapts(t *testing.T) {
	cases := []struct {
		name            string
		response, total int
	}{
		{"response limit only", 30, service.RelayTimeoutUnlimited},
		{"total limit only", service.RelayTimeoutUnlimited, 30},
		{"both limits", 30, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting, &operation_setting.RelayTimeoutSetting{Enabled: true})
			common.SetContextKey(c, constant.ContextKeyUserNonStreamResponseTimeout, tc.response)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, tc.total)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "charge")
			middleware.StartRelayRequestTimeout(c, false)
			// Stop the controller's timers when the test ends.
			if control, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl); ok {
				if closer, ok := control.(interface{ Close() }); ok {
					t.Cleanup(closer.Close)
				}
			}

			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI,
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{SupportStreamOptions: true, ApiType: constant.APITypeOpenAI},
			}
			assert.True(t, shouldAdaptUpstreamStream(c, info, &dto.GeneralOpenAIRequest{Model: "gpt-4o"}))
		})
	}
}

// L12: with the channel's ForceFormat on, the plain path re-marshals the
// response through the standard DTO, so non-standard upstream fields do not
// reach the client. The adapted path must not splice upstream's raw usage (or
// any other non-DTO field) back in.
func TestBranchAuditRegression_ForceFormatIgnored(t *testing.T) {
	const plain = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6,"x_vendor_cost":9}}`
	stream := `data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}` + "\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6,"x_vendor_cost":9}}` + "\n" +
		"data: [DONE]\n"

	plainC, plainRec, plainInfo := bufferedCase(t)
	plainInfo.UpstreamStreamAdapted = false
	plainInfo.ChannelSetting.ForceFormat = true
	_, plainErr := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{}, Body: io.NopCloser(strings.NewReader(plain))})
	require.Nil(t, plainErr)
	require.False(t, gjson.Get(plainRec.Body.String(), "usage.x_vendor_cost").Exists(), "precondition: ForceFormat strips it")

	c, rec, info := bufferedCase(t)
	info.ChannelSetting.ForceFormat = true
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(stream))
	require.Nil(t, apiErr)
	assert.False(t, gjson.Get(rec.Body.String(), "usage.x_vendor_cost").Exists(),
		"ForceFormat channel leaked a non-standard usage field: %s", gjson.Get(rec.Body.String(), "usage").Raw)
}

// L12, field by field against the plain path: under ForceFormat every field
// the standard DTO does not model is dropped by both paths, and the null
// content of a tool-call-only turn (which dto.Message does model) is kept by
// both. Without ForceFormat the adapted path keeps them all.
func TestHandleAdaptedUpstreamStream_ForceFormatMatchesPlainPath(t *testing.T) {
	const plain = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","system_fingerprint":"fp_1","service_tier":"default",` +
		`"prompt_filter_results":[{"prompt_index":0}],` +
		`"choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}},"message":{"role":"assistant","content":null,"refusal":"no","annotations":[{"type":"url_citation"}],` +
		`"tool_calls":[{"id":"call_a","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":"tool_calls"}],` +
		`"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`
	stream := `data: {"choices":[],"prompt_filter_results":[{"prompt_index":0}]}` + "\n" +
		`data: {"id":"c","created":1,"model":"gpt-4o","system_fingerprint":"fp_1","service_tier":"default","choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}},"delta":{"role":"assistant","content":null,"refusal":"no","annotations":[{"type":"url_citation"}],` +
		`"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}` + "\n" +
		"data: [DONE]\n"
	dropped := []string{"system_fingerprint", "service_tier", "prompt_filter_results", "choices.0.content_filter_results",
		"choices.0.message.refusal", "choices.0.message.annotations"}

	plainC, plainRec, plainInfo := bufferedCase(t)
	plainInfo.UpstreamStreamAdapted = false
	plainInfo.ChannelSetting.ForceFormat = true
	_, plainErr := openai.OpenaiHandler(plainC, plainInfo, &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{}, Body: io.NopCloser(strings.NewReader(plain))})
	require.Nil(t, plainErr)

	c, rec, info := bufferedCase(t)
	info.ChannelSetting.ForceFormat = true
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(stream))
	require.Nil(t, apiErr)

	for _, path := range dropped {
		require.False(t, gjson.Get(plainRec.Body.String(), path).Exists(), "precondition: plain ForceFormat drops %s", path)
		assert.False(t, gjson.Get(rec.Body.String(), path).Exists(), "adapted ForceFormat kept %s: %s", path, rec.Body.String())
	}
	require.Equal(t, gjson.Null, gjson.Get(plainRec.Body.String(), "choices.0.message.content").Type, "precondition")
	assert.Equal(t, gjson.Null, gjson.Get(rec.Body.String(), "choices.0.message.content").Type,
		"a tool-call-only turn keeps content null under ForceFormat, as on the plain path")
	assert.Equal(t, "fn", gjson.Get(rec.Body.String(), "choices.0.message.tool_calls.0.function.name").String())

	c, rec, info = bufferedCase(t)
	_, apiErr = handleAdaptedUpstreamStream(c, info, upstreamSSE(stream))
	require.Nil(t, apiErr)
	for _, path := range dropped {
		assert.True(t, gjson.Get(rec.Body.String(), path).Exists(), "without ForceFormat %s is carried: %s", path, rec.Body.String())
	}
}

// L13: Azure OpenAI (ApiType OpenAI, stream-options capable, so adapted)
// reports prompt_filter_results — in the non-stream body at top level, in the
// stream on an early choices:[] frame. The plain path forwards it; the adapted
// response carries it too.
func TestBranchAuditRegression_AzurePromptFilterResultsDropped(t *testing.T) {
	stream := `data: {"choices":[],"created":0,"id":"","model":"","object":"","prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}}}]}` + "\n" +
		`data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}` + "\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}` + "\n" +
		"data: [DONE]\n"
	c, rec, info := bufferedCase(t)
	info.ChannelType = constant.ChannelTypeAzure
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(stream))
	require.Nil(t, apiErr)
	assert.True(t, gjson.Get(rec.Body.String(), "prompt_filter_results").IsArray(),
		"body: %s", rec.Body.String())
	assert.Equal(t, "safe", gjson.Get(rec.Body.String(), "prompt_filter_results.0.content_filter_results.hate.severity").String(),
		"carried verbatim")
}

// L13: the verdict is charged to the aggregation budget like other
// upstream-sized text; a non-array value or a frame without it adds nothing.
func TestAggregator_PromptFilterResults(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.AddFrameExtras(`{"choices":[],"prompt_filter_results":null}`)
	a.AddFrameExtras(`{"choices":[{"index":0,"delta":{"content":"prompt_filter_results"}}]}`)
	assert.Empty(t, a.promptFilterResults, "only an array is a verdict")

	const raw = `[{"prompt_index":0}]`
	before := a.budget
	a.AddFrameExtras(`{"choices":[],"prompt_filter_results":` + raw + `}`)
	assert.Equal(t, raw, a.promptFilterResults)
	assert.Equal(t, before-len(raw), a.budget, "charged to the budget")

	full := newChatStreamAggregator("m")
	full.budget = len(raw) - 1
	full.AddFrameExtras(`{"choices":[],"prompt_filter_results":` + raw + `}`)
	assert.Empty(t, full.promptFilterResults, "dropped once the budget cannot hold it")
	assert.True(t, full.OverBudget())
}
