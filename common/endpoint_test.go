package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestGetEndpointTypesByChannelType(t *testing.T) {
	et := func(v ...constant.EndpointType) []constant.EndpointType { return v }

	cases := []struct {
		name    string
		channel int
		model   string
		want    []constant.EndpointType
	}{
		{"jina rerank", constant.ChannelTypeJina, "rerank-model",
			et(constant.EndpointTypeJinaRerank)},
		{"anthropic", constant.ChannelTypeAnthropic, "claude-3",
			et(constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI)},
		{"aws falls through to anthropic", constant.ChannelTypeAws, "claude-3",
			et(constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI)},
		{"gemini", constant.ChannelTypeGemini, "gemini-pro",
			et(constant.EndpointTypeGemini, constant.EndpointTypeOpenAI)},
		{"vertex falls through to gemini", constant.ChannelTypeVertexAi, "gemini-pro",
			et(constant.EndpointTypeGemini, constant.EndpointTypeOpenAI)},
		{"openrouter openai only", constant.ChannelTypeOpenRouter, "some-model",
			et(constant.EndpointTypeOpenAI)},
		{"xai", constant.ChannelTypeXai, "grok",
			et(constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse)},
		{"sora video", constant.ChannelTypeSora, "sora",
			et(constant.EndpointTypeOpenAIVideo)},
		{"default openai", constant.ChannelTypeDeepSeek, "deepseek-chat",
			et(constant.EndpointTypeOpenAI)},
		{"default response-only model", constant.ChannelTypeDeepSeek, "o3-pro",
			et(constant.EndpointTypeOpenAIResponse)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetEndpointTypesByChannelType(tc.channel, tc.model))
		})
	}
}

func TestGetEndpointTypesByChannelType_ImageGenerationPrepended(t *testing.T) {
	got := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "dall-e-3")
	assert.Equal(t, constant.EndpointTypeImageGeneration, got[0])
	assert.Contains(t, got, constant.EndpointTypeOpenAI)
}

func TestGetDefaultEndpointInfo(t *testing.T) {
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeOpenAI)
	assert.True(t, ok)
	assert.Equal(t, "/v1/chat/completions", info.Path)
	assert.Equal(t, "POST", info.Method)

	info, ok = GetDefaultEndpointInfo(constant.EndpointTypeAnthropic)
	assert.True(t, ok)
	assert.Equal(t, "/v1/messages", info.Path)

	_, ok = GetDefaultEndpointInfo(constant.EndpointType("nonexistent-endpoint"))
	assert.False(t, ok)
}
