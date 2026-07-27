package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseHTTPStatusCodeRanges ─────────────────────────────────────────────

func TestParseHTTPStatusCodeRanges_EmptyAndBlank(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		ranges, err := ParseHTTPStatusCodeRanges(in)
		require.NoError(t, err)
		assert.Nil(t, ranges)
	}
}

func TestParseHTTPStatusCodeRanges_OnlySeparators(t *testing.T) {
	// Every segment blank → no ranges, no error.
	ranges, err := ParseHTTPStatusCodeRanges(" , , ")
	require.NoError(t, err)
	assert.Nil(t, ranges)
}

func TestParseHTTPStatusCodeRanges_SingleAndRange(t *testing.T) {
	ranges, err := ParseHTTPStatusCodeRanges("401,500-599")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 401}, {500, 599}}, ranges)
}

func TestParseHTTPStatusCodeRanges_FullWidthComma(t *testing.T) {
	// Chinese full-width comma is normalized to ASCII.
	ranges, err := ParseHTTPStatusCodeRanges("401，403")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 401}, {403, 403}}, ranges)
}

func TestParseHTTPStatusCodeRanges_SortsAndMergesAdjacent(t *testing.T) {
	// Out of order + adjacent (403 and 404 merge because 404 <= 403+1) + overlap.
	ranges, err := ParseHTTPStatusCodeRanges("500-505,504,403,401,402,404")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 404}, {500, 505}}, ranges)
}

func TestParseHTTPStatusCodeRanges_MergeExtendsEnd(t *testing.T) {
	// Second range starts within the first but extends further.
	ranges, err := ParseHTTPStatusCodeRanges("400-410,405-420")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{400, 420}}, ranges)
}

func TestParseHTTPStatusCodeRanges_MergeContainedNoExtend(t *testing.T) {
	// Second range fully inside the first: End not extended (hits the `if r.End > last.End` false branch).
	ranges, err := ParseHTTPStatusCodeRanges("400-420,405-410")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{400, 420}}, ranges)
}

func TestParseHTTPStatusCodeRanges_NonAdjacentStaySeparate(t *testing.T) {
	// Gap larger than 1 keeps ranges separate.
	ranges, err := ParseHTTPStatusCodeRanges("100-200,300-400")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{100, 200}, {300, 400}}, ranges)
}

func TestParseHTTPStatusCodeRanges_SameStartSortsByEnd(t *testing.T) {
	// Two tokens share a start; sort tie-breaks on End, then they merge.
	ranges, err := ParseHTTPStatusCodeRanges("401-401,401-405")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 405}}, ranges)
}

func TestParseHTTPStatusCodeRanges_InvalidTokensCollected(t *testing.T) {
	_, err := ParseHTTPStatusCodeRanges("99,600,foo,500-400,500-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid http status code rules")
}

func TestParseHTTPStatusCodeRanges_SkipsBlankSegmentsBetweenValid(t *testing.T) {
	ranges, err := ParseHTTPStatusCodeRanges("401,,403")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 401}, {403, 403}}, ranges)
}

// ── parseHTTPStatusCodeToken (via ParseHTTPStatusCodeRanges error paths) ────

func TestParseHTTPStatusCodeToken_ErrorClasses(t *testing.T) {
	cases := []struct{ name, token string }{
		{"non-numeric-single", "abc"},
		{"single-below-100", "99"},
		{"single-above-599", "600"},
		{"range-empty-left", "-500"},
		{"range-empty-right", "500-"},
		{"range-too-many-dashes", "1-2-3"},
		{"range-non-numeric-start", "x-500"},
		{"range-non-numeric-end", "500-y"},
		{"range-start-gt-end", "500-400"},
		{"range-start-below-100", "50-200"},
		{"range-end-above-599", "500-700"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A single bad token makes the whole parse fail.
			_, err := ParseHTTPStatusCodeRanges(c.token)
			require.Error(t, err)
		})
	}
}

func TestParseHTTPStatusCodeToken_BoundaryValid(t *testing.T) {
	// 100 and 599 are inclusive bounds.
	ranges, err := ParseHTTPStatusCodeRanges("100,599,100-599")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{100, 599}}, ranges)
}

func TestParseHTTPStatusCodeToken_TrimsInnerSpaces(t *testing.T) {
	// Spaces inside a token are stripped ("401 - 405" -> "401-405").
	ranges, err := ParseHTTPStatusCodeRanges("401 - 405")
	require.NoError(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 405}}, ranges)
}

// parseHTTPStatusCodeToken's empty-token guard is unreachable through the
// public ParseHTTPStatusCodeRanges (blank segments are skipped first), so we
// exercise it directly (white-box).
func TestParseHTTPStatusCodeToken_EmptyTokenGuard(t *testing.T) {
	_, err := parseHTTPStatusCodeToken("   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty token")
}

// ── statusCodeRangesToString ──────────────────────────────────────────────

func TestStatusCodeRangesToString(t *testing.T) {
	assert.Equal(t, "", statusCodeRangesToString(nil))
	assert.Equal(t, "", statusCodeRangesToString([]StatusCodeRange{}))
	assert.Equal(t, "401", statusCodeRangesToString([]StatusCodeRange{{401, 401}}))
	assert.Equal(t, "401,500-599",
		statusCodeRangesToString([]StatusCodeRange{{401, 401}, {500, 599}}))
}

// ── shouldMatchStatusCodeRanges (via public wrappers) ─────────────────────

func TestShouldMatchStatusCodeRanges_OutOfHTTPBounds(t *testing.T) {
	orig := AutomaticDisableStatusCodeRanges
	t.Cleanup(func() { AutomaticDisableStatusCodeRanges = orig })
	AutomaticDisableStatusCodeRanges = []StatusCodeRange{{100, 599}}
	assert.False(t, ShouldDisableByStatusCode(99))  // below 100
	assert.False(t, ShouldDisableByStatusCode(600)) // above 599
	assert.True(t, ShouldDisableByStatusCode(100))
	assert.True(t, ShouldDisableByStatusCode(599))
}

func TestShouldMatchStatusCodeRanges_EarlyReturnBelowStart(t *testing.T) {
	orig := AutomaticDisableStatusCodeRanges
	t.Cleanup(func() { AutomaticDisableStatusCodeRanges = orig })
	AutomaticDisableStatusCodeRanges = []StatusCodeRange{{400, 410}, {500, 510}}
	assert.False(t, ShouldDisableByStatusCode(300), "code below first range start returns early")
	assert.False(t, ShouldDisableByStatusCode(450), "code in the gap between ranges")
	assert.True(t, ShouldDisableByStatusCode(405))
	assert.True(t, ShouldDisableByStatusCode(505))
	assert.False(t, ShouldDisableByStatusCode(520), "code past the last range")
}

// ── AutomaticDisable* round trip ──────────────────────────────────────────

func TestAutomaticDisableStatusCodes_RoundTrip(t *testing.T) {
	orig := AutomaticDisableStatusCodeRanges
	t.Cleanup(func() { AutomaticDisableStatusCodeRanges = orig })

	require.NoError(t, AutomaticDisableStatusCodesFromString("401,500-599"))
	assert.Equal(t, "401,500-599", AutomaticDisableStatusCodesToString())
	assert.True(t, ShouldDisableByStatusCode(550))
}

func TestAutomaticDisableStatusCodesFromString_InvalidReturnsErrorAndKeepsOld(t *testing.T) {
	orig := AutomaticDisableStatusCodeRanges
	t.Cleanup(func() { AutomaticDisableStatusCodeRanges = orig })
	AutomaticDisableStatusCodeRanges = []StatusCodeRange{{401, 401}}

	err := AutomaticDisableStatusCodesFromString("foo")
	require.Error(t, err)
	assert.Equal(t, []StatusCodeRange{{401, 401}}, AutomaticDisableStatusCodeRanges,
		"invalid input must not mutate the existing ranges")
}

// ── AutomaticRetry* round trip ────────────────────────────────────────────

func TestAutomaticRetryStatusCodes_RoundTrip(t *testing.T) {
	orig := AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { AutomaticRetryStatusCodeRanges = orig })

	require.NoError(t, AutomaticRetryStatusCodesFromString("429,500-503"))
	assert.Equal(t, "429,500-503", AutomaticRetryStatusCodesToString())
}

func TestAutomaticRetryStatusCodesFromString_Invalid(t *testing.T) {
	orig := AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { AutomaticRetryStatusCodeRanges = orig })
	require.Error(t, AutomaticRetryStatusCodesFromString("bad"))
}

// ── ShouldRetryByStatusCode ───────────────────────────────────────────────

func TestShouldRetryByStatusCode_AlwaysSkipWins(t *testing.T) {
	orig := AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { AutomaticRetryStatusCodeRanges = orig })
	// 504/524 are in the configured range but always-skip overrides.
	AutomaticRetryStatusCodeRanges = []StatusCodeRange{{500, 599}}
	assert.False(t, ShouldRetryByStatusCode(504))
	assert.False(t, ShouldRetryByStatusCode(524))
	assert.True(t, ShouldRetryByStatusCode(500))
}

func TestShouldRetryByStatusCode_DefaultLegacyBehavior(t *testing.T) {
	// Against shipped defaults (no mutation).
	assert.False(t, ShouldRetryByStatusCode(200))
	assert.False(t, ShouldRetryByStatusCode(400))
	assert.False(t, ShouldRetryByStatusCode(408))
	assert.True(t, ShouldRetryByStatusCode(401))
	assert.True(t, ShouldRetryByStatusCode(429))
	assert.True(t, ShouldRetryByStatusCode(500))
	assert.False(t, ShouldRetryByStatusCode(504))
	assert.False(t, ShouldRetryByStatusCode(524))
	assert.True(t, ShouldRetryByStatusCode(599))
	assert.False(t, ShouldRetryByStatusCode(700), "out of HTTP range")
}

// ── IsAlwaysSkipRetry* ────────────────────────────────────────────────────

func TestIsAlwaysSkipRetryStatusCode(t *testing.T) {
	assert.True(t, IsAlwaysSkipRetryStatusCode(504))
	assert.True(t, IsAlwaysSkipRetryStatusCode(524))
	assert.False(t, IsAlwaysSkipRetryStatusCode(500))
	assert.False(t, IsAlwaysSkipRetryStatusCode(200))
}

func TestIsAlwaysSkipRetryCode(t *testing.T) {
	assert.True(t, IsAlwaysSkipRetryCode(types.ErrorCodeBadResponseBody))
	assert.False(t, IsAlwaysSkipRetryCode(types.ErrorCode("some_other_code")))
}
