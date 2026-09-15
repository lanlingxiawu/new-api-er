package controller

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// planCleanup runs the planner against a fresh context and reports the targets,
// whether the request was rejected, and the rejection body.
func planCleanup(t *testing.T, submitted map[string]float64) ([]string, bool, apiResp) {
	t.Helper()
	encoded, err := common.Marshal(submitted)
	require.NoError(t, err)
	return planCleanupRaw(t, string(encoded))
}

func planCleanupRaw(t *testing.T, submitted string) ([]string, bool, apiResp) {
	t.Helper()
	ctx, rec := newCtx(t, http.MethodPut, "/api/option/", nil)
	asAdmin(ctx, nextTestID())
	targets, aborted := planGroupRatioCleanup(ctx, submitted)
	if !aborted {
		return targets, false, apiResp{}
	}
	return nil, true, decodeResp(t, rec)
}

func TestPlanGroupRatioCleanupIgnoresRatioOnlyEdits(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1, "vip": 9})

	require.False(t, aborted)
	assert.Empty(t, targets, "changing only ratios must not touch any user rows")
}

func TestPlanGroupRatioCleanupTargetsRemovedGroup(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1})

	require.False(t, aborted)
	assert.Equal(t, []string{gone}, targets)
}

// A recreated name is not the same pricing object as the deleted one, so rules
// negotiated against the old pricing must not come back with the name.
func TestPlanGroupRatioCleanupTargetsAddedGroup(t *testing.T) {
	requireDB(t)
	added := uniq("added")
	withGroupRatio(t, map[string]float64{"default": 1})

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1, added: 2})

	require.False(t, aborted)
	assert.Equal(t, []string{added}, targets)
}

func TestPlanGroupRatioCleanupTargetsBothRemovedAndAdded(t *testing.T) {
	requireDB(t)
	gone, added := uniq("gone"), uniq("added")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1, added: 2})

	require.False(t, aborted)
	assert.ElementsMatch(t, []string{gone, added}, targets)
}

// Deleting a group that still serves traffic would leave those channels priced
// at the fallback ratio, so the save is refused outright.
func TestPlanGroupRatioCleanupRejectsGroupWithEnabledChannel(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})
	mkChannel(t, func(ch *model.Channel) { ch.Group = gone })

	targets, aborted, resp := planCleanup(t, map[string]float64{"default": 1})

	require.True(t, aborted, "deleting a group with enabled channels must be refused")
	assert.Nil(t, targets)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, gone, "the message must name the blocking group")
}

func TestPlanGroupRatioCleanupAllowsGroupWithOnlyDisabledChannels(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})
	mkChannel(t, func(ch *model.Channel) {
		ch.Group = gone
		ch.Status = common.ChannelStatusManuallyDisabled
	})

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1})

	require.False(t, aborted)
	assert.Equal(t, []string{gone}, targets)
}

// channels.group holds a comma-separated list, which is why the check parses it
// instead of comparing the column.
func TestPlanGroupRatioCleanupRejectsGroupListedSecondOnAChannel(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})
	mkChannel(t, func(ch *model.Channel) { ch.Group = "default, " + gone })

	_, aborted, resp := planCleanup(t, map[string]float64{"default": 1})

	require.True(t, aborted)
	assert.False(t, resp.Success)
}

// Adding a group is never blocked by channels; only removals are checked.
func TestPlanGroupRatioCleanupDoesNotCheckChannelsWhenOnlyAdding(t *testing.T) {
	requireDB(t)
	added := uniq("added")
	withGroupRatio(t, map[string]float64{"default": 1})
	mkChannel(t, func(ch *model.Channel) { ch.Group = added })

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 1, added: 2})

	require.False(t, aborted)
	assert.Equal(t, []string{added}, targets)
}

// Re-creating a group whose cleanup is still running would let the old exclusive
// ratios — not yet removed from every user — bill the new group, so the save is
// refused until the cleanup has finished.
func TestPlanGroupRatioCleanupRejectsRecreatingGroupUnderCleanup(t *testing.T) {
	requireDB(t)
	busy := uniq("busy")
	withGroupRatio(t, map[string]float64{"default": 1})
	previous := activeGroupRatioCleanups
	activeGroupRatioCleanups = func(groups []string) []string {
		var active []string
		for _, name := range groups {
			if name == busy {
				active = append(active, name)
			}
		}
		return active
	}
	t.Cleanup(func() { activeGroupRatioCleanups = previous })

	targets, aborted, resp := planCleanup(t, map[string]float64{"default": 1, busy: 2})

	require.True(t, aborted, "re-creating a group under cleanup must be refused")
	assert.Nil(t, targets)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, busy, "the message must name the group")
}

// Only names being added are checked: deleting or re-pricing other groups while a
// cleanup runs is unaffected.
func TestPlanGroupRatioCleanupAllowsOtherChangesDuringCleanup(t *testing.T) {
	requireDB(t)
	busy, gone := uniq("busy"), uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})
	previous := activeGroupRatioCleanups
	activeGroupRatioCleanups = func(groups []string) []string {
		var active []string
		for _, name := range groups {
			if name == busy {
				active = append(active, name)
			}
		}
		return active
	}
	t.Cleanup(func() { activeGroupRatioCleanups = previous })

	targets, aborted, _ := planCleanup(t, map[string]float64{"default": 2})

	require.False(t, aborted)
	assert.Equal(t, []string{gone}, targets)
}

func TestPlanGroupRatioCleanupRejectsInvalidJSON(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})

	targets, aborted, resp := planCleanupRaw(t, `{"default":`)

	require.True(t, aborted)
	assert.Nil(t, targets)
	assert.False(t, resp.Success)
}

// The planner is exercised directly above; this covers the wiring, which unit
// tests would otherwise miss entirely: removing a group through the real option
// handler must dispatch the cleanup that strips it from users.
func TestUpdateOptionDispatchesCleanupOnGroupRemoval(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	withGroupRatio(t, map[string]float64{"default": 1, gone: 3})

	// The handler persists GroupRatio; put the stored row back afterwards.
	var stored model.Option
	hadRow := model.DB.Where(&model.Option{Key: "GroupRatio"}).First(&stored).Error == nil
	t.Cleanup(func() {
		if hadRow {
			model.DB.Model(&model.Option{}).Where(&model.Option{Key: "GroupRatio"}).Update("value", stored.Value)
			return
		}
		model.DB.Where(&model.Option{Key: "GroupRatio"}).Delete(&model.Option{})
	})

	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"` + gone + `":3}` })

	ctx, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{
		"key": "GroupRatio", "value": `{"default":1}`,
	})
	asAdmin(ctx, nextTestID())
	UpdateOption(ctx)
	require.True(t, decodeResp(t, rec).Success)

	deadline := time.Now().Add(20 * time.Second)
	for {
		var current model.User
		require.NoError(t, model.DB.First(&current, user.Id).Error)
		if current.GroupRatios == "{}" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup was never dispatched; group_ratios still %q", current.GroupRatios)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
