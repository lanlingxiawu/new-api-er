package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeParts(t *testing.T, content any) []dto.ClaudeMediaMessage {
	t.Helper()
	parts, ok := content.([]dto.ClaudeMediaMessage)
	require.True(t, ok, "content is not []ClaudeMediaMessage: %T", content)
	return parts
}

func TestOpenAIResponsesRequestToClaudeMessagesNilAndModel(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToClaudeMessages(c, nil)
	require.Error(t, err)

	_, err = OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestOpenAIResponsesRequestToClaudeMessagesRejectsStatefulFields(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model:        "claude-test",
		Conversation: mustRawMessage(t, "conv"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stateful fields")
}

func TestOpenAIResponsesRequestToClaudeMessagesBasic(t *testing.T) {
	c := newTestGinContext()
	maxTokens := uint(256)
	temp := 0.0
	topP := 0.5
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model:           "claude-test",
		Temperature:     &temp,
		TopP:            &topP,
		MaxOutputTokens: &maxTokens,
		Instructions:    mustRawMessage(t, "be nice"),
		Input:           mustRawMessage(t, "hello"),
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-test", got.Model)
	require.NotNil(t, got.MaxTokens)
	assert.Equal(t, uint(256), *got.MaxTokens)
	require.NotNil(t, got.Temperature)
	assert.Equal(t, 0.0, *got.Temperature)
	// system message from instructions
	sys := claudeParts(t, got.System)
	require.Len(t, sys, 1)
	assert.Equal(t, "be nice", sys[0].GetText())
	// user message from input string
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
	parts := claudeParts(t, got.Messages[0].Content)
	require.Len(t, parts, 1)
	assert.Equal(t, "hello", parts[0].GetText())
}

func TestOpenAIResponsesRequestToClaudeMessagesDefaultMaxTokens(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, "hi"),
	})
	require.NoError(t, err)
	require.NotNil(t, got.MaxTokens)
	assert.Greater(t, int(*got.MaxTokens), 0)
}

func TestOpenAIResponsesRequestToClaudeMessagesReasoningEffort(t *testing.T) {
	c := newTestGinContext()
	tests := []struct {
		effort string
		budget int
	}{
		{"low", 1280},
		{"medium", 2048},
		{"high", 4096},
	}
	for _, tt := range tests {
		t.Run(tt.effort, func(t *testing.T) {
			got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
				Model:     "claude-test",
				Input:     mustRawMessage(t, "hi"),
				Reasoning: &dto.Reasoning{Effort: tt.effort},
			})
			require.NoError(t, err)
			require.NotNil(t, got.Thinking)
			assert.Equal(t, "enabled", got.Thinking.Type)
			require.NotNil(t, got.Thinking.BudgetTokens)
			assert.Equal(t, tt.budget, *got.Thinking.BudgetTokens)
		})
	}

	// no effort -> no thinking
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, "hi"),
	})
	require.NoError(t, err)
	assert.Nil(t, got.Thinking)
}

func TestOpenAIResponsesRequestToClaudeMessagesToolsAndToolChoice(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, "hi"),
		Tools: mustRawMessage(t, []map[string]any{
			{"type": "function", "name": "lookup", "description": "d", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}},
			}},
		}),
		ToolChoice:        mustRawMessage(t, "auto"),
		ParallelToolCalls: mustRawMessage(t, true),
	})
	require.NoError(t, err)
	tools, ok := got.Tools.([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool := tools[0].(*dto.Tool)
	assert.Equal(t, "lookup", tool.Name)
	assert.Equal(t, "object", tool.InputSchema["type"])
	assert.NotNil(t, got.ToolChoice)
}

func TestOpenAIResponsesRequestToClaudeMessagesInputItems(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "system", "content": "sys via input"},
			{"role": "user", "content": []map[string]any{{"type": "input_text", "text": "question"}}},
			{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": map[string]any{"q": "x"}},
			{"type": "function_call_output", "call_id": "call_1", "output": map[string]any{"ok": true}},
		}),
	})
	require.NoError(t, err)

	// system captured
	sys := claudeParts(t, got.System)
	require.Len(t, sys, 1)
	assert.Equal(t, "sys via input", sys[0].GetText())

	// messages: user(question) -> assistant(tool_use) -> user(tool_result)
	require.Len(t, got.Messages, 3)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "assistant", got.Messages[1].Role)
	toolUse := claudeParts(t, got.Messages[1].Content)
	require.Len(t, toolUse, 1)
	assert.Equal(t, "tool_use", toolUse[0].Type)
	assert.Equal(t, "call_1", toolUse[0].Id)
	assert.Equal(t, "lookup", toolUse[0].Name)
	assert.Equal(t, "user", got.Messages[2].Role)
	toolResult := claudeParts(t, got.Messages[2].Content)
	require.Len(t, toolResult, 1)
	assert.Equal(t, "tool_result", toolResult[0].Type)
	assert.Equal(t, "call_1", toolResult[0].ToolUseId)
}

func TestOpenAIResponsesRequestToClaudeMessagesCustomToolCall(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "custom_tool_call", "call_id": "cc_1", "name": "apply", "input": "body"},
			{"type": "custom_tool_call_output", "call_id": "cc_1", "output": "result"},
		}),
	})
	require.NoError(t, err)
	// custom_tool_call starts an assistant turn, so a synthetic leading user
	// message is prepended: user(placeholder) -> assistant(tool_use) -> user(tool_result)
	require.Len(t, got.Messages, 3)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "assistant", got.Messages[1].Role)
	assert.Equal(t, "user", got.Messages[2].Role)
}

func TestOpenAIResponsesRequestToClaudeMessagesEmptyUserContentPlaceholder(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{}}, // empty -> placeholder "..."
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	parts := claudeParts(t, got.Messages[0].Content)
	require.Len(t, parts, 1)
	assert.Equal(t, "...", parts[0].GetText())
}

func TestOpenAIResponsesRequestToClaudeMessagesEnsureStartsWithUser(t *testing.T) {
	c := newTestGinContext()
	// first item is an assistant message -> a synthetic leading user message is prepended
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "assistant", "content": "prior answer"},
		}),
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(got.Messages), 2)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "assistant", got.Messages[1].Role)
}

func TestOpenAIResponsesRequestToClaudeMessagesMediaParts(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "see"},
				{"type": "input_image", "image_url": "https://x/png.png"},
				{"type": "input_file", "file": map[string]any{"file_data": "https://x/doc.pdf"}},
			}},
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	parts := claudeParts(t, got.Messages[0].Content)
	require.Len(t, parts, 3)
	assert.Equal(t, "text", parts[0].Type)
	assert.Equal(t, "image", parts[1].Type) // image/png -> image
	require.NotNil(t, parts[1].Source)
	assert.Equal(t, "image/png", parts[1].Source.MediaType)
	assert.Equal(t, "document", parts[2].Type) // application/pdf -> document
	assert.Equal(t, "application/pdf", parts[2].Source.MediaType)
}

func TestOpenAIResponsesRequestToClaudeMessagesMediaResolverError(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_image", "image_url": "https://x/trigger-error.png"},
			}},
		}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get file data failed")
}

func TestOpenAIResponsesRequestToClaudeMessagesMediaPartNilSourceSkipped(t *testing.T) {
	c := newTestGinContext()
	// input_image with no resolvable data yields a nil FileSource and is skipped,
	// leaving only the text part.
	got, err := OpenAIResponsesRequestToClaudeMessages(c, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "only text"},
				{"type": "input_image"}, // no url/data -> nil source -> skipped
			}},
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	parts := claudeParts(t, got.Messages[0].Content)
	require.Len(t, parts, 1)
	assert.Equal(t, "only text", parts[0].GetText())
}

// ---- wrapper + helper branch tests ----

func TestConvertOpenAIResponsesRequestToClaudeMessagesWrapper(t *testing.T) {
	c := newTestGinContext()
	out, err := convertOpenAIResponsesRequestToClaudeMessages(c, nil, &dto.OpenAIResponsesRequest{
		Model: "claude-test",
		Input: mustRawMessage(t, "hi"),
	})
	require.NoError(t, err)
	_, ok := out.(*dto.ClaudeRequest)
	assert.True(t, ok)

	_, err = convertOpenAIResponsesRequestToClaudeMessages(c, nil, "not a request")
	require.Error(t, err)
}

func TestResponsesFunctionParametersToClaudeInputSchema(t *testing.T) {
	// map without type/properties gets defaults filled in
	schema := responsesFunctionParametersToClaudeInputSchema(map[string]any{"required": []any{"q"}})
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, map[string]interface{}{}, schema["properties"])

	// map with explicit type/properties preserved
	schema = responsesFunctionParametersToClaudeInputSchema(map[string]any{
		"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}},
	})
	assert.Equal(t, "object", schema["type"])
	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "q")

	// non-map -> default object schema
	schema = responsesFunctionParametersToClaudeInputSchema("not a map")
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, map[string]interface{}{}, schema["properties"])
}

func TestClaudeMessageContentParts(t *testing.T) {
	// []ClaudeMediaMessage passthrough
	in := []dto.ClaudeMediaMessage{{Type: "text"}}
	assert.Equal(t, in, claudeMessageContentParts(in))

	// empty string -> nil
	assert.Nil(t, claudeMessageContentParts(""))

	// non-empty string -> single text part
	parts := claudeMessageContentParts("hi")
	require.Len(t, parts, 1)
	assert.Equal(t, "hi", parts[0].GetText())

	// default (nil / other) -> Any2Type attempt
	assert.Empty(t, claudeMessageContentParts(nil))
}

func TestResponsesClaudeRole(t *testing.T) {
	assert.Equal(t, "assistant", responsesClaudeRole(map[string]any{"role": "assistant"}))
	assert.Equal(t, "system", responsesClaudeRole(map[string]any{"role": "system"}))
	assert.Equal(t, "system", responsesClaudeRole(map[string]any{"role": "developer"}))
	assert.Equal(t, "user", responsesClaudeRole(map[string]any{"role": "user"}))
	assert.Equal(t, "user", responsesClaudeRole(map[string]any{}))
}

func TestResponsesToolOutputValue(t *testing.T) {
	assert.Equal(t, "", responsesToolOutputValue(nil))
	assert.Equal(t, "x", responsesToolOutputValue("x"))
}

func TestAppendClaudeToolUseNewAssistant(t *testing.T) {
	// no existing assistant -> new message appended
	msgs := appendClaudeToolUse(nil, dto.ClaudeMediaMessage{Type: "tool_use", Id: "1"})
	require.Len(t, msgs, 1)
	assert.Equal(t, "assistant", msgs[0].Role)

	// existing assistant with string content -> merged
	msgs = []dto.ClaudeMessage{{Role: "assistant", Content: "prior"}}
	msgs = appendClaudeToolUse(msgs, dto.ClaudeMediaMessage{Type: "tool_use", Id: "2"})
	require.Len(t, msgs, 1)
	parts := claudeParts(t, msgs[0].Content)
	require.Len(t, parts, 2)
}

func TestAppendClaudeToolResultNewUser(t *testing.T) {
	msgs := appendClaudeToolResult(nil, dto.ClaudeMediaMessage{Type: "tool_result", ToolUseId: "1"})
	require.Len(t, msgs, 1)
	assert.Equal(t, "user", msgs[0].Role)

	msgs = []dto.ClaudeMessage{{Role: "user", Content: "prior"}}
	msgs = appendClaudeToolResult(msgs, dto.ClaudeMediaMessage{Type: "tool_result", ToolUseId: "2"})
	require.Len(t, msgs, 1)
	parts := claudeParts(t, msgs[0].Content)
	require.Len(t, parts, 2)
}

func TestEnsureClaudeMessagesStartWithUser(t *testing.T) {
	// already starts with user
	msgs := []dto.ClaudeMessage{{Role: "user"}}
	assert.Equal(t, msgs, ensureClaudeMessagesStartWithUser(msgs))

	// empty
	assert.Empty(t, ensureClaudeMessagesStartWithUser(nil))

	// starts with assistant -> prepends user
	out := ensureClaudeMessagesStartWithUser([]dto.ClaudeMessage{{Role: "assistant"}})
	require.Len(t, out, 2)
	assert.Equal(t, "user", out[0].Role)
}
