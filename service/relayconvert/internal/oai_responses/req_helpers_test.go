package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesRequestFromAny(t *testing.T) {
	t.Run("pointer input", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{Model: "m"}
		got, err := OpenAIResponsesRequestFromAny(req)
		require.NoError(t, err)
		assert.Same(t, req, got)
	})
	t.Run("value input", func(t *testing.T) {
		got, err := OpenAIResponsesRequestFromAny(dto.OpenAIResponsesRequest{Model: "m"})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "m", got.Model)
	})
	t.Run("wrong type", func(t *testing.T) {
		_, err := OpenAIResponsesRequestFromAny("not a request")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected OpenAI responses request")
	})
	t.Run("typed nil pointer", func(t *testing.T) {
		var typedNil *dto.OpenAIResponsesRequest
		_, err := OpenAIResponsesRequestFromAny(typedNil)
		require.Error(t, err)
	})
}

func TestInputItems(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		items, err := InputItems(nil)
		require.NoError(t, err)
		assert.Nil(t, items)
	})
	t.Run("null", func(t *testing.T) {
		items, err := InputItems([]byte("null"))
		require.NoError(t, err)
		assert.Nil(t, items)
	})
	t.Run("string becomes user message", func(t *testing.T) {
		items, err := InputItems(mustRawMessage(t, "hello"))
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "user", items[0]["role"])
		assert.Equal(t, "hello", items[0]["content"])
	})
	t.Run("array passes through", func(t *testing.T) {
		items, err := InputItems(mustRawMessage(t, []map[string]any{{"role": "user"}}))
		require.NoError(t, err)
		require.Len(t, items, 1)
	})
	t.Run("unsupported type", func(t *testing.T) {
		_, err := InputItems([]byte("123"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported responses input type")
	})
	t.Run("invalid array elements", func(t *testing.T) {
		_, err := InputItems([]byte("[1,2,3]"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid input array")
	})
}

func TestContentParts(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		parts, err := ContentParts(nil)
		require.NoError(t, err)
		assert.Nil(t, parts)
	})
	t.Run("string", func(t *testing.T) {
		parts, err := ContentParts("hi")
		require.NoError(t, err)
		require.Len(t, parts, 1)
		assert.Equal(t, "input_text", parts[0]["type"])
		assert.Equal(t, "hi", parts[0]["text"])
	})
	t.Run("slice of maps passthrough", func(t *testing.T) {
		in := []map[string]any{{"type": "input_text", "text": "a"}}
		parts, err := ContentParts(in)
		require.NoError(t, err)
		assert.Equal(t, in, parts)
	})
	t.Run("slice of any mixed", func(t *testing.T) {
		parts, err := ContentParts([]any{
			"raw string",
			map[string]any{"type": "input_image"},
			42,
		})
		require.NoError(t, err)
		require.Len(t, parts, 3)
		assert.Equal(t, "raw string", parts[0]["text"])
		assert.Equal(t, "input_image", parts[1]["type"])
		assert.Equal(t, "42", parts[2]["text"])
	})
	t.Run("default marshals", func(t *testing.T) {
		parts, err := ContentParts(map[string]int{"a": 1})
		require.NoError(t, err)
		require.Len(t, parts, 1)
		assert.JSONEq(t, `{"a":1}`, parts[0]["text"].(string))
	})
}

func TestRequestFunctionDeclarations(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		fns, err := RequestFunctionDeclarations(nil)
		require.NoError(t, err)
		assert.Nil(t, fns)
	})
	t.Run("invalid", func(t *testing.T) {
		_, err := RequestFunctionDeclarations([]byte(`{"not":"array"}`))
		require.Error(t, err)
	})
	t.Run("filters non-function and empty name", func(t *testing.T) {
		fns, err := RequestFunctionDeclarations(mustRawMessage(t, []map[string]any{
			{"type": "function", "name": "keep", "description": "d", "parameters": map[string]any{"type": "object"}},
			{"type": "web_search"},        // wrong type, skipped
			{"type": "function", "name": ""}, // empty name, skipped
		}))
		require.NoError(t, err)
		require.Len(t, fns, 1)
		assert.Equal(t, "keep", fns[0].Name)
		assert.Equal(t, "d", fns[0].Description)
	})
}

func TestReasoningEffort(t *testing.T) {
	assert.Equal(t, "", ReasoningEffort(nil))
	assert.Equal(t, "", ReasoningEffort(&dto.OpenAIResponsesRequest{}))
	assert.Equal(t, "high", ReasoningEffort(&dto.OpenAIResponsesRequest{Reasoning: &dto.Reasoning{Effort: "high"}}))
}

func TestObjectValue(t *testing.T) {
	assert.Equal(t, map[string]any{}, ObjectValue(nil, "k"))
	assert.Equal(t, map[string]any{"a": 1.0}, ObjectValue(map[string]any{"a": 1.0}, "k"))
	assert.Equal(t, map[string]any{"a": "b"}, ObjectValue(`{"a":"b"}`, "k"))
	// valid JSON array string -> fallbackKey
	assert.Equal(t, map[string]any{"k": []any{1.0, 2.0}}, ObjectValue(`[1,2]`, "k"))
	// invalid JSON string -> fallbackKey: raw string
	assert.Equal(t, map[string]any{"k": "notjson"}, ObjectValue("notjson", "k"))
	// []any -> fallbackKey
	assert.Equal(t, map[string]any{"k": []any{"x"}}, ObjectValue([]any{"x"}, "k"))
	// default (other type)
	assert.Equal(t, map[string]any{"k": 7}, ObjectValue(7, "k"))
}

func TestGeminiResponseMap(t *testing.T) {
	assert.Equal(t, map[string]interface{}{}, GeminiResponseMap(nil))
	assert.Equal(t, map[string]interface{}{"a": 1.0}, GeminiResponseMap(map[string]any{"a": 1.0}))
	assert.Equal(t, map[string]interface{}{"a": "b"}, GeminiResponseMap(`{"a":"b"}`))
	assert.Equal(t, map[string]interface{}{"result": []interface{}{1.0}}, GeminiResponseMap(`[1]`))
	assert.Equal(t, map[string]interface{}{"content": "notjson"}, GeminiResponseMap("notjson"))
	assert.Equal(t, map[string]interface{}{"result": []any{"x"}}, GeminiResponseMap([]any{"x"}))
	assert.Equal(t, map[string]interface{}{"content": 9}, GeminiResponseMap(9))
}

func TestParallelToolCalls(t *testing.T) {
	assert.Nil(t, ParallelToolCalls(nil))
	assert.Nil(t, ParallelToolCalls([]byte(`"yes"`))) // non-boolean
	tru := ParallelToolCalls([]byte("true"))
	require.NotNil(t, tru)
	assert.True(t, *tru)
	fal := ParallelToolCalls([]byte("false"))
	require.NotNil(t, fal)
	assert.False(t, *fal)
}

func TestContentPartToFileSource(t *testing.T) {
	t.Run("input_image url", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{"type": "input_image", "image_url": "https://x/png.png"})
		require.NotNil(t, src)
		assert.Equal(t, "https://x/png.png", src.GetRawData())
	})
	t.Run("input_image nested object with mime", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{
			"type":      "input_image",
			"image_url": map[string]any{"url": "https://x/a.png", "mime_type": "image/png"},
		})
		require.NotNil(t, src)
		assert.Equal(t, "https://x/a.png", src.GetRawData())
	})
	t.Run("input_file file_data", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{"type": "input_file", "file": map[string]any{"file_data": "ZmY="}})
		require.NotNil(t, src)
	})
	t.Run("input_audio derives mime from format", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{
			"type":        "input_audio",
			"input_audio": map[string]any{"format": "wav"},
		})
		// no data value -> nil returned
		assert.Nil(t, src)
	})
	t.Run("input_audio with data and format mime", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{
			"type":        "input_audio",
			"input_audio": map[string]any{"data": "YWJj", "format": "wav"},
		})
		require.NotNil(t, src)
		assert.Equal(t, "YWJj", src.GetRawData())
	})
	t.Run("input_video url", func(t *testing.T) {
		src := ContentPartToFileSource(map[string]any{"type": "input_video", "video_url": "https://x/v.mp4"})
		require.NotNil(t, src)
	})
	t.Run("unknown type -> nil", func(t *testing.T) {
		assert.Nil(t, ContentPartToFileSource(map[string]any{"type": "input_text"}))
	})
	t.Run("empty data -> nil", func(t *testing.T) {
		assert.Nil(t, ContentPartToFileSource(map[string]any{"type": "input_image"}))
	})
}

func TestResponsesPartDataAndMimeMissingAndEmptyKeys(t *testing.T) {
	// mime_type provided at part level; first key missing, second key empty string, third key valid.
	data, mime := responsesPartDataAndMime(map[string]any{
		"mime_type": "image/png",
		"url":       "", // empty -> skipped
		"file_data": "ZGF0YQ==",
	}, "missing_key", "url", "file_data")
	assert.Equal(t, "ZGF0YQ==", data)
	assert.Equal(t, "image/png", mime)
}

func TestJSONString(t *testing.T) {
	s, err := JSONString([]byte(`"quoted"`))
	require.NoError(t, err)
	assert.Equal(t, "quoted", s)

	// non-string returns raw bytes as-is
	s, err = JSONString([]byte(`{"a":1}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, s)
}

func TestRawJSONPresent(t *testing.T) {
	assert.False(t, RawJSONPresent(nil))
	assert.False(t, RawJSONPresent([]byte("")))
	assert.False(t, RawJSONPresent([]byte("null")))
	assert.True(t, RawJSONPresent([]byte(`"x"`)))
}

func TestCallID(t *testing.T) {
	assert.Equal(t, "cid", CallID(map[string]any{"call_id": "cid", "id": "iid"}))
	assert.Equal(t, "iid", CallID(map[string]any{"id": "iid"}))
	assert.Equal(t, "", CallID(map[string]any{}))
}
