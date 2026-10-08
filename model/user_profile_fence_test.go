// Every write of a cached user field must bump users.profile_version (and be
// published), and no path may write profile_version back from an old read.
// Otherwise a cache fill that read the row before the write can land after it
// and re-cache the old value, or the column falls below the Redis profile floor
// and every fill for the user is rejected until the floor expires (a DB read
// plus two Redis round trips on each relay request).
// See docs/design/user-exclusive-group-ratio-deletion.md §14.2.

package model

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func readProfileVersion(t *testing.T, id int) int64 {
	t.Helper()
	var row User
	require.NoError(t, DB.Unscoped().Select("id", "profile_version").First(&row, id).Error)
	return row.ProfileVersion
}

func fenceTestCleanup(t *testing.T, id int) {
	t.Cleanup(func() {
		_ = common.RDB.Del(context.Background(), getUserCacheKey(id), getUserAuthFenceKey(id),
			getUserAuthVersionKey(id), getUserProfileVersionKey(id)).Err()
	})
}

// UpdateUserSetting (user settings page, subscription preference) used a
// field-level cache write without a version bump.
func TestUpdateUserSettingBumpsProfileVersionAndFencesStaleFill(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Setting = `{"language":"en"}` })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, invalidateUserCache(u.Id))

	stale, err := GetUserById(u.Id, false) // a fill reads the row
	require.NoError(t, err)
	require.NoError(t, UpdateUserSetting(u.Id, dto.UserSetting{Language: "fr"}))
	assert.Equal(t, stale.ProfileVersion+1, readProfileVersion(t, u.Id))

	_ = populateUserCache(*stale) // the delayed fill lands
	base, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "fr", base.GetSetting().Language, "the pre-update setting must not be re-cached")
}

// With a cached hash, UpdateUserSetting publishes the new value immediately.
func TestUpdateUserSettingRefreshesCachedHash(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, nil)
	fenceTestCleanup(t, u.Id)
	require.NoError(t, populateUserCache(*u))

	require.NoError(t, UpdateUserSetting(u.Id, dto.UserSetting{Language: "ja"}))
	cached, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "ja", cached.GetSetting().Language)
}

// The group-deletion cleanup rewrites group_ratios by CAS; a fill that read the
// row before the CAS must not re-cache the removed group's ratio, even when the
// user had no cached hash at cleanup time.
func TestCleanupUserGroupRatios_RejectsFillReadBeforeCleanup(t *testing.T) {
	enableRedis(t)
	gone := uniq("gone")
	u := newTestUser(t, func(u *User) { u.GroupRatios = `{"` + gone + `":0.1,"default":0.9}` })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, invalidateUserCache(u.Id))

	stale, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	CleanupUserGroupRatios([]string{gone})
	assert.Equal(t, stale.ProfileVersion+1, readProfileVersion(t, u.Id), "the CAS bumps profile_version")
	floor, err := common.RDB.Get(context.Background(), getUserProfileVersionKey(u.Id)).Int64()
	require.NoError(t, err, "uncached user: the floor is advanced without a publish")
	assert.Equal(t, stale.ProfileVersion+1, floor)
	exists, err := common.RDB.Exists(context.Background(), getUserCacheKey(u.Id)).Result()
	require.NoError(t, err)
	assert.Zero(t, exists)

	_ = populateUserCache(*stale)
	base, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.JSONEq(t, `{"default":0.9}`, base.GroupRatios)
}

func TestCasUpdateUserGroupRatiosBumpsOnlyWhenApplied(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.GroupRatios = `{"a":1}` })
	before := readProfileVersion(t, u.Id)

	applied, err := casUpdateUserGroupRatios(u.Id, `{"stale":1}`, "{}")
	require.NoError(t, err)
	require.False(t, applied)
	assert.Equal(t, before, readProfileVersion(t, u.Id), "a lost CAS changes nothing")

	applied, err = casUpdateUserGroupRatios(u.Id, `{"a":1}`, "{}")
	require.NoError(t, err)
	require.True(t, applied)
	assert.Equal(t, before+1, readProfileVersion(t, u.Id))
}

// inviteUser used to read the inviter, then DB.Save the whole row: an edit
// committed in between was overwritten and profile_version rolled back.
// It now only increments the invite columns.
func TestInviteUserOnlyTouchesInviteColumns(t *testing.T) {
	requireDB(t)
	inviter := newTestUser(t, func(u *User) { u.AffCount = 2; u.AffQuota = 10; u.AffHistoryQuota = 30 })

	// An admin edit commits right before inviteUser's UPDATE executes (after
	// any read inviteUser may have done).
	const hook = "test:interleave_inviter_edit"
	fired := false
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(hook, func(db *gorm.DB) {
		if fired || db.Statement.Table != "users" {
			return
		}
		fired = true
		edited, err := GetUserById(inviter.Id, false)
		require.NoError(t, err)
		edited.GroupRatios = `{"vip":0.5}`
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return edited.EditWithTx(tx, false) }))
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(hook) })

	require.NoError(t, inviteUser(inviter.Id))
	require.True(t, fired)

	var row User
	require.NoError(t, DB.First(&row, inviter.Id).Error)
	assert.Equal(t, 3, row.AffCount)
	assert.Equal(t, 10+common.QuotaForInviter, row.AffQuota)
	assert.Equal(t, 30+common.QuotaForInviter, row.AffHistoryQuota)
	assert.Equal(t, `{"vip":0.5}`, row.GroupRatios, "the concurrent edit must survive")
	assert.Equal(t, inviter.ProfileVersion+1, row.ProfileVersion, "profile_version must never move backwards")

	assert.ErrorIs(t, inviteUser(-1), gorm.ErrRecordNotFound, "unknown inviter is reported like before")
}

func TestTransferAffQuotaToQuotaOnlyTouchesQuotaColumns(t *testing.T) {
	requireDB(t)
	amount := int(common.QuotaPerUnit)
	u := newTestUser(t, func(u *User) { u.AffQuota = amount * 3; u.Quota = 7 })
	edited := *u
	edited.GroupRatios = `{"vip":0.5}`
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return edited.EditWithTx(tx, false) }))
	versionAfterEdit := readProfileVersion(t, u.Id)

	caller := &User{Id: u.Id}
	require.NoError(t, caller.TransferAffQuotaToQuota(amount))
	assert.Equal(t, amount*2, caller.AffQuota, "the caller's struct reflects the transfer")
	assert.Equal(t, 7+amount, caller.Quota)

	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Equal(t, amount*2, row.AffQuota)
	assert.Equal(t, 7+amount, row.Quota)
	assert.Equal(t, `{"vip":0.5}`, row.GroupRatios)
	assert.Equal(t, versionAfterEdit, row.ProfileVersion)

	assert.Error(t, caller.TransferAffQuotaToQuota(amount*3), "insufficient invite quota")
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Equal(t, amount*2, row.AffQuota, "a rejected transfer writes nothing")
}

// Rolling deploy: a field-level refresh of a hash written under another schema
// writes the field but leaves CacheSchema alone. Stamping the current schema
// would make the fields an old schema lacks (GroupRatios, timeouts) read as
// empty on new nodes; skipping the write would leave old nodes on the stale
// field (e.g. the previous Group) until the hash expires.
func TestUserCacheFieldWriteOnForeignSchemaHashKeepsSchema(t *testing.T) {
	enableRedis(t)
	for _, schema := range []int{userCacheSchemaVersion - 1, userCacheSchemaVersion + 1} {
		u := newTestUser(t, func(u *User) { u.GroupRatios = `{"vip":0.5}` })
		fenceTestCleanup(t, u.Id)
		ctx := context.Background()
		key := getUserCacheKey(u.Id)
		// A hash from a node on another schema: no GroupRatios field.
		require.NoError(t, common.RDB.HSet(ctx, key, "Id", u.Id, "AuthVersion", u.AuthVersion,
			"CacheSchema", schema, "Setting", "old", "Group", "old-group", "Quota", 42).Err())
		newGroup := uniq("g")
		require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Update("group", newGroup).Error)

		require.NoError(t, RefreshUserGroupCache(u.Id))
		require.NoError(t, updateUserCacheFieldAtVersion(u.Id, "Setting", "new", u.AuthVersion, u.ProfileVersion))
		fields, err := common.RDB.HGetAll(ctx, key).Result()
		require.NoError(t, err)
		assert.Equal(t, newGroup, fields["Group"], "the other node must see the new group now")
		assert.Equal(t, "new", fields["Setting"])
		assert.Equal(t, strconv.Itoa(schema), fields["CacheSchema"], "the schema stamp is never changed by a field write")
		_, hasRatios := fields["GroupRatios"]
		assert.False(t, hasRatios)

		// This node still treats the hash as stale and refills it in full,
		// keeping the Redis-side quota.
		_, err = cacheGetUserBase(u.Id)
		assert.Error(t, err)
		base, err := GetUserCache(u.Id)
		require.NoError(t, err)
		assert.JSONEq(t, `{"vip":0.5}`, base.GroupRatios)
		cached, err := cacheGetUserBase(u.Id)
		require.NoError(t, err, "the refill stamps the current schema")
		assert.JSONEq(t, `{"vip":0.5}`, cached.GroupRatios)
		assert.Equal(t, 42, cached.Quota, "the refill must not overwrite the cached quota")
	}
}

// Field-level refreshes honour the profile floor: a value read at an older
// profile version does not overwrite a newer published hash.
func TestUserCacheFieldWriteHonoursProfileFloor(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Setting = `{"language":"en"}` })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, UpdateUserSetting(u.Id, dto.UserSetting{Language: "fr"}))
	current, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	require.NoError(t, populateUserCache(*current))

	require.NoError(t, updateUserCacheFieldAtVersion(u.Id, "Setting", `{"language":"en"}`, current.AuthVersion, current.ProfileVersion-1))
	cached, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "fr", cached.GetSetting().Language, "an older-profile value is dropped")

	require.NoError(t, updateUserCacheFieldAtVersion(u.Id, "Setting", `{"language":"de"}`, current.AuthVersion, current.ProfileVersion))
	cached, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "de", cached.GetSetting().Language, "a value at the current profile version is written")

	require.NoError(t, updateUserCacheFieldAtVersion(u.Id, "Group", "vip-refresh", current.AuthVersion, profileUnfenced))
	cached, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "vip-refresh", cached.Group, "unfenced fields ignore the profile floor")
}

// The DB fallbacks of GetUserSetting / GetUsernameById read the value and its
// versions in one query and refresh the cached field asynchronously.
func TestUserFieldReadFallbacksRefreshCache(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Setting = `{"language":"en"}` })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, populateUserCache(*u))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{
		"setting": `{"language":"vi"}`, "username": u.Username + "x",
	}).Error)

	setting, err := GetUserSetting(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vi", setting.Language)
	name, err := GetUsernameById(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, u.Username+"x", name)

	require.Eventually(t, func() bool {
		cached, err := cacheGetUserBase(u.Id)
		return err == nil && cached.GetSetting().Language == "vi" && cached.Username == u.Username+"x"
	}, 2*time.Second, 10*time.Millisecond)

	// Soft-deleted users stay invisible, as with the previous Find-based reads.
	gone := newTestUser(t, nil)
	require.NoError(t, DB.Delete(&User{}, gone.Id).Error)
	name, err = GetUsernameById(gone.Id, true)
	require.NoError(t, err)
	assert.Empty(t, name)

	// Missing user: empty result, no error, no cache write.
	setting, err = GetUserSetting(-1, true)
	require.NoError(t, err)
	assert.Empty(t, setting.Language)
	name, err = GetUsernameById(-1, true)
	require.NoError(t, err)
	assert.Empty(t, name)
}

// UpdateWithTx callers (ManageUser enable/disable/promote/demote, access token,
// aff code, OAuth/WeChat bind, customer remark) pass a full row read earlier.
// Writing its admin-owned columns back undid a concurrent admin edit, and the
// profile_version bump then fenced the cache in favour of the old values.
func TestUpdateWithTxStaleRowDoesNotUndoAdminEdit(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) {
		u.GroupRatios = `{"vip":2}`
		u.RetryTimes = 1
		u.StreamResponseTimeout = 10
		u.NonStreamTimeoutBilling = "refund"
	})
	fenceTestCleanup(t, u.Id)

	stale, err := GetUserById(u.Id, false) // e.g. ManageUser reads the row
	require.NoError(t, err)

	edited, err := GetUserById(u.Id, false) // the admin edit commits in between
	require.NoError(t, err)
	edited.GroupRatios = `{"vip":0.5}`
	edited.RetryTimes = 3
	edited.StreamResponseTimeout = 20
	edited.StreamResponseTimeoutMode = "idle"
	edited.StreamTotalTimeout = 30
	edited.NonStreamResponseTimeout = 40
	edited.NonStreamTotalTimeout = 50
	edited.NonStreamTimeoutBilling = "charge"
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return edited.EditWithTx(tx, false) }))
	require.NoError(t, PublishUserAuthCache(u.Id))

	stale.Status = common.UserStatusDisabled
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return stale.UpdateWithTx(tx, false) }))
	require.NoError(t, updateUserCache(*stale))

	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, row.Status, "the caller's own change is written")
	assert.Equal(t, `{"vip":0.5}`, row.GroupRatios)
	assert.Equal(t, 3, row.RetryTimes)
	assert.Equal(t, 20, row.StreamResponseTimeout)
	assert.Equal(t, "idle", row.StreamResponseTimeoutMode)
	assert.Equal(t, 30, row.StreamTotalTimeout)
	assert.Equal(t, 40, row.NonStreamResponseTimeout)
	assert.Equal(t, 50, row.NonStreamTotalTimeout)
	assert.Equal(t, "charge", row.NonStreamTimeoutBilling)
	assert.Equal(t, edited.ProfileVersion+1, row.ProfileVersion)

	base, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, `{"vip":0.5}`, base.GroupRatios, "billing must use the edited ratio")
	assert.Equal(t, 3, base.RetryTimes)
}

// UpdateWithTx still writes the upstream columns it is given (display name,
// remark, ...) and keeps bumping profile_version.
func TestUpdateWithTxWritesUpstreamFields(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, nil)
	caller := User{Id: u.Id, DisplayName: "dn-update", Remark: "remark-update"}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return caller.UpdateWithTx(tx, false) }))
	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Equal(t, "dn-update", row.DisplayName)
	assert.Equal(t, "remark-update", row.Remark)
	assert.Equal(t, u.ProfileVersion+1, row.ProfileVersion)
}

// EditWithTxGroupRatios(..., false) leaves group_ratios to whatever the row
// holds at commit time, even if the caller's struct carries another value.
func TestEditWithTxGroupRatiosLeavesColumnWhenNotWriting(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.GroupRatios = `{"gone":0.1,"vip":2}` })
	stale, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	applied, err := casUpdateUserGroupRatios(u.Id, `{"gone":0.1,"vip":2}`, `{"vip":2}`)
	require.NoError(t, err)
	require.True(t, applied)

	stale.Remark = "edit-keeps-ratios"
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return stale.EditWithTxGroupRatios(tx, false, false) }))
	assert.Equal(t, `{"vip":2}`, stale.GroupRatios, "the struct is reloaded from the row")
	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Equal(t, `{"vip":2}`, row.GroupRatios)
	assert.Equal(t, "edit-keeps-ratios", row.Remark)
	assert.Equal(t, u.ProfileVersion+2, row.ProfileVersion, "cleanup and edit each bump once")
}

// ClearBinding("email") changes a cached field: it bumps profile_version and
// publishes, so a fill that read the row before cannot restore the address.
// Other bindings are not cached and leave the version alone.
func TestClearBindingEmailBumpsProfileVersionAndFencesStaleFill(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Email = uniq("clr") + "@x.com"; u.GitHubId = uniq("gh") })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, invalidateUserCache(u.Id))

	stale, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	caller := &User{Id: u.Id}
	require.NoError(t, caller.ClearBinding("email"))
	assert.Equal(t, stale.ProfileVersion+1, readProfileVersion(t, u.Id))
	assert.Empty(t, caller.Email)

	_ = populateUserCache(*stale) // the delayed fill lands
	base, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.Empty(t, base.Email, "the cleared email must not be re-cached")

	require.NoError(t, caller.ClearBinding("github"))
	assert.Equal(t, stale.ProfileVersion+1, readProfileVersion(t, u.Id), "non-cached bindings do not bump")
	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	assert.Empty(t, row.GitHubId)
}

// The Creem top-up fills an empty email: same UPDATE bumps profile_version,
// and the cached hash is refreshed without touching the cached quota.
func TestRechargeCreemEmailFillBumpsProfileVersionAndPublishes(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Email = ""; u.Quota = 0 })
	fenceTestCleanup(t, u.Id)
	require.NoError(t, populateUserCache(*u))
	require.NoError(t, cacheIncrUserQuota(u.Id, 5)) // Redis-only delta not yet flushed
	before := readProfileVersion(t, u.Id)

	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderCreem
		tp.PaymentMethod = PaymentMethodCreem
		tp.Amount = 100
	})
	email := uniq("creem") + "@x.com"
	require.NoError(t, RechargeCreem(tp.TradeNo, email, "N", "ip"))
	assert.Equal(t, before+1, readProfileVersion(t, u.Id))
	cached, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, email, cached.Email, "the hash is refreshed immediately")
	assert.Equal(t, 5, cached.Quota, "the publish must not touch the cached quota")

	// Email already set: no bump.
	tp2 := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderCreem
		tp.PaymentMethod = PaymentMethodCreem
		tp.Amount = 100
	})
	require.NoError(t, RechargeCreem(tp2.TradeNo, uniq("other")+"@x.com", "N", "ip"))
	assert.Equal(t, before+1, readProfileVersion(t, u.Id))
}
