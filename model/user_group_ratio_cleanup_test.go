package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readGroupRatios reads the raw column back, bypassing any caching.
func readGroupRatios(t *testing.T, userID int) string {
	t.Helper()
	var raw string
	require.NoError(t, DB.Model(&User{}).Unscoped().
		Where("id = ?", userID).
		Select("group_ratios").
		Scan(&raw).Error)
	return raw
}

func TestCleanupUserGroupRatios_RemovesTargetKeepsOthers(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	kept := uniq("kept")

	target := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":3,"` + kept + `":2}`
	})
	untouched := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + kept + `":2}`
	})

	CleanupUserGroupRatios([]string{gone})

	assert.JSONEq(t, `{"`+kept+`":2}`, readGroupRatios(t, target.Id))
	assert.JSONEq(t, `{"`+kept+`":2}`, readGroupRatios(t, untouched.Id),
		"users without the target group must not be rewritten")
}

// An explicit 0 means "free", not "unset"; only its own group being deleted
// may remove it.
func TestCleanupUserGroupRatios_PreservesExplicitZero(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	free := uniq("free")

	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":1,"` + free + `":0}`
	})

	CleanupUserGroupRatios([]string{gone})

	assert.JSONEq(t, `{"`+free+`":0}`, readGroupRatios(t, u.Id))
}

func TestCleanupUserGroupRatios_RemovesMultipleTargets(t *testing.T) {
	requireDB(t)
	a, b, kept := uniq("a"), uniq("b"), uniq("kept")

	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + a + `":1,"` + b + `":2,"` + kept + `":3}`
	})

	CleanupUserGroupRatios([]string{a, b})

	assert.JSONEq(t, `{"`+kept+`":3}`, readGroupRatios(t, u.Id))
}

func TestCleanupUserGroupRatios_EmptiedMapBecomesEmptyObject(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")

	u := mkUser(t, func(u *User) { u.GroupRatios = `{"` + gone + `":1}` })

	CleanupUserGroupRatios([]string{gone})

	assert.Equal(t, "{}", readGroupRatios(t, u.Id),
		"an emptied map must be stored as {} so later scans skip the row")
}

// Soft-deleted accounts keep their rules and would carry them back if restored.
func TestCleanupUserGroupRatios_IncludesSoftDeletedUsers(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")

	u := mkUser(t, func(u *User) { u.GroupRatios = `{"` + gone + `":4}` })
	require.NoError(t, DB.Delete(&User{}, u.Id).Error)

	CleanupUserGroupRatios([]string{gone})

	assert.Equal(t, "{}", readGroupRatios(t, u.Id))
}

// A single corrupt value must not be overwritten, and must not stop the rest of
// the scan.
func TestCleanupUserGroupRatios_SkipsInvalidJSONAndContinues(t *testing.T) {
	requireDB(t)
	gone := uniq("gone")
	corrupt := `{"` + gone + `":`

	broken := mkUser(t, func(u *User) { u.GroupRatios = corrupt })
	later := mkUser(t, func(u *User) { u.GroupRatios = `{"` + gone + `":5}` })
	require.Greater(t, later.Id, broken.Id, "the valid row must be scanned after the corrupt one")

	CleanupUserGroupRatios([]string{gone})

	assert.Equal(t, corrupt, readGroupRatios(t, broken.Id), "corrupt data must be left untouched")
	assert.Equal(t, "{}", readGroupRatios(t, later.Id), "one bad row must not abort the cleanup")
}

func TestCleanupUserGroupRatios_NoTargetsIsNoop(t *testing.T) {
	requireDB(t)
	kept := uniq("kept")
	raw := `{"` + kept + `":2}`

	u := mkUser(t, func(u *User) { u.GroupRatios = raw })

	CleanupUserGroupRatios(nil)
	CleanupUserGroupRatios([]string{})

	assert.Equal(t, raw, readGroupRatios(t, u.Id))
}

func TestCleanupUserGroupRatios_IsIdempotent(t *testing.T) {
	requireDB(t)
	gone, kept := uniq("gone"), uniq("kept")

	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":1,"` + kept + `":2}`
	})

	CleanupUserGroupRatios([]string{gone})
	first := readGroupRatios(t, u.Id)
	CleanupUserGroupRatios([]string{gone})

	assert.Equal(t, first, readGroupRatios(t, u.Id))
}

// The compare-and-set guard is what stops the cleanup from clobbering a rule an
// admin saved between the scan's read and its write.
func TestCasUpdateUserGroupRatios_RejectsStaleExpectedValue(t *testing.T) {
	requireDB(t)
	current := `{"` + uniq("now") + `":2}`

	u := mkUser(t, func(u *User) { u.GroupRatios = current })

	applied, err := casUpdateUserGroupRatios(u.Id, `{"stale":1}`, "{}")
	require.NoError(t, err)
	assert.False(t, applied, "a stale expected value must not overwrite the row")
	assert.Equal(t, current, readGroupRatios(t, u.Id))

	applied, err = casUpdateUserGroupRatios(u.Id, current, "{}")
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, "{}", readGroupRatios(t, u.Id))
}

// Relay bills from the Redis user cache, so cleanup must refresh the cached
// GroupRatios right away (otherwise a re-created group keeps billing the old
// ratio until the cache expires). It must still never delete the hash or touch
// Quota, which is authoritative in Redis between batch flushes.
func TestCleanupUserGroupRatios_RefreshesCachedRatiosKeepsQuota(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	gone := uniq("gone")

	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":3,"default":0.9}`
		u.AuthVersion = 1
		u.Quota = 500
	})
	require.NoError(t, populateUserCache(*u))
	require.NoError(t, common.RedisHSetField(getUserCacheKey(u.Id), "Quota", "123"))

	CleanupUserGroupRatios([]string{gone})

	quota, err := common.RDB.HGet(context.Background(), getUserCacheKey(u.Id), "Quota").Result()
	require.NoError(t, err, "the user cache must still exist after cleanup")
	assert.Equal(t, "123", quota, "cleanup must not touch the cached Quota")
	cached, err := common.RDB.HGet(context.Background(), getUserCacheKey(u.Id), "GroupRatios").Result()
	require.NoError(t, err)
	assert.Equal(t, `{"default":0.9}`, cached, "the cached exclusive ratios must drop the deleted group immediately")
}

// Users without a cached copy are not read back or re-cached: a later cache
// fill reads the already-cleaned row anyway.
func TestCleanupUserGroupRatios_DoesNotCreateMissingUserCache(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	gone := uniq("gone")

	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":3}`
		u.AuthVersion = 1
	})
	require.NoError(t, common.RDB.Del(context.Background(), getUserCacheKey(u.Id)).Err())

	CleanupUserGroupRatios([]string{gone})

	assert.Equal(t, "{}", readGroupRatios(t, u.Id))
	n, err := common.RDB.Exists(context.Background(), getUserCacheKey(u.Id)).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "cleanup must not create a cache entry for an uncached user")
}

func TestGroupsWithEnabledChannels(t *testing.T) {
	requireDB(t)
	used := uniq("used")
	unused := uniq("unused")
	disabledOnly := uniq("disabled")

	mkChannel(t, func(ch *Channel) { ch.Group = used })
	mkChannel(t, func(ch *Channel) {
		ch.Group = disabledOnly
		ch.Status = common.ChannelStatusManuallyDisabled
	})

	blocked, err := GroupsWithEnabledChannels([]string{used, unused, disabledOnly})
	require.NoError(t, err)
	assert.Equal(t, []string{used}, blocked)
}

// channels.group is a comma-separated list, which is exactly why the check
// parses it instead of matching the column directly.
func TestGroupsWithEnabledChannels_MultiGroupChannel(t *testing.T) {
	requireDB(t)
	first, second := uniq("first"), uniq("second")

	mkChannel(t, func(ch *Channel) { ch.Group = first + ", " + second })

	blocked, err := GroupsWithEnabledChannels([]string{second})
	require.NoError(t, err)
	assert.Equal(t, []string{second}, blocked, "a group listed second in the channel must still be found")
}

func TestGroupsWithEnabledChannels_EmptyInput(t *testing.T) {
	requireDB(t)
	blocked, err := GroupsWithEnabledChannels(nil)
	require.NoError(t, err)
	assert.Empty(t, blocked)
}

func TestGroupsWithEnabledChannels_NoMatch(t *testing.T) {
	requireDB(t)
	blocked, err := GroupsWithEnabledChannels([]string{uniq("never")})
	require.NoError(t, err)
	assert.Empty(t, blocked)
}

// waitForCleanupQueue blocks until the background drainer has finished.
func waitForCleanupQueue(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		groupRatioQueueMu.Lock()
		idle := !groupRatioQueueDraining && len(groupRatioQueuePending) == 0
		groupRatioQueueMu.Unlock()
		if idle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cleanup queue did not drain in time")
}

func TestScheduleUserGroupRatioCleanup_RunsAsynchronously(t *testing.T) {
	requireDB(t)
	gone, kept := uniq("gone"), uniq("kept")
	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + gone + `":1,"` + kept + `":2}`
	})

	ScheduleUserGroupRatioCleanup([]string{gone})
	waitForCleanupQueue(t)

	assert.JSONEq(t, `{"`+kept+`":2}`, readGroupRatios(t, u.Id))
}

// A burst of group edits must cost one pass over the users table, not one pass
// per edit, and no target may be dropped along the way.
func TestScheduleUserGroupRatioCleanup_MergesBurst(t *testing.T) {
	requireDB(t)
	a, b, c, kept := uniq("a"), uniq("b"), uniq("c"), uniq("kept")
	u := mkUser(t, func(u *User) {
		u.GroupRatios = `{"` + a + `":1,"` + b + `":2,"` + c + `":3,"` + kept + `":4}`
	})

	ScheduleUserGroupRatioCleanup([]string{a})
	ScheduleUserGroupRatioCleanup([]string{b})
	ScheduleUserGroupRatioCleanup([]string{c})
	waitForCleanupQueue(t)

	assert.JSONEq(t, `{"`+kept+`":4}`, readGroupRatios(t, u.Id),
		"every queued target must be applied even when merged")
}

// 清理排队或扫描中的分组要报告出来（保存用户专属倍率时据此拒绝设置新规则）；空闲的分组不报。
func TestGroupRatioCleanupsActive(t *testing.T) {
	groupRatioQueueMu.Lock()
	prevPending, prevRunning := groupRatioQueuePending, groupRatioQueueRunning
	groupRatioQueuePending = map[string]struct{}{"queued": {}}
	groupRatioQueueRunning = map[string]struct{}{"scanning": {}}
	groupRatioQueueMu.Unlock()
	t.Cleanup(func() {
		groupRatioQueueMu.Lock()
		groupRatioQueuePending, groupRatioQueueRunning = prevPending, prevRunning
		groupRatioQueueMu.Unlock()
	})

	assert.Equal(t, []string{"queued", "scanning"}, GroupRatioCleanupsActive([]string{"scanning", "idle", "queued"}))
	assert.Empty(t, GroupRatioCleanupsActive([]string{"idle"}))
	assert.Empty(t, GroupRatioCleanupsActive(nil))
}

func TestScheduleUserGroupRatioCleanup_EmptyIsNoop(t *testing.T) {
	requireDB(t)
	raw := `{"` + uniq("kept") + `":2}`
	u := mkUser(t, func(u *User) { u.GroupRatios = raw })

	ScheduleUserGroupRatioCleanup(nil)
	ScheduleUserGroupRatioCleanup([]string{})
	waitForCleanupQueue(t)

	assert.Equal(t, raw, readGroupRatios(t, u.Id))
	groupRatioQueueMu.Lock()
	defer groupRatioQueueMu.Unlock()
	assert.False(t, groupRatioQueueDraining, "an empty schedule must not start a worker")
}
