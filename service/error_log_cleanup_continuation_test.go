package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestErrorLogCleanupContinuationFences(t *testing.T) {
	for _, mode := range []string{"expired", "owner", "checkpoint", "receipt_failure", "release_failure"} {
		t.Run(mode, func(t *testing.T) {
			run, task, _ := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
			injected := errors.New("injected continuation rollback")
			want := model.ErrSystemTaskLockLost
			switch mode {
			case "expired":
				require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Update("locked_until", time.Now().Unix()-1).Error)
			case "owner":
				require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Update("locked_by", "replacement").Error)
			case "checkpoint":
				run.Scanned++ // Only saved checkpoints can be transferred.
			case "receipt_failure":
				want = injected
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:receipt_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "data_cleanup_runs" {
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:receipt_failure")) })
			case "release_failure":
				want = injected
				require.NoError(t, model.DB.Callback().Delete().Before("gorm:delete").Register("test:release_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "system_task_locks" {
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Delete().Remove("test:release_failure")) })
			}
			next, err := model.ContinueErrorLogCleanup(context.Background(), run, task, "bounded-cleanup")
			require.ErrorIs(t, err, want)
			require.Nil(t, next)
			var tasks []model.SystemTask
			require.NoError(t, model.DB.Find(&tasks).Error)
			require.Len(t, tasks, 1, "no orphan task may survive a rolled-back handoff")
			require.Equal(t, model.SystemTaskStatusRunning, tasks[0].Status)
			require.NotNil(t, tasks[0].ActiveKey)
			var saved model.DataCleanupRun
			require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
			require.Equal(t, task.TaskID, *saved.TaskID)
			require.Zero(t, saved.Scanned)
			var leases int64
			require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Count(&leases).Error)
			require.EqualValues(t, 1, leases)
		})
	}
}

// Real transactions commit; only delivery of the acknowledgement is faulted.
type cleanupContinuationAckPool struct{ *sql.DB }

func (pool cleanupContinuationAckPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &cleanupContinuationAckTx{tx}, nil
}

type cleanupContinuationAckTx struct{ *sql.Tx }

func (tx cleanupContinuationAckTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

func TestErrorLogCleanupContinuationCommitAcknowledgement(t *testing.T) {
	run, task, _ := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	confirmedDB := model.DB
	pool, err := confirmedDB.DB()
	require.NoError(t, err)
	unconfirmed := confirmedDB.Session(&gorm.Session{NewDB: true}).WithContext(context.Background())
	unconfirmed.ConnPool = cleanupContinuationAckPool{pool}
	unconfirmed.Statement.ConnPool = unconfirmed.ConnPool
	model.DB = unconfirmed
	t.Cleanup(func() { model.DB = confirmedDB })
	next, err := model.ContinueErrorLogCleanup(context.Background(), run, task, "bounded-cleanup")
	model.DB = confirmedDB
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, next)
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, "pending", saved.Status)
	require.NotEqual(t, task.TaskID, *saved.TaskID)
	require.ErrorIs(t, model.UpdateLeasedDataCleanupRun(context.Background(), run.ID, task.TaskID, "bounded-cleanup", map[string]any{"deleted": 999}), model.ErrSystemTaskLockLost)
	require.ErrorIs(t, model.FinishDataCleanupRun(context.Background(), run, "bounded-cleanup", model.SystemTaskStatusFailed, "cleanup.failed"), model.ErrSystemTaskLockLost)
	var active int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("active_key = ?", model.SystemTaskTypeLogCleanup).Count(&active).Error)
	require.EqualValues(t, 1, active)
	// Recovery does not depend on the previous process sending a notification.
	next, err = model.GetSystemTaskByTaskID(*saved.TaskID)
	require.NoError(t, err)
	next, ok, err := model.ClaimSystemTask(next.ID, next.Type, "recovered-worker", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	errorLogCleanupHandler{}.Run(context.Background(), next, "recovered-worker")
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", saved.Status)
	require.EqualValues(t, 1, saved.Deleted)
}
