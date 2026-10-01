package relay

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolCallChunk(index int, id, name, args string) *dto.ChatCompletionsStreamResponse {
	i := index
	return &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
			Index: &i, ID: id, Function: dto.FunctionResponse{Name: name, Arguments: args},
		}}},
	}}}
}

// Every retained byte counts against the budget, not only arguments: an
// upstream streaming endless distinct tool calls with empty arguments would
// otherwise grow the aggregator without bound and never trip the cut-off.
func TestChatStreamAggregator_ToolCallMetadataCountsAgainstBudget(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.budget = 4 << 10
	name := strings.Repeat("n", 64)
	for i := 0; i < 10000 && !a.OverBudget(); i++ {
		a.AddChunk(toolCallChunk(i, "call_"+strings.Repeat("x", 32), name, ""))
	}
	require.True(t, a.OverBudget(), "metadata alone must exhaust the budget")
	count := a.ToolCallCount()
	assert.Less(t, count, 100, "entries stop being created once the budget is spent")

	a.AddChunk(toolCallChunk(count+1, "call_more", name, ""))
	assert.Equal(t, count, a.ToolCallCount(), "no new entries past the budget")
}

// Distinct choice indexes are entries too.
func TestChatStreamAggregator_ChoiceEntriesCountAgainstBudget(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.budget = 4 << 10
	for i := 0; i < 10000 && !a.OverBudget(); i++ {
		a.AddChunk(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: i}}})
	}
	require.True(t, a.OverBudget())
	assert.Less(t, len(a.choices), 100)
	a.AddFrameExtras(`{"choices":[{"index":99999,"delta":{"refusal":"no"}}]}`)
	assert.NotContains(t, a.choices, 99999, "raw-frame extras cannot add entries past the budget either")
}

// A tool call already seen keeps receiving its argument fragments; only the
// entry, id and name are charged once.
func TestChatStreamAggregator_ToolCallFragmentsStillAccumulate(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.AddChunk(toolCallChunk(0, "call_1", "get_weather", `{"city":`))
	a.AddChunk(toolCallChunk(0, "", "", `"Paris"}`))
	resp := a.Snapshot("id", false)
	var calls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(resp.Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, "call_1", calls[0].ID)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
}

// Content dropped by the budget makes the answer incomplete, whatever finish
// reason upstream sent with it: the client must see "length", not "stop".
func TestChatStreamAggregator_BudgetCutOverridesFinishReason(t *testing.T) {
	a := newChatStreamAggregator("m")
	a.budget = aggregatedEntryBytes + 8
	text := strings.Repeat("x", 64)
	stop := "stop"
	a.AddChunk(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &stop,
	}}})
	require.True(t, a.OverBudget())
	resp := a.Snapshot("id", a.OverBudget())
	assert.Equal(t, "length", resp.Choices[0].FinishReason)
	assert.True(t, a.UpstreamFinishedWith("stop"), "upstream's own reason stays readable for logging")
}

// Within budget, upstream's finish reason is kept; an incomplete stream with
// no finish reason is reported as "length".
func TestChatStreamAggregator_FinishReasonWithinBudget(t *testing.T) {
	stop := "stop"
	text := "hi"
	a := newChatStreamAggregator("m")
	a.AddChunk(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &stop,
	}}})
	assert.Equal(t, "stop", a.Snapshot("id", true).Choices[0].FinishReason,
		"a missing [DONE] after a finish reason is still a complete answer")

	b := newChatStreamAggregator("m")
	b.AddChunk(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text},
	}}})
	assert.Equal(t, "length", b.Snapshot("id", true).Choices[0].FinishReason)
	assert.Equal(t, "", b.Snapshot("id", false).Choices[0].FinishReason)
}

// An upstream that ignores stream:true answers with a plain body. The hold put
// on the response timer for the adapted call must end there, as the plain
// call's first byte would have stopped the timer; left held, the timer runs on
// through body reading and settlement and can 504 a completed request.
func TestAbandonUpstreamStreamAdaptation_ReleasesTimer(t *testing.T) {
	c, _, info := bufferedCase(t)
	recorder := &releaseRecorder{}
	c.Set(string(constant.ContextKeyRelayTimeoutControl), recorder)
	abandonUpstreamStreamAdaptation(c, info)
	assert.False(t, info.UpstreamStreamAdapted)
	assert.Equal(t, 1, recorder.released)
}

// A request that was never adapted holds nothing; the plain path's own first
// byte handling applies and nothing is released here.
func TestAbandonUpstreamStreamAdaptation_NotAdapted(t *testing.T) {
	c, _, info := bufferedCase(t)
	info.UpstreamStreamAdapted = false
	recorder := &releaseRecorder{}
	c.Set(string(constant.ContextKeyRelayTimeoutControl), recorder)
	abandonUpstreamStreamAdaptation(c, info)
	assert.Equal(t, 0, recorder.released)
}
