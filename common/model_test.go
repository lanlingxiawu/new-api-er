package common

import (
	"testing"

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
