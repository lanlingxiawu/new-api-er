package relayconvert

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Registry listing + alias resolution
// ---------------------------------------------------------------------------

func TestLookupResponseConverterByIDAndAlias(t *testing.T) {
	tests := []struct {
		lookupID       string
		id             string
		from           types.RelayFormat
		to             types.RelayFormat
		quality        ResponseConverterQuality
		stepConverters []string
	}{
		{ResponseConverterOAIChatToOAIResponses, ConverterOpenAIChatToOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseConverterQualityGood, nil},
		{ResponseConverterOAIResponsesToOAIChat, ConverterOpenAIResponsesToOpenAIChat, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, ResponseConverterQualityGood, nil},
		{ResponseConverterOAIChatToClaudeMessages, ConverterOpenAIChatToClaudeMessages, types.RelayFormatOpenAI, types.RelayFormatClaude, ResponseConverterQualityFair, nil},
		{ResponseConverterOAIChatToGeminiChat, ConverterOpenAIChatToGeminiContent, types.RelayFormatOpenAI, types.RelayFormatGemini, ResponseConverterQualityFair, nil},
		{ResponseConverterClaudeMessagesToOAIChat, ConverterClaudeMessagesToOpenAIChat, types.RelayFormatClaude, types.RelayFormatOpenAI, ResponseConverterQualityFair, nil},
		{ResponseConverterGeminiChatToOAIChat, ConverterGeminiContentToOpenAIChat, types.RelayFormatGemini, types.RelayFormatOpenAI, ResponseConverterQualityFair, nil},
		{responseConverterClaudeToGemini, requestConverterClaudeToGemini, types.RelayFormatClaude, types.RelayFormatGemini, ResponseConverterQualityDiscouraged, []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToGeminiContent}},
		{responseConverterClaudeToResponses, requestConverterClaudeToResponses, types.RelayFormatClaude, types.RelayFormatOpenAIResponses, ResponseConverterQualityFair, []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}},
		{responseConverterGeminiToClaude, requestConverterGeminiToClaude, types.RelayFormatGemini, types.RelayFormatClaude, ResponseConverterQualityDiscouraged, []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToClaudeMessages}},
		{responseConverterGeminiToResponses, requestConverterGeminiToResponses, types.RelayFormatGemini, types.RelayFormatOpenAIResponses, ResponseConverterQualityFair, []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}},
		{responseConverterResponsesToClaude, requestConverterResponsesToClaude, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, ResponseConverterQualityFair, []string{ConverterOpenAIResponsesToOpenAIChat, ConverterOpenAIChatToClaudeMessages}},
		{responseConverterResponsesToGemini, ConverterOpenAIResponsesToGemini, types.RelayFormatOpenAIResponses, types.RelayFormatGemini, ResponseConverterQualityFair, []string{ConverterOpenAIResponsesToOpenAIChat, ConverterOpenAIChatToGeminiContent}},
	}

	for _, tt := range tests {
		t.Run(tt.lookupID, func(t *testing.T) {
			spec, ok := LookupResponseConverter(tt.lookupID)
			require.True(t, ok)
			assert.Equal(t, tt.id, spec.ID)
			assert.Equal(t, tt.from, spec.From)
			assert.Equal(t, tt.to, spec.To)
			assert.Equal(t, tt.quality, spec.Quality)
			assert.Equal(t, tt.stepConverters, spec.StepConverters)
			if len(tt.stepConverters) == 0 {
				assert.NotNil(t, spec.Convert)
			} else {
				assert.Nil(t, spec.Convert)
			}
		})
	}

	_, ok := LookupResponseConverter("missing")
	assert.False(t, ok)

	// Trim + alias resolution together.
	spec, ok := LookupResponseConverter("  " + ResponseConverterClaudeMessagesToOAIChat + "  ")
	require.True(t, ok)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, spec.ID)
}

func TestResolveResponseConverterID(t *testing.T) {
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, resolveResponseConverterID(ResponseConverterClaudeMessagesToOAIChat))
	// Non-alias input is returned trimmed but unchanged.
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, resolveResponseConverterID("  "+ConverterOpenAIChatToOpenAIResponses+"  "))
	assert.Equal(t, "unknown", resolveResponseConverterID("unknown"))
}

// ---------------------------------------------------------------------------
// ConvertResponse (non-stream)
// ---------------------------------------------------------------------------

func TestConvertResponseSameFormatPassthrough(t *testing.T) {
	resp := textRegistryChatResponse()
	result, err := ConvertResponse(nil, nil, types.RelayFormatOpenAI, resp)
	require.NoError(t, err)
	assert.Same(t, resp, result.Value)
	assert.False(t, result.Stream)
	require.NotNil(t, result.Usage)
	assert.Equal(t, 9, result.Usage.TotalTokens)
}

func TestConvertResponseNilAndUnsupportedRoute(t *testing.T) {
	_, err := ConvertResponse(nil, nil, types.RelayFormatOpenAI, (*dto.OpenAITextResponse)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")

	_, err = ConvertResponse(nil, nil, "", &dto.OpenAITextResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target relay format is required")

	_, err = ConvertResponse(nil, nil, types.RelayFormatEmbedding, &dto.OpenAITextResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

func TestConvertResponseDirectRoutes(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"}}

	toResponses, err := ConvertResponse(nil, info, types.RelayFormatOpenAIResponses, textRegistryChatResponse())
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, toResponses.Converter)
	assert.Equal(t, ResponseConverterQualityGood, toResponses.Quality)
	require.IsType(t, &dto.OpenAIResponsesResponse{}, toResponses.Value)
	assert.Equal(t, 9, toResponses.Usage.TotalTokens)

	responses := textRegistryResponsesResponse()
	toChat, err := ConvertResponse(nil, info, types.RelayFormatOpenAI, responses)
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat, toChat.Converter)
	require.IsType(t, &dto.OpenAITextResponse{}, toChat.Value)
	assert.Equal(t, 11, toChat.Usage.TotalTokens)

	toClaude, err := ConvertResponse(nil, info, types.RelayFormatClaude, textRegistryChatResponse())
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIChatToClaudeMessages, toClaude.Converter)
	require.IsType(t, &dto.ClaudeResponse{}, toClaude.Value)

	toGemini, err := ConvertResponse(nil, info, types.RelayFormatGemini, textRegistryChatResponse())
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIChatToGeminiContent, toGemini.Converter)
	require.IsType(t, &dto.GeminiChatResponse{}, toGemini.Value)

	// claude -> openai and gemini -> openai native providers.
	claude := &dto.ClaudeResponse{
		Id: "msg_1", Type: "message", Role: "assistant", Model: "claude-test", StopReason: "end_turn",
		Content: []dto.ClaudeMediaMessage{{Type: "text", Text: respPtr("hi")}},
		Usage:   &dto.ClaudeUsage{InputTokens: 10, OutputTokens: 5},
	}
	toChat, err = ConvertResponse(nil, nil, types.RelayFormatOpenAI, claude)
	require.NoError(t, err)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, toChat.Converter)
	assert.Equal(t, 15, toChat.Usage.TotalTokens)

	gemini := &dto.GeminiChatResponse{
		Candidates:    []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5},
	}
	toChat, err = ConvertResponse(nil, info, types.RelayFormatOpenAI, gemini)
	require.NoError(t, err)
	assert.Equal(t, ConverterGeminiContentToOpenAIChat, toChat.Converter)
	assert.Equal(t, 5, toChat.Usage.TotalTokens)
}

func TestConvertResponseMultiHopRoutes(t *testing.T) {
	responses := textRegistryResponsesResponse()

	toClaude, err := ConvertResponse(nil, &relaycommon.RelayInfo{}, types.RelayFormatClaude, responses)
	require.NoError(t, err)
	assert.Equal(t, requestConverterResponsesToClaude, toClaude.Converter)
	assert.Equal(t, []ResponseStep{
		{Converter: ConverterOpenAIResponsesToOpenAIChat, From: types.RelayFormatOpenAIResponses, To: types.RelayFormatOpenAI},
		{Converter: ConverterOpenAIChatToClaudeMessages, From: types.RelayFormatOpenAI, To: types.RelayFormatClaude},
	}, toClaude.Steps)
	require.IsType(t, &dto.ClaudeResponse{}, toClaude.Value)
	assert.Equal(t, 11, toClaude.Usage.TotalTokens)

	toGemini, err := ConvertResponse(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"}}, types.RelayFormatGemini, responses)
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIResponsesToGemini, toGemini.Converter)
	assert.Equal(t, []ResponseStep{
		{Converter: ConverterOpenAIResponsesToOpenAIChat, From: types.RelayFormatOpenAIResponses, To: types.RelayFormatOpenAI},
		{Converter: ConverterOpenAIChatToGeminiContent, From: types.RelayFormatOpenAI, To: types.RelayFormatGemini},
	}, toGemini.Steps)
	require.IsType(t, &dto.GeminiChatResponse{}, toGemini.Value)
	assert.Equal(t, 11, toGemini.Usage.TotalTokens)
}

// ---------------------------------------------------------------------------
// ConvertResponseByID
// ---------------------------------------------------------------------------

func TestConvertResponseByID(t *testing.T) {
	responses := textRegistryResponsesResponse()

	result, err := ConvertResponseByID(nil, nil, responseConverterResponsesToGemini, responses)
	require.NoError(t, err)
	assert.Equal(t, ConverterOpenAIResponsesToGemini, result.Converter)
	require.Len(t, result.Steps, 2)

	// Source mismatch: alias expects responses, given a chat response.
	_, err = ConvertResponseByID(nil, nil, responseConverterResponsesToGemini, textRegistryChatResponse())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects openai_responses response")

	// Unknown converter.
	_, err = ConvertResponseByID(nil, nil, "missing", responses)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")

	// Nil response.
	_, err = ConvertResponseByID(nil, nil, ResponseConverterOAIChatToOAIResponses, (*dto.OpenAITextResponse)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")
}

// ---------------------------------------------------------------------------
// ConvertStreamResponse (stateless)
// ---------------------------------------------------------------------------

func TestConvertStreamResponseSameFormatPassthrough(t *testing.T) {
	resp := &dto.ChatCompletionsStreamResponse{Id: "x", Usage: &dto.Usage{TotalTokens: 4}}
	result, err := ConvertStreamResponse(nil, nil, types.RelayFormatOpenAI, resp)
	require.NoError(t, err)
	assert.True(t, result.Stream)
	assert.Same(t, resp, result.Value)
	assert.Equal(t, 4, result.Usage.TotalTokens)
}

func TestConvertStreamResponseErrors(t *testing.T) {
	_, err := ConvertStreamResponse(nil, nil, types.RelayFormatOpenAI, (*dto.ChatCompletionsStreamResponse)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")

	_, err = ConvertStreamResponse(nil, nil, "", &dto.ChatCompletionsStreamResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target relay format is required")

	_, err = ConvertStreamResponse(nil, nil, types.RelayFormatEmbedding, &dto.ChatCompletionsStreamResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

func TestConvertStreamResponseStatelessDirect(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
	}
	info.SendResponseCount = 1
	finishReason := "stop"
	result, err := ConvertStreamResponse(nil, info, types.RelayFormatClaude, &dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{FinishReason: &finishReason, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: respPtr("hello")}},
		},
		Usage: &dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
	})
	require.NoError(t, err)
	assert.True(t, result.Stream)
	assert.Equal(t, ConverterOpenAIChatToClaudeMessages, result.Converter)
	require.IsType(t, []*dto.ClaudeResponse{}, result.Value)
	assert.Equal(t, 5, result.Usage.TotalTokens)

	// gemini -> openai stateless stream.
	result, err = ConvertStreamResponse(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"}}, types.RelayFormatOpenAI, &dto.GeminiChatResponse{
		Candidates:    []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hello"}}}}},
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 1, CandidatesTokenCount: 2, TotalTokenCount: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, ConverterGeminiContentToOpenAIChat, result.Converter)
	require.IsType(t, &dto.ChatCompletionsStreamResponse{}, result.Value)
	assert.Equal(t, 3, result.Usage.TotalTokens)
}

// Stateless stream over a converter that REQUIRES stream state (chat->responses)
// must error, because it exposes only chunk/state converters.
func TestConvertStreamResponseRequiresState(t *testing.T) {
	_, err := ConvertStreamResponse(nil, nil, types.RelayFormatOpenAIResponses, &dto.ChatCompletionsStreamResponse{Id: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires response stream state")
}

// Stateless multi-hop stream where an intermediate converter needs state
// (responses -> claude goes via chat, but the responses->chat step needs state).
func TestConvertStreamResponseMultiHopRequiresState(t *testing.T) {
	_, err := ConvertStreamResponse(nil, nil, types.RelayFormatClaude, &dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires response stream state")
}

// ---------------------------------------------------------------------------
// Stateful streaming
// ---------------------------------------------------------------------------

func TestNewResponseStreamStateErrors(t *testing.T) {
	_, err := NewResponseStreamState("", types.RelayFormatOpenAI, ResponseStreamOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source relay format is required")

	_, err = NewResponseStreamState(types.RelayFormatOpenAI, "", ResponseStreamOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target relay format is required")

	_, err = NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatEmbedding, ResponseStreamOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

func TestNewResponseStreamStateSameFormat(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAI, ResponseStreamOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.RelayFormatOpenAI, state.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), state.To)
	assert.Empty(t, state.specs)
}

func TestNewResponseStreamStateByID(t *testing.T) {
	state, err := NewResponseStreamStateByID(ResponseConverterOAIChatToOAIResponses, ResponseStreamOptions{ID: "resp_1", Model: "gpt-test"})
	require.NoError(t, err)
	assert.Equal(t, types.RelayFormatOpenAI, state.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), state.To)

	_, err = NewResponseStreamStateByID("missing", ResponseStreamOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestConvertStreamResponseChunkNilState(t *testing.T) {
	_, err := ConvertStreamResponseChunk(nil, nil, nil, &dto.ChatCompletionsStreamResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response stream state is required")
}

func TestConvertStreamResponseChunkNilResponse(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_1"})
	require.NoError(t, err)
	_, err = ConvertStreamResponseChunk(nil, nil, state, (*dto.ChatCompletionsStreamResponse)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")
}

func TestConvertStreamResponseChunkSourceMismatch(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_1"})
	require.NoError(t, err)
	_, err = ConvertStreamResponseChunk(nil, nil, state, &dto.ClaudeResponse{Type: "message"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects openai response, got claude")
}

func TestConvertStreamResponseChunkSameFormatPassthrough(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAI, ResponseStreamOptions{})
	require.NoError(t, err)
	chunk := &dto.ChatCompletionsStreamResponse{Id: "x", Usage: &dto.Usage{TotalTokens: 6}}
	results, err := ConvertStreamResponseChunk(nil, nil, state, chunk)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Same(t, chunk, results[0].Value)
	assert.Equal(t, 6, state.Usage().TotalTokens)
}

func TestStatefulChatToResponsesStream(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_1", Model: "gpt-test"})
	require.NoError(t, err)

	results, err := ConvertStreamResponseChunk(nil, nil, state, &dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: respPtr("hello")}}},
		Usage:   &dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, results[0].Converter)
	assert.Equal(t, []ResponseStep{{Converter: ConverterOpenAIChatToOpenAIResponses, From: types.RelayFormatOpenAI, To: types.RelayFormatOpenAIResponses}}, results[0].Steps)
	assert.True(t, results[0].Stream)
	assert.Equal(t, 5, state.Usage().TotalTokens)

	final, err := FinalizeStreamResponse(nil, nil, state)
	require.NoError(t, err)
	require.NotEmpty(t, final)
	last, ok := final[len(final)-1].Value.(ChatToResponsesStreamEvent)
	require.True(t, ok)
	assert.Equal(t, "response.completed", last.Type)
}

func TestStatefulResponsesToChatStream(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, ResponseStreamOptions{ID: "chatcmpl_1", Model: "gpt-test"})
	require.NoError(t, err)
	results, err := ConvertStreamResponseChunk(nil, nil, state, &dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat, results[0].Converter)
	require.IsType(t, dto.ChatCompletionsStreamResponse{}, results[len(results)-1].Value)
}

func TestStatefulMultiHopResponsesToClaudeStream(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
	}
	state, err := NewResponseStreamState(types.RelayFormatOpenAIResponses, types.RelayFormatClaude, ResponseStreamOptions{ID: "chatcmpl_1", Model: "gpt-test"})
	require.NoError(t, err)

	results, err := ConvertStreamResponseChunk(nil, info, state, &dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, requestConverterResponsesToClaude, results[0].Converter)
	assert.Equal(t, []ResponseStep{
		{Converter: ConverterOpenAIResponsesToOpenAIChat, From: types.RelayFormatOpenAIResponses, To: types.RelayFormatOpenAI},
		{Converter: ConverterOpenAIChatToClaudeMessages, From: types.RelayFormatOpenAI, To: types.RelayFormatClaude},
	}, results[0].Steps)

	var sawTextDelta bool
	for _, result := range results {
		claudeResponse, ok := result.Value.(*dto.ClaudeResponse)
		if !ok || claudeResponse == nil {
			continue
		}
		if claudeResponse.Type == "content_block_delta" && claudeResponse.Delta != nil && claudeResponse.Delta.Text != nil && *claudeResponse.Delta.Text == "hello" {
			sawTextDelta = true
		}
	}
	assert.True(t, sawTextDelta)

	state.SetUsage(&dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5})
	_, err = FinalizeStreamResponse(nil, info, state)
	require.NoError(t, err)
	assert.Equal(t, 5, state.Usage().TotalTokens)
}

func TestFinalizeStreamResponseNilState(t *testing.T) {
	_, err := FinalizeStreamResponse(nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response stream state is required")
}

func TestFinalizeStreamResponseSameFormatNoop(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAI, ResponseStreamOptions{})
	require.NoError(t, err)
	results, err := FinalizeStreamResponse(nil, nil, state)
	require.NoError(t, err)
	assert.Nil(t, results)
}

// ---------------------------------------------------------------------------
// ResponseStreamState.{Usage, SetUsage, UsageText}
// ---------------------------------------------------------------------------

func TestResponseStreamStateUsageNilReceiver(t *testing.T) {
	var s *ResponseStreamState
	assert.Nil(t, s.Usage())
	assert.NotPanics(t, func() { s.SetUsage(&dto.Usage{}) })
	assert.Equal(t, "", s.UsageText())
}

func TestResponseStreamStateUsageRemembered(t *testing.T) {
	s := &ResponseStreamState{}
	assert.Nil(t, s.Usage())
	s.SetUsage(nil) // guarded no-op
	assert.Nil(t, s.Usage())
	s.SetUsage(&dto.Usage{TotalTokens: 12})
	require.NotNil(t, s.Usage())
	assert.Equal(t, 12, s.Usage().TotalTokens)
}

func TestResponseStreamStateUsageFromStepState(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_1", Model: "gpt-test"})
	require.NoError(t, err)
	// Usage() falls through to inspect ChatToResponsesStreamState.Usage.
	require.Len(t, state.stepStates, 1)
	chatState, ok := state.stepStates[0].(*ChatToResponsesStreamState)
	require.True(t, ok)
	chatState.Usage = &dto.Usage{TotalTokens: 21}
	require.NotNil(t, state.Usage())
	assert.Equal(t, 21, state.Usage().TotalTokens)

	// SetUsage propagates into the ChatToResponsesStreamState.
	state.SetUsage(&dto.Usage{PromptTokens: 4, CompletionTokens: 6, TotalTokens: 10})
	require.NotNil(t, chatState.Usage)
}

func TestResponseStreamStateUsageFromResponsesToChatStepState(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, ResponseStreamOptions{ID: "chatcmpl_1", Model: "gpt-test"})
	require.NoError(t, err)
	require.Len(t, state.stepStates, 1)
	respState, ok := state.stepStates[0].(*ResponsesToChatStreamState)
	require.True(t, ok)
	respState.Usage = &dto.Usage{TotalTokens: 33}
	require.NotNil(t, state.Usage())
	assert.Equal(t, 33, state.Usage().TotalTokens)

	state.SetUsage(&dto.Usage{TotalTokens: 44})
	assert.Equal(t, 44, respState.Usage.TotalTokens)
}

// ---------------------------------------------------------------------------
// expandResponseConverterSteps
// ---------------------------------------------------------------------------

func TestExpandResponseConverterStepsDirectNoImpl(t *testing.T) {
	_, err := expandResponseConverterSteps(ResponseConverterSpec{ID: "x", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no registered implementation")
}

func TestExpandResponseConverterStepsMissingStep(t *testing.T) {
	_, err := expandResponseConverterSteps(ResponseConverterSpec{ID: "x", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude, StepConverters: []string{"unknown"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references missing step converter")
}

func TestExpandResponseConverterStepsStepNotDirect(t *testing.T) {
	_, err := expandResponseConverterSteps(ResponseConverterSpec{ID: "x", From: types.RelayFormatClaude, To: types.RelayFormatGemini, StepConverters: []string{requestConverterClaudeToGemini}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a direct converter")
}

func TestExpandResponseConverterStepsFromMismatch(t *testing.T) {
	_, err := expandResponseConverterSteps(ResponseConverterSpec{ID: "x", From: types.RelayFormatGemini, To: types.RelayFormatClaude, StepConverters: []string{ConverterOpenAIChatToClaudeMessages}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects openai response")
}

func TestExpandResponseConverterStepsWrongEnd(t *testing.T) {
	_, err := expandResponseConverterSteps(ResponseConverterSpec{ID: "x", From: types.RelayFormatOpenAI, To: types.RelayFormatGemini, StepConverters: []string{ConverterOpenAIChatToClaudeMessages}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ends at claude, expected gemini")
}

// ---------------------------------------------------------------------------
// Low-level executor error paths
// ---------------------------------------------------------------------------

func TestExecuteResponseStepNoNonStreamImpl(t *testing.T) {
	spec := ResponseConverterSpec{ID: "stream_only", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude}
	_, _, _, err := executeResponseStep(nil, nil, spec, &dto.OpenAITextResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no non-stream implementation")
}

func TestExecuteResponseStepPropagatesError(t *testing.T) {
	sentinel := errors.New("resp boom")
	spec := ResponseConverterSpec{
		ID: "err", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude,
		Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, *dto.Usage, error) {
			return nil, nil, sentinel
		},
	}
	_, _, _, err := executeResponseStep(nil, nil, spec, &dto.OpenAITextResponse{})
	require.ErrorIs(t, err, sentinel)
}

func TestExecuteResponseStreamStepNoImpl(t *testing.T) {
	spec := ResponseConverterSpec{ID: "no_stream", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude}
	_, _, err := executeResponseStreamStep(nil, nil, spec, nil, &dto.OpenAITextResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no stream implementation")
}

func TestFinalizeResponseStreamStepNoFinalizer(t *testing.T) {
	values, usage, err := finalizeResponseStreamStep(nil, nil, ResponseConverterSpec{ID: "x"}, nil)
	require.NoError(t, err)
	assert.Nil(t, values)
	assert.Nil(t, usage)
}

// ---------------------------------------------------------------------------
// prepareResponseStreamInfo — SendResponseCount bump only for openai->claude/gemini
// ---------------------------------------------------------------------------

func TestPrepareResponseStreamInfo(t *testing.T) {
	assert.NotPanics(t, func() { prepareResponseStreamInfo(nil, ResponseConverterSpec{}) })

	info := &relaycommon.RelayInfo{}
	prepareResponseStreamInfo(info, ResponseConverterSpec{From: types.RelayFormatClaude, To: types.RelayFormatOpenAI})
	assert.Equal(t, 0, info.SendResponseCount, "non-openai source does not bump")

	prepareResponseStreamInfo(info, ResponseConverterSpec{From: types.RelayFormatOpenAI, To: types.RelayFormatOpenAIResponses})
	assert.Equal(t, 0, info.SendResponseCount, "openai->responses does not bump")

	prepareResponseStreamInfo(info, ResponseConverterSpec{From: types.RelayFormatOpenAI, To: types.RelayFormatClaude})
	assert.Equal(t, 1, info.SendResponseCount, "openai->claude bumps")

	prepareResponseStreamInfo(info, ResponseConverterSpec{From: types.RelayFormatOpenAI, To: types.RelayFormatGemini})
	assert.Equal(t, 2, info.SendResponseCount, "openai->gemini bumps")
}

// ---------------------------------------------------------------------------
// responseStreamResults edge cases
// ---------------------------------------------------------------------------

func TestResponseStreamResultsEdgeCases(t *testing.T) {
	assert.Nil(t, responseStreamResults(nil, []any{1}, nil))
	assert.Nil(t, responseStreamResults(&ResponseStreamState{}, nil, nil))
}

// ---------------------------------------------------------------------------
// cloneResponseConverterSpec — deep-copy independence
// ---------------------------------------------------------------------------

func TestCloneResponseConverterSpecIndependence(t *testing.T) {
	spec, ok := LookupResponseConverter(responseConverterResponsesToClaude)
	require.True(t, ok)
	require.NotEmpty(t, spec.StepConverters)
	spec.StepConverters[0] = "mutated"

	fresh, ok := LookupResponseConverter(responseConverterResponsesToClaude)
	require.True(t, ok)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat, fresh.StepConverters[0])
}

// ---------------------------------------------------------------------------
// registerBuiltinResponseConverter — validation panics
// ---------------------------------------------------------------------------

func TestRegisterBuiltinResponseConverterPanics(t *testing.T) {
	noop := func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, *dto.Usage, error) { return nil, nil, nil }

	tests := []struct {
		name string
		spec ResponseConverterSpec
		msg  string
	}{
		{"empty id", ResponseConverterSpec{}, "ID is required"},
		{"missing from/to", ResponseConverterSpec{ID: "r1", Quality: ResponseConverterQualityFair, Convert: noop}, "must declare from and to"},
		{"missing quality", ResponseConverterSpec{ID: "r2", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Convert: noop}, "must declare quality"},
		{"no impl", ResponseConverterSpec{ID: "r3", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: ResponseConverterQualityFair}, "must declare convert, stream convert, or step converters"},
		{"direct and steps", ResponseConverterSpec{ID: "r4", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: ResponseConverterQualityFair, Convert: noop, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "cannot declare direct implementations and step converters together"},
		{"duplicate id", ResponseConverterSpec{ID: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: ResponseConverterQualityFair, Convert: noop}, "is already registered"},
		{"duplicate route", ResponseConverterSpec{ID: "r5", From: types.RelayFormatClaude, To: types.RelayFormatOpenAI, Quality: ResponseConverterQualityFair, Convert: noop}, "route from claude to openai is already registered"},
		{"unknown step", ResponseConverterSpec{ID: "r6", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: ResponseConverterQualityFair, StepConverters: []string{"unknown"}}, "references unknown step converter"},
		{"step not direct", ResponseConverterSpec{ID: "r7", From: types.RelayFormat("fnd-a"), To: types.RelayFormat("fnd-b"), Quality: ResponseConverterQualityFair, StepConverters: []string{requestConverterClaudeToGemini}}, "must be a direct converter"},
		{"step from mismatch", ResponseConverterSpec{ID: "r8", From: types.RelayFormat("ffm-a"), To: types.RelayFormatOpenAI, Quality: ResponseConverterQualityFair, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "expects claude after ffm-a"},
		{"step wrong end", ResponseConverterSpec{ID: "r9", From: types.RelayFormatClaude, To: types.RelayFormat("fwe-b"), Quality: ResponseConverterQualityFair, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "ends at openai, expected fwe-b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanicContains(t, tt.msg, func() {
				registerBuiltinResponseConverter(tt.spec)
			})
		})
	}
}

// ---------------------------------------------------------------------------
// registerResponseConverterAlias — panics + no-op branch
// ---------------------------------------------------------------------------

func TestRegisterResponseConverterAliasNoopSameName(t *testing.T) {
	assert.NotPanics(t, func() {
		registerResponseConverterAlias(ConverterClaudeMessagesToOpenAIChat, ConverterClaudeMessagesToOpenAIChat)
	})
}

func TestRegisterResponseConverterAliasPanics(t *testing.T) {
	tests := []struct {
		name     string
		alias    string
		target   string
		msg      string
	}{
		{"empty alias", "", ConverterClaudeMessagesToOpenAIChat, "alias is required"},
		{"empty target", "some_alias", "", "target is required"},
		{"alias conflicts with converter", ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToClaudeMessages, "conflicts with registered converter"},
		{"unknown target", "brand_new_alias", "not_a_converter", "references unknown converter"},
		{"already registered different", ResponseConverterClaudeMessagesToOAIChat, ConverterOpenAIChatToClaudeMessages, "is already registered for"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanicContains(t, tt.msg, func() {
				registerResponseConverterAlias(tt.alias, tt.target)
			})
		})
	}
}
