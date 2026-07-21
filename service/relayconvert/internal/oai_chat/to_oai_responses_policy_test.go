package oaichat

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/QuantumNous/new-api/setting/model_setting"
)

func TestShouldChatCompletionsUseResponsesPolicy(t *testing.T) {
	t.Run("disabled policy short-circuits", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       false,
			AllChannels:   true,
			ModelPatterns: []string{".*"},
		}
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4"))
	})

	t.Run("enabled all channels, model matches", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       true,
			AllChannels:   true,
			ModelPatterns: []string{"^gpt-"},
		}
		assert.True(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4"))
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "claude-3"))
	})

	t.Run("channel enabled but model pattern does not match", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       true,
			AllChannels:   true,
			ModelPatterns: []string{"^o1"},
		}
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4"))
	})

	t.Run("by channel id", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       true,
			ChannelIDs:    []int{42},
			ModelPatterns: []string{".*"},
		}
		assert.True(t, ShouldChatCompletionsUseResponsesPolicy(p, 42, 0, "anything"))
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 7, 0, "anything"))
	})

	t.Run("by channel type", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       true,
			ChannelTypes:  []int{3},
			ModelPatterns: []string{".*"},
		}
		assert.True(t, ShouldChatCompletionsUseResponsesPolicy(p, 0, 3, "anything"))
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 0, 9, "anything"))
	})

	t.Run("no model patterns never matches", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:     true,
			AllChannels: true,
		}
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4"))
	})
}

func TestShouldChatCompletionsUseResponsesGlobal(t *testing.T) {
	// The default global policy is disabled, so this must return false without
	// mutating global settings.
	assert.False(t, ShouldChatCompletionsUseResponsesGlobal(1, 1, "gpt-4"))
}
