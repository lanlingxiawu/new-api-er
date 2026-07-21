package reasoning

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TrimEffortSuffixWithSuffixes is the core; lo.Find returns the FIRST suffix (in
// slice order) that modelName ends with.

func TestTrimEffortSuffixWithSuffixes(t *testing.T) {
	cases := []struct {
		name       string
		model      string
		suffixes   []string
		wantBase   string
		wantEffort string
		wantOK     bool
	}{
		{"matches high", "gpt-high", EffortSuffixes, "gpt", "high", true},
		{"matches max", "model-max", EffortSuffixes, "model", "max", true},
		{"matches minimal", "m-minimal", EffortSuffixes, "m", "minimal", true},
		{"xhigh not confused with high", "foo-xhigh", EffortSuffixes, "foo", "xhigh", true},
		{"no suffix returns original", "gpt-4", EffortSuffixes, "gpt-4", "", false},
		{"empty string", "", EffortSuffixes, "", "", false},
		{"dash prefix stripped from effort", "a-low", EffortSuffixes, "a", "low", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, effort, ok := TrimEffortSuffixWithSuffixes(tc.model, tc.suffixes)
			assert.Equal(t, tc.wantBase, base)
			assert.Equal(t, tc.wantEffort, effort)
			assert.Equal(t, tc.wantOK, ok)
		})
	}
}

func TestTrimEffortSuffix_UsesEffortSuffixes(t *testing.T) {
	base, effort, ok := TrimEffortSuffix("claude-medium")
	assert.True(t, ok)
	assert.Equal(t, "claude", base)
	assert.Equal(t, "medium", effort)

	base, effort, ok = TrimEffortSuffix("claude")
	assert.False(t, ok)
	assert.Equal(t, "claude", base)
	assert.Equal(t, "", effort)
}

func TestParseOpenAIReasoningEffortFromModelSuffix(t *testing.T) {
	// Returns (effort, baseModel) — note the order.
	effort, base := ParseOpenAIReasoningEffortFromModelSuffix("o1-high")
	assert.Equal(t, "high", effort)
	assert.Equal(t, "o1", base)

	// "-none" is an OpenAI-specific suffix.
	effort, base = ParseOpenAIReasoningEffortFromModelSuffix("gpt-5-none")
	assert.Equal(t, "none", effort)
	assert.Equal(t, "gpt-5", base)

	// No suffix => empty effort, original model returned as the "base" slot.
	effort, base = ParseOpenAIReasoningEffortFromModelSuffix("gpt-4")
	assert.Equal(t, "", effort)
	assert.Equal(t, "gpt-4", base)
}

func TestParseDeepSeekV4ThinkingSuffix(t *testing.T) {
	cases := []struct {
		name         string
		model        string
		wantBase     string
		wantThinking string
		wantEffort   string
		wantOK       bool
	}{
		{"none => disabled", "deepseek-v4-chat-none", "deepseek-v4-chat", "disabled", "", true},
		{"max => enabled/max", "deepseek-v4-chat-max", "deepseek-v4-chat", "enabled", "max", true},
		{"suffix present but wrong family", "gpt-4-none", "gpt-4-none", "", "", false},
		{"deepseek family but no suffix", "deepseek-v4-chat", "deepseek-v4-chat", "", "", false},
		{"unrelated model", "llama-3", "llama-3", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, thinking, effort, ok := ParseDeepSeekV4ThinkingSuffix(tc.model)
			assert.Equal(t, tc.wantBase, base)
			assert.Equal(t, tc.wantThinking, thinking)
			assert.Equal(t, tc.wantEffort, effort)
			assert.Equal(t, tc.wantOK, ok)
		})
	}
}
