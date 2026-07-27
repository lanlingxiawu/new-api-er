package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFallbackToolKey(t *testing.T) {
	idx := 5
	assert.Equal(t, "output:5", fallbackToolKey("item", "call", &idx))
	assert.Equal(t, "item:it", fallbackToolKey("it", "call", nil))
	assert.Equal(t, "call:cl", fallbackToolKey("", "cl", nil))
	assert.Equal(t, "", fallbackToolKey("", "", nil))
}

func TestFallbackCallID(t *testing.T) {
	assert.Equal(t, "", fallbackCallID(nil))
	assert.Equal(t, "iid", fallbackCallID(&dto.ResponsesStreamResponse{ItemID: "iid"}))
	idx := 4
	assert.Equal(t, "call_output_4", fallbackCallID(&dto.ResponsesStreamResponse{OutputIndex: &idx}))
	assert.Equal(t, "", fallbackCallID(&dto.ResponsesStreamResponse{}))
}

func TestResponseStreamEventItemID(t *testing.T) {
	assert.Equal(t, "", responseStreamEventItemID(nil))
	assert.Equal(t, "from_item", responseStreamEventItemID(&dto.ResponsesStreamResponse{Item: &dto.ResponsesOutput{ID: "from_item"}}))
	assert.Equal(t, "from_field", responseStreamEventItemID(&dto.ResponsesStreamResponse{ItemID: "from_field"}))
	// item present but empty ID -> falls back to ItemID
	assert.Equal(t, "field", responseStreamEventItemID(&dto.ResponsesStreamResponse{Item: &dto.ResponsesOutput{}, ItemID: "field"}))
}

func TestKeyForEvent(t *testing.T) {
	state := newTestResponsesStreamState()
	assert.Equal(t, "", state.keyForEvent(nil))

	idx := 2
	assert.Equal(t, "output:2", state.keyForEvent(&dto.ResponsesStreamResponse{OutputIndex: &idx}))
	assert.Equal(t, "item:abc", state.keyForEvent(&dto.ResponsesStreamResponse{Item: &dto.ResponsesOutput{ID: "abc"}}))
	assert.Equal(t, "call:xyz", state.keyForEvent(&dto.ResponsesStreamResponse{Item: &dto.ResponsesOutput{CallId: "xyz"}}))
	assert.Equal(t, "item:field", state.keyForEvent(&dto.ResponsesStreamResponse{ItemID: "field"}))
	assert.Equal(t, "", state.keyForEvent(&dto.ResponsesStreamResponse{Item: &dto.ResponsesOutput{}}))
}

func TestFlushPendingToolBuildsFallbackByItemID(t *testing.T) {
	state := newTestResponsesStreamState()
	// args delta by item id only -> stored as pending, no tool yet
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, ItemID: "z1", Delta: `{"a":1}`,
	}))
	// args done by same item id -> fallback tool built and pending drained
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDone, ItemID: "z1",
	})
	require.NotEmpty(t, chunks)
	last := chunks[len(chunks)-1]
	tool := last.Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "z1", tool.ID) // fallbackCallID uses item id
	assert.Equal(t, `{"a":1}`, tool.Function.Arguments)
	assert.Empty(t, state.pendingArgsByItemID)
}

func TestFlushPendingToolNoIdentifiersReturnsNil(t *testing.T) {
	state := newTestResponsesStreamState()
	// args done with no output index / item id / item -> no fallback tool
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDone}))
}

func TestToolItemWithoutArguments(t *testing.T) {
	state := newTestResponsesStreamState()
	idx := 0
	// item added carrying no arguments exercises the args=="" branch in toolItem
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &idx,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc", CallId: "call", Name: "n"},
	})
	require.NotEmpty(t, chunks)
	last := chunks[len(chunks)-1]
	assert.Equal(t, "n", last.Choices[0].Delta.ToolCalls[0].Function.Name)
	assert.Empty(t, last.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}

func TestToolItemWithNoIdentifiersReturnsNil(t *testing.T) {
	state := newTestResponsesStreamState()
	// tool item with no ID / CallId / OutputIndex cannot be keyed -> no chunks
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, Name: "anon"},
	}))
}

func TestFindToolForEventViaItemKey(t *testing.T) {
	state := newTestResponsesStreamState()
	// register a tool keyed by item id (no output index)
	mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "itemK", CallId: "callK", Name: "f"},
	})
	// args done referencing the same Item (no output index, no ItemID field) resolves via keyForEvent(Item)
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDone,
		Item: &dto.ResponsesOutput{ID: "itemK"},
	})
	// tool already sent with no new args -> may be empty, but must not error or panic
	_ = chunks
}

func TestBufferedApplyToolMetadataCallIDFallsBackToItemID(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	// Item with ID but no CallId -> CallID falls back to ID
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "only_id", Name: "f"},
	})
	out := acc.BuildOutput()
	require.Len(t, out, 1)
	assert.Equal(t, "only_id", out[0].CallId)
	assert.Equal(t, "only_id", out[0].ID)
}

func TestStreamToolCallIDFallsBackToItemIDWhenCallIDEmpty(t *testing.T) {
	state := newTestResponsesStreamState()
	idx := 0
	// item added with ID but no CallId: tool.CallID falls back to Item.ID
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &idx,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "the_id", Name: "f"},
	})
	require.NotEmpty(t, chunks)
	tool := chunks[len(chunks)-1].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "the_id", tool.ID)
}
