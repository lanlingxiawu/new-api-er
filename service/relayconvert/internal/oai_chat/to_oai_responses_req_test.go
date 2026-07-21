package oaichat

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/QuantumNous/new-api/dto"
)

func assistantMessageWithTool(content string, id string, name string, args string) dto.Message {
	msg := dto.Message{Role: "assistant", Content: content}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: id, Type: "function", Function: dto.FunctionRequest{Name: name, Arguments: args}},
	})
	return msg
}

func TestResponsesReq_NilAndValidation(t *testing.T) {
	_, err := ChatCompletionsRequestToResponsesRequest(nil)
	require.Error(t, err)

	_, err = ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")

	_, err = ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{Model: "m", N: lo.ToPtr(2)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "n>1")
}

func TestResponsesReq_InstructionsToolsAndInputItems(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		N:     lo.ToPtr(1),
		Messages: []dto.Message{
			{Role: "system", Content: "system rules"},
			{Role: "developer", Content: "developer rules"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png"}},
			}},
			assistantMessageWithTool("partial text", "call_1", "lookup", `{"q":"x"}`),
			{Role: "tool", ToolCallId: "call_1", Content: "tool result"},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)

	assert.Equal(t, "gpt-test", got.Model)
	assert.Equal(t, `"system rules\n\ndeveloper rules"`, string(got.Instructions))
	assert.Equal(t, "input_image", gjson.GetBytes(got.Input, "0.content.1.type").String())
	assert.Equal(t, "function_call", gjson.GetBytes(got.Input, "2.type").String())
	assert.Equal(t, "call_1", gjson.GetBytes(got.Input, "2.call_id").String())
	assert.Equal(t, "function_call_output", gjson.GetBytes(got.Input, "3.type").String())
}

func TestResponsesReq_SkipsEmptyRole(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "", Content: "ignored"},
			{Role: "user", Content: "kept"},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	// Only one input item (the empty-role message is skipped).
	assert.Equal(t, int64(1), gjson.GetBytes(got.Input, "#").Int())
	assert.Equal(t, "user", gjson.GetBytes(got.Input, "0.role").String())
}

func TestResponsesReq_ToolMissingCallIDBecomesUserText(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "tool", ToolCallId: "", Content: "orphaned output"},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "user", gjson.GetBytes(got.Input, "0.role").String())
	assert.Contains(t, gjson.GetBytes(got.Input, "0.content").String(), "tool_output_missing_call_id")
}

func TestResponsesReq_ToolNonStringContentMarshaled(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "tool", ToolCallId: "c1", Content: []any{
				map[string]any{"type": "text", "text": "hi"},
			}},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "function_call_output", gjson.GetBytes(got.Input, "0.type").String())
	// output is the marshaled JSON string of the content array.
	assert.Contains(t, gjson.GetBytes(got.Input, "0.output").String(), "text")
}

func TestResponsesReq_ToolNilContentEmptyOutput(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "tool", ToolCallId: "c1", Content: nil},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "", gjson.GetBytes(got.Input, "0.output").String())
}

func TestResponsesReq_SystemWithContentPartsAndNilSkipped(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "system", Content: nil}, // skipped
			{Role: "system", Content: []any{
				map[string]any{"type": "text", "text": "line one"},
				map[string]any{"type": "text", "text": "line two"},
			}},
			{Role: "user", Content: "hi"},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "line one\nline two", gjson.GetBytes(got.Instructions, "@this").String())
}

func TestResponsesReq_AssistantNilContentWithToolCalls(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: nil}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
		{ID: "", Type: "function", Function: dto.FunctionRequest{Name: "skipme"}},      // no id -> skipped
		{ID: "c2", Type: "custom", Function: dto.FunctionRequest{Name: "nope"}},        // non-function type -> skipped
		{ID: "c3", Type: "function", Function: dto.FunctionRequest{Name: ""}},          // empty name -> skipped
	})
	req := &dto.GeneralOpenAIRequest{Model: "gpt-test", Messages: []dto.Message{msg}}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	// item 0: assistant content ""; item 1: the single valid function_call.
	assert.Equal(t, "", gjson.GetBytes(got.Input, "0.content").String())
	assert.Equal(t, "function_call", gjson.GetBytes(got.Input, "1.type").String())
	assert.Equal(t, "c1", gjson.GetBytes(got.Input, "1.call_id").String())
	assert.Equal(t, int64(2), gjson.GetBytes(got.Input, "#").Int())
}

func TestResponsesReq_ContentPartTypes(t *testing.T) {
	// Use pre-parsed MediaContent so every content-part branch (including
	// video_url and the default passthrough) is exercised — Message.ParseContent
	// silently drops unknown/malformed map parts.
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "t"},
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "http://x/y.png"}},
		{Type: dto.ContentTypeInputAudio, InputAudio: &dto.MessageInputAudio{Data: "AAA", Format: "wav"}},
		{Type: dto.ContentTypeFile, File: &dto.MessageFile{FileData: "d", FileName: "f.txt"}},
		{Type: dto.ContentTypeVideoUrl, VideoUrl: &dto.MessageVideoUrl{Url: "http://x/v.mp4"}},
	})
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model:    "gpt-test",
		Messages: []dto.Message{msg},
	})
	require.NoError(t, err)
	assert.Equal(t, "input_text", gjson.GetBytes(got.Input, "0.content.0.type").String())
	assert.Equal(t, "input_image", gjson.GetBytes(got.Input, "0.content.1.type").String())
	assert.Equal(t, "http://x/y.png", gjson.GetBytes(got.Input, "0.content.1.image_url").String())
	assert.Equal(t, "input_audio", gjson.GetBytes(got.Input, "0.content.2.type").String())
	assert.Equal(t, "input_file", gjson.GetBytes(got.Input, "0.content.3.type").String())
	assert.Equal(t, "input_video", gjson.GetBytes(got.Input, "0.content.4.type").String())
}

func TestResponsesReq_AssistantOutputTextRole(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "assistant", Content: []any{
				map[string]any{"type": "text", "text": "answer"},
			}},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	// assistant text parts become output_text.
	assert.Equal(t, "output_text", gjson.GetBytes(got.Input, "0.content.0.type").String())
}

func TestResponsesReq_UnknownContentPartTypePassthrough(t *testing.T) {
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{{Type: "weird_thing", Text: "x"}})
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model:    "gpt-test",
		Messages: []dto.Message{msg},
	})
	require.NoError(t, err)
	assert.Equal(t, "weird_thing", gjson.GetBytes(got.Input, "0.content.0.type").String())
}

func TestResponsesReq_ToolsFunctionAndUnknownType(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Description: "d", Parameters: map[string]any{"type": "object"}}},
			{Type: "web_search"},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "function", gjson.GetBytes(got.Tools, "0.type").String())
	assert.Equal(t, "lookup", gjson.GetBytes(got.Tools, "0.name").String())
	assert.Equal(t, "web_search", gjson.GetBytes(got.Tools, "1.type").String())
}

func TestResponsesReq_ToolChoiceVariants(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model: "m", ToolChoice: "auto",
		})
		require.NoError(t, err)
		assert.Equal(t, "auto", gjson.ParseBytes(got.ToolChoice).String())
	})
	t.Run("function object chat shape", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model: "m",
			ToolChoice: map[string]any{
				"type":     "function",
				"function": map[string]any{"name": "lookup"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "function", gjson.GetBytes(got.ToolChoice, "type").String())
		assert.Equal(t, "lookup", gjson.GetBytes(got.ToolChoice, "name").String())
	})
	t.Run("function object responses shape", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model: "m",
			ToolChoice: map[string]any{
				"type": "function",
				"name": "already",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "already", gjson.GetBytes(got.ToolChoice, "name").String())
	})
	t.Run("non-function object passthrough", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model:      "m",
			ToolChoice: map[string]any{"type": "none"},
		})
		require.NoError(t, err)
		assert.Equal(t, "none", gjson.GetBytes(got.ToolChoice, "type").String())
	})
}

func TestResponsesReq_ZeroValuePreservation(t *testing.T) {
	// Rule 5: explicit zero temperature, zero top_p, false parallel_tool_calls,
	// and max_tokens=0 must all survive into the converted responses request.
	req := &dto.GeneralOpenAIRequest{
		Model:            "m",
		Temperature:      ptr(0.0),
		TopP:             ptr(0.0),
		ParallelTooCalls: ptr(false),
		MaxTokens:        ptr(uint(0)),
		Stream:           ptr(false),
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	require.NotNil(t, got.Temperature)
	assert.Equal(t, 0.0, *got.Temperature)
	require.NotNil(t, got.TopP)
	assert.Equal(t, 0.0, *got.TopP)
	assert.Equal(t, "false", string(got.ParallelToolCalls))
	require.NotNil(t, got.MaxOutputTokens)
	assert.Equal(t, uint(0), *got.MaxOutputTokens)
	require.NotNil(t, got.Stream)
	assert.False(t, *got.Stream)
}

func TestResponsesReq_MaxCompletionTokensWins(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:               "m",
		MaxTokens:           ptr(uint(10)),
		MaxCompletionTokens: ptr(uint(99)),
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	require.NotNil(t, got.MaxOutputTokens)
	assert.Equal(t, uint(99), *got.MaxOutputTokens)
}

func TestResponsesReq_MaxOutputTokensAbsentWhenNoTokenFields(t *testing.T) {
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{Model: "m"})
	require.NoError(t, err)
	assert.Nil(t, got.MaxOutputTokens)
}

func TestResponsesReq_ReasoningEffort(t *testing.T) {
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model:           "m",
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	require.NotNil(t, got.Reasoning)
	assert.Equal(t, "high", got.Reasoning.Effort)
	assert.Equal(t, "detailed", got.Reasoning.Summary)
}

func TestResponsesReq_ResponseFormatText(t *testing.T) {
	t.Run("json_schema", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model: "m",
			ResponseFormat: &dto.ResponseFormat{
				Type:       "json_schema",
				JsonSchema: []byte(`{"name":"foo","schema":{"type":"object"}}`),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "json_schema", gjson.GetBytes(got.Text, "format.type").String())
		assert.Equal(t, "foo", gjson.GetBytes(got.Text, "format.name").String())
	})
	t.Run("json_object", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model:          "m",
			ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
		})
		require.NoError(t, err)
		assert.Equal(t, "json_object", gjson.GetBytes(got.Text, "format.type").String())
	})
	t.Run("empty type -> nil text", func(t *testing.T) {
		got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model:          "m",
			ResponseFormat: &dto.ResponseFormat{Type: "  "},
		})
		require.NoError(t, err)
		assert.Nil(t, got.Text)
	})
}

func TestNormalizeChatImageURLToString(t *testing.T) {
	assert.Equal(t, "u", normalizeChatImageURLToString("u"))
	assert.Equal(t, "u", normalizeChatImageURLToString(map[string]any{"url": "u"}))
	assert.Equal(t, "u", normalizeChatImageURLToString(dto.MessageImageUrl{Url: "u"}))
	assert.Equal(t, "u", normalizeChatImageURLToString(&dto.MessageImageUrl{Url: "u"}))
	// map without url -> returned unchanged
	m := map[string]any{"detail": "high"}
	assert.Equal(t, m, normalizeChatImageURLToString(m))
	// nil pointer -> returned unchanged
	var np *dto.MessageImageUrl
	assert.Equal(t, np, normalizeChatImageURLToString(np))
	// default type
	assert.Equal(t, 42, normalizeChatImageURLToString(42))
}

// --- supplementary branch coverage -----------------------------------------

func TestNormalizeChatImageURLToString_EmptyURLReturnsOriginal(t *testing.T) {
	// value type with empty Url -> returns the value unchanged
	v := dto.MessageImageUrl{Url: ""}
	assert.Equal(t, v, normalizeChatImageURLToString(v))
	// pointer with empty Url -> returns pointer unchanged
	pv := &dto.MessageImageUrl{Url: ""}
	assert.Equal(t, pv, normalizeChatImageURLToString(pv))
}

func TestConvertResponseFormat_JSONSchemaNestedAndError(t *testing.T) {
	// nested "json_schema" key is hoisted, "type" key skipped.
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "m",
		ResponseFormat: &dto.ResponseFormat{
			Type:       "json_schema",
			JsonSchema: []byte(`{"type":"json_schema","json_schema":{"name":"foo","schema":{"type":"object"}}}`),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "json_schema", gjson.GetBytes(got.Text, "format.type").String())
	assert.Equal(t, "foo", gjson.GetBytes(got.Text, "format.name").String())

	// Invalid JSON schema -> the raw schema is stored under json_schema, but that
	// invalid RawMessage cannot be re-marshaled, so the resulting Text is empty
	// (the marshal error is swallowed). The error branch is still exercised.
	got2, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "m",
		ResponseFormat: &dto.ResponseFormat{
			Type:       "json_schema",
			JsonSchema: []byte(`{bad`),
		},
	})
	require.NoError(t, err)
	assert.Empty(t, got2.Text)
}

func TestResponsesReq_ToolNonStringContentMarshalError(t *testing.T) {
	// A channel value cannot be JSON-marshaled -> the fmt.Sprintf fallback runs.
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "tool", ToolCallId: "c1", Content: make(chan int)},
		},
	}
	got, err := ChatCompletionsRequestToResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "function_call_output", gjson.GetBytes(got.Input, "0.type").String())
}

func TestResponsesReq_AssistantStringContentToolCallSkips(t *testing.T) {
	// string-content assistant path with tool calls that must be skipped.
	msg := dto.Message{Role: "assistant", Content: "text here"}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "ok", Type: "function", Function: dto.FunctionRequest{Name: "keep", Arguments: `{}`}},
		{ID: "", Type: "function", Function: dto.FunctionRequest{Name: "skip"}},   // no id
		{ID: "x", Type: "custom", Function: dto.FunctionRequest{Name: "skip"}},    // non-function
		{ID: "y", Type: "function", Function: dto.FunctionRequest{Name: ""}},      // empty name
	})
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "m", Messages: []dto.Message{msg},
	})
	require.NoError(t, err)
	// item0: assistant text; item1: the single kept function_call.
	assert.Equal(t, "function_call", gjson.GetBytes(got.Input, "1.type").String())
	assert.Equal(t, "keep", gjson.GetBytes(got.Input, "1.name").String())
	assert.Equal(t, int64(2), gjson.GetBytes(got.Input, "#").Int())
}

func TestResponsesReq_AssistantMediaContentToolCallSkips(t *testing.T) {
	msg := dto.Message{Role: "assistant"}
	msg.SetMediaContent([]dto.MediaContent{{Type: dto.ContentTypeText, Text: "hi"}})
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "ok", Type: "function", Function: dto.FunctionRequest{Name: "keep", Arguments: `{}`}},
		{ID: "", Type: "function", Function: dto.FunctionRequest{Name: "skip"}},
		{ID: "x", Type: "custom", Function: dto.FunctionRequest{Name: "skip"}},
		{ID: "y", Type: "function", Function: dto.FunctionRequest{Name: ""}},
	})
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "m", Messages: []dto.Message{msg},
	})
	require.NoError(t, err)
	assert.Equal(t, "function_call", gjson.GetBytes(got.Input, "1.type").String())
	assert.Equal(t, "keep", gjson.GetBytes(got.Input, "1.name").String())
}

func TestResponsesReq_ToolChoiceNonObjectScalar(t *testing.T) {
	// A non-string, non-object tool_choice (number) exercises the m==nil marshal
	// fallback in the default case.
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model:      "m",
		ToolChoice: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, "5", string(got.ToolChoice))
}

func TestResponsesReq_ToolChoiceFunctionWithoutName(t *testing.T) {
	// type=function but function object has no name -> falls back to marshaling v.
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "m",
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"nope": "x"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "function", gjson.GetBytes(got.ToolChoice, "type").String())
}

func TestResponsesReq_ToolChoiceFunctionTypeOnly(t *testing.T) {
	// type=function with neither a top-level name nor a function object -> the
	// final else marshals the original value.
	got, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model:      "m",
		ToolChoice: map[string]any{"type": "function"},
	})
	require.NoError(t, err)
	assert.Equal(t, "function", gjson.GetBytes(got.ToolChoice, "type").String())
}
