package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGroupRatio_KnownAndUnknown(t *testing.T) {
	assert.Equal(t, 1.0, GetGroupRatio("default"))
	assert.Equal(t, 1.0, GetGroupRatio("vip"))
	// unknown group -> logs and returns 1 (never zero-bills silently)
	assert.Equal(t, 1.0, GetGroupRatio("no-such-group"))
}

func TestGetGroupRatio_CustomValue(t *testing.T) {
	snap := snapshotFloatMap(groupRatioMap)
	t.Cleanup(func() { restoreFloatMap(groupRatioMap, snap) })
	require.NoError(t, UpdateGroupRatioByJSONString(`{"default":1,"premium":0.5}`))
	assert.Equal(t, 0.5, GetGroupRatio("premium"))
}

func TestContainsGroupRatio(t *testing.T) {
	assert.True(t, ContainsGroupRatio("default"))
	assert.False(t, ContainsGroupRatio("ghost-group"))
}

func TestGetGroupRatioCopy_Independent(t *testing.T) {
	cp := GetGroupRatioCopy()
	assert.Equal(t, 1.0, cp["default"])
	cp["default"] = -99
	assert.Equal(t, 1.0, GetGroupRatio("default"), "copy mutation must not affect live map")
}

func TestGroupRatio_JSONRoundTrip(t *testing.T) {
	snap := snapshotFloatMap(groupRatioMap)
	t.Cleanup(func() { restoreFloatMap(groupRatioMap, snap) })
	require.NoError(t, UpdateGroupRatioByJSONString(`{"g":2}`))
	assert.JSONEq(t, `{"g":2}`, GroupRatio2JSONString())
}

func TestUpdateGroupRatioByJSONString_Malformed(t *testing.T) {
	snap := snapshotFloatMap(groupRatioMap)
	t.Cleanup(func() { restoreFloatMap(groupRatioMap, snap) })
	require.Error(t, UpdateGroupRatioByJSONString(`{bad`))
}

// ---- GetGroupGroupRatio (group-special / per-consuming-group override) ----

func TestGetGroupGroupRatio_Hit(t *testing.T) {
	// default seeds vip -> {edit_this: 0.9}
	ratio, ok := GetGroupGroupRatio("vip", "edit_this")
	assert.True(t, ok)
	assert.Equal(t, 0.9, ratio)
}

func TestGetGroupGroupRatio_UnknownUserGroup(t *testing.T) {
	ratio, ok := GetGroupGroupRatio("no-user-group", "edit_this")
	assert.False(t, ok)
	assert.Equal(t, -1.0, ratio)
}

func TestGetGroupGroupRatio_UnknownUsingGroup(t *testing.T) {
	ratio, ok := GetGroupGroupRatio("vip", "no-using-group")
	assert.False(t, ok)
	assert.Equal(t, -1.0, ratio)
}

func TestGroupGroupRatio_JSONRoundTrip(t *testing.T) {
	snap := groupGroupRatioMap.ReadAll()
	t.Cleanup(func() {
		groupGroupRatioMap.Clear()
		groupGroupRatioMap.AddAll(snap)
	})
	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{"a":{"b":0.7}}`))
	ratio, ok := GetGroupGroupRatio("a", "b")
	assert.True(t, ok)
	assert.Equal(t, 0.7, ratio)
	assert.JSONEq(t, `{"a":{"b":0.7}}`, GroupGroupRatio2JSONString())
}

func TestUpdateGroupGroupRatioByJSONString_Malformed(t *testing.T) {
	snap := groupGroupRatioMap.ReadAll()
	t.Cleanup(func() {
		groupGroupRatioMap.Clear()
		groupGroupRatioMap.AddAll(snap)
	})
	require.Error(t, UpdateGroupGroupRatioByJSONString(`{bad`))
}

// ---- CheckGroupRatio (validation) ----

func TestCheckGroupRatio_Valid(t *testing.T) {
	assert.NoError(t, CheckGroupRatio(`{"a":0,"b":1.5}`))
}

func TestCheckGroupRatio_NegativeRejected(t *testing.T) {
	err := CheckGroupRatio(`{"a":1,"bad":-0.1}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")
}

func TestCheckGroupRatio_ZeroBoundaryAllowed(t *testing.T) {
	// 0 is the inclusive lower boundary and must be accepted.
	assert.NoError(t, CheckGroupRatio(`{"free":0}`))
}

func TestCheckGroupRatio_MalformedJSON(t *testing.T) {
	require.Error(t, CheckGroupRatio(`{not json`))
}

// ---- GetGroupRatioSetting (config struct + lazy special-usable-group init) ----

func TestGetGroupRatioSetting_ReturnsPopulated(t *testing.T) {
	s := GetGroupRatioSetting()
	require.NotNil(t, s)
	require.NotNil(t, s.GroupRatio)
	require.NotNil(t, s.GroupGroupRatio)
	require.NotNil(t, s.GroupSpecialUsableGroup)
	r, ok := s.GroupRatio.Get("default")
	assert.True(t, ok)
	assert.Equal(t, 1.0, r)
}

func TestGetGroupRatioSetting_LazyInitSpecialUsableGroup(t *testing.T) {
	// Force the nil branch, then verify it is re-initialized.
	prev := groupRatioSetting.GroupSpecialUsableGroup
	t.Cleanup(func() { groupRatioSetting.GroupSpecialUsableGroup = prev })

	groupRatioSetting.GroupSpecialUsableGroup = nil
	s := GetGroupRatioSetting()
	assert.NotNil(t, s.GroupSpecialUsableGroup, "nil special-usable-group must be lazily rebuilt")
}
