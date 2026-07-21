package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// MatchAnyRegex returns true iff any non-empty, valid pattern matches s.
// Invalid patterns and empty patterns are skipped; empty s or empty pattern
// list short-circuits to false. Compiled patterns are cached in a sync.Map.

func TestMatchAnyRegex_EmptyPatternList(t *testing.T) {
	assert.False(t, MatchAnyRegex(nil, "anything"))
	assert.False(t, MatchAnyRegex([]string{}, "anything"))
}

func TestMatchAnyRegex_EmptySubject(t *testing.T) {
	// s == "" short-circuits regardless of patterns.
	assert.False(t, MatchAnyRegex([]string{".*"}, ""))
}

func TestMatchAnyRegex_EmptyPatternEntrySkipped(t *testing.T) {
	// The only entry is empty -> skipped -> no match.
	assert.False(t, MatchAnyRegex([]string{""}, "abc"))
}

func TestMatchAnyRegex_Match(t *testing.T) {
	assert.True(t, MatchAnyRegex([]string{`^gpt-4`}, "gpt-4o"))
}

func TestMatchAnyRegex_NoMatch(t *testing.T) {
	assert.False(t, MatchAnyRegex([]string{`^claude-`}, "gpt-4o"))
}

func TestMatchAnyRegex_InvalidPatternSkipped(t *testing.T) {
	// Invalid regex is treated as non-matching (continue), not an error.
	assert.False(t, MatchAnyRegex([]string{`[unterminated`}, "abc"))
}

func TestMatchAnyRegex_InvalidThenValidStillMatches(t *testing.T) {
	// A later valid pattern must still match even after an invalid one is skipped.
	assert.True(t, MatchAnyRegex([]string{`[bad(`, `^ok-`}, "ok-model"))
}

func TestMatchAnyRegex_CacheHitOnSecondCall(t *testing.T) {
	// Use a distinct pattern so the first call compiles+stores, second call
	// exercises the cache Load (ok==true) branch.
	pattern := `^cache-hit-model-[0-9]+$`
	assert.True(t, MatchAnyRegex([]string{pattern}, "cache-hit-model-42"))
	assert.True(t, MatchAnyRegex([]string{pattern}, "cache-hit-model-7"))
	assert.False(t, MatchAnyRegex([]string{pattern}, "cache-hit-model-x"))
}
