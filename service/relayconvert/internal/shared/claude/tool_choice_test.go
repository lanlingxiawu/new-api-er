package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MapOpenAIToolChoice maps OpenAI tool_choice (string or {function:{name}}) to
// the Claude tool_choice shape, and folds parallel_tool_calls into
// disable_parallel_tool_use (except when type=="none").

func boolPtr(b bool) *bool { return &b }

func TestMapOpenAIToolChoice_StringAuto(t *testing.T) {
	got := MapOpenAIToolChoice("auto", nil)
	require.NotNil(t, got)
	assert.Equal(t, "auto", got.Type)
	assert.False(t, got.DisableParallelToolUse)
}

func TestMapOpenAIToolChoice_StringRequiredMapsToAny(t *testing.T) {
	got := MapOpenAIToolChoice("required", nil)
	require.NotNil(t, got)
	assert.Equal(t, "any", got.Type)
}

func TestMapOpenAIToolChoice_StringNone(t *testing.T) {
	got := MapOpenAIToolChoice("none", nil)
	require.NotNil(t, got)
	assert.Equal(t, "none", got.Type)
}

func TestMapOpenAIToolChoice_UnknownStringNoParallel(t *testing.T) {
	// Unknown string and no parallel flag -> nil.
	assert.Nil(t, MapOpenAIToolChoice("bogus", nil))
}

func TestMapOpenAIToolChoice_FunctionMap(t *testing.T) {
	tc := map[string]interface{}{
		"type":     "function",
		"function": map[string]interface{}{"name": "get_weather"},
	}
	got := MapOpenAIToolChoice(tc, nil)
	require.NotNil(t, got)
	assert.Equal(t, "tool", got.Type)
	assert.Equal(t, "get_weather", got.Name)
}

func TestMapOpenAIToolChoice_FunctionMapMissingName(t *testing.T) {
	// function present but no name -> not matched -> nil (no parallel flag).
	tc := map[string]interface{}{"function": map[string]interface{}{}}
	assert.Nil(t, MapOpenAIToolChoice(tc, nil))
}

func TestMapOpenAIToolChoice_MapWithoutFunction(t *testing.T) {
	tc := map[string]interface{}{"type": "auto"}
	assert.Nil(t, MapOpenAIToolChoice(tc, nil))
}

func TestMapOpenAIToolChoice_NilChoiceNoParallel(t *testing.T) {
	assert.Nil(t, MapOpenAIToolChoice(nil, nil))
}

func TestMapOpenAIToolChoice_ParallelDefaultsToAutoWhenNil(t *testing.T) {
	// parallel_tool_calls=false with no explicit choice -> synthesize auto and
	// disable parallel use.
	got := MapOpenAIToolChoice(nil, boolPtr(false))
	require.NotNil(t, got)
	assert.Equal(t, "auto", got.Type)
	assert.True(t, got.DisableParallelToolUse)
}

func TestMapOpenAIToolChoice_ParallelTrueKeepsEnabled(t *testing.T) {
	got := MapOpenAIToolChoice(nil, boolPtr(true))
	require.NotNil(t, got)
	assert.Equal(t, "auto", got.Type)
	assert.False(t, got.DisableParallelToolUse)
}

func TestMapOpenAIToolChoice_ParallelAppliedToExistingChoice(t *testing.T) {
	got := MapOpenAIToolChoice("required", boolPtr(false))
	require.NotNil(t, got)
	assert.Equal(t, "any", got.Type)
	assert.True(t, got.DisableParallelToolUse)
}

func TestMapOpenAIToolChoice_ParallelNotAppliedToNone(t *testing.T) {
	// type=="none" must never carry disable_parallel_tool_use.
	got := MapOpenAIToolChoice("none", boolPtr(false))
	require.NotNil(t, got)
	assert.Equal(t, "none", got.Type)
	assert.False(t, got.DisableParallelToolUse)
}

func TestMapOpenAIToolChoice_ResultShapeIsClaudeType(t *testing.T) {
	var _ *dto.ClaudeToolChoice = MapOpenAIToolChoice("auto", nil)
}
