package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Repository compatibility only. Production acceptance uses MySQL + PostgreSQL.
func TestQuotaRecoverySQLiteCompatibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "compat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Channel{}, &BusinessStatsAppliedBatch{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	old := DB
	DB = db
	t.Cleanup(func() {
		DB = old
		require.NoError(t, sqlDB.Close())
	})
	t.Run("rollback-replay-corruption", TestQuotaRecoveryRollbackTemporarySnapshotAndCorruption)
}

func TestQuotaRecoveryRejectsAmbiguousWriteErrors(t *testing.T) {
	require.False(t, quotaWriteDefinitelyRejected(errors.New("connection reset after commit")))
	require.False(t, quotaWriteDefinitelyRejected(context.DeadlineExceeded))
	require.True(t, quotaWriteDefinitelyRejected(fmt.Errorf("wrapped: %w", &mysqlDriver.MySQLError{Number: 1290})))
	require.False(t, quotaWriteDefinitelyRejected(&mysqlDriver.MySQLError{Number: 1062}))
}

func TestQuotaRecoveryRollbackTemporarySnapshotAndCorruption(t *testing.T) {
	if DB == nil {
		t.Skip("requires a real database")
	}
	resetBatchStores(t)
	oldDir, oldUnsaved := quotaRecoveryDir, quotaRecoveryUnsaved
	quotaRecoveryDir, quotaRecoveryUnsaved = t.TempDir(), nil
	t.Cleanup(func() { quotaRecoveryDir, quotaRecoveryUnsaved = oldDir, oldUnsaved })
	name := "qr" + strings.ReplaceAll(common.GetUUID(), "-", "")[:12]
	user := User{Username: name, AffCode: name, Password: "unused", Quota: 1000}
	require.NoError(t, DB.Create(&user).Error)
	t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error) })
	batch := quotaRecoveryBatch{Version: 1, ID: strings.ReplaceAll(common.GetUUID(), "-", ""), Entries: []quotaRecoveryDelta{
		{Kind: BatchUpdateTypeUserQuota, ID: user.Id, Quota: -100},
		{Kind: BatchUpdateTypeTokenQuota, ID: -1, Quota: -100},
	}}
	batch.Checksum, _ = quotaBatchChecksum(batch)
	require.Error(t, applyQuotaRecoveryBatch(context.Background(), batch))
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, 1000, user.Quota, "a failed batch must roll back earlier wallet updates and its receipt")
	var receipts int64
	require.NoError(t, DB.Model(&BusinessStatsAppliedBatch{}).Where("batch_key LIKE ?", quotaRecoveryPrefix+batch.ID+":%").Count(&receipts).Error)
	require.Zero(t, receipts)
	batch.Entries = batch.Entries[:1]
	batch.Checksum, _ = quotaBatchChecksum(batch)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("batch_key LIKE ?", quotaRecoveryPrefix+batch.ID+":%").Delete(&BusinessStatsAppliedBatch{}).Error)
	})
	data, err := common.Marshal(batch)
	require.NoError(t, err)
	path := filepath.Join(quotaRecoveryDir, batch.ID+".json")
	require.NoError(t, os.WriteFile(path+".tmp", data, 0o640))
	flushQuotaRecovery()
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, 900, user.Quota)
	require.NoFileExists(t, path)
	// A restored snapshot after commit must not debit again.
	require.NoError(t, os.WriteFile(path, data, 0o640))
	flushQuotaRecovery()
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, 900, user.Quota)
	batch.Entries[0].Quota = -999 // checksum no longer matches
	corrupt, err := common.Marshal(batch)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, corrupt, 0o640))
	flushQuotaRecovery()
	require.NoFileExists(t, path)
	quarantined, err := filepath.Glob(path + ".invalid.*")
	require.NoError(t, err)
	require.Len(t, quarantined, 1)
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, 900, user.Quota)
	// Administratively deleting a channel must not block wallet recovery.
	channel := Channel{Name: name, Key: "synthetic"}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Delete(&channel).Error)
	token := Token{UserId: user.Id, Name: name, Key: name, RemainQuota: 1000}
	require.NoError(t, DB.Create(&token).Error)
	t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&Token{}, token.Id).Error) })
	require.NoError(t, DB.Delete(&token).Error)
	lifecycle := quotaRecoveryBatch{Version: 1, ID: strings.ReplaceAll(common.GetUUID(), "-", ""), Entries: []quotaRecoveryDelta{
		{Kind: BatchUpdateTypeUserQuota, ID: user.Id, Quota: -10},
		{Kind: BatchUpdateTypeTokenQuota, ID: token.Id, Quota: -10},
		{Kind: BatchUpdateTypeChannelUsedQuota, ID: channel.Id, Quota: 10},
	}}
	lifecycle.Checksum, err = quotaBatchChecksum(lifecycle)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("batch_key LIKE ?", quotaRecoveryPrefix+lifecycle.ID+":%").Delete(&BusinessStatsAppliedBatch{}).Error)
	})
	require.NoError(t, applyQuotaRecoveryBatch(context.Background(), lifecycle))
	require.NoError(t, applyQuotaRecoveryBatch(context.Background(), lifecycle))
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, 890, user.Quota)
	require.NoError(t, DB.Unscoped().First(&token, token.Id).Error)
	require.Equal(t, 990, token.RemainQuota)
	require.Equal(t, 10, token.UsedQuota)

}
