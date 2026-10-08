package model

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ToBaseUser normalizes the stored column so a row written before this column
// existed (empty string) still reaches the relay path as "refund".
func TestToBaseUser_NonStreamTimeoutBillingNormalization(t *testing.T) {
	cases := []struct {
		stored string
		want   string
	}{
		{"", "refund"},
		{"   ", "refund"},
		{"refund", "refund"},
		{"charge", "charge"},
	}
	for _, tc := range cases {
		t.Run("stored="+tc.stored, func(t *testing.T) {
			u := &User{Id: nextTestID(), NonStreamTimeoutBilling: tc.stored}
			assert.Equal(t, tc.want, u.ToBaseUser().NonStreamTimeoutBilling)
		})
	}
}

func TestUserBase_WriteContext_NonStreamTimeoutBilling(t *testing.T) {
	cases := []struct {
		cached string
		want   string
	}{
		{"charge", "charge"},
		{"refund", "refund"},
		// A hash cached under an older schema has no such field at all; the
		// decoded zero value must not read as "charge".
		{"", "refund"},
	}
	for _, tc := range cases {
		t.Run("cached="+tc.cached, func(t *testing.T) {
			ub := &UserBase{Id: nextTestID(), NonStreamTimeoutBilling: tc.cached}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ub.WriteContext(c)
			assert.Equal(t, tc.want,
				common.GetContextKeyString(c, constant.ContextKeyUserNonStreamTimeoutBilling))
		})
	}
}

// Bumping userCacheSchemaVersion is what forces hashes written before this
// column existed to be discarded instead of decoding the field as "".
func TestUserCacheSchemaVersionBumpedForTimeoutBilling(t *testing.T) {
	assert.GreaterOrEqual(t, userCacheSchemaVersion, 3,
		"adding NonStreamTimeoutBilling to UserBase requires a schema bump")
}

func TestUserTimeoutBilling_DBRoundTrip(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.NonStreamTimeoutBilling = "charge" })

	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, "charge", stored.NonStreamTimeoutBilling)
	assert.Equal(t, "charge", stored.ToBaseUser().NonStreamTimeoutBilling)
}

// Edit() writes through a map, which - unlike Updates(struct) - does not skip
// zero values. Switching charge -> refund must therefore actually persist.
func TestUserTimeoutBilling_EditPersistsBothDirections(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.NonStreamTimeoutBilling = "charge" })

	u.NonStreamTimeoutBilling = "refund"
	require.NoError(t, u.Edit(false))
	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, "refund", stored.NonStreamTimeoutBilling)

	u.NonStreamTimeoutBilling = "charge"
	require.NoError(t, u.Edit(false))
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, "charge", stored.NonStreamTimeoutBilling)
}

// Update() goes through Updates(struct), which skips zero values. The
// self-service profile path builds a cleanUser with only username/password/
// display_name set, so it must not wipe an admin-configured billing mode.
func TestUserTimeoutBilling_SelfUpdateDoesNotWipe(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.NonStreamTimeoutBilling = "charge" })

	cleanUser := User{Id: u.Id, Username: u.Username, DisplayName: "renamed"}
	require.NoError(t, cleanUser.Update(false))

	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, "charge", stored.NonStreamTimeoutBilling,
		"partial self-update must not reset the billing mode")
	assert.Equal(t, "renamed", stored.DisplayName)
}

func TestUserTimeoutBilling_RedisRoundTrip(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.NonStreamTimeoutBilling = "charge" })
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })

	require.NoError(t, populateUserCache(*u))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, "charge", base.NonStreamTimeoutBilling)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	base.WriteContext(c)
	assert.Equal(t, "charge",
		common.GetContextKeyString(c, constant.ContextKeyUserNonStreamTimeoutBilling))
}

// ---------------------------------------------------------------------------
// Per-user retry quota column
// ---------------------------------------------------------------------------

func TestUserRetryTimes_DBRoundTripAndCache(t *testing.T) {
	requireDB(t)
	// -1 (disabled) must survive the round trip distinctly from 0 (inherit).
	for _, value := range []int{-1, 0, 3} {
		u := newTestUser(t, func(u *User) { u.RetryTimes = value })

		var stored User
		require.NoError(t, DB.First(&stored, u.Id).Error)
		assert.Equal(t, value, stored.RetryTimes)
		assert.Equal(t, value, stored.ToBaseUser().RetryTimes)

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		stored.ToBaseUser().WriteContext(c)
		assert.Equal(t, value,
			common.GetContextKeyInt(c, constant.ContextKeyUserRetryTimes))
	}
}

// Edit() writes through a map, so switching a quota back to 0 (inherit) must
// actually persist rather than being skipped as a zero value.
func TestUserRetryTimes_EditPersistsZero(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.RetryTimes = 5 })

	u.RetryTimes = 0
	require.NoError(t, u.Edit(false))
	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, 0, stored.RetryTimes, "reverting to inherit must persist")
}

// The self-service profile path uses Updates(struct), which skips zero values,
// so a partial update must not wipe an admin-configured quota.
func TestUserRetryTimes_SelfUpdateDoesNotWipe(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) { u.RetryTimes = 4 })

	cleanUser := User{Id: u.Id, Username: u.Username, DisplayName: "renamed2"}
	require.NoError(t, cleanUser.Update(false))

	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	assert.Equal(t, 4, stored.RetryTimes)
}

func TestUserRetryTimes_RedisRoundTrip(t *testing.T) {
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.RetryTimes = -1 })
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })

	require.NoError(t, populateUserCache(*u))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, -1, base.RetryTimes,
		"a disabled quota must not decode as 0 (inherit) through Redis")
}

func TestUserCacheSchemaVersionBumpedForRetryTimes(t *testing.T) {
	assert.GreaterOrEqual(t, userCacheSchemaVersion, 4,
		"adding RetryTimes to UserBase requires a schema bump")
}

// Empty stored values get their default in three places — the cache, the
// request context and the timeout service — plus the column default, which a
// struct tag cannot take from a constant. All must agree with constant.*.
func TestUserTimeoutDefaults_AgreeWithSharedConstants(t *testing.T) {
	base := (&User{}).ToBaseUser()
	assert.Equal(t, constant.NonStreamTimeoutBillingRefund, base.NonStreamTimeoutBilling)
	assert.Equal(t, constant.RelayStreamResponseTimeoutModeFirstOutput, base.StreamResponseTimeoutMode)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	(&UserBase{}).WriteContext(c)
	assert.Equal(t, constant.NonStreamTimeoutBillingRefund, common.GetContextKeyString(c, constant.ContextKeyUserNonStreamTimeoutBilling))
	assert.Equal(t, constant.RelayStreamResponseTimeoutModeFirstOutput, common.GetContextKeyString(c, constant.ContextKeyUserStreamResponseTimeoutMode))

	field, ok := reflect.TypeOf(User{}).FieldByName("NonStreamTimeoutBilling")
	require.True(t, ok)
	assert.Contains(t, field.Tag.Get("gorm"), "default:'"+constant.NonStreamTimeoutBillingRefund+"'")
}
