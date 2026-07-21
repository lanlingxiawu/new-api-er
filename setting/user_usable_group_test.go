package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveUserUsableGroups(t *testing.T) {
	t.Helper()
	userUsableGroupsMutex.Lock()
	orig := userUsableGroups
	userUsableGroupsMutex.Unlock()
	t.Cleanup(func() {
		userUsableGroupsMutex.Lock()
		userUsableGroups = orig
		userUsableGroupsMutex.Unlock()
	})
}

func TestGetUserUsableGroupsCopy_IndependentCopy(t *testing.T) {
	saveUserUsableGroups(t)
	userUsableGroups = map[string]string{"default": "D", "vip": "V"}

	cp := GetUserUsableGroupsCopy()
	require.Equal(t, map[string]string{"default": "D", "vip": "V"}, cp)

	// Mutating the copy must not touch the source.
	cp["default"] = "mutated"
	assert.Equal(t, "D", userUsableGroups["default"])
}

func TestGetUserUsableGroupsCopy_Empty(t *testing.T) {
	saveUserUsableGroups(t)
	userUsableGroups = map[string]string{}
	cp := GetUserUsableGroupsCopy()
	assert.NotNil(t, cp)
	assert.Empty(t, cp)
}

func TestUserUsableGroups2JSONString(t *testing.T) {
	saveUserUsableGroups(t)
	userUsableGroups = map[string]string{"default": "默认分组"}
	assert.JSONEq(t, `{"default":"默认分组"}`, UserUsableGroups2JSONString())
}

func TestUpdateUserUsableGroupsByJSONString_Valid(t *testing.T) {
	saveUserUsableGroups(t)
	err := UpdateUserUsableGroupsByJSONString(`{"a":"A","b":"B"}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "A", "b": "B"}, GetUserUsableGroupsCopy())
}

func TestUpdateUserUsableGroupsByJSONString_Invalid(t *testing.T) {
	saveUserUsableGroups(t)
	err := UpdateUserUsableGroupsByJSONString(`nope`)
	require.Error(t, err)
	// Reset to fresh empty map before unmarshal.
	assert.Empty(t, GetUserUsableGroupsCopy())
}

func TestGetUsableGroupDescription_Found(t *testing.T) {
	saveUserUsableGroups(t)
	userUsableGroups = map[string]string{"vip": "VIP Tier"}
	assert.Equal(t, "VIP Tier", GetUsableGroupDescription("vip"))
}

func TestGetUsableGroupDescription_NotFoundReturnsName(t *testing.T) {
	saveUserUsableGroups(t)
	userUsableGroups = map[string]string{"vip": "VIP Tier"}
	// Fallback returns the queried name unchanged.
	assert.Equal(t, "unknown", GetUsableGroupDescription("unknown"))
}
