package relay

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Response shape: what a real OpenAI client actually parses
// ---------------------------------------------------------------------------

// choices is typed as an array in the OpenAI schema and SDKs iterate it
// directly; emitting null breaks them. A usage-only stream is the case that
// produces it.
func TestAudit_ChoicesNeverMarshalsAsNull(t *testing.T) {
	cases := map[string]string{
		"usage only":    `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}}`,
		"empty choices": `{"id":"c","choices":[]}`,
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			a := newChatStreamAggregator("m")
			feed(t, a, frame)
			body, err := common.Marshal(a.Snapshot("fb", false))
			require.NoError(t, err)
			assert.Contains(t, string(body), `"choices":[]`)
			assert.NotContains(t, string(body), `"choices":null`)
		})
	}
}

// created is a unix timestamp; 0 is not a valid one.
func TestAudit_CreatedIsAlwaysPlausible(t *testing.T) {
	a := newChatStreamAggregator("m")
	feed(t, a, `{"id":"c","choices":[{"index":0,"delta":{"content":"x"}}]}`)
	out := a.Snapshot("fb", false)
	created, ok := out.Created.(int64)
	require.True(t, ok, "created should be an int64 timestamp, got %T", out.Created)
	assert.Greater(t, created, int64(1_600_000_000), "created must be a real timestamp")

	// An upstream-provided created must win over the fallback.
	b := newChatStreamAggregator("m")
	feed(t, b, `{"id":"c","created":1790000000,"choices":[{"index":0,"delta":{"content":"x"}}]}`)
	assert.Equal(t, int64(1790000000), b.Snapshot("fb", false).Created)
}

// The assembled body must be shaped like a real non-stream completion end to
// end, not just contain the right text.
func TestAudit_AssembledBodyMatchesNonStreamSchema(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-x","created":1790000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
		`data: {"id":"chatcmpl-x","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}]}`,
		`data: {"id":"chatcmpl-x","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		`data: [DONE]`, "",
	}, "\n")

	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)

	raw := rec.Body.String()
	for _, want := range []string{`"object":"chat.completion"`, `"finish_reason":"stop"`, `"role":"assistant"`} {
		assert.Contains(t, raw, want)
	}
	for _, unwanted := range []string{"data:", "chat.completion.chunk", `"delta"`, `"choices":null`} {
		assert.NotContains(t, raw, unwanted, "response must not leak streaming shape")
	}
}

// ---------------------------------------------------------------------------
// Error-frame detection under adversarial content
// ---------------------------------------------------------------------------

// Content that merely talks about errors is not an error frame. JSON escaping
// means a quoted "error" inside content can never look like a top-level key,
// but the guard must hold regardless.
func TestAudit_ErrorFrameNotTriggeredByContent(t *testing.T) {
	benign := []string{
		`{"choices":[{"index":0,"delta":{"content":"the error was fixed"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"say \"error\" out loud"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"{\"error\":\"nested json in text\"}"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"error"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`{"choices":[{"index":0,"delta":{"error_count":3}}]}`,
	}
	for i, frame := range benign {
		t.Run(fmt.Sprintf("benign%d", i), func(t *testing.T) {
			assert.Nil(t, adaptedStreamErrorFrame(frame), "frame: %s", frame)
		})
	}

	hostile := []string{
		`{"error":{"message":"boom","type":"server_error"}}`,
		`{"error":{"message":"no type field"}}`,
		`{"error":"plain string error"}`,
	}
	for i, frame := range hostile {
		t.Run(fmt.Sprintf("hostile%d", i), func(t *testing.T) {
			assert.NotNil(t, adaptedStreamErrorFrame(frame), "frame: %s", frame)
		})
	}
}

// Content received before the error frame must not be billed as a success.
func TestAudit_ErrorAfterContentDiscardsTheResponse(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"partial answer"}}]}`,
		`data: {"error":{"message":"upstream died","type":"server_error"}}`, "",
	}, "\n")

	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.NotNil(t, apiErr, "a mid-stream upstream error must fail the request")
	assert.Nil(t, usage)
	assert.Zero(t, rec.Body.Len(), "no partial success body may be written")
}

// ---------------------------------------------------------------------------
// Upstream protocol gating (the Claude-parsed-as-OpenAI hazard)
// ---------------------------------------------------------------------------

// Every channel type in streamSupportedChannels must be checked: only those
// whose ApiType is OpenAI may be adapted. This asserts the property over the
// real whitelist rather than a hand-picked sample, so a future addition to
// streamSupportedChannels cannot silently re-open the hazard.
func TestAudit_OnlyOpenAIWireChannelsAreAdapted(t *testing.T) {
	for channelType := 1; channelType < 70; channelType++ {
		apiType, ok := common.ChannelType2APIType(channelType)
		if !ok {
			continue
		}
		c, info, request := adaptCase(t)
		info.ApiType = apiType
		got := shouldAdaptUpstreamStream(c, info, request)
		assert.Equal(t, apiType == constant.APITypeOpenAI, got,
			"channelType=%d apiType=%d must%s be adapted", channelType, apiType,
			map[bool]string{true: "", false: " not"}[apiType == constant.APITypeOpenAI])
	}
}

// ---------------------------------------------------------------------------
// Cross-attempt state
// ---------------------------------------------------------------------------

// relayInfo is reused across retries. Anything applyUpstreamStreamAdaptation
// writes must either be reset per attempt or be harmless to carry over; this
// pins exactly what it mutates so a future field addition is a conscious choice.
func TestAudit_AdaptationMutatesOnlyKnownState(t *testing.T) {
	c, info, request := adaptCase(t)
	before := struct {
		isStream      bool
		relayFormat   any
		relayMode     int
		upstreamModel string
	}{info.IsStream, info.RelayFormat, info.RelayMode, info.UpstreamModelName}

	applyUpstreamStreamAdaptation(c, info, request)

	assert.Equal(t, before.isStream, info.IsStream, "client-facing stream mode must not change")
	assert.Equal(t, before.relayFormat, info.RelayFormat)
	assert.Equal(t, before.relayMode, info.RelayMode)
	assert.Equal(t, before.upstreamModel, info.UpstreamModelName)
	assert.True(t, info.UpstreamStreamAdapted)
	assert.True(t, info.ShouldIncludeUsage)
}

// A retry that lands on a non-adaptable channel must produce an unadapted
// request even though the previous attempt adapted.
func TestAudit_RetryToNonOpenAIChannelDoesNotAdapt(t *testing.T) {
	c, info, request := adaptCase(t)
	require.True(t, shouldAdaptUpstreamStream(c, info, request))
	applyUpstreamStreamAdaptation(c, info, request)

	// Controller resets this per attempt (controller/relay.go).
	info.UpstreamStreamAdapted = false
	info.ApiType = constant.APITypeAnthropic
	retryRequest := &dto.GeneralOpenAIRequest{Model: "claude-3-5-sonnet"}

	assert.False(t, shouldAdaptUpstreamStream(c, info, retryRequest))
	assert.Nil(t, retryRequest.Stream, "the retry body must stay non-stream")
	assert.False(t, info.UpstreamStreamAdapted)
}

// ---------------------------------------------------------------------------
// Billing edges
// ---------------------------------------------------------------------------

// Upstream explicitly reporting zero tokens is information, not absence of it.
// ValidUsage treats all-zero as invalid and the handler then estimates, which
// bills for output upstream says it never produced. Documented here because the
// same rule governs the existing streaming path.
func TestAudit_UpstreamZeroUsageFallsBackToEstimate(t *testing.T) {
	c, _, info := bufferedCase(t)
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"content":"some text"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)

	usage := adaptedStreamUsage(c, info, a, "")
	assert.Greater(t, usage.CompletionTokens, 0,
		"current behaviour: an all-zero upstream usage is treated as unreported and estimated")
}

// A stream that yields only tool calls (no text) must still bill the surcharge
// and produce a well-formed body.
func TestAudit_ToolCallOnlyStream(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n")

	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "tool_calls", out.Choices[0].FinishReason)

	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, `{"q":"x"}`, calls[0].Function.Arguments)
}

// ---------------------------------------------------------------------------
// Malformed / hostile upstream framing
// ---------------------------------------------------------------------------

func TestAudit_HostileFraming(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantErr  bool
		wantBody bool
	}{
		{"only DONE", "data: [DONE]\n", true, false},
		{"empty body", "", true, false},
		{"only comments", ": ping\n: ping\n", true, false},
		{"garbage line then done", "garbage\ndata: [DONE]\n", true, false},
		{"malformed json", "data: {broken\n", true, false},
		{"valid then DONE", `data: {"choices":[{"index":0,"delta":{"content":"x"}}]}` + "\ndata: [DONE]\n", false, true},
		{"no trailing newline", `data: {"choices":[{"index":0,"delta":{"content":"x"}}]}`, false, true},
		{"CRLF framing", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\r\ndata: [DONE]\r\n", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			if tc.wantErr {
				assert.NotNil(t, apiErr)
			} else {
				assert.Nil(t, apiErr)
			}
			if tc.wantBody {
				assert.Greater(t, rec.Body.Len(), 0)
			}
		})
	}
}

// A timeout must be honoured for every framing shape: output received so far is
// delivered as a completion, no output means 504. Either way it settles once.
func TestAudit_TimeoutAcrossFramings(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
	}{
		"no data":      {"", http.StatusGatewayTimeout},
		"partial":      {`data: {"choices":[{"index":0,"delta":{"content":"half"}}]}` + "\n", http.StatusOK},
		"only comment": {": ping\n", http.StatusGatewayTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			markTimedOut(c)
			settled := 0
			restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) { settled++ })
			defer restore()

			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			require.Nil(t, apiErr, "a timeout is answered here, not returned as an error")
			assert.Equal(t, 1, settled, "exactly one settlement per timed-out request")
			assert.Equal(t, tc.status, rec.Code)
			assert.True(t, c.GetBool(relaycommon.StreamHandledKey))
		})
	}
}

// The aggregation budget must hold regardless of which field carries the bytes.
func TestAudit_BudgetAcrossFieldKinds(t *testing.T) {
	big := strings.Repeat("x", 1000)
	fields := map[string]string{
		"content":           `{"choices":[{"index":0,"delta":{"content":"` + big + `"}}]}`,
		"reasoning_content": `{"choices":[{"index":0,"delta":{"reasoning_content":"` + big + `"}}]}`,
		"reasoning":         `{"choices":[{"index":0,"delta":{"reasoning":"` + big + `"}}]}`,
		"tool arguments":    `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"` + big + `"}}]}}]}`,
	}
	for name, frame := range fields {
		t.Run(name, func(t *testing.T) {
			a := newChatStreamAggregator("m")
			// Room for the entries (a choice, a tool call), not for the text.
			a.budget = 2*aggregatedEntryBytes + 10
			feed(t, a, frame)
			assert.True(t, a.OverBudget(), "%s must be charged against the budget", name)
		})
	}
}

// Wall-clock sanity: the handler must not block past the upstream body ending.
func TestAudit_HandlerReturnsPromptly(t *testing.T) {
	c, _, info := bufferedCase(t)
	start := time.Now()
	_, _ = handleAdaptedUpstreamStream(c, info,
		upstreamSSE(`data: {"choices":[{"index":0,"delta":{"content":"x"}}]}`+"\ndata: [DONE]\n"))
	assert.Less(t, time.Since(start), 2*time.Second)
}
