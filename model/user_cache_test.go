package model

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pure logic on UserBase (no DB / Redis)
// ---------------------------------------------------------------------------

func TestUserBase_GetSetting(t *testing.T) {
	// empty setting -> zero value
	ub := &UserBase{Setting: ""}
	assert.Equal(t, dto.UserSetting{}, ub.GetSetting())

	// valid JSON -> parsed
	ub.Setting = `{"language":"zh"}`
	assert.Equal(t, "zh", ub.GetSetting().Language)

	// invalid JSON -> silent degrade to zero value (logs, no panic)
	ub.Setting = `{not-json`
	assert.Equal(t, dto.UserSetting{}, ub.GetSetting())
}

func TestUserBase_GetGroupRatios(t *testing.T) {
	// empty -> nil
	ub := &UserBase{GroupRatios: ""}
	assert.Nil(t, ub.GetGroupRatios())

	ub.GroupRatios = `{"vip":2.5}`
	got := ub.GetGroupRatios()
	require.NotNil(t, got)
	assert.Equal(t, 2.5, got["vip"])
}

func TestUserBase_WriteContext(t *testing.T) {
	prev := ratio_setting.IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() { ratio_setting.SetUserExclusiveGroupRatioEnabled(prev) })

	ub := &UserBase{
		Id:                        nextTestID(),
		Group:                     "vipgrp",
		GroupRatios:               `{"vipgrp":3}`,
		Email:                     "a@b.com",
		Quota:                     555,
		Status:                    common.UserStatusEnabled,
		Username:                  "ctxuser",
		Setting:                   `{"language":"en"}`,
		StreamResponseTimeout:     11,
		StreamResponseTimeoutMode: "idle",
		StreamTotalTimeout:        22,
		NonStreamResponseTimeout:  33,
		NonStreamTotalTimeout:     44,
	}

	// feature enabled + non-empty ratios -> ratios written
	ratio_setting.SetUserExclusiveGroupRatioEnabled(true)
	c1, _ := gin.CreateTestContext(httptest.NewRecorder())
	ub.WriteContext(c1)
	assert.Equal(t, "vipgrp", common.GetContextKeyString(c1, constant.ContextKeyUserGroup))
	assert.Equal(t, 555, common.GetContextKeyInt(c1, constant.ContextKeyUserQuota))
	assert.Equal(t, common.UserStatusEnabled, common.GetContextKeyInt(c1, constant.ContextKeyUserStatus))
	assert.Equal(t, "ctxuser", common.GetContextKeyString(c1, constant.ContextKeyUserName))
	assert.Equal(t, 11, common.GetContextKeyInt(c1, constant.ContextKeyUserStreamResponseTimeout))
	assert.Equal(t, "idle", common.GetContextKeyString(c1, constant.ContextKeyUserStreamResponseTimeoutMode))
	assert.Equal(t, 22, common.GetContextKeyInt(c1, constant.ContextKeyUserStreamTotalTimeout))
	assert.Equal(t, 33, common.GetContextKeyInt(c1, constant.ContextKeyUserNonStreamResponseTimeout))
	assert.Equal(t, 44, common.GetContextKeyInt(c1, constant.ContextKeyUserNonStreamTotalTimeout))
	_, hasRatios := c1.Get(string(constant.ContextKeyUserGroupRatios))
	assert.True(t, hasRatios)

	// feature disabled -> ratios NOT written (other keys still written)
	ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	ub.WriteContext(c2)
	_, hasRatios2 := c2.Get(string(constant.ContextKeyUserGroupRatios))
	assert.False(t, hasRatios2)
	assert.Equal(t, "vipgrp", common.GetContextKeyString(c2, constant.ContextKeyUserGroup))

	ub.StreamResponseTimeoutMode = ""
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	ub.WriteContext(c3)
	assert.Equal(t, "first_output", common.GetContextKeyString(c3, constant.ContextKeyUserStreamResponseTimeoutMode))
}

func TestGetUserCacheKey(t *testing.T) {
	assert.Equal(t, "user:42", getUserCacheKey(42))
}

// ---------------------------------------------------------------------------
// Redis-disabled guards: every mutating cache helper is a nil no-op.
// ---------------------------------------------------------------------------

func TestUserCache_RedisDisabledNoOps(t *testing.T) {
	require.False(t, common.RedisEnabled)

	assert.NoError(t, invalidateUserCache(1))
	assert.NoError(t, InvalidateUserCache(1))
	assert.NoError(t, populateUserCache(User{Id: 1}))
	assert.NoError(t, updateUserCache(User{Id: 1, Status: common.UserStatusEnabled}))
	assert.NoError(t, cacheIncrUserQuota(1, 5))
	assert.NoError(t, cacheDecrUserQuota(1, 5))
	assert.NoError(t, updateUserStatusCache(1, true))
	assert.NoError(t, updateUserStatusCache(1, false))
	assert.NoError(t, updateUserQuotaCache(1, 10))
	assert.NoError(t, RefreshUserGroupCache(1))
	assert.NoError(t, updateUserEmailCache(1, "e"))
	assert.NoError(t, updateUserNameCache(1, "n"))
	assert.NoError(t, updateUserSettingCache(1, "s"))

	// cacheGetUserBase errors when redis disabled
	_, err := cacheGetUserBase(1)
	assert.Error(t, err)
}

// GetUserCache must fall back to the DB when Redis is disabled.
func TestGetUserCache_DBFallbackNoRedis(t *testing.T) {
	require.False(t, common.RedisEnabled)
	u := newTestUser(t, func(u *User) {
		u.Group = uniq("g")
		u.Quota = 1234
		u.Email = uniq("e") + "@x.com"
	})

	cache, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Id, cache.Id)
	assert.Equal(t, u.Group, cache.Group)
	assert.Equal(t, 1234, cache.Quota)

	// individual field getters route through GetUserCache -> DB
	grp, err := getUserGroupCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Group, grp)

	q, err := getUserQuotaCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 1234, q)

	st, err := getUserStatusCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusEnabled, st)

	name, err := getUserNameCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Username, name)
}

func TestGetUserCache_DBError(t *testing.T) {
	require.False(t, common.RedisEnabled)
	// id 0 -> GetUserById returns error -> GetUserCache propagates it
	_, err := GetUserCache(0)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Redis-enabled round trips
// ---------------------------------------------------------------------------

func TestUserCache_RedisRoundTrip(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) {
		u.Group = uniq("g")
		u.Quota = 1000
		u.Email = uniq("e") + "@x.com"
		u.Setting = `{"language":"ja"}`
		u.StreamResponseTimeout = 15
		u.StreamResponseTimeoutMode = "idle"
		u.StreamTotalTimeout = 30
		u.NonStreamResponseTimeout = 45
		u.NonStreamTotalTimeout = 60
	})
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })

	// populate hash then read back
	require.NoError(t, populateUserCache(*u))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Id, base.Id)
	assert.Equal(t, u.Group, base.Group)
	assert.Equal(t, 1000, base.Quota)
	assert.Equal(t, common.UserStatusEnabled, base.Status)
	assert.Equal(t, 15, base.StreamResponseTimeout)
	assert.Equal(t, "idle", base.StreamResponseTimeoutMode)
	assert.Equal(t, 30, base.StreamTotalTimeout)
	assert.Equal(t, 45, base.NonStreamResponseTimeout)
	assert.Equal(t, 60, base.NonStreamTotalTimeout)

	// atomic quota incr / decr on the hash
	require.NoError(t, cacheIncrUserQuota(u.Id, 250))
	require.NoError(t, cacheDecrUserQuota(u.Id, 50))
	base, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 1000+250-50, base.Quota)

	// field-level updates. Group cache is refreshed from the DB (upstream #6329
	// replaced the direct set with RefreshUserGroupCache), so update the row first.
	require.NoError(t, updateUserQuotaCache(u.Id, 7777))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Update("group", "newg").Error)
	require.NoError(t, RefreshUserGroupCache(u.Id))
	require.NoError(t, updateUserEmailCache(u.Id, "new@e.com"))
	require.NoError(t, updateUserNameCache(u.Id, "newname"))
	require.NoError(t, updateUserStatusCache(u.Id, false))
	require.NoError(t, updateUserSettingCache(u.Id, `{"language":"fr"}`))

	base, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 7777, base.Quota)
	assert.Equal(t, "newg", base.Group)
	assert.Equal(t, "new@e.com", base.Email)
	assert.Equal(t, "newname", base.Username)
	assert.Equal(t, common.UserStatusDisabled, base.Status)

	// GetUserCache now hits Redis (no DB) and returns the mutated snapshot
	cache, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "newg", cache.Group)
	assert.Equal(t, 7777, cache.Quota)

	// language + group-ratios helpers over the cache
	assert.Equal(t, "fr", GetUserLanguage(u.Id))

	// Rolling deployments keep the existing base schema. A hash written by an
	// older instance therefore lacks the timeout fields but remains a valid hit;
	// missing fields must safely mean zero/inheritance instead of forcing every
	// new instance through a synchronous DB refresh.
	require.NoError(t, common.RDB.HDel(context.Background(), getUserCacheKey(u.Id),
		"StreamResponseTimeout", "StreamTotalTimeout",
		"StreamResponseTimeoutMode", "NonStreamResponseTimeout", "NonStreamTotalTimeout").Err())
	legacyBase, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Zero(t, legacyBase.StreamResponseTimeout)
	assert.Empty(t, legacyBase.StreamResponseTimeoutMode)
	assert.Zero(t, legacyBase.StreamTotalTimeout)
	assert.Zero(t, legacyBase.NonStreamResponseTimeout)
	assert.Zero(t, legacyBase.NonStreamTotalTimeout)

	// invalidate -> subsequent hash read misses
	require.NoError(t, invalidateUserCache(u.Id))
	_, err = cacheGetUserBase(u.Id)
	assert.Error(t, err)
}

func TestUpdateUserCache_RedisEnabled(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) {
		u.Group = uniq("g")
		u.Email = uniq("e") + "@x.com"
		u.Setting = `{"language":"vi"}`
	})
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })

	// The field-level writers (RedisHSetField) only refresh an existing hash, so
	// the cache must be populated first — mirroring production where the cache is
	// created on read and later refreshed by profile/settings updates.
	require.NoError(t, populateUserCache(*u))

	// Mutate a few fields then refresh via updateUserCache (writes group/email/
	// status/name/setting, but deliberately NOT quota).
	u.Group = "refreshedgrp"
	u.Email = "refreshed@e.com"
	u.Status = common.UserStatusDisabled
	require.NoError(t, updateUserCache(*u))

	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "refreshedgrp", base.Group)
	assert.Equal(t, "refreshed@e.com", base.Email)
	assert.Equal(t, u.Username, base.Username)
	assert.Equal(t, common.UserStatusDisabled, base.Status)
}

func TestGetUserGroupRatios_Cache(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) {
		u.Group = uniq("g")
		u.GroupRatios = `{"vipgrp":4}`
	})
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })
	require.NoError(t, populateUserCache(*u))

	ratios := GetUserGroupRatios(u.Id)
	require.NotNil(t, ratios)
	assert.Equal(t, 4.0, ratios["vipgrp"])

	// getUserSettingCache path (redis hit)
	got, err := getUserSettingCache(u.Id)
	require.NoError(t, err)
	assert.Equal(t, dto.UserSetting{}, got) // no setting stored -> zero value
}

func TestGetUserGroupRatios_ErrorReturnsNil(t *testing.T) {
	// redis disabled + id 0 -> GetUserCache errors -> nil
	require.False(t, common.RedisEnabled)
	assert.Nil(t, GetUserGroupRatios(0))
	assert.Equal(t, "", GetUserLanguage(0))
}
