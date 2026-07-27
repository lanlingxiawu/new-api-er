package oaichat

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func feed(t *testing.T, state *ChatToResponsesStreamState, chunk *dto.ChatCompletionsStreamResponse) []ChatToResponsesStreamEvent {
	t.Helper()
	ev, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	require.NoError(t, err)
	return ev
}

func eventTypes(events []ChatToResponsesStreamEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func TestStreamState_NilGuards(t *testing.T) {
	ev, err := ChatCompletionsStreamChunkToResponsesEvents(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, ev)

	// nil state on non-nil chunk still returns nil (both-nil guard is combined).
	ev2, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{}, nil)
	require.NoError(t, err)
	assert.Nil(t, ev2)

	assert.Nil(t, FinalizeChatCompletionsStreamToResponses(nil))

	var s *ChatToResponsesStreamState
	assert.Equal(t, "", s.UsageText())
}

func TestStreamState_SeedsIDModelCreatedFromChunk(t *testing.T) {
	state := NewChatToResponsesStreamState("", "")
	state.Created = 0 // force the seed-from-chunk branch (constructor sets now())
	ev := feed(t, state, &dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_9", Model: "gpt-x", Created: 111,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hi")}},
		},
	})
	assert.Equal(t, "chatcmpl_9", state.ID)
	assert.Equal(t, "gpt-x", state.Model)
	assert.Equal(t, int64(111), state.Created)
	// First event is response.created.
	assert.Equal(t, responsesEventCreated, ev[0].Type)
}

func TestStreamState_FullTextToolFlowAggregates(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	state.Created = 123
	toolIndex := 0

	var events []ChatToResponsesStreamEvent
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Id: "c", Model: "gpt-test", Created: 123,
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}}},
	})...)
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hello")}}},
	})...)
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
			{Index: &toolIndex, ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup"}},
		}}}},
	})...)
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
			{Index: &toolIndex, Function: dto.FunctionResponse{Arguments: `{"q":"x"}`}},
		}}}},
	})...)
	finish := "tool_calls"
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, FinishReason: &finish}},
	})...)
	events = append(events, feed(t, state, &dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6},
	})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	require.Len(t, events, 10)
	assert.Equal(t, responsesEventCreated, events[0].Type)
	assert.Equal(t, responsesEventOutputTextDelta, events[2].Type)
	assert.Equal(t, "hello", events[2].Payload.Delta)
	assert.Equal(t, responsesEventFunctionArgsDelta, events[4].Type)
	assert.Equal(t, `{"q":"x"}`, events[4].Payload.Delta)
	assert.Equal(t, responsesEventCompleted, events[9].Type)
	require.NotNil(t, events[9].Payload.Response)
	assert.Equal(t, 6, events[9].Payload.Response.Usage.TotalTokens)
	require.Len(t, events[9].Payload.Response.Output, 2)
	assert.Equal(t, "hello", events[9].Payload.Response.Output[0].Content[0].Text)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(events[9].Payload.Response.Output[1].Arguments))

	assert.Equal(t, "hello", state.UsageText())
}

func TestStreamState_ReasoningFlow(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_r", "gpt-test")
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: lo.ToPtr("thinking hard")}},
		},
	})
	final := FinalizeChatCompletionsStreamToResponses(state)
	types := eventTypes(final)
	assert.Contains(t, types, responsesEventReasoningSummaryDone)
	assert.Contains(t, types, responsesEventCompleted)
	// The reasoning output carries the aggregated summary text.
	last := final[len(final)-1]
	require.NotNil(t, last.Payload.Response)
	require.Len(t, last.Payload.Response.Output, 1)
	assert.Equal(t, "thinking hard", last.Payload.Response.Output[0].Content[0].Text)
}

func TestStreamState_ReasoningAddedEvent(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_r", "gpt-test")
	ev := feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: lo.ToPtr("r")}},
		},
	})
	// created, output_item.added(reasoning), reasoning_summary_text.delta
	require.Len(t, ev, 3)
	assert.Equal(t, responsesEventOutputItemAdded, ev[1].Type)
	assert.Equal(t, responsesOutputTypeReasoning, ev[1].Payload.Item.Type)
	assert.Equal(t, responsesEventReasoningSummaryDelta, ev[2].Type)
}

func TestStreamState_ToolCallWithoutIndexDefaultsToZero(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_t", "gpt-test")
	ev := feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Function: dto.FunctionResponse{Name: "lookup", Arguments: `{"a":1}`}},
			}}},
		},
	})
	// created, output_item.added(function_call), function_call_arguments.delta
	require.Len(t, ev, 3)
	assert.Equal(t, responsesEventOutputItemAdded, ev[1].Type)
	assert.Equal(t, responsesOutputTypeFunctionCall, ev[1].Payload.Item.Type)
	// Generated ID because none was supplied.
	assert.Equal(t, "resp_t_call_0", ev[1].Payload.Item.ID)
}

func TestStreamState_MultipleParallelTools(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_p", "gpt-test")
	i0, i1 := 0, 1
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &i0, ID: "a", Type: "function", Function: dto.FunctionResponse{Name: "fa", Arguments: `{"x":1}`}},
				{Index: &i1, ID: "b", Type: "function", Function: dto.FunctionResponse{Name: "fb", Arguments: `{"y":2}`}},
			}}},
		},
	})
	final := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, final)
	resp := final[len(final)-1].Payload.Response
	require.NotNil(t, resp)
	require.Len(t, resp.Output, 2)
	assert.Equal(t, "fa", resp.Output[0].Name)
	assert.Equal(t, "fb", resp.Output[1].Name)
}

func TestStreamState_FinalizeIdempotent(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_i", "gpt-test")
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hi")}},
		},
	})
	first := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, first)
	// Second finalize is a no-op.
	assert.Nil(t, FinalizeChatCompletionsStreamToResponses(state))
}

func TestStreamState_IncompleteFinishReasonMarksIncomplete(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_x", "gpt-test")
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("partial")}},
		},
	})
	finish := "length"
	events := feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}},
	})
	// finish triggers done-delta events (text.done + output_item.done).
	assert.Contains(t, eventTypes(events), responsesEventOutputItemDone)

	final := FinalizeChatCompletionsStreamToResponses(state)
	last := final[len(final)-1]
	// incomplete status -> response.incomplete event.
	assert.Equal(t, responsesEventIncomplete, last.Type)
	require.NotNil(t, last.Payload.Response)
	assert.Equal(t, `"incomplete"`, string(last.Payload.Response.Status))
	require.NotNil(t, last.Payload.Response.IncompleteDetails)
	assert.Equal(t, responsesIncompleteReasonMaxTokens, last.Payload.Response.IncompleteDetails.Reason)
	// output item status is "incomplete".
	require.Len(t, last.Payload.Response.Output, 1)
	assert.Equal(t, "incomplete", last.Payload.Response.Output[0].Status)
}

func TestStreamState_ToolIDAndNameUpdatedAcrossDeltas(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_u", "gpt-test")
	idx := 0
	// First delta: no id/name, only args -> generated id, empty name.
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &idx, Function: dto.FunctionResponse{Arguments: `{"a":`}},
			}}},
		},
	})
	// Second delta: supplies id + name + more args.
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &idx, ID: "real_id", Function: dto.FunctionResponse{Name: "realname", Arguments: `1}`}},
			}}},
		},
	})
	final := FinalizeChatCompletionsStreamToResponses(state)
	resp := final[len(final)-1].Payload.Response
	require.NotNil(t, resp)
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "real_id", resp.Output[0].CallId)
	assert.Equal(t, "realname", resp.Output[0].Name)
	assert.Equal(t, `"{\"a\":1}"`, string(resp.Output[0].Arguments))
}

func TestStreamState_UsageUpdatedFromChunk(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_us", "gpt-test")
	feed(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hi")}},
		},
		Usage: &dto.Usage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33},
	})
	final := FinalizeChatCompletionsStreamToResponses(state)
	resp := final[len(final)-1].Payload.Response
	require.NotNil(t, resp)
	assert.Equal(t, 33, resp.Usage.TotalTokens)
}
