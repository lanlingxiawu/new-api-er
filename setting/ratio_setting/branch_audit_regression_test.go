// Regression tests (branch audit M1): a persisted null exclusive ratio must
// fall back to configured pricing instead of acting as a free (0) override,
// while an explicit 0 keeps meaning free.

package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchAuditNullRatioCannotGrantFreeUsage(t *testing.T) {
	oldEnabled := IsUserExclusiveGroupRatioEnabled()
	oldBase, oldSpecial := GroupRatio2JSONString(), GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		SetUserExclusiveGroupRatioEnabled(oldEnabled)
		require.NoError(t, UpdateGroupRatioByJSONString(oldBase))
		require.NoError(t, UpdateGroupGroupRatioByJSONString(oldSpecial))
	})
	require.NoError(t, UpdateGroupRatioByJSONString(`{"audit-null":2}`))
	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{}`))
	SetUserExclusiveGroupRatioEnabled(true)
	parsed := ParseUserGroupRatios(`{"audit-null":null}`)
	got, special := ResolveGroupRatio(parsed, "audit-standard", "audit-null")
	assert.Equal(t, 2.0, got, "invalid persisted null must fall back to the configured price")
	assert.False(t, special)
	parsed = ParseUserGroupRatios(`{"audit-null":0}`)
	got, special = ResolveGroupRatio(parsed, "audit-standard", "audit-null")
	assert.Zero(t, got, "an explicit zero is a deliberate free override")
	assert.True(t, special)
}

func TestDecodeUserGroupRatios(t *testing.T) {
	ratios, nullGroups, err := DecodeUserGroupRatios(`{"c":null,"vip":0.5,"free":0,"a":null}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"vip": 0.5, "free": 0}, ratios)
	assert.Equal(t, []string{"a", "c"}, nullGroups)

	ratios, nullGroups, err = DecodeUserGroupRatios(`{"vip":2}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"vip": 2}, ratios)
	assert.Nil(t, nullGroups)

	for _, raw := range []string{`{"vip":"0.5"}`, `{"vip":true}`, `[1]`, `not json`} {
		_, _, err = DecodeUserGroupRatios(raw)
		assert.Error(t, err, raw)
	}
}

// Only null entries: nothing effective remains, so the relay sees no rules.
func TestParseUserGroupRatiosAllNullIsNil(t *testing.T) {
	assert.Nil(t, ParseUserGroupRatios(`{"audit-all-null-a":null,"audit-all-null-b":null}`))
}

// Cleanup re-serialization must not turn a null into an explicit free zero.
func TestFilterUserGroupRatiosDropsNullInsteadOfZeroing(t *testing.T) {
	cleaned, removed, changed, err := FilterUserGroupRatios(`{"gone":1,"vip":null,"keep":0.5}`,
		func(group string) bool { return group == "gone" })
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"gone"}, removed)
	assert.JSONEq(t, `{"keep":0.5}`, cleaned)

	// Nothing to remove: the stored string is left untouched.
	raw := `{"vip":null,"keep":0.5}`
	cleaned, removed, changed, err = FilterUserGroupRatios(raw, func(string) bool { return false })
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, removed)
	assert.Equal(t, raw, cleaned)
}

// With the memo cache disabled the parser runs per request; the null warning
// is rate limited so one bad row cannot flood the log.
func TestLogNullUserGroupRatiosIsRateLimited(t *testing.T) {
	prev := nullUserGroupRatioLastLog.Load()
	t.Cleanup(func() { nullUserGroupRatioLastLog.Store(prev) })
	nullUserGroupRatioLastLog.Store(0)
	logNullUserGroupRatios([]string{"vip"})
	first := nullUserGroupRatioLastLog.Load()
	assert.NotZero(t, first)
	logNullUserGroupRatios([]string{"vip"})
	assert.Equal(t, first, nullUserGroupRatioLastLog.Load(), "second call inside the interval must not log")
}

// Corrupt JSON is deliberately not memoized, so every relay request of that
// user re-parses it; the error log is rate limited like the null warning and
// independently of it.
func TestParseUserGroupRatiosCorruptLogIsRateLimited(t *testing.T) {
	prevCorrupt, prevNull := corruptUserGroupRatioLastLog.Load(), nullUserGroupRatioLastLog.Load()
	t.Cleanup(func() {
		corruptUserGroupRatioLastLog.Store(prevCorrupt)
		nullUserGroupRatioLastLog.Store(prevNull)
	})
	corruptUserGroupRatioLastLog.Store(0)
	nullUserGroupRatioLastLog.Store(0)

	const raw = `{"corrupt-log":`
	assert.Nil(t, ParseUserGroupRatios(raw))
	first := corruptUserGroupRatioLastLog.Load()
	assert.NotZero(t, first, "the first failure is logged")
	assert.Nil(t, ParseUserGroupRatios(raw))
	assert.Equal(t, first, corruptUserGroupRatioLastLog.Load(), "a repeat inside the interval must not log")
	_, cached := parsedUserGroupRatios.Load(raw)
	assert.False(t, cached, "corrupt values stay out of the memo table")
	assert.Zero(t, nullUserGroupRatioLastLog.Load(), "the null limiter is separate")

	// Past the interval the next failure logs again.
	corruptUserGroupRatioLastLog.Store(first - userGroupRatioLogIntervalSeconds)
	assert.Nil(t, ParseUserGroupRatios(raw))
	assert.GreaterOrEqual(t, corruptUserGroupRatioLastLog.Load(), first)
}
