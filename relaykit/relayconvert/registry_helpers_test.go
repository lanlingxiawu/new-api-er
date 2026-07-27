package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared fixtures
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// isNilRequest / isNilResponse
// ---------------------------------------------------------------------------

func TestIsNilRequest(t *testing.T) {
	assert.True(t, isNilRequest(nil), "untyped nil")
	assert.True(t, isNilRequest((*dto.GeneralOpenAIRequest)(nil)), "typed nil pointer")

	var nilMap map[string]int
	assert.True(t, isNilRequest(nilMap), "nil map")
	var nilSlice []int
	assert.True(t, isNilRequest(nilSlice), "nil slice")
	var nilChan chan int
	assert.True(t, isNilRequest(nilChan), "nil chan")
	var nilFunc func()
	assert.True(t, isNilRequest(nilFunc), "nil func")

	assert.False(t, isNilRequest(&dto.GeneralOpenAIRequest{}), "non-nil pointer")
	assert.False(t, isNilRequest(42), "non-pointer scalar")
	assert.False(t, isNilRequest("str"), "string")
	assert.False(t, isNilRequest(map[string]int{"a": 1}), "non-nil map")
}

func TestIsNilResponse(t *testing.T) {
	assert.True(t, isNilResponse(nil), "untyped nil")
	assert.True(t, isNilResponse((*dto.OpenAITextResponse)(nil)), "typed nil pointer")

	var nilSlice []int
	assert.True(t, isNilResponse(nilSlice), "nil slice")

	assert.False(t, isNilResponse(&dto.OpenAITextResponse{}), "non-nil pointer")
	assert.False(t, isNilResponse(7), "non-pointer scalar")
}

// ---------------------------------------------------------------------------
// inferRequestRelayFormat / inferResponseRelayFormat
// ---------------------------------------------------------------------------

func TestInferRequestRelayFormat(t *testing.T) {
	tests := []struct {
		name    string
		request any
		want    types.RelayFormat
		wantErr string
	}{
		{name: "openai ptr", request: &dto.GeneralOpenAIRequest{}, want: types.RelayFormatOpenAI},
		{name: "openai value", request: dto.GeneralOpenAIRequest{}, want: types.RelayFormatOpenAI},
		{name: "responses", request: &dto.OpenAIResponsesRequest{}, want: types.RelayFormatOpenAIResponses},
		{name: "claude", request: &dto.ClaudeRequest{}, want: types.RelayFormatClaude},
		{name: "gemini", request: &dto.GeminiChatRequest{}, want: types.RelayFormatGemini},
		{name: "embedding", request: &dto.EmbeddingRequest{}, want: types.RelayFormatEmbedding},
		{name: "nil", request: nil, wantErr: "request is nil"},
		{name: "typed nil", request: (*dto.ClaudeRequest)(nil), wantErr: "request is nil"},
		{name: "unsupported", request: 12345, wantErr: "unsupported request type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inferRequestRelayFormat(tt.request)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInferResponseRelayFormat(t *testing.T) {
	tests := []struct {
		name     string
		response any
		want     types.RelayFormat
		wantErr  string
	}{
		{name: "oai text ptr", response: &dto.OpenAITextResponse{}, want: types.RelayFormatOpenAI},
		{name: "oai text value", response: dto.OpenAITextResponse{}, want: types.RelayFormatOpenAI},
		{name: "oai stream ptr", response: &dto.ChatCompletionsStreamResponse{}, want: types.RelayFormatOpenAI},
		{name: "oai stream value", response: dto.ChatCompletionsStreamResponse{}, want: types.RelayFormatOpenAI},
		{name: "responses ptr", response: &dto.OpenAIResponsesResponse{}, want: types.RelayFormatOpenAIResponses},
		{name: "responses value", response: dto.OpenAIResponsesResponse{}, want: types.RelayFormatOpenAIResponses},
		{name: "responses stream ptr", response: &dto.ResponsesStreamResponse{}, want: types.RelayFormatOpenAIResponses},
		{name: "responses stream value", response: dto.ResponsesStreamResponse{}, want: types.RelayFormatOpenAIResponses},
		{name: "claude ptr", response: &dto.ClaudeResponse{}, want: types.RelayFormatClaude},
		{name: "claude value", response: dto.ClaudeResponse{}, want: types.RelayFormatClaude},
		{name: "gemini ptr", response: &dto.GeminiChatResponse{}, want: types.RelayFormatGemini},
		{name: "gemini value", response: dto.GeminiChatResponse{}, want: types.RelayFormatGemini},
		{name: "nil", response: nil, wantErr: "response is nil"},
		{name: "typed nil", response: (*dto.ClaudeResponse)(nil), wantErr: "response is nil"},
		{name: "unsupported", response: "nope", wantErr: "unsupported response type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inferResponseRelayFormat(tt.response)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// streamValuesFromAny
// ---------------------------------------------------------------------------

func TestStreamValuesFromAny(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, streamValuesFromAny(nil))
	})
	t.Run("typed nil pointer", func(t *testing.T) {
		assert.Nil(t, streamValuesFromAny((*dto.ClaudeResponse)(nil)))
	})
	t.Run("non-slice scalar wraps into single element", func(t *testing.T) {
		out := streamValuesFromAny(42)
		require.Len(t, out, 1)
		assert.Equal(t, 42, out[0])
	})
	t.Run("byte slice treated as single value", func(t *testing.T) {
		payload := []byte("abc")
		out := streamValuesFromAny(payload)
		require.Len(t, out, 1)
		assert.Equal(t, payload, out[0])
	})
	t.Run("slice with nil pointer elements skipped", func(t *testing.T) {
		one := &dto.ClaudeResponse{Type: "one"}
		items := []*dto.ClaudeResponse{one, nil, {Type: "two"}}
		out := streamValuesFromAny(items)
		require.Len(t, out, 2)
		assert.Equal(t, one, out[0])
	})
	t.Run("slice of values", func(t *testing.T) {
		out := streamValuesFromAny([]int{1, 2, 3})
		require.Len(t, out, 3)
		assert.Equal(t, 2, out[1])
	})
	t.Run("array", func(t *testing.T) {
		out := streamValuesFromAny([2]string{"a", "b"})
		require.Len(t, out, 2)
		assert.Equal(t, "b", out[1])
	})
	t.Run("empty slice", func(t *testing.T) {
		out := streamValuesFromAny([]int{})
		assert.Empty(t, out)
	})
}

// ---------------------------------------------------------------------------
// canonicalUsageFromResponse — every response type + nil-usage branches
// ---------------------------------------------------------------------------

func TestCanonicalUsageFromResponse(t *testing.T) {
	t.Run("oai text ptr", func(t *testing.T) {
		resp := &dto.OpenAITextResponse{Usage: dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 5, usage.TotalTokens)
	})
	t.Run("oai text value", func(t *testing.T) {
		resp := dto.OpenAITextResponse{Usage: dto.Usage{TotalTokens: 8}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 8, usage.TotalTokens)
	})
	t.Run("oai stream ptr with usage", func(t *testing.T) {
		resp := &dto.ChatCompletionsStreamResponse{Usage: &dto.Usage{TotalTokens: 7}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 7, usage.TotalTokens)
	})
	t.Run("oai stream ptr nil usage", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse(&dto.ChatCompletionsStreamResponse{}))
	})
	t.Run("oai stream value with usage", func(t *testing.T) {
		usage := canonicalUsageFromResponse(dto.ChatCompletionsStreamResponse{Usage: &dto.Usage{TotalTokens: 9}})
		require.NotNil(t, usage)
		assert.Equal(t, 9, usage.TotalTokens)
	})
	t.Run("oai stream value nil usage", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse(dto.ChatCompletionsStreamResponse{}))
	})
	t.Run("responses ptr", func(t *testing.T) {
		usage := canonicalUsageFromResponse(&dto.OpenAIResponsesResponse{Usage: &dto.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7}})
		require.NotNil(t, usage)
		assert.Equal(t, 7, usage.TotalTokens)
	})
	t.Run("responses value", func(t *testing.T) {
		usage := canonicalUsageFromResponse(dto.OpenAIResponsesResponse{Usage: &dto.Usage{TotalTokens: 11}})
		require.NotNil(t, usage)
		assert.Equal(t, 11, usage.TotalTokens)
	})
	t.Run("responses stream ptr with response", func(t *testing.T) {
		resp := &dto.ResponsesStreamResponse{Response: &dto.OpenAIResponsesResponse{Usage: &dto.Usage{TotalTokens: 13}}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 13, usage.TotalTokens)
	})
	t.Run("responses stream ptr nil response", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse(&dto.ResponsesStreamResponse{}))
	})
	t.Run("responses stream value with response", func(t *testing.T) {
		resp := dto.ResponsesStreamResponse{Response: &dto.OpenAIResponsesResponse{Usage: &dto.Usage{TotalTokens: 15}}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 15, usage.TotalTokens)
	})
	t.Run("responses stream value nil response", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse(dto.ResponsesStreamResponse{}))
	})
	t.Run("claude ptr with usage", func(t *testing.T) {
		resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{InputTokens: 4, OutputTokens: 6}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 4, usage.PromptTokens)
	})
	t.Run("claude value with message usage", func(t *testing.T) {
		resp := dto.ClaudeResponse{Message: &dto.ClaudeMediaMessage{Usage: &dto.ClaudeUsage{InputTokens: 2, OutputTokens: 1}}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 2, usage.PromptTokens)
	})
	t.Run("claude ptr nil usage", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse(&dto.ClaudeResponse{}))
	})
	t.Run("gemini ptr", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 5, usage.TotalTokens)
	})
	t.Run("gemini value", func(t *testing.T) {
		resp := dto.GeminiChatResponse{UsageMetadata: dto.GeminiUsageMetadata{TotalTokenCount: 4}}
		usage := canonicalUsageFromResponse(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 4, usage.TotalTokens)
	})
	t.Run("unsupported returns nil", func(t *testing.T) {
		assert.Nil(t, canonicalUsageFromResponse("nope"))
	})
}

func TestUsageFromClaudeResponse(t *testing.T) {
	assert.Nil(t, usageFromClaudeResponse(nil))
	assert.Nil(t, usageFromClaudeResponse(&dto.ClaudeResponse{}))

	direct := usageFromClaudeResponse(&dto.ClaudeResponse{Usage: &dto.ClaudeUsage{InputTokens: 5, OutputTokens: 2}})
	require.NotNil(t, direct)
	assert.Equal(t, 5, direct.PromptTokens)

	nested := usageFromClaudeResponse(&dto.ClaudeResponse{Message: &dto.ClaudeMediaMessage{Usage: &dto.ClaudeUsage{InputTokens: 9}}})
	require.NotNil(t, nested)
	assert.Equal(t, 9, nested.PromptTokens)
}

func TestFallbackPromptTokens(t *testing.T) {
	assert.Equal(t, 0, fallbackPromptTokens(nil))

	info := &convmeta.Values{}
	info.EstimatePromptTokens = 17
	assert.Equal(t, 17, fallbackPromptTokens(info))
}

// ---------------------------------------------------------------------------
// as* response coercion helpers — pointer, value, and error branches
// ---------------------------------------------------------------------------

func TestAsResponseHelpers(t *testing.T) {
	t.Run("asOAIChatResponse", func(t *testing.T) {
		ptr := &dto.OpenAITextResponse{Id: "p"}
		got, err := asOAIChatResponse(ptr)
		require.NoError(t, err)
		assert.Same(t, ptr, got)

		got, err = asOAIChatResponse(dto.OpenAITextResponse{Id: "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", got.Id)

		_, err = asOAIChatResponse("bad")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected OAI chat response")
	})
	t.Run("asOAIChatStreamResponse", func(t *testing.T) {
		got, err := asOAIChatStreamResponse(dto.ChatCompletionsStreamResponse{Id: "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", got.Id)
		_, err = asOAIChatStreamResponse("bad")
		require.Error(t, err)
	})
	t.Run("asOAIResponsesResponse", func(t *testing.T) {
		got, err := asOAIResponsesResponse(dto.OpenAIResponsesResponse{ID: "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", got.ID)
		_, err = asOAIResponsesResponse("bad")
		require.Error(t, err)
	})
	t.Run("asOAIResponsesStreamResponse", func(t *testing.T) {
		got, err := asOAIResponsesStreamResponse(dto.ResponsesStreamResponse{Type: "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", got.Type)
		_, err = asOAIResponsesStreamResponse("bad")
		require.Error(t, err)
	})
	t.Run("asClaudeResponse", func(t *testing.T) {
		got, err := asClaudeResponse(dto.ClaudeResponse{Id: "v"})
		require.NoError(t, err)
		assert.Equal(t, "v", got.Id)
		_, err = asClaudeResponse("bad")
		require.Error(t, err)
	})
	t.Run("asGeminiChatResponse", func(t *testing.T) {
		got, err := asGeminiChatResponse(dto.GeminiChatResponse{HasUsageMetadata: true})
		require.NoError(t, err)
		assert.True(t, got.HasUsageMetadata)
		_, err = asGeminiChatResponse("bad")
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// ResponseStreamState.UsageText — interface-assert branches
// ---------------------------------------------------------------------------

type usageTextStub struct{ text string }

func (u usageTextStub) UsageText() string { return u.text }

func TestResponseStreamStateUsageTextBranches(t *testing.T) {
	// A step state that returns non-empty text is returned.
	s := &ResponseStreamState{stepStates: []any{usageTextStub{text: "billed"}}}
	assert.Equal(t, "billed", s.UsageText())

	// Empty-text state is skipped; a non-implementing state is skipped too.
	s = &ResponseStreamState{stepStates: []any{usageTextStub{text: ""}, 42, usageTextStub{text: "final"}}}
	assert.Equal(t, "final", s.UsageText())

	// No implementing states => empty.
	s = &ResponseStreamState{stepStates: []any{42, "x"}}
	assert.Equal(t, "", s.UsageText())
}

// --- 请求 fixture ---
// 这些构造函数原先与 fork 的 request_registry_test.go 同处一个文件；该文件已
// 改用上游版本，helper 移到这里，供本包其余 fork 测试复用。

func newOpenAIRequest() *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{
		Model:    "gpt-test",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
}

func newClaudeRequest() *dto.ClaudeRequest {
	return &dto.ClaudeRequest{
		Model:    "claude-test",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
}

func newGeminiRequest() *dto.GeminiChatRequest {
	return &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
	}
}

func newResponsesRequest(t *testing.T) *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": "hello"},
		}),
	}
}
