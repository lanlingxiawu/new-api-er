package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func newClaudeConvertTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func openAIStreamChunk(content string, finishReason *string, usage *dto.Usage) *dto.ChatCompletionsStreamResponse {
	delta := dto.ChatCompletionsStreamResponseChoiceDelta{}
	if content != "" {
		delta.Content = common.GetPointer(content)
	}
	return &dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl-test",
		Created: 1,
		Model:   "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}
}

func claudeResponseTypes(responses []*dto.ClaudeResponse) []string {
	types := make([]string, 0, len(responses))
	for _, response := range responses {
		types = append(types, response.Type)
	}
	return types
}

func TestStreamResponseOpenAI2ClaudeDoesNotRepeatMessageStartOnFinalChunk(t *testing.T) {
	info := newClaudeConvertTestInfo()
	info.SendResponseCount = 1

	firstResponses := service.StreamResponseOpenAI2Claude(openAIStreamChunk("hello", nil, nil), info)
	require.Equal(t, []string{"message_start", "content_block_start", "content_block_delta"}, claudeResponseTypes(firstResponses))
	require.True(t, info.ClaudeConvertInfo.MessageStartSent)

	finalResponses := service.StreamResponseOpenAI2Claude(openAIStreamChunk("", common.GetPointer("stop"), &dto.Usage{
		PromptTokens:     3,
		CompletionTokens: 1,
	}), info)

	require.Equal(t, []string{"content_block_stop", "message_delta", "message_stop"}, claudeResponseTypes(finalResponses))
	require.True(t, info.ClaudeConvertInfo.Done)
}

func TestStreamResponseOpenAI2ClaudeCanForceCloseAfterSingleContentChunk(t *testing.T) {
	info := newClaudeConvertTestInfo()

	firstResponses := service.StreamResponseOpenAI2Claude(openAIStreamChunk("hello", nil, nil), info)
	require.Equal(t, []string{"message_start", "content_block_start", "content_block_delta"}, claudeResponseTypes(firstResponses))

	closeResponses := service.StreamResponseOpenAI2Claude(openAIStreamChunk("", common.GetPointer("stop"), &dto.Usage{
		PromptTokens:     3,
		CompletionTokens: 1,
	}), info)

	require.Equal(t, []string{"content_block_stop", "message_delta", "message_stop"}, claudeResponseTypes(closeResponses))
	require.True(t, info.ClaudeConvertInfo.Done)
}
