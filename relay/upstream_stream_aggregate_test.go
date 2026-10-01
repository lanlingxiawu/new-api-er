package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chunkFrom(t *testing.T, raw string) *dto.ChatCompletionsStreamResponse {
	t.Helper()
	var chunk dto.ChatCompletionsStreamResponse
	require.NoError(t, common.UnmarshalJsonStr(raw, &chunk))
	return &chunk
}

func feed(t *testing.T, a *chatStreamAggregator, raws ...string) {
	t.Helper()
	for _, raw := range raws {
		a.AddChunk(chunkFrom(t, raw))
	}
}

func TestChatStreamAggregator_TextConcatenation(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a,
		`{"id":"c1","created":100,"model":"gpt-4o-2024","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":"lo, "}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":"stop"}]}`,
	)

	out := a.Snapshot("fallback", false)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "c1", out.Id)
	assert.Equal(t, "gpt-4o-2024", out.Model, "model from the chunk wins over the request model")
	assert.Equal(t, "chat.completion", out.Object)
	assert.Equal(t, int64(100), out.Created)
	assert.Equal(t, "assistant", out.Choices[0].Message.Role)
	assert.Equal(t, "Hello, world", out.Choices[0].Message.StringContent())
	assert.Equal(t, "stop", out.Choices[0].FinishReason)
}

func TestChatStreamAggregator_FallbackIdWhenUpstreamOmitsIt(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"content":"x"}}]}`)
	assert.Equal(t, "fallback", a.Snapshot("fallback", false).Id)
}

// A response cut off by the deadline never carries a finish_reason. Clients
// need an explicit incomplete marker rather than an empty one.
func TestChatStreamAggregator_TruncatedMarksLength(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"id":"c","choices":[{"index":0,"delta":{"content":"partial"}}]}`)

	out := a.Snapshot("fallback", true)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "length", out.Choices[0].FinishReason)
	assert.Equal(t, "partial", out.Choices[0].Message.StringContent())
}

// An upstream-reported finish_reason must survive even on a truncated stream.
func TestChatStreamAggregator_TruncatedKeepsUpstreamFinishReason(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"id":"c","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`)
	assert.Equal(t, "stop", a.Snapshot("f", true).Choices[0].FinishReason)
}

func TestChatStreamAggregator_ToolCallFragmentsMerge(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a,
		`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"ci"}}]}}]}`,
		`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"SF\"}"}}]}}]}`,
		`{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	out := a.Snapshot("f", false)
	require.Len(t, out.Choices, 1)
	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, "call_1", calls[0].ID)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, `{"city":"SF"}`, calls[0].Function.Arguments)
	assert.Equal(t, 1, a.ToolCallCount())
}

func TestChatStreamAggregator_MultipleToolCallsKeepOrder(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}}]}}]}`,
	)

	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(a.Snapshot("f", false).Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 2)
	assert.Equal(t, "first", calls[0].Function.Name)
	assert.Equal(t, "second", calls[1].Function.Name)
	assert.Equal(t, 2, a.ToolCallCount())
}

// Some providers omit the index on a single tool call; fragments must still
// join rather than each starting a new call.
func TestChatStreamAggregator_ToolCallWithoutIndex(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"a","function":{"name":"f","arguments":"{\"a\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"1}"}}]}}]}`,
	)

	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(a.Snapshot("f", false).Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, `{"a":1}`, calls[0].Function.Arguments)
}

func TestChatStreamAggregator_ReasoningFieldsBothAccumulate(t *testing.T) {
	a := newChatStreamAggregator("o1")
	feed(t, a,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"think "}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning":"more"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"answer"}}]}`,
	)

	out := a.Snapshot("f", false)
	require.NotNil(t, out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "think more", *out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "answer", out.Choices[0].Message.StringContent())
	assert.Contains(t, a.AssembledText(), "think more")
	assert.Contains(t, a.AssembledText(), "answer")
}

// include_usage puts usage in the final chunk; a later report must win so a
// mid-stream partial count never sticks.
func TestChatStreamAggregator_UsageLastReportWins(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a,
		`{"choices":[{"index":0,"delta":{"content":"x"}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
		`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":9,"total_tokens":14}}`,
	)

	require.NotNil(t, a.Usage())
	assert.Equal(t, 9, a.Usage().CompletionTokens)
	assert.Equal(t, 14, a.Snapshot("f", false).Usage.TotalTokens)
}

func TestChatStreamAggregator_NoUsageReported(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[{"index":0,"delta":{"content":"x"}}]}`)
	assert.Nil(t, a.Usage())
}

func TestChatStreamAggregator_EmptyStream(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	assert.False(t, a.ReceivedAnything())
	assert.Equal(t, "", a.AssembledText())
	assert.Equal(t, 0, a.ToolCallCount())

	out := a.Snapshot("fallback", true)
	require.NotNil(t, out)
	assert.Equal(t, "fallback", out.Id)
	assert.Empty(t, out.Choices)
}

// A usage-only chunk carries no choices; it still counts as received.
func TestChatStreamAggregator_UsageOnlyChunkCountsAsReceived(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	feed(t, a, `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}}`)
	assert.True(t, a.ReceivedAnything())
	assert.Empty(t, a.Snapshot("f", false).Choices)
}

func TestChatStreamAggregator_NilSafe(t *testing.T) {
	var a *chatStreamAggregator
	assert.NotPanics(t, func() { a.AddChunk(nil) })
	assert.Nil(t, a.Snapshot("f", false))
	assert.Nil(t, a.Usage())
	assert.Equal(t, "", a.AssembledText())
	assert.Equal(t, 0, a.ToolCallCount())
	assert.False(t, a.ReceivedAnything())

	live := newChatStreamAggregator("m")
	assert.NotPanics(t, func() { live.AddChunk(nil) })
	assert.False(t, live.ReceivedAnything())
}

func TestAdaptedStreamPayload(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{`data: {"a":1}`, `{"a":1}`, true},
		{`data:{"a":1}`, `{"a":1}`, true},
		{`  data: {"a":1}  `, `{"a":1}`, true},
		{`data: [DONE]`, `[DONE]`, true},
		{`data:`, "", false},
		{`data: `, "", false},
		{`: ping`, "", false},
		{``, "", false},
		{`event: message`, "", false},
		{`{"a":1}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got, ok := adaptedStreamPayload(tc.line)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A stream that never ends must not grow the buffer without bound; text stops
// accumulating at the budget while usage and metadata keep being folded in.
func TestChatStreamAggregator_BudgetStopsTextAccumulation(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	a.budget = aggregatedEntryBytes + 10 // one choice entry, then 10 bytes of text

	feed(t, a,
		`{"id":"c","choices":[{"index":0,"delta":{"content":"12345"}}]}`,
		`{"id":"c","choices":[{"index":0,"delta":{"content":"67890"}}]}`,
	)
	assert.False(t, a.OverBudget())
	assert.Equal(t, "1234567890", a.Snapshot("f", false).Choices[0].Message.StringContent())

	feed(t, a, `{"id":"c","choices":[{"index":0,"delta":{"content":"overflow"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	assert.True(t, a.OverBudget())

	out := a.Snapshot("f", false)
	assert.Equal(t, "1234567890", out.Choices[0].Message.StringContent(),
		"content past the budget must be dropped, not appended")
	assert.Equal(t, 3, out.Usage.TotalTokens,
		"usage must still be captured so the request settles on real numbers")
}

func TestChatStreamAggregator_BudgetAppliesToToolArguments(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	// choice and tool call entries, id "a", name "f", then 2 bytes of arguments
	a.budget = 2*aggregatedEntryBytes + 2 + 2

	feed(t, a,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"0123456789"}}]}}]}`,
	)
	assert.True(t, a.OverBudget())

	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(a.Snapshot("f", false).Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, "{}", calls[0].Function.Arguments)
}

func TestChatStreamAggregator_BudgetDefaultsToStreamFrameLimit(t *testing.T) {
	a := newChatStreamAggregator("gpt-4o")
	assert.Equal(t, relaycommon.MaxStreamFrameBytes, a.budget)
	assert.False(t, a.OverBudget())

	var nilAgg *chatStreamAggregator
	assert.False(t, nilAgg.OverBudget())
}
