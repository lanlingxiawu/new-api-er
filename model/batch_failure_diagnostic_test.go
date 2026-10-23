package model

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Opt-in fault injection against a real, isolated SQLite engine. Keeping this
// separate from ordinary tests lets operators reproduce the known recovery gap
// without introducing an outage in their shared development database.
func TestBatchFailureDiagnosticRetainsUnwrittenDeltas(t *testing.T) {
	if os.Getenv("BATCH_FAILURE_DIAGNOSTIC") != "1" {
		t.Skip("set BATCH_FAILURE_DIAGNOSTIC=1 to exercise the known batch recovery gap")
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; require.NoError(t, conn.Close()) })
	resetBatchStores(t)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Channel{}))
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
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	batchUpdate()
	t.Run("user", func(t *testing.T) {
		require.NoError(t, db.First(&user, user.Id).Error)
		require.Equal(t, 900, user.Quota, "a transient write failure must not lose the queued debit")
	})
	t.Run("token", func(t *testing.T) {
		require.NoError(t, db.First(&token, token.Id).Error)
		require.Equal(t, 900, token.RemainQuota)
	})
	t.Run("channel", func(t *testing.T) {
		require.NoError(t, db.First(&channel, channel.Id).Error)
		require.EqualValues(t, 100, channel.UsedQuota)
	})
}
