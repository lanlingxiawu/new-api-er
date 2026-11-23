package model

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Query-only fault injection is confined to this SQLite connection; the shared
// development database is never put into a read-only state.
func TestBatchFailureDiagnosticRetainsUnwrittenDeltas(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; require.NoError(t, conn.Close()) })
	resetBatchStores(t)
	oldDir, oldUnsaved := quotaRecoveryDir, quotaRecoveryUnsaved
	quotaRecoveryDir, quotaRecoveryUnsaved = t.TempDir(), nil
	t.Cleanup(func() { quotaRecoveryDir, quotaRecoveryUnsaved = oldDir, oldUnsaved })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Channel{}, &BusinessStatsAppliedBatch{}))
	user := User{Username: "batch-outage", Quota: 1000}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "synthetic-batch-outage", RemainQuota: 1000}
	require.NoError(t, db.Create(&token).Error)
	channel := Channel{Name: "batch-outage"}
	require.NoError(t, db.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -100)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 100)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -100)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 100)
	// The DB itself rejects writes; there is no mocked ORM or callback.
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	batchUpdate()
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 1000, user.Quota, "the injected failure must prevent the first debit")
	pending, err := os.ReadDir(quotaRecoveryDir)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the rejected batch must remain available for recovery")
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	batchUpdate()
	pending, err = os.ReadDir(quotaRecoveryDir)
	require.NoError(t, err)
	require.Empty(t, pending, "the committed batch must be acknowledged")
	t.Run("user", func(t *testing.T) {
		require.NoError(t, db.First(&user, user.Id).Error)
		require.Equal(t, 900, user.Quota, "a transient write failure must not lose the queued debit")
		require.Equal(t, 100, user.UsedQuota)
		require.Equal(t, 1, user.RequestCount)
	})
	t.Run("token", func(t *testing.T) {
		require.NoError(t, db.First(&token, token.Id).Error)
		require.Equal(t, 900, token.RemainQuota)
		require.Equal(t, 100, token.UsedQuota)
	})
	t.Run("channel", func(t *testing.T) {
		require.NoError(t, db.First(&channel, channel.Id).Error)
		require.EqualValues(t, 100, channel.UsedQuota)
	})
}
