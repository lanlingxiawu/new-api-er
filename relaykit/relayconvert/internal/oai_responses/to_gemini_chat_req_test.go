package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesRequestToGeminiChatNilAndModel(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToGeminiChat(c, nil, nil)
	require.Error(t, err)

	_, err = OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestOpenAIResponsesRequestToGeminiChatRejectsStatefulFields(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model:  "gemini-test",
		Prompt: mustRawMessage(t, map[string]any{"id": "p"}),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stateful fields")
}

func TestOpenAIResponsesRequestToGeminiChatBasic(t *testing.T) {
	c := newTestGinContext()
	temp := 0.0
	topP := 0.7
	maxTok := uint(100)
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model:           "gemini-test",
		Temperature:     &temp,
		TopP:            &topP,
		MaxOutputTokens: &maxTok,
		Instructions:    mustRawMessage(t, "sys rules"),
		Input:           mustRawMessage(t, "hello"),
	}, geminiMeta())
	require.NoError(t, err)
	require.NotNil(t, got.GenerationConfig.Temperature)
	assert.Equal(t, 0.0, *got.GenerationConfig.Temperature)
	require.NotNil(t, got.GenerationConfig.TopP)
	assert.Equal(t, 0.7, *got.GenerationConfig.TopP)
	require.NotNil(t, got.GenerationConfig.MaxOutputTokens)
	assert.Equal(t, uint(100), *got.GenerationConfig.MaxOutputTokens)
	// system instructions
	require.NotNil(t, got.SystemInstructions)
	assert.Equal(t, "sys rules", got.SystemInstructions.Parts[0].Text)
	// user content
	require.Len(t, got.Contents, 1)
	assert.Equal(t, "user", got.Contents[0].Role)
	assert.Equal(t, "hello", got.Contents[0].Parts[0].Text)
	// safety settings populated
	assert.NotEmpty(t, got.SafetySettings)
}

func TestOpenAIResponsesRequestToGeminiChatUpstreamModelName(t *testing.T) {
	c := newTestGinContext()
	info := &convmeta.Values{ChannelMetaAttached: true, UpstreamModelName: "gemini-upstream"}
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, "hi"),
	}, info)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestOpenAIResponsesRequestToGeminiChatTextJsonSchema(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, "hi"),
		Text: mustRawMessage(t, map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "answer",
				"schema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			},
		}),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "application/json", got.GenerationConfig.ResponseMimeType)
	assert.NotNil(t, got.GenerationConfig.ResponseSchema)
}

func TestOpenAIResponsesRequestToGeminiChatTextJsonObject(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, "hi"),
		Text:  mustRawMessage(t, map[string]any{"format": map[string]any{"type": "json_object"}}),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "application/json", got.GenerationConfig.ResponseMimeType)
	assert.Nil(t, got.GenerationConfig.ResponseSchema)
}

func TestOpenAIResponsesRequestToGeminiChatFunctionsAndToolChoice(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, "hi"),
		Tools: mustRawMessage(t, []map[string]any{
			// function with real properties -> cleaned parameters kept
			{"type": "function", "name": "lookup", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}},
			}},
			// function with empty properties -> parameters nil
			{"type": "function", "name": "noargs", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{},
			}},
		}),
		ToolChoice: mustRawMessage(t, "auto"),
	}, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, got.Tools)
	assert.NotNil(t, got.ToolConfig)
}

func TestOpenAIResponsesRequestToGeminiChatFunctionCallAndOutput(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "function_call", "call_id": "call_1", "name": "f1", "arguments": map[string]any{"a": 1}},
			{"type": "function_call", "call_id": "call_2", "name": "f2", "arguments": map[string]any{"b": 2}},
			{"type": "function_call_output", "call_id": "call_1", "output": map[string]any{"r": "ok"}},
			{"type": "function_call_output", "call_id": "call_2", "output": map[string]any{"r": "ok2"}},
		}),
	}, nil)
	require.NoError(t, err)
	// one model content with two function-call parts, one user content with two responses
	require.Len(t, got.Contents, 2)
	assert.Equal(t, "model", got.Contents[0].Role)
	require.Len(t, got.Contents[0].Parts, 2)
	assert.NotNil(t, got.Contents[0].Parts[0].FunctionCall)
	assert.Equal(t, "user", got.Contents[1].Role)
	require.Len(t, got.Contents[1].Parts, 2)
	require.NotNil(t, got.Contents[1].Parts[0].FunctionResponse)
	// name resolved (either from item or callNames map)
	assert.Equal(t, "f1", got.Contents[1].Parts[0].FunctionResponse.Name)
}

func TestOpenAIResponsesRequestToGeminiChatFunctionCallMissingName(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{{"type": "function_call", "call_id": "call_1"}}),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing name")
}

func TestOpenAIResponsesRequestToGeminiChatSystemAndRoles(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "system", "content": "sys from input"},
			{"role": "assistant", "content": "prior"},
			{"role": "user", "content": "now"},
		}),
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, got.SystemInstructions)
	assert.Contains(t, got.SystemInstructions.Parts[0].Text, "sys from input")
	// assistant -> model, user -> user
	roles := []string{}
	for _, ct := range got.Contents {
		roles = append(roles, ct.Role)
	}
	assert.Equal(t, []string{"model", "user"}, roles)
}

func TestOpenAIResponsesRequestToGeminiChatMediaSupported(t *testing.T) {
	c := newTestGinContext()
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "look"},
				{"type": "input_image", "image_url": "https://x/png.png"},
			}},
		}),
	}, nil)
	require.NoError(t, err)
	require.Len(t, got.Contents, 1)
	require.Len(t, got.Contents[0].Parts, 2)
	require.NotNil(t, got.Contents[0].Parts[1].InlineData)
	assert.Equal(t, "image/png", got.Contents[0].Parts[1].InlineData.MimeType)
}

func TestOpenAIResponsesRequestToGeminiChatMediaUnsupported(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_image", "image_url": "https://x/unsupported.bin"},
			}},
		}),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported by Gemini")
}

func TestOpenAIResponsesRequestToGeminiChatMediaResolverError(t *testing.T) {
	c := newTestGinContext()
	_, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_image", "image_url": "https://x/trigger-error.png"},
			}},
		}),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get file data")
}

// ---- convert wrapper + preprocess ----

func TestConvertOpenAIResponsesRequestToGeminiChatWrapper(t *testing.T) {
	c := newTestGinContext()
	out, err := convertOpenAIResponsesRequestToGeminiChat(c, nil, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, "hi"),
	})
	require.NoError(t, err)
	_, ok := out.(*dto.GeminiChatRequest)
	assert.True(t, ok)

	_, err = convertOpenAIResponsesRequestToGeminiChat(c, nil, 12345)
	require.Error(t, err)

	// PrepareOpenAIResponsesRequest fails (tools array of non-objects) -> wrapper propagates
	_, err = convertOpenAIResponsesRequestToGeminiChat(c, nil, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Tools: []byte(`[1,2,3]`),
	})
	require.Error(t, err)
}

func TestOpenAIResponsesRequestToGeminiChatMediaNilSourceSkipped(t *testing.T) {
	c := newTestGinContext()
	// input_image without data yields nil FileSource -> part skipped, text kept
	got, err := OpenAIResponsesRequestToGeminiChat(c, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "input_text", "text": "keep"},
				{"type": "input_image"},
			}},
		}),
	}, nil)
	require.NoError(t, err)
	require.Len(t, got.Contents, 1)
	require.Len(t, got.Contents[0].Parts, 1)
	assert.Equal(t, "keep", got.Contents[0].Parts[0].Text)
}

func TestPrepareOpenAIResponsesRequestFiltersToolsAndInput(t *testing.T) {
	req := dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Tools: mustRawMessage(t, []map[string]any{
			{"type": "function", "name": "keep"},
			{"type": "web_search_preview"},
		}),
		Input: mustRawMessage(t, []map[string]any{
			{"type": "custom_tool_call", "call_id": "cc_1", "name": "x", "input": "b"},
			{"type": "custom_tool_call_output", "call_id": "cc_1", "output": "r"},
			{"type": "function_call_output", "call_id": "cc_1", "output": "r"},  // linked to skipped custom -> removed
			{"type": "function_call_output", "call_id": "other", "output": "r"}, // kept
			{"role": "user", "content": "keep me"},
		}),
	}
	out, err := PrepareOpenAIResponsesRequest(req)
	require.NoError(t, err)

	var tools []map[string]any
	require.NoError(t, kitutil.Unmarshal(out.Tools, &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "keep", tools[0]["name"])

	var items []map[string]any
	require.NoError(t, kitutil.Unmarshal(out.Input, &items))
	// remaining: function_call_output(other) + user message
	require.Len(t, items, 2)
	assert.Equal(t, "other", items[0]["call_id"])
	assert.Equal(t, "user", items[1]["role"])
}

func TestPrepareOpenAIResponsesRequestNonArrayPassthrough(t *testing.T) {
	req := dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Tools: mustRawMessage(t, "notarray"),
		Input: mustRawMessage(t, "just a string"),
	}
	out, err := PrepareOpenAIResponsesRequest(req)
	require.NoError(t, err)
	assert.Equal(t, req.Tools, out.Tools)
	assert.Equal(t, req.Input, out.Input)
}

func TestPrepareOpenAIResponsesRequestToolsUnmarshalError(t *testing.T) {
	// array whose elements are not objects -> unmarshal into []map[string]any fails
	req := dto.OpenAIResponsesRequest{Model: "gemini-test", Tools: []byte(`[1,2,3]`)}
	_, err := PrepareOpenAIResponsesRequest(req)
	require.Error(t, err)
}

func TestPrepareOpenAIResponsesRequestInputUnmarshalError(t *testing.T) {
	req := dto.OpenAIResponsesRequest{Model: "gemini-test", Input: []byte(`[1,2,3]`)}
	_, err := PrepareOpenAIResponsesRequest(req)
	require.Error(t, err)
}

func TestPrepareOpenAIResponsesRequestAllToolsFilteredToNil(t *testing.T) {
	req := dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Tools: mustRawMessage(t, []map[string]any{{"type": "web_search_preview"}}),
	}
	out, err := PrepareOpenAIResponsesRequest(req)
	require.NoError(t, err)
	assert.Nil(t, out.Tools)
}

// ---- helper branch tests ----

func TestResponsesGeminiRole(t *testing.T) {
	assert.Equal(t, "model", responsesGeminiRole(map[string]any{"role": "assistant"}))
	assert.Equal(t, "model", responsesGeminiRole(map[string]any{"role": "model"}))
	assert.Equal(t, "system", responsesGeminiRole(map[string]any{"role": "system"}))
	assert.Equal(t, "system", responsesGeminiRole(map[string]any{"role": "developer"}))
	assert.Equal(t, "user", responsesGeminiRole(map[string]any{"role": "user"}))
	assert.Equal(t, "user", responsesGeminiRole(map[string]any{}))
}

func TestResponsesContentPartToGeminiPartsEmptyAndUnknown(t *testing.T) {
	c := newTestGinContext()
	// empty text -> nil
	parts, err := responsesContentPartToGeminiParts(c, map[string]any{"type": "input_text", "text": ""})
	require.NoError(t, err)
	assert.Nil(t, parts)

	// unknown type -> nil
	parts, err = responsesContentPartToGeminiParts(c, map[string]any{"type": "mystery"})
	require.NoError(t, err)
	assert.Nil(t, parts)

	// media source that resolves to empty -> nil (no data)
	parts, err = responsesContentPartToGeminiParts(c, map[string]any{"type": "input_image"})
	require.NoError(t, err)
	assert.Nil(t, parts)
}

func TestAppendGeminiContentPartNewRole(t *testing.T) {
	req := &dto.GeminiChatRequest{}
	appendGeminiContentPart(req, "user", dto.GeminiPart{Text: "a"})
	appendGeminiContentPart(req, "model", dto.GeminiPart{Text: "b"})
	require.Len(t, req.Contents, 2)
	assert.Equal(t, "user", req.Contents[0].Role)
	assert.Equal(t, "model", req.Contents[1].Role)
}

func TestAppendGeminiContentPartSameRoleAppendNonFunctionCall(t *testing.T) {
	req := &dto.GeminiChatRequest{}
	appendGeminiContentPart(req, "user", dto.GeminiPart{Text: "a"})
	appendGeminiContentPart(req, "user", dto.GeminiPart{Text: "b"})
	require.Len(t, req.Contents, 1)
	require.Len(t, req.Contents[0].Parts, 2)
}

func TestAppendGeminiContentPartFunctionCallInsertOrdering(t *testing.T) {
	req := &dto.GeminiChatRequest{}
	// existing model content that already has a text part after function calls
	appendGeminiContentPart(req, "model", dto.GeminiPart{FunctionCall: &dto.FunctionCall{FunctionName: "f1"}})
	// append a non-function text so insertion has to skip existing function-call prefix
	req.Contents[0].Parts = append(req.Contents[0].Parts, dto.GeminiPart{Text: "trailing"})
	// second function call inserted before the trailing text
	appendGeminiContentPart(req, "model", dto.GeminiPart{FunctionCall: &dto.FunctionCall{FunctionName: "f2"}})
	require.Len(t, req.Contents, 1)
	parts := req.Contents[0].Parts
	require.Len(t, parts, 3)
	assert.Equal(t, "f1", parts[0].FunctionCall.FunctionName)
	assert.Equal(t, "f2", parts[1].FunctionCall.FunctionName)
	assert.Equal(t, "trailing", parts[2].Text)
}

func TestResponsesFunctionOutputItemToGeminiPartNameFromCallNames(t *testing.T) {
	callNames := map[string]string{"call_1": "resolved"}
	part := responsesFunctionOutputItemToGeminiPart(map[string]any{"call_id": "call_1", "output": "r"}, callNames)
	require.NotNil(t, part.FunctionResponse)
	assert.Equal(t, "resolved", part.FunctionResponse.Name)

	// explicit name wins
	part = responsesFunctionOutputItemToGeminiPart(map[string]any{"call_id": "call_1", "name": "explicit", "output": "r"}, callNames)
	assert.Equal(t, "explicit", part.FunctionResponse.Name)
}

func TestApplyResponsesTextToGeminiNoFormat(t *testing.T) {
	req := &dto.GeminiChatRequest{}
	// absent -> no-op
	require.NoError(t, applyResponsesTextToGemini(nil, req))
	assert.Empty(t, req.GenerationConfig.ResponseMimeType)

	// text format type is neither json_schema nor json_object -> no-op
	require.NoError(t, applyResponsesTextToGemini(mustRawMessage(t, map[string]any{"format": map[string]any{"type": "text"}}), req))
	assert.Empty(t, req.GenerationConfig.ResponseMimeType)
}
