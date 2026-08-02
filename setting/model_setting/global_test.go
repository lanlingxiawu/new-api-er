package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withGlobalSettings(t *testing.T, s GlobalSettings) {
	t.Helper()
	original := globalSettings
	t.Cleanup(func() { ReplaceGlobalSettings(original) })
	// 必须走 Replace：直接改包级变量不会重新发布快照。
	ReplaceGlobalSettings(s)
}

// 返回不可变快照，而不是可变全局的指针。
func TestGetGlobalSettings_ReturnsImmutableSnapshot(t *testing.T) {
	got := GetGlobalSettings()
	require.NotNil(t, got)
	require.NotSame(t, &globalSettings, got)
}

// ---------------------------------------------------------------------------
// ChatCompletionsToResponsesPolicy.IsChannelEnabled
// Decision + condition coverage over every branch.
// ---------------------------------------------------------------------------

func TestIsChannelEnabled_DisabledShortCircuits(t *testing.T) {
	p := ChatCompletionsToResponsesPolicy{Enabled: false, AllChannels: true}
	assert.False(t, p.IsChannelEnabled(1, 1), "disabled policy is never enabled")
}

func TestIsChannelEnabled_AllChannels(t *testing.T) {
	p := ChatCompletionsToResponsesPolicy{Enabled: true, AllChannels: true}
	assert.True(t, p.IsChannelEnabled(0, 0), "AllChannels ignores id/type")
}

func TestIsChannelEnabled_ByChannelID(t *testing.T) {
	p := ChatCompletionsToResponsesPolicy{
		Enabled:    true,
		ChannelIDs: []int{5, 7},
	}
	assert.True(t, p.IsChannelEnabled(7, 0))  // id match
	assert.False(t, p.IsChannelEnabled(9, 0)) // id not in list
}

func TestIsChannelEnabled_ByChannelType(t *testing.T) {
	p := ChatCompletionsToResponsesPolicy{
		Enabled:      true,
		ChannelTypes: []int{2, 4},
	}
	assert.True(t, p.IsChannelEnabled(0, 4))  // type match
	assert.False(t, p.IsChannelEnabled(0, 3)) // type not in list
}

// Condition coverage on the compound guard:
//
//	channelID > 0 && len(ChannelIDs) > 0 && slices.Contains(...)
func TestIsChannelEnabled_ChannelIDConditionMatrix(t *testing.T) {
	pWithIDs := ChatCompletionsToResponsesPolicy{Enabled: true, ChannelIDs: []int{5}}
	pNoIDs := ChatCompletionsToResponsesPolicy{Enabled: true}

	// channelID <= 0 -> first sub-condition false
	assert.False(t, pWithIDs.IsChannelEnabled(0, 0))
	assert.False(t, pWithIDs.IsChannelEnabled(-1, 0))
	// channelID > 0 but list empty -> second sub-condition false
	assert.False(t, pNoIDs.IsChannelEnabled(5, 0))
	// channelID > 0, list non-empty, but not contained -> third false
	assert.False(t, pWithIDs.IsChannelEnabled(6, 0))
	// all true
	assert.True(t, pWithIDs.IsChannelEnabled(5, 0))
}

func TestIsChannelEnabled_ChannelTypeConditionMatrix(t *testing.T) {
	pWithTypes := ChatCompletionsToResponsesPolicy{Enabled: true, ChannelTypes: []int{2}}
	pNoTypes := ChatCompletionsToResponsesPolicy{Enabled: true}

	assert.False(t, pWithTypes.IsChannelEnabled(0, 0))  // type <= 0
	assert.False(t, pWithTypes.IsChannelEnabled(0, -1)) // type <= 0
	assert.False(t, pNoTypes.IsChannelEnabled(0, 2))    // list empty
	assert.False(t, pWithTypes.IsChannelEnabled(0, 3))  // not contained
	assert.True(t, pWithTypes.IsChannelEnabled(0, 2))   // all true
}

func TestIsChannelEnabled_NoMatchFallsThroughToFalse(t *testing.T) {
	// Enabled, not AllChannels, id/type provided but neither list matches.
	p := ChatCompletionsToResponsesPolicy{
		Enabled:      true,
		ChannelIDs:   []int{1},
		ChannelTypes: []int{1},
	}
	assert.False(t, p.IsChannelEnabled(2, 2))
}

// ---------------------------------------------------------------------------
// ShouldPreserveThinkingSuffix
// ---------------------------------------------------------------------------

func TestShouldPreserveThinkingSuffix(t *testing.T) {
	withGlobalSettings(t, GlobalSettings{ThinkingModelBlacklist: []string{
		"kimi-k2-thinking",
		"  spaced-entry  ", // entry itself has surrounding whitespace
	}})

	// exact hit
	assert.True(t, ShouldPreserveThinkingSuffix("kimi-k2-thinking"))
	// input has whitespace that gets trimmed to a match
	assert.True(t, ShouldPreserveThinkingSuffix("  kimi-k2-thinking  "))
	// blacklist entry is trimmed before compare
	assert.True(t, ShouldPreserveThinkingSuffix("spaced-entry"))
	// not on the list
	assert.False(t, ShouldPreserveThinkingSuffix("gpt-4"))
}

func TestShouldPreserveThinkingSuffix_EmptyInput(t *testing.T) {
	// Empty / whitespace-only input short-circuits to false even if the
	// blacklist contained an empty-ish entry.
	withGlobalSettings(t, GlobalSettings{ThinkingModelBlacklist: []string{""}})
	assert.False(t, ShouldPreserveThinkingSuffix(""))
	assert.False(t, ShouldPreserveThinkingSuffix("   "))
}

func TestShouldPreserveThinkingSuffix_EmptyBlacklist(t *testing.T) {
	withGlobalSettings(t, GlobalSettings{ThinkingModelBlacklist: nil})
	assert.False(t, ShouldPreserveThinkingSuffix("kimi-k2-thinking"))
}
