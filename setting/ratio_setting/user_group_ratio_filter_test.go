package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dropNames returns a predicate matching the given group names.
func dropNames(names ...string) func(string) bool {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(group string) bool {
		_, ok := set[group]
		return ok
	}
}

func TestFilterUserGroupRatios_EmptyInputs(t *testing.T) {
	for _, raw := range []string{"", "{}"} {
		cleaned, removed, changed, err := FilterUserGroupRatios(raw, dropNames("vip"))
		require.NoError(t, err)
		assert.Equal(t, raw, cleaned)
		assert.Nil(t, removed)
		assert.False(t, changed)
	}
}

func TestFilterUserGroupRatios_NilPredicate(t *testing.T) {
	raw := `{"vip":2}`
	cleaned, removed, changed, err := FilterUserGroupRatios(raw, nil)
	require.NoError(t, err)
	assert.Equal(t, raw, cleaned)
	assert.Nil(t, removed)
	assert.False(t, changed)
}

func TestFilterUserGroupRatios_NoMatchIsUnchanged(t *testing.T) {
	raw := `{"vip":2,"svip":3}`
	cleaned, removed, changed, err := FilterUserGroupRatios(raw, dropNames("legacy"))
	require.NoError(t, err)
	assert.Equal(t, raw, cleaned, "unchanged input must be returned verbatim")
	assert.Nil(t, removed)
	assert.False(t, changed)
}

func TestFilterUserGroupRatios_RemovesSingle(t *testing.T) {
	cleaned, removed, changed, err := FilterUserGroupRatios(`{"vip":2,"svip":3}`, dropNames("vip"))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"vip"}, removed)
	assert.Equal(t, map[string]float64{"svip": 3}, mustParse(t, cleaned))
}

func TestFilterUserGroupRatios_RemovesMultipleSorted(t *testing.T) {
	cleaned, removed, changed, err := FilterUserGroupRatios(`{"vip":2,"svip":3,"keep":1}`, dropNames("vip", "svip"))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"svip", "vip"}, removed, "removed names must be sorted for stable logs")
	assert.Equal(t, map[string]float64{"keep": 1}, mustParse(t, cleaned))
}

func TestFilterUserGroupRatios_RemovesAllYieldsEmptyObject(t *testing.T) {
	cleaned, removed, changed, err := FilterUserGroupRatios(`{"vip":2}`, dropNames("vip"))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"vip"}, removed)
	assert.Equal(t, "{}", cleaned, "an emptied map must serialize to {} so the scan predicate skips the row next time")
	assert.Nil(t, ParseUserGroupRatios(cleaned), "{} must stay on the parse fast path")
}

// An explicit 0 is a real ratio (free), not an absent value; it must survive
// unless its own group is dropped.
func TestFilterUserGroupRatios_PreservesExplicitZero(t *testing.T) {
	cleaned, _, changed, err := FilterUserGroupRatios(`{"free":0,"vip":2}`, dropNames("vip"))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, map[string]float64{"free": 0}, mustParse(t, cleaned))
}

func TestFilterUserGroupRatios_Idempotent(t *testing.T) {
	first, _, _, err := FilterUserGroupRatios(`{"vip":2,"keep":1}`, dropNames("vip"))
	require.NoError(t, err)
	second, removed, changed, err := FilterUserGroupRatios(first, dropNames("vip"))
	require.NoError(t, err)
	assert.False(t, changed, "re-running the same cleanup must be a no-op")
	assert.Nil(t, removed)
	assert.Equal(t, first, second)
}

func TestFilterUserGroupRatios_InvalidJSONReturnsError(t *testing.T) {
	raw := `{"vip":`
	cleaned, removed, changed, err := FilterUserGroupRatios(raw, dropNames("vip"))
	require.Error(t, err)
	assert.Equal(t, raw, cleaned, "a corrupt value must be returned untouched, never overwritten")
	assert.Nil(t, removed)
	assert.False(t, changed)
}

// The memo table backs live relay reads; cleanup input strings are about to be
// discarded and must not evict hot entries.
func TestFilterUserGroupRatios_DoesNotPopulateParseMemo(t *testing.T) {
	resetUserGroupRatioCache()
	raw := `{"vip":2,"keep":1}`
	_, _, changed, err := FilterUserGroupRatios(raw, dropNames("vip"))
	require.NoError(t, err)
	require.True(t, changed)
	_, cached := parsedUserGroupRatios.Load(raw)
	assert.False(t, cached, "FilterUserGroupRatios must bypass the parse memo")
	assert.Equal(t, int64(0), parsedUserGroupRatiosCount.Load())
}

func mustParse(t *testing.T, raw string) map[string]float64 {
	t.Helper()
	out := make(map[string]float64)
	require.NoError(t, common.UnmarshalJsonStr(raw, &out))
	return out
}
