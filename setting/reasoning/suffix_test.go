package reasoning

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCanonicalBillingModelNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "thinking on",
			in:   "qwen3-max@thinking:on",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "shuffled temperature and thinking",
			in:   "qwen3-max@temperature:0.2@thinking:on",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "thinking first then temperature",
			in:   "qwen3-max@thinking:on@temperature:0.2",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "budget normalizes to on",
			in:   "qwen3-max@thinking:8192",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "minus one normalizes to on",
			in:   "qwen3-max@thinking:-1",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "adaptive normalizes to on",
			in:   "qwen3-max@thinking:adaptive",
			want: []string{"qwen3-max@thinking:on"},
		},
		{
			name: "thinking off",
			in:   "qwen3-max@thinking:off",
			want: []string{"qwen3-max@thinking:off"},
		},
		{
			name: "effort none becomes thinking off",
			in:   "qwen3-max@effort:none",
			want: []string{"qwen3-max@thinking:off"},
		},
		{
			name: "effort high implies thinking on",
			in:   "qwen3-max@effort:high",
			want: []string{"qwen3-max@effort:high@thinking:on", "qwen3-max@thinking:on"},
		},
		{
			name: "effort and thinking keys sorted",
			in:   "qwen3-max@thinking:on@effort:high@temperature:0.2",
			want: []string{"qwen3-max@effort:high@thinking:on", "qwen3-max@thinking:on"},
		},
		{
			name: "duplicate last wins then normalize",
			in:   "qwen3-max@thinking:off@thinking:on@effort:low@effort:high",
			want: []string{"qwen3-max@effort:high@thinking:on", "qwen3-max@thinking:on"},
		},
		{
			name: "legacy thinking alias",
			in:   "claude-3-7-sonnet-thinking",
			want: []string{"claude-3-7-sonnet@thinking:on"},
		},
		{
			name: "legacy thinking budget matches explicit budget",
			in:   "gemini-2.5-flash-thinking-8192",
			want: []string{"gemini-2.5-flash@thinking:on"},
		},
		{
			name: "legacy nothinking",
			in:   "claude-3-7-sonnet-nothinking",
			want: []string{"claude-3-7-sonnet@thinking:off"},
		},
		{
			name: "temperature only has no reasoning state",
			in:   "qwen3-max@temperature:0.7",
			want: nil,
		},
	}

	originalGemini := *model_setting.GetGeminiSettings()
	t.Cleanup(func() { model_setting.ReplaceGeminiSettings(originalGemini) })
	geminiSettings := originalGemini
	geminiSettings.ThinkingAdapterEnabled = true
	model_setting.ReplaceGeminiSettings(geminiSettings)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CanonicalBillingModelNames(tt.in))
		})
	}

	assert.Equal(t,
		CanonicalBillingModelNames("gemini-2.5-flash@thinking:8192"),
		CanonicalBillingModelNames("gemini-2.5-flash-thinking-8192"),
	)
	assert.Equal(t, "gpt-5.1-codex-max", BaseModelName("gpt-5.1-codex-max"))
	assert.Empty(t, CanonicalBillingModelNames("gpt-5.1-codex-max"))
}

func TestParseOpenAIReasoningEffortPreservesCodexMax(t *testing.T) {
	effort, base := ParseOpenAIReasoningEffortFromModelSuffix("gpt-5.1-codex-max")
	assert.Empty(t, effort)
	assert.Equal(t, "gpt-5.1-codex-max", base)
}

func TestBaseModelNameStripsModifiers(t *testing.T) {
	require.Equal(t, "qwen3-max", BaseModelName("qwen3-max@thinking:on@temperature:0.2"))
}

func TestExemptAtNameIsOpaqueForBillingIdentity(t *testing.T) {
	original := *model_setting.GetGlobalSettings()
	t.Cleanup(func() { model_setting.ReplaceGlobalSettings(original) })
	settings := original
	settings.ThinkingModelBlacklist = append(append([]string(nil), original.ThinkingModelBlacklist...), "re:.*@sha256:.*")
	model_setting.ReplaceGlobalSettings(settings)

	const model = "opaque@sha256:deadbeef"
	assert.Equal(t, model, BaseModelName(model))
	assert.Empty(t, CanonicalBillingModelNames(model))
	assert.Equal(t, "kimi-k2-thinking", BaseModelName("kimi-k2-thinking"))
	assert.Empty(t, CanonicalBillingModelNames("kimi-k2-thinking"))
}
