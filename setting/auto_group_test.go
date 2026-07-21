package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveAutoGroups(t *testing.T) {
	t.Helper()
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })
}

func TestContainsAutoGroup(t *testing.T) {
	saveAutoGroups(t)
	autoGroups = []string{"default", "vip"}

	assert.True(t, ContainsAutoGroup("default"))
	assert.True(t, ContainsAutoGroup("vip"))
	assert.False(t, ContainsAutoGroup("missing"))
	assert.False(t, ContainsAutoGroup(""))
}

func TestContainsAutoGroup_EmptyList(t *testing.T) {
	saveAutoGroups(t)
	autoGroups = []string{}
	assert.False(t, ContainsAutoGroup("default"))
}

func TestUpdateAutoGroupsByJsonString_Valid(t *testing.T) {
	saveAutoGroups(t)
	err := UpdateAutoGroupsByJsonString(`["a","b","c"]`)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, GetAutoGroups())
}

func TestUpdateAutoGroupsByJsonString_EmptyArray(t *testing.T) {
	saveAutoGroups(t)
	err := UpdateAutoGroupsByJsonString(`[]`)
	require.NoError(t, err)
	assert.Empty(t, GetAutoGroups())
}

func TestUpdateAutoGroupsByJsonString_Invalid(t *testing.T) {
	saveAutoGroups(t)
	err := UpdateAutoGroupsByJsonString(`not-json`)
	require.Error(t, err)
	// Function resets to empty slice before unmarshal, regardless of error.
	assert.Empty(t, autoGroups)
}

func TestAutoGroups2JsonString(t *testing.T) {
	saveAutoGroups(t)
	autoGroups = []string{"default", "vip"}
	assert.JSONEq(t, `["default","vip"]`, AutoGroups2JsonString())
}

func TestAutoGroups2JsonString_Empty(t *testing.T) {
	saveAutoGroups(t)
	autoGroups = []string{}
	assert.Equal(t, `[]`, AutoGroups2JsonString())
}

func TestGetAutoGroups_ReturnsLiveSlice(t *testing.T) {
	saveAutoGroups(t)
	autoGroups = []string{"x"}
	assert.Equal(t, []string{"x"}, GetAutoGroups())
}
