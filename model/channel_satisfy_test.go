package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupAbilities registers a hard-delete of every ability row for a channel id.
func cleanupAbilities(t *testing.T, channelID int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("channel_id = ?", channelID).Delete(&Ability{})
		}
	})
}

// ---------------------------------------------------------------------------
// channel_satisfy.go — pure list membership + group/model enablement checks.
// ---------------------------------------------------------------------------

func TestIsChannelIDInList(t *testing.T) {
	assert.False(t, isChannelIDInList(nil, 5))
	assert.False(t, isChannelIDInList([]int{1, 2, 3}, 5))
	assert.True(t, isChannelIDInList([]int{1, 2, 3}, 2))
	assert.True(t, isChannelIDInList([]int{7}, 7))
}

func TestIsChannelEnabledForGroupModel_Guards(t *testing.T) {
	// Guard branches short-circuit to false before any DB/cache access.
	assert.False(t, IsChannelEnabledForGroupModel("", "gpt-4o", 1))
	assert.False(t, IsChannelEnabledForGroupModel("default", "", 1))
	assert.False(t, IsChannelEnabledForGroupModel("default", "gpt-4o", 0))
	assert.False(t, IsChannelEnabledForGroupModel("default", "gpt-4o", -1))
}

func TestIsChannelEnabledForGroupModel_DBPath(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)

	grp := uniq("sat")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o,gpt-4-gizmo-*"
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// exact model + correct group + correct channel => enabled
	assert.True(t, IsChannelEnabledForGroupModel(grp, "gpt-4o", ch.Id))
	// wrong channel id => false
	assert.False(t, IsChannelEnabledForGroupModel(grp, "gpt-4o", ch.Id+123456))
	// wrong group => false
	assert.False(t, IsChannelEnabledForGroupModel("nope-"+grp, "gpt-4o", ch.Id))
	// unknown model, whose normalized form == itself => false (no normalized retry hit)
	assert.False(t, IsChannelEnabledForGroupModel(grp, "no-such-model", ch.Id))

	// normalized-model retry branch: request "gpt-4-gizmo-xyz" normalizes to
	// "gpt-4-gizmo-*", which is a stored ability model.
	assert.True(t, IsChannelEnabledForGroupModel(grp, "gpt-4-gizmo-xyz", ch.Id))
}

func TestIsChannelEnabledForGroupModel_DisabledChannel(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)

	grp := uniq("satd")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
		c.Status = common.ChannelStatusManuallyDisabled // ability.Enabled == false
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// ability exists but enabled=false -> not counted
	assert.False(t, IsChannelEnabledForGroupModel(grp, "gpt-4o", ch.Id))
}

func TestIsChannelEnabledForGroupModel_MemoryCachePath(t *testing.T) {
	requireDB(t)
	grp := uniq("satmc")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o,gpt-4-gizmo-*"
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	enableMemoryCache(t) // rebuilds group2model2channels from DB

	assert.True(t, IsChannelEnabledForGroupModel(grp, "gpt-4o", ch.Id))
	assert.False(t, IsChannelEnabledForGroupModel(grp, "gpt-4o", ch.Id+999999))
	// normalized retry branch in the memory-cache path
	assert.True(t, IsChannelEnabledForGroupModel(grp, "gpt-4-gizmo-xyz", ch.Id))
	// unknown group returns false via nil map lookup
	assert.False(t, IsChannelEnabledForGroupModel("absent-"+grp, "gpt-4o", ch.Id))
}

func TestIsChannelEnabledForAnyGroupModel(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)

	// empty group slice => false without touching DB
	assert.False(t, IsChannelEnabledForAnyGroupModel(nil, "gpt-4o", 1))

	grp := uniq("satany")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// one matching group among several
	assert.True(t, IsChannelEnabledForAnyGroupModel([]string{"x", grp, "y"}, "gpt-4o", ch.Id))
	// none matching
	assert.False(t, IsChannelEnabledForAnyGroupModel([]string{"x", "y"}, "gpt-4o", ch.Id))
}
