package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bufferedCase(t *testing.T) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		RelayFormat: "openai",
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	info.UpstreamStreamAdapted = true
	return c, rec, info
}

func upstreamSSE(body string) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHandleAdaptedUpstreamStream_AssemblesJSONBody(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","created":7,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.PromptTokens)
	assert.Equal(t, 2, usage.CompletionTokens)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"),
		"a non-stream client must never be told this is an event stream")
	assert.NotContains(t, rec.Body.String(), "data:", "no SSE framing may reach the client")

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "chat.completion", out.Object)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "Hello", out.Choices[0].Message.StringContent())
	assert.Equal(t, "stop", out.Choices[0].FinishReason)
	assert.Equal(t, 6, out.Usage.TotalTokens)
}

// Upstream ended without [DONE]: the body is still delivered, flagged incomplete.
func TestHandleAdaptedUpstreamStream_TruncatedUpstream(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n"

	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "partial", out.Choices[0].Message.StringContent())
	assert.Equal(t, "length", out.Choices[0].FinishReason)
	assert.Greater(t, usage.CompletionTokens, 0,
		"locally estimated output must be billed when upstream reported no usage")
}

func TestHandleAdaptedUpstreamStream_CommentsAndBlankLinesIgnored(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`: ping`,
		``,
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		"",
	}, "\n")

	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "ok", out.Choices[0].Message.StringContent())
}

func TestHandleAdaptedUpstreamStream_NoContentIsAnError(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE("data: [DONE]\n"))
	require.NotNil(t, apiErr)
}

func TestHandleAdaptedUpstreamStream_MalformedChunkIsAnError(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE("data: {not json}\n"))
	require.NotNil(t, apiErr)
}

// brokenUpstream serves body, then fails the read the way a dropped connection does.
func brokenUpstream(body string) *http.Response {
	resp := upstreamSSE("")
	resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), failingReader{io.ErrUnexpectedEOF}))
	return resp
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// The connection dropped after output arrived. Upstream bills us for that
// output; failing would refund the user and retry, and upstream bills the retry
// again. It is delivered marked incomplete and its usage settled.
func TestHandleAdaptedUpstreamStream_ReadErrorKeepsReceivedOutput(t *testing.T) {
	c, rec, info := bufferedCase(t)
	resp := brokenUpstream(`data: {"id":"c","choices":[{"index":0,"delta":{"content":"paid output"}}]}` + "\n\n")

	usage, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0, "the output received is billed")
	assert.Equal(t, 1, info.ReceivedResponseCount)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "paid output", out.Choices[0].Message.StringContent())
	assert.Equal(t, "length", out.Choices[0].FinishReason, "a cut-off answer is marked incomplete")
}

// Without usable output there is nothing to deliver: the read error goes to the
// ordinary retry/refund path, whether nothing arrived or only a role-only delta.
func TestHandleAdaptedUpstreamStream_ReadErrorWithoutOutputIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"nothing":    "",
		"role only":  `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n",
		"usage only": `data: {"id":"c","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}` + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, brokenUpstream(body))
			require.NotNil(t, apiErr)
			assert.Nil(t, usage)
			assert.Empty(t, rec.Body.String(), "the controller writes the error, not the handler")
		})
	}
}

func TestHandleAdaptedUpstreamStream_NilResponse(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, nil)
	require.NotNil(t, apiErr)
}

// The reason this whole path exists: on the user's deadline we settle what
// upstream produced instead of refunding. What was produced is delivered: the
// user pays for it, so they get it, as an ordinary non-stream completion
// marked incomplete.
func TestHandleAdaptedUpstreamStream_TimeoutReturnsPartialOutput(t *testing.T) {
	c, rec, info := bufferedCase(t)
	markTimedOut(c)

	var settled *dto.Usage
	settleCalls := 0
	restore := stubSettlement(func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		settleCalls++
		settled = usage
	})
	defer restore()

	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"half an answer"}}]}` + "\n"
	returned, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)

	assert.Equal(t, 1, settleCalls, "received usage must be settled exactly once")
	require.NotNil(t, settled)
	assert.Greater(t, settled.CompletionTokens, 0,
		"the tokens upstream already produced (and billed us for) must be charged")
	assert.Same(t, settled, returned)

	assert.True(t, c.GetBool(relaycommon.StreamHandledKey),
		"the controller must be told not to refund or retry this request")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	out := rec.Body.String()
	assert.NotContains(t, out, "data:")
	assert.Equal(t, "chat.completion", gjson.Get(out, "object").String())
	assert.Equal(t, "half an answer", gjson.Get(out, "choices.0.message.content").String())
	assert.Equal(t, "length", gjson.Get(out, "choices.0.finish_reason").String(),
		"a cut-off answer carries the standard incomplete signal")
	assert.Equal(t, int64(settled.CompletionTokens), gjson.Get(out, "usage.completion_tokens").Int(),
		"the body reports what was billed")
	assert.False(t, gjson.Get(out, "error").Exists())
}

// Reasoning and tool calls are output too: upstream billed them and the user
// pays for them, so they are delivered like text.
func TestHandleAdaptedUpstreamStream_TimeoutReturnsReasoningAndToolCalls(t *testing.T) {
	cases := map[string]struct{ frame, path, want string }{
		"reasoning only": {
			frame: `data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking"}}]}`,
			path:  "choices.0.message.reasoning_content", want: "thinking",
		},
		"tool call": {
			frame: `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
			path:  "choices.0.message.tool_calls.0.function.name", want: "lookup",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			markTimedOut(c)
			restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})
			defer restore()

			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.frame+"\n"))
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.want, gjson.Get(rec.Body.String(), tc.path).String())
			assert.Equal(t, "length", gjson.Get(rec.Body.String(), "choices.0.finish_reason").String())
		})
	}
}

// A deadline that fires after upstream already finished (before the timer was
// released) delivers the whole answer with upstream's own finish_reason.
func TestHandleAdaptedUpstreamStream_TimeoutAfterCompletionKeepsFinishReason(t *testing.T) {
	c, rec, info := bufferedCase(t)
	markTimedOut(c)
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})
	defer restore()

	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "stop", gjson.Get(rec.Body.String(), "choices.0.finish_reason").String())
}

// Settlement writes the consume log, so upstream's content_filter finish must
// be recorded before it runs — on a timeout too, with or without output.
func TestHandleAdaptedUpstreamStream_TimeoutRecordsContentFilterBeforeSettlement(t *testing.T) {
	bodies := map[string]string{
		"with output": `data: {"choices":[{"index":0,"delta":{"content":"part"},"finish_reason":"content_filter"}]}`,
		"no output":   `data: {"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"content_filter"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			c, _, info := bufferedCase(t)
			markTimedOut(c)
			atSettlement := ""
			restore := stubSettlement(func(sc *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) {
				atSettlement = common.GetContextKeyString(sc, constant.ContextKeyAdminRejectReason)
			})
			defer restore()

			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body+"\n"))
			require.Nil(t, apiErr)
			assert.Equal(t, "openai_finish_reason=content_filter", atSettlement)
		})
	}
}

// A tool call that got no further than its id carries nothing usable.
func TestHandleAdaptedUpstreamStream_TimeoutIdOnlyToolCallIsNoOutput(t *testing.T) {
	c, rec, info := bufferedCase(t)
	markTimedOut(c)
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})
	defer restore()

	body := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function"}]}}]}` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
}

// Frames that parse but carry no output (a role-only delta, a usage frame) are
// nothing to deliver: the user gets the timeout, and what upstream counted is
// still settled.
func TestHandleAdaptedUpstreamStream_TimeoutWithoutOutputAnswers504(t *testing.T) {
	c, rec, info := bufferedCase(t)
	markTimedOut(c)
	settleCalls := 0
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) { settleCalls++ })
	defer restore()

	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, 1, settleCalls)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assert.Equal(t, "relay_timeout", gjson.Get(rec.Body.String(), "error.code").String())
	assert.True(t, c.GetBool(relaycommon.StreamHandledKey))
}

// After the deadline the timeout controller blocks ordinary writes; the partial
// completion is the request's one terminal payload and must go out through the
// controller's gate, or the client would receive nothing at all.
func TestHandleAdaptedUpstreamStream_TimeoutPartialPassesWriteGate(t *testing.T) {
	c, rec, info := bufferedCase(t)
	gate := &writeGate{}
	c.Writer = &gatedWriter{ResponseWriter: c.Writer, gate: gate}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, gatedTimedOutControl{gate: gate})
	common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 60)
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})
	defer restore()

	body := `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, 1, gate.opened)
	assert.Zero(t, gate.rejected, "nothing may be written outside the gate")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "partial", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

// writeGate mirrors the real timeout writer: writes pass only while the
// controller has the gate open.
type writeGate struct {
	open     bool
	opened   int
	rejected int
}

type gatedTimedOutControl struct{ gate *writeGate }

func (gatedTimedOutControl) RelayTimeoutKind() string { return service.RelayTimeoutKindResponse }

func (g gatedTimedOutControl) WriteTerminalError(write func()) {
	g.gate.open = true
	g.gate.opened++
	defer func() { g.gate.open = false }()
	write()
}

type gatedWriter struct {
	gin.ResponseWriter
	gate *writeGate
}

func (w *gatedWriter) allowed() bool {
	if !w.gate.open {
		w.gate.rejected++
	}
	return w.gate.open
}

func (w *gatedWriter) WriteHeader(code int) {
	if w.allowed() {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *gatedWriter) WriteHeaderNow() {
	if w.allowed() {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *gatedWriter) Write(data []byte) (int, error) {
	if !w.allowed() {
		return 0, context.DeadlineExceeded
	}
	return w.ResponseWriter.Write(data)
}

func (w *gatedWriter) WriteString(data string) (int, error) {
	if !w.allowed() {
		return 0, context.DeadlineExceeded
	}
	return w.ResponseWriter.WriteString(data)
}

// Timing out before upstream sent anything still must not refund-and-forget:
// it settles so the request is recorded, not silently dropped. The case has no
// price (a free model), so the usage is empty; a priced model bills the input
// (TestAdaptedTimeoutWithoutOutputBillsInput).
func TestHandleAdaptedUpstreamStream_TimeoutWithNoChunks(t *testing.T) {
	c, rec, info := bufferedCase(t)
	markTimedOut(c)

	var settled *dto.Usage
	restore := stubSettlement(func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		settled = usage
	})
	defer restore()

	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(""))
	require.Nil(t, apiErr)
	require.NotNil(t, settled)
	assert.Equal(t, 0, settled.CompletionTokens)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
}

// Upstream usage, when present, beats local estimation even on a timeout.
func TestAdaptedStreamUsage_PrefersUpstreamReport(t *testing.T) {
	c, _, info := bufferedCase(t)
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"content":"x"}}],"usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}`)

	usage := adaptedStreamUsage(c, info, a, "")
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 22, usage.CompletionTokens)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens),
		"confirmed upstream usage must not be flagged as locally counted")
}

func TestAdaptedStreamUsage_EstimatesWhenUpstreamSilent(t *testing.T) {
	c, _, info := bufferedCase(t)
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"content":"some generated words here"}}]}`)

	usage := adaptedStreamUsage(c, info, a, "")
	assert.Greater(t, usage.CompletionTokens, 0)
	assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens),
		"estimated usage must be flagged so the log shows local counting")
}

func TestAdaptedStreamUsage_NothingReceived(t *testing.T) {
	c, _, info := bufferedCase(t)
	usage := adaptedStreamUsage(c, info, newChatStreamAggregator("gpt-4o"), "")
	assert.Equal(t, 0, usage.CompletionTokens)
	assert.Equal(t, 0, usage.PromptTokens)
}

func TestAdaptedStreamUsage_ToolCallSurcharge(t *testing.T) {
	c, _, info := bufferedCase(t)
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"{}"}}]}}]}`)

	withTool := adaptedStreamUsage(c, info, a, "")

	c2, _, info2 := bufferedCase(t)
	plain := newChatStreamAggregator("gpt-4o")
	feed(t, plain, `{"choices":[{"index":0,"delta":{"content":"f{}"}}]}`)
	withoutTool := adaptedStreamUsage(c2, info2, plain, "")

	assert.Equal(t, withoutTool.CompletionTokens+7, withTool.CompletionTokens,
		"tool calls carry the same surcharge the streaming path applies")
}

func stubSettlement(fn func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string)) func() {
	previous := settleAdaptedTimeout
	settleAdaptedTimeout = fn
	return func() { settleAdaptedTimeout = previous }
}

// markTimedOut makes service.IsRelayRequestTimeout report a response-timer
// expiry without running the real middleware.
func markTimedOut(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, timedOutControl{})
	common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 60)
}

type timedOutControl struct{}

func (timedOutControl) RelayTimeoutKind() string { return service.RelayTimeoutKindResponse }

// An in-band error frame has no error field in ChatCompletionsStreamResponse, so
// it deserializes into an all-zero chunk. Without explicit detection the stream
// looks like it produced content and the client gets HTTP 200 with an empty
// message, billed for the input and with no retry.
func TestHandleAdaptedUpstreamStream_UpstreamErrorFrameIsAnError(t *testing.T) {
	frames := []string{
		`data: {"error":{"message":"upstream exploded","type":"server_error","code":"boom"}}` + "\n",
		`data: {"error":{"message":"rate limited"}}` + "\n",
		// Error arriving after real content must still fail the request.
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n" +
			`data: {"error":{"message":"died midway","type":"server_error"}}` + "\n",
	}
	for i, body := range frames {
		t.Run(fmt.Sprintf("frame%d", i), func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
			require.NotNil(t, apiErr, "an upstream error frame must not be reported as success")
			assert.Nil(t, usage)
			// The handler must leave the response to the controller's error path
			// rather than emitting a 200 body of its own.
			assert.Zero(t, rec.Body.Len(), "no success body may be written")
		})
	}
}

// A timed-out request whose upstream also emitted an error must surface the
// upstream error rather than settling partial usage against it.
func TestHandleAdaptedUpstreamStream_ErrorFrameBeatsTimeout(t *testing.T) {
	c, _, info := bufferedCase(t)
	markTimedOut(c)
	settled := 0
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) { settled++ })
	defer restore()

	_, apiErr := handleAdaptedUpstreamStream(c, info,
		upstreamSSE(`data: {"error":{"message":"boom","type":"server_error"}}`+"\n"))
	require.NotNil(t, apiErr)
	assert.Equal(t, 0, settled, "an upstream failure must go through refund, not settlement")
}

// Ordinary content that merely mentions the word error must not be mistaken for
// an error frame.
func TestAdaptedStreamErrorFrame_ContentIsNotAnError(t *testing.T) {
	assert.Nil(t, adaptedStreamErrorFrame(
		`{"id":"c","choices":[{"index":0,"delta":{"content":"the error was fixed"}}]}`))
	assert.Nil(t, adaptedStreamErrorFrame(`{"choices":[],"error":null}`))
	assert.Nil(t, adaptedStreamErrorFrame(`{"choices":[{"index":0,"delta":{}}]}`))
	assert.NotNil(t, adaptedStreamErrorFrame(`{"error":{"message":"x","type":"server_error"}}`))
	assert.NotNil(t, adaptedStreamErrorFrame(`{"error":{"message":"x"}}`))
}

// The adapted scanner is built locally to recycle its buffer; its line ceiling
// must stay the one every other relay stream gets from helper.NewStreamScanner.
func TestAdaptedScanMaxBytesMatchesHelper(t *testing.T) {
	prev := constant.StreamScannerMaxBufferMB
	t.Cleanup(func() { constant.StreamScannerMaxBufferMB = prev })

	constant.StreamScannerMaxBufferMB = 0
	require.Equal(t, helper.DefaultMaxScannerBufferSize, adaptedScanMaxBytes())

	for _, mb := range []int{1, 2} {
		constant.StreamScannerMaxBufferMB = mb
		line := strings.Repeat("x", adaptedScanMaxBytes()-1)
		scanner := helper.NewStreamScanner(strings.NewReader(line + "\n" + line + "x\n"))
		require.True(t, scanner.Scan(), "helper accepts a line just under the ceiling (mb=%d)", mb)
		require.False(t, scanner.Scan(), "helper rejects a line at the ceiling (mb=%d)", mb)
	}
}
