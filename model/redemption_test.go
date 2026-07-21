package model

import (
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// redemption.go — redeem code generate/validate/redeem, status transitions,
// expiry, batch, soft-delete. Billing-adjacent: the credited quota MUST equal
// the code value and a code MUST NOT be redeemable twice.
//
// Redemption.Key is char(32) UNIQUE; each fixture uses a distinct 32-char key.
// ---------------------------------------------------------------------------

// redKey builds a unique 32-char alphanumeric redemption key.
func redKey() string {
	s := strings.ReplaceAll(uniq("red"), "_", "") + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return s[:32]
}

// mkRedemption inserts a Redemption with a unique key and auto-cleanup.
func mkRedemption(t *testing.T, mut func(r *Redemption)) *Redemption {
	t.Helper()
	requireDB(t)
	r := &Redemption{
		Id:          nextTestID(),
		UserId:      0,
		Key:         redKey(),
		Name:        uniq("rn"),
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       500,
		CreatedTime: common.GetTimestamp(),
		ExpiredTime: 0,
	}
	if mut != nil {
		mut(r)
	}
	require.NoError(t, r.Insert())
	deleteByID(t, &Redemption{}, r.Id)
	return r
}

func TestRedemption_InsertGetUpdateDelete(t *testing.T) {
	requireDB(t)
	r := mkRedemption(t, func(r *Redemption) { r.Name = "orig"; r.Quota = 123 })

	got, err := GetRedemptionById(r.Id)
	require.NoError(t, err)
	assert.Equal(t, r.Key, got.Key)
	assert.Equal(t, 123, got.Quota)

	// id == 0 guard
	_, err = GetRedemptionById(0)
	assert.Error(t, err)

	// Update writes non-zero selected fields.
	r.Name = "renamed"
	r.Quota = 456
	r.Status = common.RedemptionCodeStatusDisabled
	require.NoError(t, r.Update())
	got, _ = GetRedemptionById(r.Id)
	assert.Equal(t, "renamed", got.Name)
	assert.Equal(t, 456, got.Quota)
	assert.Equal(t, common.RedemptionCodeStatusDisabled, got.Status)

	// SelectUpdate can persist zero-ish values for redeemed_time/status.
	r.Status = common.RedemptionCodeStatusUsed
	r.RedeemedTime = 999
	require.NoError(t, r.SelectUpdate())
	got, _ = GetRedemptionById(r.Id)
	assert.Equal(t, common.RedemptionCodeStatusUsed, got.Status)
	assert.EqualValues(t, 999, got.RedeemedTime)
}

func TestDeleteRedemptionById(t *testing.T) {
	requireDB(t)
	// id == 0 guard
	assert.Error(t, DeleteRedemptionById(0))
	// nonexistent id -> record-not-found propagated
	assert.Error(t, DeleteRedemptionById(nextTestID()))

	r := mkRedemption(t, nil)
	require.NoError(t, DeleteRedemptionById(r.Id))
	// soft-deleted: default scope no longer finds it
	_, err := GetRedemptionById(r.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// ---------------------------------------------------------------------------
// Redeem — the billing-critical path.
// ---------------------------------------------------------------------------

func TestRedeem_Guards(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	_, err := Redeem("", u.Id)
	require.Error(t, err)
	assert.Equal(t, "未提供兑换码", err.Error())

	_, err = Redeem("somekey", 0)
	require.Error(t, err)
	assert.Equal(t, "无效的 user id", err.Error())

	// unknown key -> wrapped ErrRedeemFailed
	_, err = Redeem(redKey(), u.Id)
	assert.ErrorIs(t, err, ErrRedeemFailed)
}

func TestRedeem_HappyPath_ExactQuotaAndNoDoubleRedeem(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	r := mkRedemption(t, func(r *Redemption) { r.Quota = 500 })

	// First redeem credits EXACTLY the code value.
	got, err := Redeem(r.Key, u.Id)
	require.NoError(t, err)
	assert.Equal(t, 500, got)

	reloaded, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 500, reloaded.Quota)

	// Code transitioned to used + records the redeemer.
	rr, _ := GetRedemptionById(r.Id)
	assert.Equal(t, common.RedemptionCodeStatusUsed, rr.Status)
	assert.Equal(t, u.Id, rr.UsedUserId)
	assert.Greater(t, rr.RedeemedTime, int64(0))

	// Second redeem of the SAME code must fail and must NOT credit again.
	_, err = Redeem(r.Key, u.Id)
	assert.ErrorIs(t, err, ErrRedeemFailed)
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, 500, reloaded2.Quota) // unchanged — no double-credit
}

func TestRedeem_DisabledCode(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	r := mkRedemption(t, func(r *Redemption) {
		r.Status = common.RedemptionCodeStatusDisabled
		r.Quota = 999
	})
	_, err := Redeem(r.Key, u.Id)
	assert.ErrorIs(t, err, ErrRedeemFailed)
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 0, reloaded.Quota)
}

func TestRedeem_ExpiredCode(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	r := mkRedemption(t, func(r *Redemption) {
		r.Status = common.RedemptionCodeStatusEnabled
		r.ExpiredTime = common.GetTimestamp() - 100 // already expired
		r.Quota = 777
	})
	_, err := Redeem(r.Key, u.Id)
	assert.ErrorIs(t, err, ErrRedeemFailed)
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 0, reloaded.Quota)
}

func TestRedeem_NotYetExpired(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	r := mkRedemption(t, func(r *Redemption) {
		r.ExpiredTime = common.GetTimestamp() + 3600 // future
		r.Quota = 250
	})
	got, err := Redeem(r.Key, u.Id)
	require.NoError(t, err)
	assert.Equal(t, 250, got)
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 250, reloaded.Quota)
}

// ---------------------------------------------------------------------------
// Listing / search / bulk cleanup
// ---------------------------------------------------------------------------

func TestGetAllRedemptions(t *testing.T) {
	requireDB(t)
	r1 := mkRedemption(t, nil)
	r2 := mkRedemption(t, nil)

	// Large page fetches a superset containing our two rows.
	all, total, err := GetAllRedemptions(0, 1000)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(2))
	ids := map[int]bool{}
	for _, r := range all {
		ids[r.Id] = true
	}
	assert.True(t, ids[r1.Id])
	assert.True(t, ids[r2.Id])
}

func TestSearchRedemptions(t *testing.T) {
	requireDB(t)
	prefix := uniq("srchred")
	r1 := mkRedemption(t, func(r *Redemption) { r.Name = prefix + "-a"; r.Status = common.RedemptionCodeStatusEnabled })
	mkRedemption(t, func(r *Redemption) { r.Name = prefix + "-b"; r.Status = common.RedemptionCodeStatusDisabled })

	// keyword by unique name prefix -> both rows
	rows, total, err := SearchRedemptions(prefix, "", 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, rows, 2)

	// keyword + status filter (disabled) -> only the disabled one
	rows, total, err = SearchRedemptions(prefix, strconv.Itoa(common.RedemptionCodeStatusDisabled), 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, prefix+"-b", rows[0].Name)

	// keyword + enabled status -> only enabled (non-expired)
	rows, _, err = SearchRedemptions(prefix, strconv.Itoa(common.RedemptionCodeStatusEnabled), 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, r1.Id, rows[0].Id)

	// numeric keyword matches by id
	rows, total, err = SearchRedemptions(strconv.Itoa(r1.Id), "", 0, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
	found := false
	for _, r := range rows {
		if r.Id == r1.Id {
			found = true
		}
	}
	assert.True(t, found)
}

func TestSearchRedemptions_ExpiredStatus(t *testing.T) {
	requireDB(t)
	prefix := uniq("srchexp")
	// enabled but expired
	exp := mkRedemption(t, func(r *Redemption) {
		r.Name = prefix + "-exp"
		r.Status = common.RedemptionCodeStatusEnabled
		r.ExpiredTime = common.GetTimestamp() - 100
	})
	// enabled and valid
	mkRedemption(t, func(r *Redemption) {
		r.Name = prefix + "-live"
		r.Status = common.RedemptionCodeStatusEnabled
		r.ExpiredTime = 0
	})

	rows, total, err := SearchRedemptions(prefix, "expired", 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, exp.Id, rows[0].Id)
}

func TestDeleteInvalidRedemptions(t *testing.T) {
	requireDB(t)
	// used, disabled, expired-enabled -> all invalid; valid enabled -> kept.
	used := mkRedemption(t, func(r *Redemption) { r.Status = common.RedemptionCodeStatusUsed })
	disabled := mkRedemption(t, func(r *Redemption) { r.Status = common.RedemptionCodeStatusDisabled })
	expired := mkRedemption(t, func(r *Redemption) {
		r.Status = common.RedemptionCodeStatusEnabled
		r.ExpiredTime = common.GetTimestamp() - 100
	})
	valid := mkRedemption(t, func(r *Redemption) {
		r.Status = common.RedemptionCodeStatusEnabled
		r.ExpiredTime = 0
	})

	n, err := DeleteInvalidRedemptions()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(3))

	// invalid ones gone (soft-deleted -> not found in default scope)
	for _, id := range []int{used.Id, disabled.Id, expired.Id} {
		_, e := GetRedemptionById(id)
		assert.ErrorIs(t, e, gorm.ErrRecordNotFound, "id %d should be deleted", id)
	}
	// valid one survives
	_, e := GetRedemptionById(valid.Id)
	assert.NoError(t, e)
}
