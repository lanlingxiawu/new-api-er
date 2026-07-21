package reasonmap

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
)

// TestClaudeStopReasonToOpenAIFinishReason covers every switch case plus the
// default pass-through and the case-insensitivity of the ToLower normalization.
func TestClaudeStopReasonToOpenAIFinishReason(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"stop_sequence", "stop"},
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"refusal", constant.FinishReasonContentFilter},
		// default: unknown reason passes through unchanged
		{"something_else", "something_else"},
		{"", ""},
		// case-insensitive: ToLower normalizes before matching
		{"END_TURN", "stop"},
		{"Tool_Use", "tool_calls"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, ClaudeStopReasonToOpenAIFinishReason(tc.in))
		})
	}
}

// TestOpenAIFinishReasonToClaudeStopReason covers every switch case, including
// the two-value "length"/"max_tokens" case, the content_filter constant case,
// the default pass-through, and case-insensitivity.
func TestOpenAIFinishReasonToClaudeStopReason(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"stop", "end_turn"},
		{"stop_sequence", "stop_sequence"},
		{"length", "max_tokens"},
		{"max_tokens", "max_tokens"},
		{constant.FinishReasonContentFilter, "refusal"},
		{"tool_calls", "tool_use"},
		// default: unknown reason passes through unchanged
		{"mystery", "mystery"},
		{"", ""},
		// case-insensitive
		{"STOP", "end_turn"},
		{"Length", "max_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, OpenAIFinishReasonToClaudeStopReason(tc.in))
		})
	}
}

// TestRoundTrip_StopReason documents the (lossy) round-trip semantics for the
// canonical reasons that map back to themselves, and where the mapping is
// intentionally not bijective (stop_sequence -> stop -> end_turn).
func TestRoundTrip_StopReason(t *testing.T) {
	// end_turn <-> stop is a stable round trip
	assert.Equal(t, "stop", ClaudeStopReasonToOpenAIFinishReason("end_turn"))
	assert.Equal(t, "end_turn", OpenAIFinishReasonToClaudeStopReason("stop"))

	// tool_use <-> tool_calls is a stable round trip
	assert.Equal(t, "tool_calls", ClaudeStopReasonToOpenAIFinishReason("tool_use"))
	assert.Equal(t, "tool_use", OpenAIFinishReasonToClaudeStopReason("tool_calls"))

	// refusal <-> content_filter is a stable round trip
	assert.Equal(t, constant.FinishReasonContentFilter, ClaudeStopReasonToOpenAIFinishReason("refusal"))
	assert.Equal(t, "refusal", OpenAIFinishReasonToClaudeStopReason(constant.FinishReasonContentFilter))

	// non-bijective: stop_sequence collapses to stop, which maps back to end_turn
	openai := ClaudeStopReasonToOpenAIFinishReason("stop_sequence") // "stop"
	assert.Equal(t, "stop", openai)
	assert.Equal(t, "end_turn", OpenAIFinishReasonToClaudeStopReason(openai))
}
