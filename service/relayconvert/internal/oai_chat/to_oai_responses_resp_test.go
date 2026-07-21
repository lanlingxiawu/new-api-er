package oaichat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/dto"
)

// --- ChatCompletionsResponseToResponsesResponse ----------------------------

func TestResponsesResp_NilInput(t *testing.T) {
	_, _, err := ChatCompletionsResponseToResponsesResponse(nil, "id")
	require.Error(t, err)
}

func TestResponsesResp_NoChoices(t *testing.T) {
	out, usage, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Model: "gpt-test",
	}, "resp_1")
	require.NoError(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, `"completed"`, string(out.Status))
	assert.Empty(t, out.Output)
}

func TestResponsesResp_TextToolCallsAndUsage(t *testing.T) {
	chat := &dto.OpenAITextResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Created: 456,
		Choices: []dto.OpenAITextResponseChoice{
			{Message: assistantMessageWithTool("I will call.", "call_1", "lookup", `{"q":"x"}`), FinishReason: "tool_calls"},
		},
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
	}
	resp, usage, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_1")
	require.NoError(t, err)
	require.NotNil(t, usage)

	assert.Equal(t, "resp_1", resp.ID)
	assert.Equal(t, "response", resp.Object)
	assert.Equal(t, `"completed"`, string(resp.Status))
	assert.Equal(t, 456, resp.CreatedAt)
	assert.Equal(t, 3, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
	require.Len(t, resp.Output, 2)
	assert.Equal(t, responsesOutputTypeMessage, resp.Output[0].Type)
	assert.Equal(t, "I will call.", resp.Output[0].Content[0].Text)
	assert.Equal(t, responsesOutputTypeFunctionCall, resp.Output[1].Type)
	assert.Equal(t, "call_1", resp.Output[1].CallId)
	assert.Equal(t, "lookup", resp.Output[1].Name)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(resp.Output[1].Arguments))
}

func TestResponsesResp_ReasoningOutput(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: "answer", ReasoningContent: ptr("because")}
	resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Id: "c", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "stop"}},
	}, "resp_1")
	require.NoError(t, err)
	require.Len(t, resp.Output, 2)
	assert.Equal(t, responsesOutputTypeMessage, resp.Output[0].Type)
	assert.Equal(t, responsesOutputTypeReasoning, resp.Output[1].Type)
	assert.Equal(t, "because", resp.Output[1].Content[0].Text)
}

func TestResponsesResp_IncompleteFinishReasons(t *testing.T) {
	tests := []struct {
		name, finishReason, wantReason string
	}{
		{"length", "length", responsesIncompleteReasonMaxTokens},
		{"content_filter", "content_filter", responsesIncompleteReasonContentFilter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
				Id: "c", Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: dto.Message{Role: "assistant", Content: "partial"}, FinishReason: tt.finishReason},
				},
			}, "resp_1")
			require.NoError(t, err)
			assert.Equal(t, `"incomplete"`, string(resp.Status))
			require.NotNil(t, resp.IncompleteDetails)
			assert.Equal(t, tt.wantReason, resp.IncompleteDetails.Reason)
			require.Len(t, resp.Output, 1)
			assert.Equal(t, "incomplete", resp.Output[0].Status)
		})
	}
}

func TestResponsesResp_CustomToolCall(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: ""}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "custom_tool", Custom: []byte(`{"raw":true}`)},
	})
	resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Id: "c", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "stop"}},
	}, "resp_1")
	require.NoError(t, err)
	// text ("") is empty AND there is a tool call, so no message output; only the
	// custom tool output.
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "custom_tool", resp.Output[0].Type)
	assert.Equal(t, "c1", resp.Output[0].CallId)
	assert.Equal(t, `{"raw":true}`, string(resp.Output[0].Arguments))
}

func TestResponsesResp_ToolCallMissingIDGetsGeneratedID(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: ""}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{}`}},
	})
	resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Id: "c", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "stop"}},
	}, "resp_1")
	require.NoError(t, err)
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "resp_1_call_0", resp.Output[0].CallId)
}

// --- ResponsesStatusFromChatFinishReason -----------------------------------

func TestResponsesStatusFromChatFinishReason(t *testing.T) {
	s, d := ResponsesStatusFromChatFinishReason("length")
	assert.Equal(t, "incomplete", s)
	require.NotNil(t, d)
	assert.Equal(t, responsesIncompleteReasonMaxTokens, d.Reason)

	s, d = ResponsesStatusFromChatFinishReason("content_filter")
	assert.Equal(t, "incomplete", s)
	require.NotNil(t, d)

	s, d = ResponsesStatusFromChatFinishReason("stop")
	assert.Equal(t, "completed", s)
	assert.Nil(t, d)

	// whitespace trimmed
	s, _ = ResponsesStatusFromChatFinishReason("  length  ")
	assert.Equal(t, "incomplete", s)
}

// --- UsageFromChatUsage ----------------------------------------------------

func TestUsageFromChatUsage_Nil(t *testing.T) {
	u := UsageFromChatUsage(nil)
	require.NotNil(t, u)
	assert.Equal(t, 0, u.PromptTokens)
}

func TestUsageFromChatUsage_FullMapping(t *testing.T) {
	src := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 4,
		TotalTokens:      14,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2,
			TextTokens:   8,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 1},
		ClaudeCacheCreation5mTokens: 3,
		ClaudeCacheCreation1hTokens: 1,
	}
	u := UsageFromChatUsage(src)
	assert.Equal(t, 10, u.PromptTokens)
	assert.Equal(t, 10, u.InputTokens)
	assert.Equal(t, 4, u.CompletionTokens)
	assert.Equal(t, 4, u.OutputTokens)
	assert.Equal(t, 14, u.TotalTokens)
	require.NotNil(t, u.InputTokensDetails)
	assert.Equal(t, 2, u.InputTokensDetails.CachedTokens)
	assert.Equal(t, 1, u.CompletionTokenDetails.ReasoningTokens)
	assert.Equal(t, 3, u.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 1, u.ClaudeCacheCreation1hTokens)
	require.NotNil(t, u.BillingUsage)
}

func TestUsageFromChatUsage_TotalDerivedWhenZero(t *testing.T) {
	u := UsageFromChatUsage(&dto.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 0})
	assert.Equal(t, 8, u.TotalTokens)
}

func TestUsageFromChatUsage_ReusesExistingBillingUsage(t *testing.T) {
	existing := dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 7})
	u := UsageFromChatUsage(&dto.Usage{PromptTokens: 1, BillingUsage: existing})
	require.NotNil(t, u.BillingUsage)
	require.NotNil(t, u.BillingUsage.OpenAIUsage)
	assert.Equal(t, 7, u.BillingUsage.OpenAIUsage.PromptTokens)
}

// --- chatCreatedAt ---------------------------------------------------------

func TestChatCreatedAt(t *testing.T) {
	assert.Equal(t, 5, chatCreatedAt(5))
	assert.Equal(t, 6, chatCreatedAt(int64(6)))
	assert.Equal(t, 7, chatCreatedAt(float64(7)))
	assert.Equal(t, 8, chatCreatedAt(float32(8)))
	assert.Equal(t, 9, chatCreatedAt("9"))
	// unparseable string -> now()
	got := chatCreatedAt("not-a-number")
	assert.InDelta(t, time.Now().Unix(), got, 5)
	// unknown type -> now()
	got2 := chatCreatedAt(nil)
	assert.InDelta(t, time.Now().Unix(), got2, 5)
}

// --- chatArgumentsRawMessage / intPtr --------------------------------------

func TestChatArgumentsRawMessage(t *testing.T) {
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(chatArgumentsRawMessage(`{"q":"x"}`)))
	assert.Equal(t, `""`, string(chatArgumentsRawMessage("")))
}

func TestIntPtr(t *testing.T) {
	p := intPtr(3)
	require.NotNil(t, p)
	assert.Equal(t, 3, *p)
}

// --- responseOutputStatus / responseStatusString ---------------------------

func TestResponseOutputStatusHelpers(t *testing.T) {
	assert.Equal(t, "completed", responseOutputStatus(nil))
	assert.Equal(t, "", responseStatusString(nil))

	completed := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)}
	assert.Equal(t, "completed", responseOutputStatus(completed))
	assert.Equal(t, "completed", responseStatusString(completed))

	incomplete := &dto.OpenAIResponsesResponse{Status: []byte(`"incomplete"`)}
	assert.Equal(t, "incomplete", responseOutputStatus(incomplete))
}
