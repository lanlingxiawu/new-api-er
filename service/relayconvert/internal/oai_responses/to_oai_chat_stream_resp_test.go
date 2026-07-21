package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestResponsesStreamState() *ResponsesToChatStreamState {
	state := NewResponsesToChatStreamState("gpt-test", false)
	state.ID = "chatcmpl_test"
	state.Created = 123
	return state
}

func mustStreamChunks(t *testing.T, state *ResponsesToChatStreamState, event *dto.ResponsesStreamResponse) []dto.ChatCompletionsStreamResponse {
	t.Helper()
	chunks, err := ResponsesStreamEventToChatChunks(event, state)
	require.NoError(t, err)
	return chunks
}

func TestResponsesStreamEventNilGuards(t *testing.T) {
	chunks, err := ResponsesStreamEventToChatChunks(nil, newTestResponsesStreamState())
	require.NoError(t, err)
	assert.Nil(t, chunks)

	chunks, err = ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{Type: responsesEventCreated}, nil)
	require.NoError(t, err)
	assert.Nil(t, chunks)

	// unknown event type -> no chunks
	chunks = mustStreamChunks(t, newTestResponsesStreamState(), &dto.ResponsesStreamResponse{Type: "response.some.unknown"})
	assert.Nil(t, chunks)
}

func TestResponsesStreamCreatedAppliesMetadata(t *testing.T) {
	// fresh state with empty ID so applyResponseMetadata adopts the response ID
	state := NewResponsesToChatStreamState("gpt-test", false)
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventCreated,
		Response: &dto.OpenAIResponsesResponse{
			ID:        "resp_meta",
			Model:     "gpt-meta",
			CreatedAt: 999,
			Usage:     &dto.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		},
	})
	require.Len(t, chunks, 1)
	assert.Equal(t, "resp_meta", state.ID)
	assert.Equal(t, "gpt-meta", state.Model)
	assert.Equal(t, int64(999), state.Created)
	assert.Equal(t, 5, state.Usage.TotalTokens)
	assert.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)

	// second created is a no-op for start
	chunks = mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventCreated})
	assert.Nil(t, chunks)
}

func TestResponsesStreamTextDelta(t *testing.T) {
	state := newTestResponsesStreamState()
	// empty delta -> no chunk
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta, Delta: ""}))

	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta, Delta: "hello"})
	// start chunk + text chunk
	require.Len(t, chunks, 2)
	assert.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	assert.Equal(t, "hello", chunks[1].Choices[0].Delta.GetContentString())
	assert.Equal(t, "hello", state.UsageText())
}

func TestResponsesStreamReasoningSummaryBreak(t *testing.T) {
	state := newTestResponsesStreamState()
	// empty reasoning delta -> nil
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: ""}))

	mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningSummaryDelta, Delta: "part1"})
	// done sets the break flag because reasoning was already sent
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningSummaryDone}))
	assert.True(t, state.needsReasoningSummaryBreak)

	// next delta without newline prefix gets "\n\n" prepended
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "part2"})
	require.Len(t, chunks, 1)
	assert.Equal(t, "\n\npart2", chunks[0].Choices[0].Delta.GetReasoningContent())
	assert.False(t, state.needsReasoningSummaryBreak)
}

func TestResponsesStreamReasoningSummaryBreakSingleNewlinePrefix(t *testing.T) {
	state := newTestResponsesStreamState()
	mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "a"})
	state.needsReasoningSummaryBreak = true
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "\nmore"})
	require.Len(t, chunks, 1)
	assert.Equal(t, "\n\nmore", chunks[0].Choices[0].Delta.GetReasoningContent())
}

func TestResponsesStreamReasoningSummaryBreakDoubleNewlinePrefix(t *testing.T) {
	state := newTestResponsesStreamState()
	mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "a"})
	state.needsReasoningSummaryBreak = true
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "\n\nkept"})
	require.Len(t, chunks, 1)
	assert.Equal(t, "\n\nkept", chunks[0].Choices[0].Delta.GetReasoningContent())
}

func TestResponsesStreamReasoningDoneWithoutPriorReasoning(t *testing.T) {
	state := newTestResponsesStreamState()
	// done with no prior reasoning does not set break flag
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDone}))
	assert.False(t, state.needsReasoningSummaryBreak)
}

func TestResponsesStreamOutputItemNonTool(t *testing.T) {
	state := newTestResponsesStreamState()
	// output_item.added for a non-tool item is ignored
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeMessage},
	}))
	// output_item with nil Item ignored
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventOutputItemAdded}))
}

func TestResponsesStreamUsesOutputIndexForToolArguments(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 1

	var chunks []dto.ChatCompletionsStreamResponse
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventCreated})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta, Delta: "text before tool"})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, Delta: `{"cmd":"ls"}`,
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "exec"},
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type:     responsesEventCompleted,
		Response: &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`), Usage: &dto.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
	})...)

	require.Len(t, chunks, 4)
	assert.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	assert.Equal(t, "text before tool", chunks[1].Choices[0].Delta.GetContentString())
	tool := chunks[2].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tool.Index)
	assert.Equal(t, 0, *tool.Index)
	assert.Equal(t, "call_1", tool.ID)
	assert.Equal(t, "exec", tool.Function.Name)
	assert.Equal(t, `{"cmd":"ls"}`, tool.Function.Arguments)
	require.NotNil(t, chunks[3].Choices[0].FinishReason)
	assert.Equal(t, "tool_calls", *chunks[3].Choices[0].FinishReason)
	assert.Equal(t, 3, state.Usage.TotalTokens)
}

func TestResponsesStreamNoDuplicatePendingArgsWithOutputIndexAndItemID(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 1

	var chunks []dto.ChatCompletionsStreamResponse
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventCreated})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, ItemID: "fc_1", Delta: `{"q":"x"}`,
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex, ItemID: "fc_1",
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup"},
	})...)

	require.Len(t, chunks, 2)
	tool := chunks[1].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_1", tool.ID)
	assert.Equal(t, "lookup", tool.Function.Name)
	assert.Equal(t, `{"q":"x"}`, tool.Function.Arguments)
	assert.Empty(t, state.pendingArgsByOutputIndex)
	assert.Empty(t, state.pendingArgsByItemID)
}

func TestResponsesStreamDrainsItemOnlyPendingArgsWhenOutputIndexArrives(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 1

	var chunks []dto.ChatCompletionsStreamResponse
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventCreated})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, ItemID: "fc_1", Delta: `{"q":"x"}`,
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex, ItemID: "fc_1",
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, CallId: "call_1", Name: "lookup"},
	})...)

	require.Len(t, chunks, 2)
	tool := chunks[1].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_1", tool.ID)
	assert.Equal(t, "lookup", tool.Function.Name)
	assert.Equal(t, `{"q":"x"}`, tool.Function.Arguments)
	assert.Empty(t, state.pendingArgsByOutputIndex)
	assert.Empty(t, state.pendingArgsByItemID)
}

func TestResponsesStreamCustomToolAndReasoning(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 0

	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "thinking"})
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeCustomToolCall, ID: "ct_1", CallId: "call_custom", Name: "apply_patch"},
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventCustomToolInputDelta, OutputIndex: &outputIndex, Delta: "patch body",
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type:     responsesEventIncomplete,
		Response: &dto.OpenAIResponsesResponse{IncompleteDetails: &dto.IncompleteDetails{Reason: responsesIncompleteReasonContentFilter}},
	})...)

	require.Len(t, chunks, 5)
	assert.Equal(t, "thinking", chunks[1].Choices[0].Delta.GetReasoningContent())
	assert.Equal(t, "apply_patch", chunks[2].Choices[0].Delta.ToolCalls[0].Function.Name)
	assert.Equal(t, "patch body", chunks[3].Choices[0].Delta.ToolCalls[0].Function.Arguments)
	require.NotNil(t, chunks[4].Choices[0].FinishReason)
	assert.Equal(t, "content_filter", *chunks[4].Choices[0].FinishReason)
}

func TestResponsesStreamUsesTerminalDoneOutput(t *testing.T) {
	state := newTestResponsesStreamState()
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventDone,
		Response: &dto.OpenAIResponsesResponse{
			Status: []byte(`"completed"`),
			Output: []dto.ResponsesOutput{
				{Type: responsesOutputTypeReasoning, Content: []dto.ResponsesOutputContent{{Text: "why"}}},
				{Type: responsesOutputTypeMessage, Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "terminal text"}}},
				{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup", Arguments: []byte(`{"q":"x"}`)},
			},
		},
	})

	// start + reasoning + text + tool + finish
	require.Len(t, chunks, 5)
	assert.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	assert.Equal(t, "why", chunks[1].Choices[0].Delta.GetReasoningContent())
	assert.Equal(t, "terminal text", chunks[2].Choices[0].Delta.GetContentString())
	tool := chunks[3].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "lookup", tool.Function.Name)
	assert.Equal(t, `{"q":"x"}`, tool.Function.Arguments)
	require.NotNil(t, chunks[4].Choices[0].FinishReason)
	assert.Equal(t, "tool_calls", *chunks[4].Choices[0].FinishReason)
}

func TestResponsesStreamDoesNotResendToolOnTerminalOutput(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 0

	var chunks []dto.ChatCompletionsStreamResponse
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventCreated})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup"},
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, Delta: `{"q":"x"}`,
	})...)
	chunks = append(chunks, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventCompleted,
		Response: &dto.OpenAIResponsesResponse{
			Status: []byte(`"completed"`),
			Output: []dto.ResponsesOutput{{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup", Arguments: []byte(`{"q":"x"}`)}},
		},
	})...)

	totalArgs := ""
	toolIndexes := map[int]bool{}
	var finishReason string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			for _, tc := range choice.Delta.ToolCalls {
				require.NotNil(t, tc.Index)
				toolIndexes[*tc.Index] = true
				totalArgs += tc.Function.Arguments
			}
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
	}
	assert.Equal(t, map[int]bool{0: true}, toolIndexes)
	assert.Equal(t, `{"q":"x"}`, totalArgs)
	assert.Equal(t, "tool_calls", finishReason)
}

func TestResponsesStreamArgumentsDeltaEmpty(t *testing.T) {
	state := newTestResponsesStreamState()
	// empty delta on function args -> nil
	assert.Nil(t, mustStreamChunks(t, state, &dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDelta, Delta: ""}))
}

func TestResponsesStreamFunctionArgsDoneFlushesFallbackTool(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 3
	// args.done with no known tool builds a fallback tool
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDone, OutputIndex: &outputIndex,
	})
	// start + tool delta (empty args, no name) -> tool with call id
	require.GreaterOrEqual(t, len(chunks), 1)
}

func TestFinalizeFlushesPendingDeltaOnlyArguments(t *testing.T) {
	state := newTestResponsesStreamState()
	outputIndex := 2
	_, err := ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, Delta: `{"pending":true}`,
	}, state)
	require.NoError(t, err)

	chunks := FinalizeResponsesToChatStream(state)
	require.Len(t, chunks, 3)
	tool := chunks[1].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_output_2", tool.ID)
	assert.Equal(t, `{"pending":true}`, tool.Function.Arguments)
	require.NotNil(t, chunks[2].Choices[0].FinishReason)
	assert.Equal(t, "tool_calls", *chunks[2].Choices[0].FinishReason)
}

func TestFinalizeFlushesItemOnlyPendingArguments(t *testing.T) {
	state := newTestResponsesStreamState()
	_, err := ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, ItemID: "fc_x", Delta: `{"a":1}`,
	}, state)
	require.NoError(t, err)

	chunks := FinalizeResponsesToChatStream(state)
	// start + tool + finish
	require.Len(t, chunks, 3)
	tool := chunks[1].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "fc_x", tool.ID)
	assert.Equal(t, `{"a":1}`, tool.Function.Arguments)
}

func TestFinalizeNilStateAndDoubleFinalize(t *testing.T) {
	assert.Nil(t, FinalizeResponsesToChatStream(nil))

	state := newTestResponsesStreamState()
	first := FinalizeResponsesToChatStream(state)
	require.NotEmpty(t, first)
	// second finalize is a no-op
	assert.Nil(t, FinalizeResponsesToChatStream(state))
}

func TestFinalizeIncludesUsageChunk(t *testing.T) {
	state := NewResponsesToChatStreamState("gpt-test", true)
	state.Usage = &dto.Usage{TotalTokens: 42}
	chunks := FinalizeResponsesToChatStream(state)
	// start + finish + usage
	require.Len(t, chunks, 3)
	last := chunks[len(chunks)-1]
	require.NotNil(t, last.Usage)
	assert.Equal(t, 42, last.Usage.TotalTokens)
	assert.Equal(t, "chat.completion.chunk", last.Object)
}

func TestResponsesStreamFailedAndErrorEvents(t *testing.T) {
	_, err := ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{Type: responsesEventFailed}, newTestResponsesStreamState())
	require.Error(t, err)
	_, err = ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{Type: responsesEventError}, newTestResponsesStreamState())
	require.Error(t, err)
}

func TestUsageTextNilReceiver(t *testing.T) {
	var s *ResponsesToChatStreamState
	assert.Equal(t, "", s.UsageText())
}

func TestNewStreamStateInitialization(t *testing.T) {
	s := NewResponsesToChatStreamState("m", true)
	assert.Equal(t, "m", s.Model)
	assert.True(t, s.IncludeUsage)
	require.NotNil(t, s.Usage)
	assert.NotZero(t, s.Created)
}

// findToolForEvent by item ID (no output index) and via keyForEvent Item path.
func TestResponsesStreamToolByItemIDAndKeyForEvent(t *testing.T) {
	state := newTestResponsesStreamState()
	// added by item id only (no output index)
	mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "item_1", CallId: "call_1", Name: "f"},
	})
	// arguments delta referenced by item id resolves same tool
	chunks := mustStreamChunks(t, state, &dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, ItemID: "item_1", Delta: `{"x":1}`,
	})
	require.NotEmpty(t, chunks)
	last := chunks[len(chunks)-1]
	tool := last.Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_1", tool.ID)
	assert.Equal(t, `{"x":1}`, tool.Function.Arguments)
}

// ---------------------------------------------------------------------------
// ResponsesBufferedAccumulator
// ---------------------------------------------------------------------------

func TestBufferedAccumulatorNilGuards(t *testing.T) {
	var a *ResponsesBufferedAccumulator
	a.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta})
	a.SupplementResponseOutput(&dto.OpenAIResponsesResponse{})
	assert.Nil(t, a.BuildOutput())

	acc := NewResponsesBufferedAccumulator()
	acc.ProcessEvent(nil)
}

func TestBufferedAccumulatorSupplementsEmptyTerminalOutput(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	outputIndex := 1
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventReasoningTextDelta, Delta: "reasoning "})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta, Delta: "buffered text"})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup"},
	})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, Delta: `{"q":"x"}`})

	resp := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`), Model: "gpt-test"}
	acc.SupplementResponseOutput(resp)
	require.Len(t, resp.Output, 3) // reasoning + message + tool

	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl_1")
	require.NoError(t, err)
	assert.Equal(t, "buffered text", chat.Choices[0].Message.StringContent())
	assert.Equal(t, "reasoning ", chat.Choices[0].Message.GetReasoningContent())
	toolCalls := chat.Choices[0].Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
}

func TestBufferedAccumulatorDoesNotSupplementWhenOutputPresent(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventOutputTextDelta, Delta: "buffered"})
	resp := &dto.OpenAIResponsesResponse{
		Status: []byte(`"completed"`),
		Output: []dto.ResponsesOutput{{Type: responsesOutputTypeMessage, Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "existing"}}}},
	}
	acc.SupplementResponseOutput(resp)
	// unchanged
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "existing", resp.Output[0].Content[0].Text)
}

func TestBufferedAccumulatorNoDuplicatePendingArgsWithOutputIndexAndItemID(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	outputIndex := 1
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventFunctionArgsDelta, OutputIndex: &outputIndex, ItemID: "fc_1", Delta: `{"q":"x"}`,
	})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded, OutputIndex: &outputIndex, ItemID: "fc_1",
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup"},
	})

	resp := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`), Model: "gpt-test"}
	acc.SupplementResponseOutput(resp)
	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl_1")
	require.NoError(t, err)
	toolCalls := chat.Choices[0].Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
	assert.Empty(t, acc.pendingByOutputIndex)
	assert.Empty(t, acc.pendingByItemID)
}

func TestBufferedAccumulatorItemAddedThenArgsWithItemID(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	// tool added with item id (no output index), then args by item id -> existing index path
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "item_1", CallId: "call_1", Name: "f", Arguments: []byte(`{"pre":1}`)},
	})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDelta, ItemID: "item_1", Delta: `-extra`})

	out := acc.BuildOutput()
	require.Len(t, out, 1)
	// args reset on item-added then delta appended after
	assert.Equal(t, "call_1", out[0].CallId)
	assert.Equal(t, "item_1", out[0].ID)
	assert.Equal(t, "f", out[0].Name)
}

func TestBufferedAccumulatorItemOnlyPendingDrained(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	// args arrive by item id before item added
	acc.ProcessEvent(&dto.ResponsesStreamResponse{Type: responsesEventFunctionArgsDelta, ItemID: "item_9", Delta: `{"a":1}`})
	acc.ProcessEvent(&dto.ResponsesStreamResponse{
		Type: responsesEventOutputItemAdded,
		Item: &dto.ResponsesOutput{Type: responsesOutputTypeFunctionCall, ID: "item_9", CallId: "call_9", Name: "g"},
	})
	out := acc.BuildOutput()
	require.Len(t, out, 1)
	assert.Equal(t, "g", out[0].Name)
	assert.Empty(t, acc.pendingByItemID)
}
