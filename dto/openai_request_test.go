package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Rule 5: pointer / explicit-zero-value semantics for GeneralOpenAIRequest
// ---------------------------------------------------------------------------
//
// For each optional scalar declared as a pointer with omitempty the contract is:
//   - field absent in client JSON      => nil       => omitted on marshal
//   - field explicitly set to zero/false => non-nil  => still sent upstream
//
// We test both unmarshal (JSON -> struct) and marshal (struct -> JSON) directions.

func TestGeneralOpenAIRequest_Unmarshal_AbsentOptionalScalarsAreNil(t *testing.T) {
	var r GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"gpt-4.1"}`), &r))

	assert.Nil(t, r.Stream)
	assert.Nil(t, r.MaxTokens)
	assert.Nil(t, r.MaxCompletionTokens)
	assert.Nil(t, r.Temperature)
	assert.Nil(t, r.TopP)
	assert.Nil(t, r.TopK)
	assert.Nil(t, r.N)
	assert.Nil(t, r.FrequencyPenalty)
	assert.Nil(t, r.PresencePenalty)
	assert.Nil(t, r.Seed)
	assert.Nil(t, r.ParallelTooCalls)
	assert.Nil(t, r.LogProbs)
	assert.Nil(t, r.TopLogProbs)
	assert.Nil(t, r.Dimensions)
	assert.Nil(t, r.ReturnImages)
	assert.Nil(t, r.ReturnRelatedQuestions)
}

func TestGeneralOpenAIRequest_Unmarshal_ExplicitZeroValuesAreNonNil(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1",
		"stream":false,
		"max_tokens":0,
		"max_completion_tokens":0,
		"temperature":0,
		"top_p":0,
		"top_k":0,
		"n":0,
		"frequency_penalty":0,
		"presence_penalty":0,
		"seed":0,
		"parallel_tool_calls":false,
		"logprobs":false,
		"top_logprobs":0,
		"dimensions":0,
		"return_images":false,
		"return_related_questions":false
	}`)
	var r GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(raw, &r))

	require.NotNil(t, r.Stream)
	assert.False(t, *r.Stream)
	require.NotNil(t, r.MaxTokens)
	assert.Equal(t, uint(0), *r.MaxTokens)
	require.NotNil(t, r.MaxCompletionTokens)
	assert.Equal(t, uint(0), *r.MaxCompletionTokens)
	require.NotNil(t, r.Temperature)
	assert.Equal(t, 0.0, *r.Temperature)
	require.NotNil(t, r.TopP)
	assert.Equal(t, 0.0, *r.TopP)
	require.NotNil(t, r.TopK)
	assert.Equal(t, 0, *r.TopK)
	require.NotNil(t, r.N)
	assert.Equal(t, 0, *r.N)
	require.NotNil(t, r.FrequencyPenalty)
	require.NotNil(t, r.PresencePenalty)
	require.NotNil(t, r.Seed)
	require.NotNil(t, r.ParallelTooCalls)
	assert.False(t, *r.ParallelTooCalls)
	require.NotNil(t, r.LogProbs)
	require.NotNil(t, r.TopLogProbs)
	require.NotNil(t, r.Dimensions)
	require.NotNil(t, r.ReturnImages)
	require.NotNil(t, r.ReturnRelatedQuestions)
}

func TestGeneralOpenAIRequest_Marshal_NilOptionalScalarsAreOmitted(t *testing.T) {
	r := GeneralOpenAIRequest{Model: "gpt-4.1"}
	data, err := common.Marshal(&r)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(data, &m))

	for _, key := range []string{
		"stream", "max_tokens", "max_completion_tokens", "temperature", "top_p",
		"top_k", "n", "frequency_penalty", "presence_penalty", "seed",
		"parallel_tool_calls", "logprobs", "top_logprobs", "dimensions",
		"return_images", "return_related_questions",
	} {
		_, present := m[key]
		assert.Falsef(t, present, "expected key %q to be omitted when nil", key)
	}
}

func TestGeneralOpenAIRequest_Marshal_ExplicitZeroValuesArePresent(t *testing.T) {
	zeroBool := false
	zeroUint := uint(0)
	zeroFloat := 0.0
	zeroInt := 0
	r := GeneralOpenAIRequest{
		Model:            "gpt-4.1",
		Stream:           &zeroBool,
		MaxTokens:        &zeroUint,
		Temperature:      &zeroFloat,
		TopP:             &zeroFloat,
		TopK:             &zeroInt,
		N:                &zeroInt,
		FrequencyPenalty: &zeroFloat,
		ParallelTooCalls: &zeroBool,
	}
	data, err := common.Marshal(&r)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(data, &m))

	assert.JSONEq(t, "false", string(m["stream"]))
	assert.JSONEq(t, "0", string(m["max_tokens"]))
	assert.JSONEq(t, "0", string(m["temperature"]))
	assert.JSONEq(t, "0", string(m["top_p"]))
	assert.JSONEq(t, "0", string(m["top_k"]))
	assert.JSONEq(t, "0", string(m["n"]))
	assert.JSONEq(t, "0", string(m["frequency_penalty"]))
	assert.JSONEq(t, "false", string(m["parallel_tool_calls"]))
}

func TestGeneralOpenAIRequest_RoundTrip_PreservesExplicitZero(t *testing.T) {
	raw := `{"model":"gpt-4.1","stream":false,"temperature":0,"max_tokens":0}`
	var r GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(raw), &r))
	out, err := common.Marshal(&r)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(out, &m))
	assert.JSONEq(t, "false", string(m["stream"]))
	assert.JSONEq(t, "0", string(m["temperature"]))
	assert.JSONEq(t, "0", string(m["max_tokens"]))
}

// ---------------------------------------------------------------------------
// GeneralOpenAIRequest helper methods
// ---------------------------------------------------------------------------

func TestGeneralOpenAIRequest_IsStream(t *testing.T) {
	tr := true
	fa := false
	assert.False(t, (&GeneralOpenAIRequest{}).IsStream(nil), "nil pointer => false")
	assert.True(t, (&GeneralOpenAIRequest{Stream: &tr}).IsStream(nil))
	assert.False(t, (&GeneralOpenAIRequest{Stream: &fa}).IsStream(nil))
}

func TestGeneralOpenAIRequest_SetModelName(t *testing.T) {
	r := &GeneralOpenAIRequest{Model: "orig"}
	r.SetModelName("")
	assert.Equal(t, "orig", r.Model, "empty name is ignored")
	r.SetModelName("new")
	assert.Equal(t, "new", r.Model)
}

func TestGeneralOpenAIRequest_GetMaxTokens(t *testing.T) {
	mk := func(v uint) *uint { return &v }
	// both nil => 0
	assert.Equal(t, uint(0), (&GeneralOpenAIRequest{}).GetMaxTokens())
	// max_completion_tokens non-zero wins
	assert.Equal(t, uint(5), (&GeneralOpenAIRequest{MaxCompletionTokens: mk(5), MaxTokens: mk(9)}).GetMaxTokens())
	// max_completion_tokens zero => fall back to max_tokens
	assert.Equal(t, uint(9), (&GeneralOpenAIRequest{MaxCompletionTokens: mk(0), MaxTokens: mk(9)}).GetMaxTokens())
	// only max_tokens
	assert.Equal(t, uint(7), (&GeneralOpenAIRequest{MaxTokens: mk(7)}).GetMaxTokens())
}

func TestGeneralOpenAIRequest_GetSystemRoleName(t *testing.T) {
	cases := map[string]string{
		"o1":          "developer",
		"o3-mini":     "developer",
		"o4-preview":  "developer",
		"o1-mini":     "system", // explicitly excluded
		"o1-preview":  "system", // explicitly excluded
		"gpt-5":       "developer",
		"gpt-5-turbo": "developer",
		"gpt-4.1":     "system",
		"":            "system",
	}
	for model, want := range cases {
		got := (&GeneralOpenAIRequest{Model: model}).GetSystemRoleName()
		assert.Equalf(t, want, got, "model=%q", model)
	}
}

func TestIsOpenAIReasoningOModel(t *testing.T) {
	assert.True(t, IsOpenAIReasoningOModel("o1"))
	assert.True(t, IsOpenAIReasoningOModel("o3-mini"))
	assert.True(t, IsOpenAIReasoningOModel("o4"))
	assert.False(t, IsOpenAIReasoningOModel("gpt-4"))
	assert.False(t, IsOpenAIReasoningOModel("o2"))
	assert.False(t, IsOpenAIReasoningOModel(""))
}

func TestIsOpenAIGPT5Model(t *testing.T) {
	assert.True(t, IsOpenAIGPT5Model("gpt-5"))
	assert.True(t, IsOpenAIGPT5Model("gpt-5-mini"))
	assert.False(t, IsOpenAIGPT5Model("gpt-4"))
	assert.False(t, IsOpenAIGPT5Model(""))
}

func TestGeneralOpenAIRequest_ParseInput(t *testing.T) {
	// nil input
	assert.Nil(t, (&GeneralOpenAIRequest{}).ParseInput())
	// string input
	assert.Equal(t, []string{"hello"}, (&GeneralOpenAIRequest{Input: "hello"}).ParseInput())
	// array of strings (mixed with non-strings, which are skipped)
	got := (&GeneralOpenAIRequest{Input: []any{"a", 1, "b"}}).ParseInput()
	assert.Equal(t, []string{"a", "b"}, got)
	// array empty
	assert.Equal(t, []string{}, (&GeneralOpenAIRequest{Input: []any{}}).ParseInput())
	// unsupported type (number) => nil
	assert.Nil(t, (&GeneralOpenAIRequest{Input: 42}).ParseInput())
}

func TestGeneralOpenAIRequest_ToMap(t *testing.T) {
	tr := true
	r := &GeneralOpenAIRequest{Model: "gpt-4.1", Stream: &tr}
	m := r.ToMap()
	assert.Equal(t, "gpt-4.1", m["model"])
	assert.Equal(t, true, m["stream"])
	_, hasTemp := m["temperature"]
	assert.False(t, hasTemp, "nil optional omitted from map")
}

func TestGeneralOpenAIRequest_GetTokenCountMeta_PromptForms(t *testing.T) {
	// string prompt
	meta := (&GeneralOpenAIRequest{Prompt: "hi"}).GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "hi")
	// []any prompt with strings + non-strings
	meta = (&GeneralOpenAIRequest{Prompt: []any{"a", 5}}).GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "a")
	// arbitrary (non-string, non-slice) prompt -> fmt fallback
	meta = (&GeneralOpenAIRequest{Prompt: 123}).GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "123")
}

func TestGeneralOpenAIRequest_GetTokenCountMeta_MaxTokensSelection(t *testing.T) {
	mk := func(v uint) *uint { return &v }
	// completion tokens larger => used
	meta := (&GeneralOpenAIRequest{MaxCompletionTokens: mk(10), MaxTokens: mk(4)}).GetTokenCountMeta()
	assert.Equal(t, 10, meta.MaxTokens)
	// max tokens larger => used
	meta = (&GeneralOpenAIRequest{MaxCompletionTokens: mk(2), MaxTokens: mk(8)}).GetTokenCountMeta()
	assert.Equal(t, 8, meta.MaxTokens)
}

func TestGeneralOpenAIRequest_GetTokenCountMeta_MessagesAndMedia(t *testing.T) {
	name := "alice"
	r := &GeneralOpenAIRequest{
		Messages: []Message{
			{
				Role: "user",
				Name: &name,
				Content: []any{
					map[string]any{"type": "text", "text": "describe"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png", "detail": "low"}},
					map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "AAAA", "format": "wav"}},
					map[string]any{"type": "file", "file": map[string]any{"filename": "f.pdf", "file_data": "ZZZZ"}},
					map[string]any{"type": "video_url", "video_url": "https://example.com/v.mp4"},
				},
			},
		},
		Tools: []ToolCallRequest{
			{Type: "function", Function: FunctionRequest{Name: "getW", Description: "desc", Parameters: map[string]any{"k": "v"}}},
		},
	}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, 1, meta.MessagesCount)
	assert.Equal(t, 1, meta.NameCount)
	assert.Equal(t, 1, meta.ToolsCount)
	assert.Contains(t, meta.CombineText, "user")
	assert.Contains(t, meta.CombineText, "alice")
	assert.Contains(t, meta.CombineText, "describe")
	assert.Contains(t, meta.CombineText, "getW")
	assert.Contains(t, meta.CombineText, "desc")
	// image + audio + file + video => 4 file metas
	assert.Len(t, meta.Files, 4)
	// image detail carried through
	var image *types.FileMeta
	for _, f := range meta.Files {
		if f.FileType == types.FileTypeImage {
			image = f
		}
	}
	require.NotNil(t, image)
	assert.Equal(t, "low", image.Detail)
}

func TestGeneralOpenAIRequest_GetTokenCountMeta_NilMessageContentSkipped(t *testing.T) {
	r := &GeneralOpenAIRequest{Messages: []Message{{Role: "system", Content: nil}}}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, 1, meta.MessagesCount)
	assert.Equal(t, 0, meta.NameCount)
	assert.Empty(t, meta.Files)
}

// ---------------------------------------------------------------------------
// Message helper methods
// ---------------------------------------------------------------------------

func TestMessage_GetReasoningContent(t *testing.T) {
	rc := "reason-content"
	rs := "reason-short"
	assert.Equal(t, "", (&Message{}).GetReasoningContent(), "both nil => empty")
	assert.Equal(t, rc, (&Message{ReasoningContent: &rc}).GetReasoningContent())
	assert.Equal(t, rs, (&Message{Reasoning: &rs}).GetReasoningContent(), "reasoning fallback")
	// ReasoningContent takes precedence over Reasoning
	assert.Equal(t, rc, (&Message{ReasoningContent: &rc, Reasoning: &rs}).GetReasoningContent())
}

func TestMessage_GetSetPrefix(t *testing.T) {
	m := &Message{}
	assert.False(t, m.GetPrefix(), "nil => false")
	m.SetPrefix(true)
	require.NotNil(t, m.Prefix)
	assert.True(t, m.GetPrefix())
	m.SetPrefix(false)
	assert.False(t, m.GetPrefix())
}

func TestMessage_ParseToolCalls(t *testing.T) {
	// nil => nil
	assert.Nil(t, (&Message{}).ParseToolCalls())
	// valid JSON array
	m := &Message{ToolCalls: json.RawMessage(`[{"id":"1","type":"function","function":{"name":"f"}}]`)}
	tc := m.ParseToolCalls()
	require.Len(t, tc, 1)
	assert.Equal(t, "1", tc[0].ID)
	assert.Equal(t, "f", tc[0].Function.Name)
	// invalid JSON => nil (error swallowed)
	assert.Nil(t, (&Message{ToolCalls: json.RawMessage(`not-json`)}).ParseToolCalls())
}

func TestMessage_SetToolCalls(t *testing.T) {
	m := &Message{}
	m.SetToolCalls([]ToolCallRequest{{ID: "x", Type: "function"}})
	require.NotNil(t, m.ToolCalls)
	tc := m.ParseToolCalls()
	require.Len(t, tc, 1)
	assert.Equal(t, "x", tc[0].ID)
}

func TestMessage_StringContent(t *testing.T) {
	// plain string
	assert.Equal(t, "hi", (&Message{Content: "hi"}).StringContent())
	// array of text parts concatenated
	m := &Message{Content: []any{
		map[string]any{"type": "text", "text": "foo"},
		map[string]any{"type": "text", "text": "bar"},
		map[string]any{"type": "image_url", "image_url": "x"}, // ignored
		"not-a-map", // ignored
	}}
	assert.Equal(t, "foobar", m.StringContent())
	// nil / unsupported type => ""
	assert.Equal(t, "", (&Message{Content: nil}).StringContent())
	assert.Equal(t, "", (&Message{Content: 5}).StringContent())
}

func TestMessage_IsStringContent(t *testing.T) {
	assert.True(t, (&Message{Content: "s"}).IsStringContent())
	assert.False(t, (&Message{Content: []any{}}).IsStringContent())
	assert.False(t, (&Message{Content: nil}).IsStringContent())
}

func TestMessage_SetContentHelpers(t *testing.T) {
	m := &Message{}
	m.SetStringContent("abc")
	assert.Equal(t, "abc", m.Content)
	assert.Nil(t, m.parsedContent)

	media := []MediaContent{{Type: ContentTypeText, Text: "t"}}
	m.SetMediaContent(media)
	assert.Equal(t, media, m.Content)
	assert.Equal(t, media, m.parsedContent)

	m.SetNullContent()
	assert.Nil(t, m.Content)
	assert.Nil(t, m.parsedContent)
}

func TestMessage_ParseContent_String(t *testing.T) {
	m := &Message{Content: "hello"}
	got := m.ParseContent()
	require.Len(t, got, 1)
	assert.Equal(t, ContentTypeText, got[0].Type)
	assert.Equal(t, "hello", got[0].Text)
	// cached
	assert.Equal(t, got, m.parsedContent)
}

func TestMessage_ParseContent_NilAndCache(t *testing.T) {
	assert.Nil(t, (&Message{Content: nil}).ParseContent())
	// pre-populated cache returned directly
	cached := []MediaContent{{Type: ContentTypeText, Text: "cached"}}
	m := &Message{Content: "ignored", parsedContent: cached}
	assert.Equal(t, cached, m.ParseContent())
}

func TestMessage_ParseContent_ArrayAllTypes(t *testing.T) {
	m := &Message{Content: []any{
		map[string]any{"type": "text", "text": "t1"},
		map[string]any{"type": "image_url", "image_url": "https://img/1.png"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://img/2.png", "detail": "low"}},
		map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "d", "format": "wav"}},
		map[string]any{"type": "file", "file": map[string]any{"file_id": "fid"}},
		map[string]any{"type": "file", "file": map[string]any{"filename": "n", "file_data": "fd"}},
		map[string]any{"type": "video_url", "video_url": "https://v/1.mp4"},
	}}
	got := m.ParseContent()
	require.Len(t, got, 7)

	assert.Equal(t, "t1", got[0].Text)

	img1 := got[1].GetImageMedia()
	require.NotNil(t, img1)
	assert.Equal(t, "https://img/1.png", img1.Url)
	assert.Equal(t, "high", img1.Detail, "string image_url defaults detail=high")

	img2 := got[2].GetImageMedia()
	require.NotNil(t, img2)
	assert.Equal(t, "https://img/2.png", img2.Url)
	assert.Equal(t, "low", img2.Detail)

	audio := got[3].GetInputAudio()
	require.NotNil(t, audio)
	assert.Equal(t, "d", audio.Data)
	assert.Equal(t, "wav", audio.Format)

	file1 := got[4].GetFile()
	require.NotNil(t, file1)
	assert.Equal(t, "fid", file1.FileId)

	file2 := got[5].GetFile()
	require.NotNil(t, file2)
	assert.Equal(t, "n", file2.FileName)
	assert.Equal(t, "fd", file2.FileData)

	video := got[6].GetVideoUrl()
	require.NotNil(t, video)
	assert.Equal(t, "https://v/1.mp4", video.Url)
}

func TestMessage_ParseContent_ArraySkipsInvalidItems(t *testing.T) {
	m := &Message{Content: []any{
		"plain-string",                             // not a map, skipped
		map[string]any{"no_type": "x"},             // no type, skipped
		map[string]any{"type": 5},                  // type not string, skipped
		map[string]any{"type": "text"},             // text missing => skipped
		map[string]any{"type": "unknown_type"},     // unknown type, skipped
		map[string]any{"type": "text", "text": "y"},// valid
	}}
	got := m.ParseContent()
	require.Len(t, got, 1)
	assert.Equal(t, "y", got[0].Text)
}

func TestMessage_ParseContent_PreParsedMediaContentItem(t *testing.T) {
	m := &Message{Content: []any{
		MediaContent{Type: ContentTypeText, Text: "pre"},
	}}
	got := m.ParseContent()
	require.Len(t, got, 1)
	assert.Equal(t, "pre", got[0].Text)
}

func TestMessage_ParseContent_NonStringNonArray(t *testing.T) {
	// number content: not string, not []any => empty list, not cached
	m := &Message{Content: 42}
	assert.Empty(t, m.ParseContent())
}

// ---------------------------------------------------------------------------
// MediaContent getters + ToFileSource
// ---------------------------------------------------------------------------

func TestMediaContent_GetImageMedia(t *testing.T) {
	// nil
	assert.Nil(t, (&MediaContent{}).GetImageMedia())
	// already-typed pointer
	typed := &MessageImageUrl{Url: "u"}
	assert.Equal(t, typed, (&MediaContent{ImageUrl: typed}).GetImageMedia())
	// map form
	got := (&MediaContent{ImageUrl: map[string]any{"url": "u2", "detail": "high", "mime_type": "image/png"}}).GetImageMedia()
	require.NotNil(t, got)
	assert.Equal(t, "u2", got.Url)
	assert.Equal(t, "high", got.Detail)
	assert.Equal(t, "image/png", got.MimeType)
	// non-map, non-pointer => nil
	assert.Nil(t, (&MediaContent{ImageUrl: 5}).GetImageMedia())
}

func TestMediaContent_GetInputAudio(t *testing.T) {
	assert.Nil(t, (&MediaContent{}).GetInputAudio())
	typed := &MessageInputAudio{Data: "d"}
	assert.Equal(t, typed, (&MediaContent{InputAudio: typed}).GetInputAudio())
	got := (&MediaContent{InputAudio: map[string]any{"data": "dd", "format": "mp3"}}).GetInputAudio()
	require.NotNil(t, got)
	assert.Equal(t, "dd", got.Data)
	assert.Equal(t, "mp3", got.Format)
	assert.Nil(t, (&MediaContent{InputAudio: 1}).GetInputAudio())
}

func TestMediaContent_GetFile(t *testing.T) {
	assert.Nil(t, (&MediaContent{}).GetFile())
	typed := &MessageFile{FileId: "id"}
	assert.Equal(t, typed, (&MediaContent{File: typed}).GetFile())
	got := (&MediaContent{File: map[string]any{"file_name": "n", "file_data": "d", "file_id": "id2"}}).GetFile()
	require.NotNil(t, got)
	assert.Equal(t, "n", got.FileName)
	assert.Equal(t, "d", got.FileData)
	assert.Equal(t, "id2", got.FileId)
	assert.Nil(t, (&MediaContent{File: 1}).GetFile())
}

func TestMediaContent_GetVideoUrl(t *testing.T) {
	assert.Nil(t, (&MediaContent{}).GetVideoUrl())
	typed := &MessageVideoUrl{Url: "u"}
	assert.Equal(t, typed, (&MediaContent{VideoUrl: typed}).GetVideoUrl())
	got := (&MediaContent{VideoUrl: map[string]any{"url": "u2"}}).GetVideoUrl()
	require.NotNil(t, got)
	assert.Equal(t, "u2", got.Url)
	assert.Nil(t, (&MediaContent{VideoUrl: 1}).GetVideoUrl())
}

func TestMediaContent_ToFileSource(t *testing.T) {
	// image URL
	src := (&MediaContent{Type: ContentTypeImageURL, ImageUrl: map[string]any{"url": "https://x/a.png", "mime_type": "image/png"}}).ToFileSource()
	require.NotNil(t, src)
	assert.True(t, src.IsURL())
	// image empty url => nil
	assert.Nil(t, (&MediaContent{Type: ContentTypeImageURL, ImageUrl: map[string]any{"url": ""}}).ToFileSource())
	assert.Nil(t, (&MediaContent{Type: ContentTypeImageURL}).ToFileSource())

	// audio with format => base64 source
	src = (&MediaContent{Type: ContentTypeInputAudio, InputAudio: map[string]any{"data": "AAAA", "format": "wav"}}).ToFileSource()
	require.NotNil(t, src)
	assert.False(t, src.IsURL())
	assert.Nil(t, (&MediaContent{Type: ContentTypeInputAudio, InputAudio: map[string]any{"data": ""}}).ToFileSource())

	// file
	src = (&MediaContent{Type: ContentTypeFile, File: map[string]any{"file_data": "ZZ"}}).ToFileSource()
	require.NotNil(t, src)
	assert.Nil(t, (&MediaContent{Type: ContentTypeFile, File: map[string]any{"file_data": ""}}).ToFileSource())

	// video
	src = (&MediaContent{Type: ContentTypeVideoUrl, VideoUrl: map[string]any{"url": "https://x/v.mp4"}}).ToFileSource()
	require.NotNil(t, src)
	assert.Nil(t, (&MediaContent{Type: ContentTypeVideoUrl, VideoUrl: map[string]any{"url": ""}}).ToFileSource())

	// unknown type => nil
	assert.Nil(t, (&MediaContent{Type: "text"}).ToFileSource())
}

func TestMessageImageUrl_IsRemoteImage(t *testing.T) {
	assert.True(t, (&MessageImageUrl{Url: "http://x"}).IsRemoteImage())
	assert.True(t, (&MessageImageUrl{Url: "https://x"}).IsRemoteImage())
	assert.False(t, (&MessageImageUrl{Url: "data:image/png;base64,AAA"}).IsRemoteImage())
}

// ---------------------------------------------------------------------------
// OpenAIResponsesRequest
// ---------------------------------------------------------------------------

func TestOpenAIResponsesRequest_IsStreamSetModel(t *testing.T) {
	tr := true
	assert.False(t, (&OpenAIResponsesRequest{}).IsStream(nil))
	assert.True(t, (&OpenAIResponsesRequest{Stream: &tr}).IsStream(nil))
	r := &OpenAIResponsesRequest{Model: "a"}
	r.SetModelName("")
	assert.Equal(t, "a", r.Model)
	r.SetModelName("b")
	assert.Equal(t, "b", r.Model)
}

func TestOpenAIResponsesRequest_Rule5Stream(t *testing.T) {
	var r OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"m","stream":false,"temperature":0}`), &r))
	require.NotNil(t, r.Stream)
	assert.False(t, *r.Stream)
	require.NotNil(t, r.Temperature)
	assert.Equal(t, 0.0, *r.Temperature)
	// absent
	var r2 OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"m"}`), &r2))
	assert.Nil(t, r2.Stream)
	assert.Nil(t, r2.Temperature)
}

func TestOpenAIResponsesRequest_GetToolsMap(t *testing.T) {
	assert.Nil(t, (&OpenAIResponsesRequest{}).GetToolsMap())
	r := &OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"function","name":"f"}]`)}
	tools := r.GetToolsMap()
	require.Len(t, tools, 1)
	assert.Equal(t, "function", tools[0]["type"])
}

func TestOpenAIResponsesRequest_ParseInput_String(t *testing.T) {
	r := &OpenAIResponsesRequest{Input: json.RawMessage(`"hello world"`)}
	got := r.ParseInput()
	require.Len(t, got, 1)
	assert.Equal(t, "input_text", got[0].Type)
	assert.Equal(t, "hello world", got[0].Text)
}

func TestOpenAIResponsesRequest_ParseInput_Nil(t *testing.T) {
	assert.Nil(t, (&OpenAIResponsesRequest{}).ParseInput())
}

func TestOpenAIResponsesRequest_ParseInput_ArrayParts(t *testing.T) {
	raw := `[
		{"role":"user","content":"plain string content"},
		{"role":"user","content":[
			{"type":"input_text","text":"t"},
			{"type":"input_image","image_url":"https://img/a.png"},
			{"type":"input_image","image_url":{"url":"https://img/b.png"}},
			{"type":"input_file","file_url":"https://f/a.pdf"},
			{"type":"input_file","file_url":{"url":"https://f/b.pdf"}},
			{"type":"unknown"},
			{"no_type":true}
		]}
	]`
	r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
	got := r.ParseInput()
	// 1 (string content) + 5 recognized parts
	require.Len(t, got, 6)
	assert.Equal(t, "input_text", got[0].Type)
	assert.Equal(t, "plain string content", got[0].Text)
	assert.Equal(t, "input_text", got[1].Type)
	assert.Equal(t, "input_image", got[2].Type)
	assert.Equal(t, "https://img/a.png", got[2].ImageUrl)
	assert.Equal(t, "https://img/b.png", got[3].ImageUrl)
	assert.Equal(t, "input_file", got[4].Type)
	assert.Equal(t, "https://f/a.pdf", got[4].FileUrl)
	assert.Equal(t, "https://f/b.pdf", got[5].FileUrl)
}

func TestOpenAIResponsesRequest_GetTokenCountMeta(t *testing.T) {
	mk := func(v uint) *uint { return &v }
	raw := `[{"role":"user","content":[
		{"type":"input_text","text":"question"},
		{"type":"input_image","image_url":"https://img/a.png"},
		{"type":"input_file","file_url":"https://f/a.pdf"}
	]}]`
	r := &OpenAIResponsesRequest{
		Input:           json.RawMessage(raw),
		Instructions:    json.RawMessage(`"be brief"`),
		Metadata:        json.RawMessage(`{"k":"v"}`),
		Text:            json.RawMessage(`{"format":"x"}`),
		ToolChoice:      json.RawMessage(`"auto"`),
		Prompt:          json.RawMessage(`{"id":"p"}`),
		Tools:           json.RawMessage(`[{"type":"function"}]`),
		MaxOutputTokens: mk(64),
	}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, 64, meta.MaxTokens)
	assert.Contains(t, meta.CombineText, "question")
	assert.Contains(t, meta.CombineText, "be brief")
	assert.Len(t, meta.Files, 2) // image + file
	var kinds []types.FileType
	for _, f := range meta.Files {
		kinds = append(kinds, f.FileType)
	}
	assert.Contains(t, kinds, types.FileTypeImage)
	assert.Contains(t, kinds, types.FileTypeFile)
}

func TestResponseFormat_Marshal(t *testing.T) {
	// sanity round-trip for ResponseFormat / FormatJsonSchema types
	rf := ResponseFormat{Type: "json_schema", JsonSchema: json.RawMessage(`{"name":"x"}`)}
	data, err := common.Marshal(rf)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "json_schema"))
}
