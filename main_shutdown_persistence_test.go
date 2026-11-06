package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A periodic flush can own a batch that it has removed from the shared maps.
// Hold its first SQL before the transaction begins, then exercise the production
// shutdown coordinator. Returning from shutdown must mean that this batch has
// also reached the database, rather than only checking the now-empty maps.
// SQLite is an explicitly isolated scheduling fixture here; this test neither
// changes database behavior nor substitutes for the three-database load runs.
func TestShutdownWithBatchFlush_WaitsForBatchAlreadyInFlight(t *testing.T) {
	if os.Getenv("FORK_SHUTDOWN_LOAD") != "1" {
		t.Skip("set FORK_SHUTDOWN_LOAD=1 to run controlled shutdown persistence scenarios")
	}
	const childEnv = "FORK_SHUTDOWN_PERSISTENCE_CHILD"
	if os.Getenv(childEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestShutdownWithBatchFlush_WaitsForBatchAlreadyInFlight$", "-test.count=1", "-test.timeout=40s", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1", "SQL_DSN=", "LOG_SQL_DSN=", "REDIS_CONN_STRING=")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "isolated persistence child failed:\n%s", output)
		t.Logf("isolated persistence child:\n%s", output)
		return
	}
	// The child owns these globals for its entire lifetime. Redis defaults to
	// enabled before InitRedisClient, and DecreaseUserQuota launches a cache task
	// even when disabled, so never restore this flag while that task may read it.
	common.RedisEnabled = false

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "inflight-batch.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}))
	user := model.User{Username: "shutdown-batch", Password: "unused-fixture", AffCode: "shutdown-batch", Quota: 1000}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "shutdown-batch-fixture", Name: "shutdown-batch", RemainQuota: 1000}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Key: "synthetic", Name: "shutdown-batch"}
	require.NoError(t, db.Create(&channel).Error)

	oldDB, oldBatch := model.DB, common.BatchUpdateEnabled
	model.DB, common.BatchUpdateEnabled = db, true
	gateEntered, releaseGate := make(chan struct{}), make(chan struct{})
	periodicDone, shutdownDone := make(chan struct{}), make(chan struct{})
	var gateOnce, releaseOnce sync.Once
	periodicStarted, shutdownStarted := false, false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseGate) })
		if periodicStarted {
			select {
			case <-periodicDone:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "periodic batch did not finish after releasing the SQL gate")
			}
		}
		if shutdownStarted {
			select {
			case <-shutdownDone:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "shutdown did not finish after releasing the SQL gate")
			}
		}
		model.DB, common.BatchUpdateEnabled = oldDB, oldBatch
	})
	require.NoError(t, db.Callback().Update().Before("gorm:begin_transaction").Register("shutdown_batch_sql_gate", func(tx *gorm.DB) {
		if tx.Statement.Table != "tokens" {
			return
		}
		gateOnce.Do(func() {
			close(gateEntered)
			<-releaseGate
		})
	}))

	const charge = 150
	require.NoError(t, model.DecreaseUserQuota(user.Id, charge, false))
	require.NoError(t, model.DecreaseTokenQuota(token.Id, token.Key, charge))
	model.UpdateUserUsedQuotaAndRequestCount(user.Id, charge)
	model.UpdateChannelUsedQuota(channel.Id, charge)
	periodicStarted = true
	go func() {
		defer close(periodicDone)
		model.FlushBatchUpdate()
	}()
	select {
	case <-gateEntered:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "periodic flush did not reach the first SQL gate")
	}

	shutdownStarted = true
	go func() {
		defer close(shutdownDone)
		shutdownWithBatchFlush(func() error { return nil }, nil, model.FlushBatchUpdate)
	}()
	select {
	case <-shutdownDone:
		// The gate is still closed. Any early return exposes unpersisted quota.
	case <-time.After(5 * time.Second):
		// A coordinator that joins the in-flight flush waits for the DB to resume.
		releaseOnce.Do(func() { close(releaseGate) })
		select {
		case <-shutdownDone:
		case <-time.After(10 * time.Second):
			require.FailNow(t, "shutdown did not join the released periodic flush")
		}
	}

	var persistedUser model.User
	var persistedToken model.Token
	var persistedChannel model.Channel
	require.NoError(t, db.First(&persistedUser, user.Id).Error)
	require.NoError(t, db.First(&persistedToken, token.Id).Error)
	require.NoError(t, db.First(&persistedChannel, channel.Id).Error)
	assert.Equal(t, 1000-charge, persistedUser.Quota, "shutdown returned before the user wallet debit persisted")
	assert.Equal(t, charge, persistedUser.UsedQuota, "shutdown returned before user usage persisted")
	assert.Equal(t, 1, persistedUser.RequestCount, "shutdown returned before the request count persisted")
	assert.Equal(t, 1000-charge, persistedToken.RemainQuota, "shutdown returned before the token debit persisted")
	assert.Equal(t, charge, persistedToken.UsedQuota, "shutdown returned before token usage persisted")
	assert.EqualValues(t, charge, persistedChannel.UsedQuota, "shutdown returned before channel usage persisted")
}
