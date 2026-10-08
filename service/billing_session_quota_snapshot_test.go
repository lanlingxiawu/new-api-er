package service

import (
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 钱包预扣费复用 TokenAuth 已读到的额度快照（relayInfo.UserQuota）：快照足够时
// 不再读一次用户额度；快照可能不足时照旧实时读，判定与报错完全以实时值为准。

// countUserQuotaReads 统计对 users 表的查询次数（Redis 关闭时 GetUserQuota 直接查库）。
func countUserQuotaReads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var reads atomic.Int64
	name := "test:count_user_quota_reads"
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "users" {
			reads.Add(1)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(name) })
	return &reads
}

func TestBS_WalletSnapshot_SufficientSkipsQuotaRead(t *testing.T) {
	truncate(t)
	const uid, tid = 3101, 3101
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-bs-snap-ok", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-snap-ok", "wallet_only")
	info.UserQuota = 100000
	reads := countUserQuotaReads(t)

	s, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.Nil(t, apiErr)
	require.NotNil(t, s)
	assert.Zero(t, reads.Load())
	assert.Equal(t, 98000, getUserQuota(t, uid))
	assert.Equal(t, 100000, info.UserQuota)
}

func TestBS_WalletSnapshot_ExactlyEnoughSkipsQuotaRead(t *testing.T) {
	truncate(t)
	const uid, tid = 3102, 3102
	seedUser(t, uid, 2000)
	seedToken(t, tid, uid, "sk-bs-snap-eq", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-snap-eq", "wallet_only")
	info.UserQuota = 2000
	reads := countUserQuotaReads(t)

	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.Nil(t, apiErr)
	assert.Zero(t, reads.Load())
	assert.Equal(t, 0, getUserQuota(t, uid))
}

func TestBS_WalletSnapshot_ZeroPreConsumeSkipsQuotaRead(t *testing.T) {
	truncate(t)
	const uid, tid = 3103, 3103
	seedUser(t, uid, 500)
	seedToken(t, tid, uid, "sk-bs-snap-zero", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-snap-zero", "wallet_only")
	info.UserQuota = 500
	reads := countUserQuotaReads(t)

	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 0)
	require.Nil(t, apiErr)
	assert.Zero(t, reads.Load())
}

func TestBS_WalletSnapshot_ShortByOneReadsFresh(t *testing.T) {
	truncate(t)
	const uid, tid = 3104, 3104
	seedUser(t, uid, 100000) // 实时余额充足
	seedToken(t, tid, uid, "sk-bs-snap-short", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-snap-short", "wallet_only")
	info.UserQuota = 1999
	reads := countUserQuotaReads(t)

	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.Nil(t, apiErr)
	assert.Equal(t, int64(1), reads.Load())
	assert.Equal(t, 100000, info.UserQuota, "the fresh value replaces the snapshot")
	assert.Equal(t, 98000, getUserQuota(t, uid))
}

func TestBS_WalletSnapshot_ZeroSnapshotReadsFresh(t *testing.T) {
	truncate(t)
	const uid, tid = 3105, 3105
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-bs-snap-unset", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-snap-unset", "wallet_only")
	reads := countUserQuotaReads(t)

	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 0)
	require.Nil(t, apiErr)
	assert.Equal(t, int64(1), reads.Load())
}

func TestBS_WalletSnapshot_BothInsufficientRejectsWithFreshValue(t *testing.T) {
	truncate(t)
	const uid = 3106
	seedUser(t, uid, 1500)

	info := bsRelayInfo(uid, 0, "", "wallet_only")
	info.UserQuota = 1000
	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Equal(t, 1500, getUserQuota(t, uid), "nothing deducted on reject")
}
