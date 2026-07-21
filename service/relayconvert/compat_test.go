package relayconvert

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGinContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

// ---------------------------------------------------------------------------
// request_compat.go — thin re-export wrappers
// ---------------------------------------------------------------------------

func TestRequestCompatWrappers(t *testing.T) {
	c := testGinContext()
	info := &relaycommon.RelayInfo{}

	t.Run("ClaudeMessagesRequestToOpenAIChat", func(t *testing.T) {
		out, err := ClaudeMessagesRequestToOpenAIChat(*newClaudeRequest(), info)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("OpenAIChatRequestToClaudeMessages", func(t *testing.T) {
		out, err := OpenAIChatRequestToClaudeMessages(c, *newOpenAIRequest())
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("GeminiGenerateContentRequestToOpenAIChat", func(t *testing.T) {
		out, err := GeminiGenerateContentRequestToOpenAIChat(newGeminiRequest(), info)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("OpenAIChatRequestToGeminiGenerateContent", func(t *testing.T) {
		out, err := OpenAIChatRequestToGeminiGenerateContent(c, *newOpenAIRequest(), info)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("ApplyGeminiThinkingConfig", func(t *testing.T) {
		assert.NotPanics(t, func() {
			ApplyGeminiThinkingConfig(newGeminiRequest(), info)
		})
	})
	t.Run("ChatCompletionsRequestToResponsesRequest", func(t *testing.T) {
		out, err := ChatCompletionsRequestToResponsesRequest(newOpenAIRequest())
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("ResponsesRequestToChatCompletionsRequest", func(t *testing.T) {
		out, err := ResponsesRequestToChatCompletionsRequest(newResponsesRequest(t))
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("OpenAIResponsesRequestToClaudeMessages", func(t *testing.T) {
		out, err := OpenAIResponsesRequestToClaudeMessages(c, newResponsesRequest(t))
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("OpenAIResponsesRequestToGeminiChat", func(t *testing.T) {
		out, err := OpenAIResponsesRequestToGeminiChat(c, newResponsesRequest(t), info)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
	t.Run("ShouldChatCompletionsUseResponsesPolicy", func(t *testing.T) {
		policy := model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, AllChannels: true}
		assert.NotPanics(t, func() { ShouldChatCompletionsUseResponsesPolicy(policy, 1, 1, "gpt-test") })
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(model_setting.ChatCompletionsToResponsesPolicy{}, 1, 1, "gpt-test"))
	})
	t.Run("ShouldChatCompletionsUseResponsesGlobal", func(t *testing.T) {
		assert.NotPanics(t, func() {
			ShouldChatCompletionsUseResponsesGlobal(1, 1, "gpt-test")
		})
	})
}

// ---------------------------------------------------------------------------
// response_compat.go — thin re-export wrappers
// ---------------------------------------------------------------------------

func claudeStreamResponse() *dto.ClaudeResponse {
	idx := 0
	return &dto.ClaudeResponse{
		Id:    "msg_1",
		Type:  "content_block_delta",
		Model: "claude-test",
		Index: &idx,
		Delta: &dto.ClaudeMediaMessage{Type: "text_delta", Text: respPtr("hello")},
		Usage: &dto.ClaudeUsage{InputTokens: 4, OutputTokens: 2},
	}
}

func TestResponseCompatWrappers(t *testing.T) {
	info := &relaycommon.RelayInfo{}

	t.Run("NormalizeCacheCreationSplit", func(t *testing.T) {
		a, b := NormalizeCacheCreationSplit(10, 4, 6)
		assert.Equal(t, 4, a)
		assert.Equal(t, 6, b)
	})
	t.Run("StopReasonClaudeToOpenAI", func(t *testing.T) {
		assert.NotEmpty(t, StopReasonClaudeToOpenAI("end_turn"))
	})
	t.Run("StreamResponseClaude2OpenAI", func(t *testing.T) {
		assert.NotNil(t, StreamResponseClaude2OpenAI(claudeStreamResponse()))
	})
	t.Run("UsageFromClaudeUsage", func(t *testing.T) {
		usage := UsageFromClaudeUsage(&dto.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5})
		require.NotNil(t, usage)
	})
	t.Run("BuildMessageDeltaPatchUsage", func(t *testing.T) {
		info := &ClaudeResponseInfo{Usage: &dto.Usage{PromptTokens: 3, CompletionTokens: 2}}
		assert.NotPanics(t, func() {
			BuildMessageDeltaPatchUsage(claudeStreamResponse(), info)
		})
	})
	t.Run("PatchClaudeMessageDeltaUsageData", func(t *testing.T) {
		out := PatchClaudeMessageDeltaUsageData(`{"type":"message_delta","usage":{"output_tokens":1}}`, &dto.ClaudeUsage{OutputTokens: 2})
		assert.NotEmpty(t, out)
	})
	t.Run("FormatClaudeResponseInfo", func(t *testing.T) {
		claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
		oaiResp := &dto.ChatCompletionsStreamResponse{}
		assert.NotPanics(t, func() {
			FormatClaudeResponseInfo(claudeStreamResponse(), oaiResp, claudeInfo)
		})
	})
	t.Run("StreamResponseOpenAI2Gemini", func(t *testing.T) {
		stream := &dto.ChatCompletionsStreamResponse{
			Id:      "chatcmpl_1",
			Model:   "gpt-test",
			Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: respPtr("hi")}}},
		}
		assert.NotNil(t, StreamResponseOpenAI2Gemini(stream, info))
	})
	t.Run("ResponsesStatusFromChatFinishReason", func(t *testing.T) {
		status, _ := ResponsesStatusFromChatFinishReason("stop")
		assert.NotEmpty(t, status)
	})
	t.Run("ResponsesFinishReasonFromStatus", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)}
		assert.NotPanics(t, func() {
			ResponsesFinishReasonFromStatus(resp)
		})
	})
	t.Run("ExtractOutputTextFromResponses", func(t *testing.T) {
		assert.Equal(t, "hello", ExtractOutputTextFromResponses(textRegistryResponsesResponse()))
	})
	t.Run("ExtractReasoningTextFromResponses", func(t *testing.T) {
		assert.NotPanics(t, func() {
			ExtractReasoningTextFromResponses(textRegistryResponsesResponse())
		})
	})
	t.Run("NewResponsesBufferedAccumulator", func(t *testing.T) {
		assert.NotNil(t, NewResponsesBufferedAccumulator())
	})
}

// ---------------------------------------------------------------------------
// Stateless stream branches not otherwise reached
// ---------------------------------------------------------------------------

func TestConvertStreamResponseClaudeToOpenAI(t *testing.T) {
	result, err := ConvertStreamResponse(nil, &relaycommon.RelayInfo{}, types.RelayFormatOpenAI, claudeStreamResponse())
	require.NoError(t, err)
	assert.True(t, result.Stream)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, result.Converter)
}

func TestConvertStreamResponseOpenAIToGemini(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"}}
	stream := &dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: respPtr("hi")}}},
		Usage:   &dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	result, err := ConvertStreamResponse(nil, info, types.RelayFormat(types.RelayFormatGemini), stream)
	require.NoError(t, err)
	assert.True(t, result.Stream)
	assert.Equal(t, ConverterOpenAIChatToGeminiContent, result.Converter)
}
