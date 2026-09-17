package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestIsOpenAIResponseOnlyModel(t *testing.T) {
	assert.True(t, IsOpenAIResponseOnlyModel("o3-pro"))
	assert.True(t, IsOpenAIResponseOnlyModel("my-o3-deep-research-x"))
	assert.True(t, IsOpenAIResponseOnlyModel("o4-mini-deep-research"))
	assert.False(t, IsOpenAIResponseOnlyModel("gpt-4o"))
	assert.False(t, IsOpenAIResponseOnlyModel(""))
}

func TestIsImageGenerationModel(t *testing.T) {
	assert.True(t, IsImageGenerationModel("dall-e-3"))
	assert.True(t, IsImageGenerationModel("DALL-E-2")) // lowercased internally
	assert.True(t, IsImageGenerationModel("gpt-image-1"))
	assert.True(t, IsImageGenerationModel("flux-pro"))
	// prefix: matcher
	assert.True(t, IsImageGenerationModel("imagen-3.0"))
	assert.False(t, IsImageGenerationModel("x-imagen-3.0")) // prefix must be at start
	assert.False(t, IsImageGenerationModel("gpt-4o"))
}

func TestIsOpenAITextModel(t *testing.T) {
	assert.True(t, IsOpenAITextModel("gpt-4o"))
	assert.True(t, IsOpenAITextModel("O1-preview"))
	assert.True(t, IsOpenAITextModel("chatgpt-4o-latest"))
	assert.False(t, IsOpenAITextModel("claude-3"))
	assert.False(t, IsOpenAITextModel(""))
}

func TestWanEndpointsDistinguishImagesFromVideos(t *testing.T) {
	for _, name := range []string{
		"wan2.7-image-pro", "wan2.7-image", "wan2.6-image", "wan2.6-t2i",
		"wan2.5-t2i-preview", "wan2.2-t2i-flash", "wan2.2-t2i-plus",
		"wanx2.1-t2i-turbo", "wanx2.1-t2i-plus", "wanx2.0-t2i-turbo",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, GetEndpointTypesByChannelType(constant.ChannelTypeAli, name), constant.EndpointTypeImageGeneration)
		})
	}
	for _, name := range []string{
		"wanx2.1-t2v-plus", "wanx2.1-t2v-turbo", "wanx2.1-i2v-plus", "wanx2.1-i2v-turbo",
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeAli, name), constant.EndpointTypeImageGeneration)
		})
	}
}
