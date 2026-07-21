package ratio_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- feature flag ----

func TestUserExclusiveGroupRatioEnabled_Toggle(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })

	SetUserExclusiveGroupRatioEnabled(true)
	assert.True(t, IsUserExclusiveGroupRatioEnabled())
	SetUserExclusiveGroupRatioEnabled(false)
	assert.False(t, IsUserExclusiveGroupRatioEnabled())
}

// ---- cache cap accessors ----

func TestSetGetUserGroupRatioCacheMax(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)

	SetUserGroupRatioCacheMax(10)
	assert.Equal(t, 10, GetUserGroupRatioCacheMax())

	// Negative clamps to 0.
	SetUserGroupRatioCacheMax(-5)
	assert.Equal(t, 0, GetUserGroupRatioCacheMax())
}

// ---- ParseUserGroupRatios ----

func TestParseUserGroupRatios_EmptyInputs(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	assert.Nil(t, ParseUserGroupRatios(""))
	assert.Nil(t, ParseUserGroupRatios("{}"))
}

func TestParseUserGroupRatios_Valid(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	m := ParseUserGroupRatios(`{"vip":0.8,"svip":0.5}`)
	require.NotNil(t, m)
	assert.Equal(t, 0.8, m["vip"])
	assert.Equal(t, 0.5, m["svip"])
}

func TestParseUserGroupRatios_Malformed(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	assert.Nil(t, ParseUserGroupRatios(`{not json`))
}

func TestParseUserGroupRatios_EmptyAfterParse(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	// Valid JSON, not literally "{}", but decodes to empty map -> nil.
	assert.Nil(t, ParseUserGroupRatios(`{ }`))
}

func TestParseUserGroupRatios_CacheHitReturnsSameMap(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	js := `{"vip":0.9}`
	first := ParseUserGroupRatios(js)
	second := ParseUserGroupRatios(js)
	require.NotNil(t, first)
	// Same backing map instance is returned from the memo cache.
	assert.Equal(t, first["vip"], second["vip"])
	assert.Equal(t, int64(1), parsedUserGroupRatiosCount.Load(), "identical config cached once")
}

func TestParseUserGroupRatios_CachingDisabled(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	SetUserGroupRatioCacheMax(0) // disable memo
	m := ParseUserGroupRatios(`{"vip":0.7}`)
	require.NotNil(t, m)
	assert.Equal(t, int64(0), parsedUserGroupRatiosCount.Load(), "cache disabled -> nothing stored")
}

func TestParseUserGroupRatios_SweepEvictsColdEntries(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	SetUserGroupRatioCacheMax(1)

	// Insert entry A. New entries start "hot".
	ParseUserGroupRatios(`{"a":1}`)
	assert.Equal(t, int64(1), parsedUserGroupRatiosCount.Load())

	// Inserting B is at/over cap -> triggers a CLOCK sweep first. A is hot, so it
	// survives with its bit cleared (second chance); then B is added.
	ParseUserGroupRatios(`{"b":2}`)
	assert.Equal(t, int64(2), parsedUserGroupRatiosCount.Load(),
		"first sweep gives the hot entry a second chance")

	// Inserting C sweeps again. A was never re-accessed (cold now) -> evicted.
	ParseUserGroupRatios(`{"c":3}`)
	_, aStillThere := parsedUserGroupRatios.Load(`{"a":1}`)
	assert.False(t, aStillThere, "cold entry A evicted by the second sweep")
}

func TestParseUserGroupRatios_CacheHitReMarksReferenced(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	SetUserGroupRatioCacheMax(4)

	ParseUserGroupRatios(`{"a":1}`) // stored hot
	// A sweep clears the referenced bit (entry survives, now "cold").
	sweepUserGroupRatioCache()
	e, ok := parsedUserGroupRatios.Load(`{"a":1}`)
	require.True(t, ok)
	assert.False(t, e.(*userGroupRatioEntry).hot.Load(), "sweep cleared the bit")

	// Accessing it again is a cache hit that re-marks it referenced.
	ParseUserGroupRatios(`{"a":1}`)
	assert.True(t, e.(*userGroupRatioEntry).hot.Load(), "cache hit re-set the referenced bit")
}

func TestParseUserGroupRatios_SweepReentrancyGuard(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	// Pretend a sweep is already in progress; sweepUserGroupRatioCache must no-op.
	parsedUserGroupRatiosSweep.Store(true)
	sweepUserGroupRatioCache() // should return immediately, not deadlock/panic
	assert.True(t, parsedUserGroupRatiosSweep.Load(), "guard remains held; concurrent sweep skipped")
	parsedUserGroupRatiosSweep.Store(false)
}

// ---- lookupUserExclusive ----

func TestLookupUserExclusive_Matrix(t *testing.T) {
	m := map[string]float64{
		"ok":   0.8,
		"zero": 0, // 0 is a VALID free ratio (only <0 is rejected)
		"neg":  -0.1,
		"nan":  math.NaN(),
		"pinf": math.Inf(1),
		"ninf": math.Inf(-1),
	}
	r, ok := lookupUserExclusive(m, "ok")
	assert.True(t, ok)
	assert.Equal(t, 0.8, r)

	r, ok = lookupUserExclusive(m, "zero")
	assert.True(t, ok, "exactly 0 is an allowed ratio")
	assert.Equal(t, 0.0, r)

	for _, key := range []string{"neg", "nan", "pinf", "ninf", "absent"} {
		_, ok := lookupUserExclusive(m, key)
		assert.False(t, ok, "key %s must be rejected", key)
	}
}

func TestLookupUserExclusive_NilMap(t *testing.T) {
	r, ok := lookupUserExclusive(nil, "x")
	assert.False(t, ok)
	assert.Equal(t, 0.0, r)
}

// ---- ResolveGroupRatio (priority: user-exclusive > group-group > group) ----

func TestResolveGroupRatio_UserExclusiveWins_WhenEnabled(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(true)

	// vip->edit_this exists globally (0.9) but user-exclusive edit_this=0.3 wins.
	userRatios := map[string]float64{"edit_this": 0.3}
	ratio, special := ResolveGroupRatio(userRatios, "vip", "edit_this")
	assert.Equal(t, 0.3, ratio)
	assert.True(t, special)
}

func TestResolveGroupRatio_UserExclusiveIgnoredWhenDisabled(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(false)

	userRatios := map[string]float64{"edit_this": 0.3}
	// Feature off -> falls to group-group (0.9).
	ratio, special := ResolveGroupRatio(userRatios, "vip", "edit_this")
	assert.Equal(t, 0.9, ratio)
	assert.True(t, special)
}

func TestResolveGroupRatio_FallsToGroupGroup(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(true)

	// Enabled but user map has no matching usingGroup -> group-group applies.
	ratio, special := ResolveGroupRatio(map[string]float64{"other": 0.1}, "vip", "edit_this")
	assert.Equal(t, 0.9, ratio)
	assert.True(t, special)
}

func TestResolveGroupRatio_FallsToGlobalGroupRatio(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(true)

	// No user-exclusive, no group-group match -> base GetGroupRatio, special=false.
	ratio, special := ResolveGroupRatio(nil, "default", "default")
	assert.Equal(t, 1.0, ratio)
	assert.False(t, special)
}

func TestResolveGroupRatio_UnknownUsingGroupDefaultsToOne(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(false)

	ratio, special := ResolveGroupRatio(nil, "default", "totally-unknown")
	assert.Equal(t, 1.0, ratio, "unknown consuming group -> base default 1")
	assert.False(t, special)
}

func TestResolveGroupRatio_UserExclusiveZeroWins(t *testing.T) {
	prev := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { SetUserExclusiveGroupRatioEnabled(prev) })
	SetUserExclusiveGroupRatioEnabled(true)

	// A user-exclusive ratio of exactly 0 is honored (free), returns special=true.
	ratio, special := ResolveGroupRatio(map[string]float64{"edit_this": 0}, "vip", "edit_this")
	assert.Equal(t, 0.0, ratio)
	assert.True(t, special)
}
