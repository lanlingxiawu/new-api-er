package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestVerifiedTopUpRechecksSnapshotAndCreditsOnce(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &Log{}))
	oldLog, oldRedis := LOG_DB, common.RedisEnabled
	LOG_DB, common.RedisEnabled = db, false
	t.Cleanup(func() { LOG_DB, common.RedisEnabled = oldLog, oldRedis })
	user := User{Username: "verified-topup-" + common.GetRandomString(8), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	order := TopUp{UserId: user.Id, Amount: 500, Money: 1, TradeNo: "verified-" + common.GetRandomString(15), PaymentProvider: PaymentProviderInfini, ProviderOrderId: "fixture-provider-order", Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	t.Cleanup(func() {
		db.Where("user_id = ?", user.Id).Delete(&Log{})
		db.Delete(&order)
		db.Unscoped().Delete(&user)
	})
	changed := order
	changed.Amount++
	_, err := CompleteVerifiedTopUp(context.Background(), changed, "192.0.2.1")
	require.ErrorIs(t, err, ErrTopUpVerificationChanged)
	first, err := CompleteVerifiedTopUp(context.Background(), order, "192.0.2.1")
	require.NoError(t, err)
	require.Equal(t, 500, first.QuotaAdded)
	require.False(t, first.AlreadyCompleted)
	second, err := CompleteVerifiedTopUp(context.Background(), order, "192.0.2.1")
	require.NoError(t, err)
	require.True(t, second.AlreadyCompleted)
	require.Zero(t, second.QuotaAdded)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 600, stored.Quota)
	require.NoError(t, db.Model(&user).Update("role", common.RoleAdminUser).Error)
	_, err = CompleteVerifiedTopUp(context.Background(), order, "192.0.2.1")
	require.ErrorIs(t, err, ErrUserQuotaPermission)
}
