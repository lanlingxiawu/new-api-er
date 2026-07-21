package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupUserExtension registers a hard-delete of the extension row for userId.
func cleanupUserExtension(t *testing.T, userId int) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", userId).Delete(&UserExtension{})
		}
	})
}

func TestEnsureUserExtension(t *testing.T) {
	requireDB(t)
	uid := nextTestID()
	cleanupUserExtension(t, uid)

	// first insert creates the row
	require.NoError(t, EnsureUserExtension(uid))

	var count int64
	require.NoError(t, DB.Model(&UserExtension{}).Where("user_id = ?", uid).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	// second call is a no-op (ON CONFLICT DO NOTHING) — still exactly one row
	require.NoError(t, EnsureUserExtension(uid))
	require.NoError(t, DB.Model(&UserExtension{}).Where("user_id = ?", uid).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGetUserExtension(t *testing.T) {
	requireDB(t)

	t.Run("existing row returned", func(t *testing.T) {
		uid := nextTestID()
		cleanupUserExtension(t, uid)
		ext := &UserExtension{UserId: uid, ProfitTotalQuota: 4242, RevenueCustomerCount: 3}
		require.NoError(t, DB.Create(ext).Error)

		got, err := GetUserExtension(uid)
		require.NoError(t, err)
		assert.Equal(t, uid, got.UserId)
		assert.EqualValues(t, 4242, got.ProfitTotalQuota)
		assert.Equal(t, 3, got.RevenueCustomerCount)
	})

	t.Run("missing row returns zero-value struct without error", func(t *testing.T) {
		uid := nextTestID() // never inserted
		got, err := GetUserExtension(uid)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, uid, got.UserId)
		assert.EqualValues(t, 0, got.ProfitTotalQuota)
		assert.EqualValues(t, 0, got.CommissionTotalQuota)
	})
}

func TestGetUserExtensionsByUserIds(t *testing.T) {
	requireDB(t)

	// empty input -> empty (non-nil) map
	m, err := GetUserExtensionsByUserIds(nil)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Empty(t, m)

	uidA := nextTestID()
	uidB := nextTestID()
	uidMissing := nextTestID()
	cleanupUserExtension(t, uidA)
	cleanupUserExtension(t, uidB)
	require.NoError(t, DB.Create(&UserExtension{UserId: uidA, CommissionPendingQuota: 10}).Error)
	require.NoError(t, DB.Create(&UserExtension{UserId: uidB, CommissionPendingQuota: 20}).Error)

	m, err = GetUserExtensionsByUserIds([]int{uidA, uidB, uidMissing})
	require.NoError(t, err)
	require.Len(t, m, 2)
	require.Contains(t, m, uidA)
	require.Contains(t, m, uidB)
	assert.NotContains(t, m, uidMissing)
	assert.EqualValues(t, 10, m[uidA].CommissionPendingQuota)
	assert.EqualValues(t, 20, m[uidB].CommissionPendingQuota)
}
