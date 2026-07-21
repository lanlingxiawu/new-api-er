package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesRequestToChatCompletionsRequestNilAndModel(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")

	_, err = ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestResponsesRequestToChatCompletionsRequestInstructionsAndScalarInput(t *testing.T) {
	stream := true
	temperature := 0.0 // explicit zero (Rule 5) must survive
	topP := 0.9
	maxOutputTokens := uint(128)
	parallelToolCalls := true

	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model:                "gpt-test",
		Instructions:         mustRawMessage(t, "system rules"),
		Input:                mustRawMessage(t, "hello"),
		Stream:               &stream,
		StreamOptions:        &dto.StreamOptions{IncludeUsage: true},
		MaxOutputTokens:      &maxOutputTokens,
		Temperature:          &temperature,
		TopP:                 &topP,
		User:                 mustRawMessage(t, "user-1"),
		Store:                mustRawMessage(t, false),
		Metadata:             mustRawMessage(t, map[string]any{"trace": "abc"}),
		ParallelToolCalls:    mustRawMessage(t, parallelToolCalls),
		PromptCacheKey:       mustRawMessage(t, "cache-key"),
		PromptCacheRetention: mustRawMessage(t, "24h"),
		ServiceTier:          "flex",
		Reasoning:            &dto.Reasoning{Effort: "medium"},
	})
	require.NoError(t, err)

	assert.Equal(t, "gpt-test", got.Model)
	require.Len(t, got.Messages, 2)
	assert.Equal(t, dto.Message{Role: "system", Content: "system rules"}, got.Messages[0])
	assert.Equal(t, dto.Message{Role: "user", Content: "hello"}, got.Messages[1])
	assert.Same(t, &stream, got.Stream)
	require.NotNil(t, got.StreamOptions)
	assert.True(t, got.StreamOptions.IncludeUsage)
	assert.Equal(t, maxOutputTokens, lo.FromPtr(got.MaxCompletionTokens))
	// Rule 5: explicit zero temperature preserved as non-nil pointer holding 0.0.
	require.NotNil(t, got.Temperature)
	assert.Equal(t, 0.0, *got.Temperature)
	assert.Equal(t, 0.9, lo.FromPtr(got.TopP))
	require.NotNil(t, got.ParallelTooCalls)
	assert.True(t, *got.ParallelTooCalls)
	assert.Equal(t, "cache-key", got.PromptCacheKey)
	assert.Equal(t, "medium", got.ReasoningEffort)
	assert.Equal(t, `"user-1"`, string(got.User))
	assert.Equal(t, `false`, string(got.Store))
	assert.Equal(t, "abc", gjson.GetBytes(got.Metadata, "trace").String())
	assert.Equal(t, `"flex"`, string(got.ServiceTier))
}

func TestResponsesRequestToChatCompletionsRequestParallelFalsePreserved(t *testing.T) {
	// Rule 5: explicit parallel_tool_calls=false must survive as *bool(false).
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model:             "gpt-test",
		Input:             mustRawMessage(t, "hi"),
		ParallelToolCalls: mustRawMessage(t, false),
	})
	require.NoError(t, err)
	require.NotNil(t, got.ParallelTooCalls)
	assert.False(t, *got.ParallelTooCalls)
}

func TestResponsesRequestToChatCompletionsRequestMultimodalInput(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "look"},
					{"type": "input_image", "image_url": "https://example.test/a.png", "detail": "low"},
					{"type": "input_file", "file_id": "file_1", "filename": "a.txt"},
					{"type": "input_audio", "input_audio": map[string]any{"data": "abc", "format": "wav"}},
					{"type": "input_video", "video_url": map[string]any{"url": "https://example.test/v.mp4"}},
				},
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
	parts := got.Messages[0].ParseContent()
	require.Len(t, parts, 5)
	assert.Equal(t, dto.ContentTypeText, parts[0].Type)
	assert.Equal(t, "look", parts[0].Text)
	assert.Equal(t, dto.ContentTypeImageURL, parts[1].Type)
	assert.Equal(t, "https://example.test/a.png", parts[1].GetImageMedia().Url)
	assert.Equal(t, dto.ContentTypeFile, parts[2].Type)
	assert.Equal(t, "file_1", parts[2].GetFile().FileId)
	assert.Equal(t, dto.ContentTypeInputAudio, parts[3].Type)
	assert.Equal(t, "wav", parts[3].GetInputAudio().Format)
	assert.Equal(t, dto.ContentTypeVideoUrl, parts[4].Type)
	assert.Equal(t, "https://example.test/v.mp4", parts[4].GetVideoUrl().Url)
}

func TestResponsesRequestToChatCompletionsRequestUnknownPartAndOnlyText(t *testing.T) {
	// A content array that contains only text parts collapses to a string.
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "a"},
				{"type": "text", "text": "b"},
			}},
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "ab", got.Messages[0].StringContent())

	// Unknown content part type forces the onlyText=false path so the content is
	// kept as a structured slice (not collapsed to a string).
	got, err = ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "a"},
				{"type": "mystery", "foo": "bar"},
			}},
		}),
	})
	require.NoError(t, err)
	rawParts, ok := got.Messages[0].Content.([]any)
	require.True(t, ok, "content should remain a structured slice")
	require.Len(t, rawParts, 2)
}

func TestResponsesRequestToChatCompletionsRequestAssistantTextAndFunctionCallCoexist(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "I will call."}}},
			{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": map[string]any{"q": "x"}},
			{"type": "function_call_output", "call_id": "call_1", "output": map[string]any{"ok": true}},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 2)
	assert.Equal(t, "assistant", got.Messages[0].Role)
	assert.Equal(t, "I will call.", got.Messages[0].StringContent())
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
	assert.Equal(t, "function", toolCalls[0].Type)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
	assert.JSONEq(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, "tool", got.Messages[1].Role)
	assert.Equal(t, "call_1", got.Messages[1].ToolCallId)
	assert.JSONEq(t, `{"ok":true}`, got.Messages[1].StringContent())
}

func TestResponsesRequestToChatCompletionsRequestOnlyFunctionCallCreatesAssistant(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": `{"q":"x"}`},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 1)
	assert.Equal(t, "assistant", got.Messages[0].Role)
	assert.Nil(t, got.Messages[0].Content)
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
}

func TestResponsesRequestToChatCompletionsRequestFunctionCallMissingName(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{{"type": "function_call", "call_id": "c1"}}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing name")
}

func TestResponsesRequestToChatCompletionsRequestFunctionCallOutputUsesID(t *testing.T) {
	// function_call_output with only "id" (no call_id): responsesCallID is not used
	// for the output (it reads call_id directly), so ToolCallId is empty here.
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "function_call_output", "output": "plain string result"},
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "tool", got.Messages[0].Role)
	assert.Equal(t, "plain string result", got.Messages[0].StringContent())
}

func TestResponsesRequestToChatCompletionsRequestToolsToolChoiceAndTextFormat(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, "hello"),
		Tools: mustRawMessage(t, []map[string]any{
			{
				"type":        "function",
				"name":        "lookup",
				"description": "Lookup data",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"q": map[string]any{"type": "string"}},
				},
			},
			{"type": "web_search_preview"}, // non-function preserved as custom
		}),
		ToolChoice: mustRawMessage(t, map[string]any{"type": "function", "name": "lookup"}),
		Text: mustRawMessage(t, map[string]any{
			"format": map[string]any{"type": "json_schema", "name": "answer", "schema": map[string]any{"type": "object"}, "strict": true},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Tools, 2)
	assert.Equal(t, "function", got.Tools[0].Type)
	assert.Equal(t, "lookup", got.Tools[0].Function.Name)
	assert.Equal(t, "Lookup data", got.Tools[0].Function.Description)
	assert.Equal(t, "object", got.Tools[0].Function.Parameters.(map[string]any)["type"])
	assert.Equal(t, "web_search_preview", got.Tools[1].Type)
	assert.NotEmpty(t, got.Tools[1].Custom)
	assert.Equal(t, map[string]any{
		"type":     "function",
		"function": map[string]any{"name": "lookup"},
	}, got.ToolChoice)
	require.NotNil(t, got.ResponseFormat)
	assert.Equal(t, "json_schema", got.ResponseFormat.Type)
	assert.Equal(t, "answer", gjson.GetBytes(got.ResponseFormat.JsonSchema, "name").String())
	assert.True(t, gjson.GetBytes(got.ResponseFormat.JsonSchema, "strict").Bool())
}

func TestResponsesRequestToChatCompletionsRequestCustomToolCallPreservesRawShape(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "custom_tool_call", "call_id": "call_custom", "name": "apply_patch", "input": "patch body"},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 1)
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, dto.CustomType, toolCalls[0].Type)
	assert.Equal(t, "call_custom", toolCalls[0].ID)
	assert.Equal(t, "apply_patch", toolCalls[0].Function.Name)
	assert.Equal(t, "patch body", toolCalls[0].Function.Arguments)
	assert.Equal(t, "custom_tool_call", gjson.GetBytes(toolCalls[0].Custom, "type").String())
	assert.Equal(t, "patch body", gjson.GetBytes(toolCalls[0].Custom, "input").String())
}

func TestResponsesRequestToChatCompletionsRequestInputUnsupportedType(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte("123"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported responses input type")
}

func TestResponsesRequestToChatCompletionsRequestInputArrayInvalid(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte("[1,2]"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid input array")
}

func TestResponsesRequestToChatCompletionsRequestEmptyInstructionsSkipped(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model:        "gpt-test",
		Instructions: mustRawMessage(t, "   "),
		Input:        mustRawMessage(t, "hi"),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
}

func TestResponsesRequestToChatCompletionsRequestNoInput(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model:        "gpt-test",
		Instructions: mustRawMessage(t, "sys"),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "system", got.Messages[0].Role)
}

func TestResponsesRequestToChatCompletionsRequestRejectsStatefulFields(t *testing.T) {
	tests := []struct {
		name string
		req  *dto.OpenAIResponsesRequest
		want string
	}{
		{"conversation", &dto.OpenAIResponsesRequest{Model: "gpt-test", Conversation: mustRawMessage(t, "conv_1")}, "conversation"},
		{"previous response", &dto.OpenAIResponsesRequest{Model: "gpt-test", PreviousResponseID: "resp_1"}, "previous_response_id"},
		{"prompt", &dto.OpenAIResponsesRequest{Model: "gpt-test", Prompt: mustRawMessage(t, map[string]any{"id": "pmpt_1"})}, "prompt"},
		{"context management", &dto.OpenAIResponsesRequest{Model: "gpt-test", ContextManagement: mustRawMessage(t, map[string]any{"type": "auto"})}, "context_management"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResponsesRequestToChatCompletionsRequest(tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "stateful fields")
		})
	}
}

// ---- targeted unexported-helper branch tests ----

func TestResponsesRequestToolChoiceToChat(t *testing.T) {
	// absent
	got, err := RequestToolChoiceToChat(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	// string
	got, err = RequestToolChoiceToChat(mustRawMessage(t, "auto"))
	require.NoError(t, err)
	assert.Equal(t, "auto", got)

	// function with name -> normalized shape
	got, err = RequestToolChoiceToChat(mustRawMessage(t, map[string]any{"type": "function", "name": "f"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "function", "function": map[string]any{"name": "f"}}, got)

	// function without name -> returned as-is
	got, err = RequestToolChoiceToChat(mustRawMessage(t, map[string]any{"type": "function"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "function"}, got)

	// other object type -> as-is
	got, err = RequestToolChoiceToChat(mustRawMessage(t, map[string]any{"type": "allowed_tools"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "allowed_tools"}, got)

	// invalid string-typed but malformed object
	_, err = RequestToolChoiceToChat([]byte(`{bad`))
	require.Error(t, err)
}

func TestResponsesRequestTextToChatResponseFormat(t *testing.T) {
	// absent
	got, err := RequestTextToChatResponseFormat(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	// invalid json
	_, err = RequestTextToChatResponseFormat([]byte(`{bad`))
	require.Error(t, err)

	// no format key
	got, err = RequestTextToChatResponseFormat(mustRawMessage(t, map[string]any{"verbosity": "low"}))
	require.NoError(t, err)
	assert.Nil(t, got)

	// format type empty
	got, err = RequestTextToChatResponseFormat(mustRawMessage(t, map[string]any{"format": map[string]any{}}))
	require.NoError(t, err)
	assert.Nil(t, got)

	// json_object (non json_schema) -> no JsonSchema
	got, err = RequestTextToChatResponseFormat(mustRawMessage(t, map[string]any{"format": map[string]any{"type": "json_object"}}))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "json_object", got.Type)
	assert.Empty(t, got.JsonSchema)
}

func TestResponsesImagePartToChatImageURL(t *testing.T) {
	// explicit image_url passthrough
	assert.Equal(t, "u", responsesImagePartToChatImageURL(map[string]any{"image_url": "u"}))
	// built from url/file_id/detail
	built := responsesImagePartToChatImageURL(map[string]any{"url": "u", "detail": "high"})
	assert.Equal(t, map[string]any{"url": "u", "detail": "high"}, built)
	// nothing usable -> returns part
	part := map[string]any{"other": "x"}
	assert.Equal(t, part, responsesImagePartToChatImageURL(part))
}

func TestResponsesFilePartToChatFile(t *testing.T) {
	assert.Equal(t, "f", responsesFilePartToChatFile(map[string]any{"file": "f"}))
	built := responsesFilePartToChatFile(map[string]any{"file_id": "id", "filename": "n"})
	assert.Equal(t, map[string]any{"file_id": "id", "filename": "n"}, built)
	part := map[string]any{"other": "x"}
	assert.Equal(t, part, responsesFilePartToChatFile(part))
}

func TestResponsesVideoPartToChatVideoURL(t *testing.T) {
	// video_url map with url
	assert.Equal(t, "u", responsesVideoPartToChatVideoURL(map[string]any{"video_url": map[string]any{"url": "u"}}))
	// video_url present but not a map -> returned as-is
	assert.Equal(t, "str", responsesVideoPartToChatVideoURL(map[string]any{"video_url": "str"}))
	// url top-level
	assert.Equal(t, "top", responsesVideoPartToChatVideoURL(map[string]any{"url": "top"}))
	// payload fallback (strip type)
	out := responsesVideoPartToChatVideoURL(map[string]any{"type": "input_video", "foo": "bar"})
	assert.Equal(t, map[string]any{"foo": "bar"}, out)
}

func TestResponsesPartPayload(t *testing.T) {
	// key present
	assert.Equal(t, "v", responsesPartPayload(map[string]any{"input_audio": "v"}, "input_audio"))
	// key absent -> strips "type"
	assert.Equal(t, map[string]any{"a": "b"}, responsesPartPayload(map[string]any{"type": "x", "a": "b"}, "missing"))
}

func TestResponsesArgumentsString(t *testing.T) {
	assert.Equal(t, "", responsesArgumentsString(nil))
	assert.Equal(t, "s", responsesArgumentsString("s"))
	assert.JSONEq(t, `{"a":1}`, responsesArgumentsString(map[string]any{"a": 1}))
}

func TestResponseToolOutputToChatContent(t *testing.T) {
	assert.Equal(t, "", responseToolOutputToChatContent(nil))
	assert.Equal(t, "s", responseToolOutputToChatContent("s"))
	assert.JSONEq(t, `{"a":1}`, responseToolOutputToChatContent(map[string]any{"a": 1}).(string))
}

func TestResponsesInputContentToChatContentTypes(t *testing.T) {
	// nil -> empty string
	got, err := responsesInputContentToChatContent(nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)

	// string
	got, err = responsesInputContentToChatContent("hi")
	require.NoError(t, err)
	assert.Equal(t, "hi", got)

	// []map[string]any collapse to text
	got, err = responsesInputContentToChatContent([]map[string]any{{"type": "input_text", "text": "x"}})
	require.NoError(t, err)
	assert.Equal(t, "x", got)

	// default type (number) returned as-is
	got, err = responsesInputContentToChatContent(3)
	require.NoError(t, err)
	assert.Equal(t, 3, got)
}

func TestResponsesContentPartsToChatContentNonMapElement(t *testing.T) {
	// a non-map element forces onlyText=false and is preserved
	out, err := responsesContentPartsToChatContent([]any{"raw", map[string]any{"type": "input_text", "text": "t"}})
	require.NoError(t, err)
	parts, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	assert.Equal(t, "raw", parts[0])
}
