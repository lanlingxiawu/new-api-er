package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Non-stream response conversion
// ---------------------------------------------------------------------------

func TestResponsesResponseToChatCompletionsNil(t *testing.T) {
	_, _, err := ResponsesResponseToChatCompletionsResponse(nil, "id")
	require.Error(t, err)
}

func TestResponsesResponseToChatCompletionsPreservesTextAndToolCalls(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_1",
		CreatedAt: 123,
		Model:     "gpt-test",
		Status:    []byte(`"completed"`),
		Output: []dto.ResponsesOutput{
			{
				Type:    responsesOutputTypeMessage,
				Role:    "assistant",
				Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "I will call a tool."}},
			},
			{Type: responsesOutputTypeFunctionCall, ID: "fc_1", CallId: "call_1", Name: "lookup", Arguments: []byte(`{"q":"x"}`)},
			// tool output with empty name is skipped
			{Type: responsesOutputTypeFunctionCall, ID: "fc_2", CallId: "call_2", Name: "", Arguments: []byte(`{}`)},
		},
		Usage: &dto.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
	}

	chat, usage, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl_1")
	require.NoError(t, err)
	require.NotNil(t, usage)

	require.Len(t, chat.Choices, 1)
	assert.Equal(t, "chat.completion", chat.Object)
	assert.Equal(t, int64(123), chat.Created)
	assert.Equal(t, "tool_calls", chat.Choices[0].FinishReason)
	assert.Equal(t, "I will call a tool.", chat.Choices[0].Message.StringContent())
	toolCalls := chat.Choices[0].Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, 7, usage.TotalTokens)
}

func TestResponsesResponseToolCallUsesIDWhenCallIDEmpty(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		Status: []byte(`"completed"`),
		Output: []dto.ResponsesOutput{
			{Type: responsesOutputTypeFunctionCall, ID: "fc_only", Name: "lookup", Arguments: []byte(`{}`)},
		},
	}
	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "id")
	require.NoError(t, err)
	toolCalls := chat.Choices[0].Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "fc_only", toolCalls[0].ID)
}

func TestResponsesResponsePlainTextStop(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		Status: []byte(`"completed"`),
		Output: []dto.ResponsesOutput{
			{Type: responsesOutputTypeMessage, Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "hi"}}},
		},
	}
	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "id")
	require.NoError(t, err)
	assert.Equal(t, "stop", chat.Choices[0].FinishReason)
	assert.Equal(t, "hi", chat.Choices[0].Message.StringContent())
}

func TestResponsesResponseIncompleteFinishReason(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		Status:            []byte(`"incomplete"`),
		IncompleteDetails: &dto.IncompleteDetails{Reason: responsesIncompleteReasonMaxTokens},
		Output:            []dto.ResponsesOutput{{Type: responsesOutputTypeMessage, Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "partial"}}}},
	}
	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "id")
	require.NoError(t, err)
	assert.Equal(t, "length", chat.Choices[0].FinishReason)
}

func TestResponsesResponsePreservesReasoningSummary(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		Status: []byte(`"completed"`),
		Output: []dto.ResponsesOutput{
			{Type: responsesOutputTypeReasoning, Content: []dto.ResponsesOutputContent{
				{Type: "summary_text", Text: "first summary"},
				{Type: "summary_text", Text: "\n\nsecond summary"},
			}},
			{Type: responsesOutputTypeMessage, Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "final"}}},
		},
	}
	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl_1")
	require.NoError(t, err)
	assert.Equal(t, "first summary\n\nsecond summary", chat.Choices[0].Message.GetReasoningContent())
	assert.Equal(t, "final", chat.Choices[0].Message.StringContent())
}

// ---------------------------------------------------------------------------
// ExtractOutputText / ExtractReasoningText
// ---------------------------------------------------------------------------

func TestExtractOutputText(t *testing.T) {
	assert.Equal(t, "", ExtractOutputTextFromResponses(nil))
	assert.Equal(t, "", ExtractOutputTextFromResponses(&dto.OpenAIResponsesResponse{}))

	// prefers assistant message outputs, skips non-assistant role
	resp := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{
		{Type: "message", Role: "user", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "USER"}}},
		{Type: "message", Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "AB"}}},
		{Type: "message", Role: "", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "C"}}},
	}}
	assert.Equal(t, "AB\n\nC", ExtractOutputTextFromResponses(resp))
}

func TestExtractOutputTextFallback(t *testing.T) {
	// no message-typed output: falls back to concatenating any content text
	resp := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{
		{Type: "other", Content: []dto.ResponsesOutputContent{{Type: "x", Text: "fallback"}}},
	}}
	assert.Equal(t, "fallback", ExtractOutputTextFromResponses(resp))
}

func TestExtractReasoningText(t *testing.T) {
	assert.Equal(t, "", ExtractReasoningTextFromResponses(nil))
	assert.Equal(t, "", ExtractReasoningTextFromResponses(&dto.OpenAIResponsesResponse{}))
	resp := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{
		{Type: responsesOutputTypeReasoning, Content: []dto.ResponsesOutputContent{{Text: "r1"}, {Text: "r2"}}},
		{Type: "message", Content: []dto.ResponsesOutputContent{{Text: "ignored"}}},
	}}
	assert.Equal(t, "r1\n\nr2", ExtractReasoningTextFromResponses(resp))
}

// ---------------------------------------------------------------------------
// ResponsesFinishReasonFromStatus
// ---------------------------------------------------------------------------

func TestResponsesFinishReasonFromStatus(t *testing.T) {
	_, ok := ResponsesFinishReasonFromStatus(nil)
	assert.False(t, ok)

	// not incomplete
	_, ok = ResponsesFinishReasonFromStatus(&dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)})
	assert.False(t, ok)

	// incomplete + nil details -> length
	got, ok := ResponsesFinishReasonFromStatus(&dto.OpenAIResponsesResponse{Status: []byte(`"incomplete"`)})
	require.True(t, ok)
	assert.Equal(t, "length", got)

	tests := []struct {
		name, reason, want string
	}{
		{"max output", responsesIncompleteReasonMaxTokens, "length"},
		{"content filter", responsesIncompleteReasonContentFilter, "content_filter"},
		{"unknown", "other", "length"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResponsesFinishReasonFromStatus(&dto.OpenAIResponsesResponse{
				Status:            []byte(`"incomplete"`),
				IncompleteDetails: &dto.IncompleteDetails{Reason: tt.reason},
			})
			require.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// UsageFromResponsesUsage
// ---------------------------------------------------------------------------

func TestUsageFromResponsesUsageNil(t *testing.T) {
	usage := UsageFromResponsesUsage(nil)
	require.NotNil(t, usage)
	assert.Equal(t, 0, usage.TotalTokens)
}

func TestUsageFromResponsesUsageFull(t *testing.T) {
	src := &dto.Usage{
		UsageSemantic:               "openai",
		UsageSource:                 "responses",
		InputTokens:                 10,
		OutputTokens:                20,
		TotalTokens:                 30,
		InputTokensDetails:          &dto.InputTokenDetails{CachedTokens: 4, TextTokens: 6, ImageTokens: 1, AudioTokens: 2, CacheWriteTokens: 3, CachedCreationTokens: 5},
		CompletionTokenDetails:      dto.OutputTokenDetails{ReasoningTokens: 7, TextTokens: 8, AudioTokens: 1, ImageTokens: 2},
		ClaudeCacheCreation5mTokens: 11,
		ClaudeCacheCreation1hTokens: 12,
	}
	usage := UsageFromResponsesUsage(src)
	assert.Equal(t, 10, usage.PromptTokens)
	assert.Equal(t, 10, usage.InputTokens)
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Equal(t, 20, usage.OutputTokens)
	assert.Equal(t, 30, usage.TotalTokens)
	assert.Equal(t, 4, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 3, usage.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 5, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 7, usage.CompletionTokenDetails.ReasoningTokens)
	assert.Equal(t, 11, usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 12, usage.ClaudeCacheCreation1hTokens)
	// billing usage should be constructed from tokens
	require.NotNil(t, usage.BillingUsage)
}

func TestUsageFromResponsesUsageTotalDefaultsToSum(t *testing.T) {
	src := &dto.Usage{InputTokens: 5, OutputTokens: 6} // TotalTokens 0
	usage := UsageFromResponsesUsage(src)
	assert.Equal(t, 11, usage.TotalTokens)
}

func TestUsageFromResponsesUsageClonesExistingBillingUsage(t *testing.T) {
	src := &dto.Usage{
		InputTokens:  1,
		OutputTokens: 1,
		BillingUsage: &dto.BillingUsage{Source: "custom-src", Semantic: "custom-sem"},
	}
	usage := UsageFromResponsesUsage(src)
	require.NotNil(t, usage.BillingUsage)
	assert.Equal(t, "custom-src", usage.BillingUsage.Source)
}

// ---------------------------------------------------------------------------
// responseStatusString / ensureIncompleteResponse / isResponsesToolOutputType
// ---------------------------------------------------------------------------

func TestResponseStatusString(t *testing.T) {
	assert.Equal(t, "", responseStatusString(nil))
	assert.Equal(t, "", responseStatusString(&dto.OpenAIResponsesResponse{}))
	assert.Equal(t, "completed", responseStatusString(&dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)}))
}

func TestEnsureIncompleteResponse(t *testing.T) {
	r := ensureIncompleteResponse(nil)
	require.NotNil(t, r)
	assert.Equal(t, `"incomplete"`, string(r.Status))

	r2 := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)}
	assert.Same(t, r2, ensureIncompleteResponse(r2))
	assert.Equal(t, `"completed"`, string(r2.Status))
}

func TestIsResponsesToolOutputType(t *testing.T) {
	assert.True(t, isResponsesToolOutputType(responsesOutputTypeFunctionCall))
	assert.True(t, isResponsesToolOutputType(responsesOutputTypeCustomToolCall))
	assert.False(t, isResponsesToolOutputType("message"))
}

func TestNormalizeResponsesUsagePreservesImageCacheDetails(t *testing.T) {
	for _, tc := range []struct {
		name        string
		imageTokens int
	}{
		{name: "image cache", imageTokens: 200},
		{name: "explicit zero image cache", imageTokens: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imageTokens, audioTokens := tc.imageTokens, 0
			source := &dto.Usage{
				InputTokens:  1000,
				OutputTokens: 100,
				InputTokensDetails: &dto.InputTokenDetails{
					CachedTokens:         300,
					CachedCreationTokens: 20,
					CacheWriteTokens:     10,
					TextTokens:           400,
					ImageTokens:          600,
					CachedTokensDetails: &dto.CachedTokenDetails{
						ImageTokens: &imageTokens,
						AudioTokens: &audioTokens,
					},
				},
			}

			usage := NormalizeResponsesUsage(source)
			assert.Equal(t, *source.InputTokensDetails, usage.PromptTokensDetails)
			details := usage.PromptTokensDetails.CachedTokensDetails
			require.NotNil(t, details)
			require.NotNil(t, details.ImageTokens)
			require.NotNil(t, details.AudioTokens)
			assert.Nil(t, details.TextTokens, "missing modalities must remain absent")

			imageTokens, audioTokens = 999, 888
			source.InputTokensDetails.ImageTokens = 9999
			assert.Equal(t, tc.imageTokens, *details.ImageTokens, "later source updates must not change billable image cache")
			assert.Zero(t, *details.AudioTokens, "explicit zero must survive normalization and source mutation")
			assert.Equal(t, 600, usage.PromptTokensDetails.ImageTokens)
		})
	}
}

func TestResponsesResponseToChatCompletionsSeparatesInterleavedOutputItems(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:     "resp_1",
		Model:  "gpt-test",
		Status: []byte(`"completed"`),
		Output: interleavedReasoningAndTextOutput(),
	}

	chat, _, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl_1")
	require.NoError(t, err)
	assert.Equal(t, "**Planning file inspection**\n\n**Clarifying environment task requirements**", chat.Choices[0].Message.GetReasoningContent())
	assert.Equal(t, "I’ll inspect the starter repository.\n\nWhat would you like me to build?", chat.Choices[0].Message.StringContent())
}

func TestResponsesBufferedAccumulatorPreservesInterleavedReasoningAndTextItems(t *testing.T) {
	acc := NewResponsesBufferedAccumulator()
	events := []dto.ResponsesStreamResponse{
		bufferedOutputItemAdded(0, "rs_1", responsesOutputTypeReasoning),
		{Type: responsesEventReasoningSummaryDelta, OutputIndex: intPointer(0), ItemID: "rs_1", Delta: "**Planning file inspection**"},
		bufferedOutputItemAdded(1, "msg_1", responsesOutputTypeMessage),
		{Type: responsesEventOutputTextDelta, OutputIndex: intPointer(1), ItemID: "msg_1", Delta: "I’ll inspect the starter repository."},
		bufferedOutputItemAdded(2, "rs_2", responsesOutputTypeReasoning),
		{Type: responsesEventReasoningSummaryDelta, OutputIndex: intPointer(2), ItemID: "rs_2", Delta: "**Clarifying environment task requirements**"},
		bufferedOutputItemAdded(3, "msg_2", responsesOutputTypeMessage),
		{Type: responsesEventOutputTextDelta, OutputIndex: intPointer(3), ItemID: "msg_2", Delta: "What would you like me to build?"},
	}
	for index := range events {
		acc.ProcessEvent(&events[index])
	}

	output := acc.BuildOutput()
	require.Len(t, output, 4)
	assert.Equal(t, []string{
		responsesOutputTypeReasoning,
		responsesOutputTypeMessage,
		responsesOutputTypeReasoning,
		responsesOutputTypeMessage,
	}, []string{output[0].Type, output[1].Type, output[2].Type, output[3].Type})
	assert.Equal(t, "**Planning file inspection**", output[0].Summary[0].Text)
	assert.Equal(t, "I’ll inspect the starter repository.", output[1].Content[0].Text)
	assert.Equal(t, "**Clarifying environment task requirements**", output[2].Summary[0].Text)
	assert.Equal(t, "What would you like me to build?", output[3].Content[0].Text)
}

func TestResponsesStreamTerminalOutputPreservesInterleavedReasoningAndTextItems(t *testing.T) {
	state := newTestResponsesStreamState()
	chunks, err := ResponsesStreamEventToChatChunks(&dto.ResponsesStreamResponse{
		Type: responsesEventCompleted,
		Response: &dto.OpenAIResponsesResponse{
			Status: []byte(`"completed"`),
			Output: interleavedReasoningAndTextOutput(),
		},
	}, state)
	require.NoError(t, err)

	var deltas []string
	for _, chunk := range chunks {
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.ReasoningContent != nil {
			deltas = append(deltas, "thinking:"+*delta.ReasoningContent)
		}
		if delta.Content != nil && *delta.Content != "" {
			deltas = append(deltas, "text:"+*delta.Content)
		}
	}
	assert.Equal(t, []string{
		"thinking:**Planning file inspection**",
		"text:I’ll inspect the starter repository.",
		"thinking:**Clarifying environment task requirements**",
		"text:What would you like me to build?",
	}, deltas)
}

func bufferedOutputItemAdded(outputIndex int, itemID string, itemType string) dto.ResponsesStreamResponse {
	return dto.ResponsesStreamResponse{
		Type:        responsesEventOutputItemAdded,
		OutputIndex: &outputIndex,
		ItemID:      itemID,
		Item:        &dto.ResponsesOutput{ID: itemID, Type: itemType},
	}
}

func interleavedReasoningAndTextOutput() []dto.ResponsesOutput {
	return []dto.ResponsesOutput{
		{Type: responsesOutputTypeReasoning, Summary: []dto.ResponsesReasoningSummaryPart{{Type: "summary_text", Text: "**Planning file inspection**"}}},
		{Type: responsesOutputTypeMessage, Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "I’ll inspect the starter repository."}}},
		{Type: responsesOutputTypeReasoning, Summary: []dto.ResponsesReasoningSummaryPart{{Type: "summary_text", Text: "**Clarifying environment task requirements**"}}},
		{Type: responsesOutputTypeMessage, Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "What would you like me to build?"}}},
	}
}

func intPointer(value int) *int {
	return &value
}
