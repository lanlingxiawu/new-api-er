package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

func cleanupWorkflowDB(t *testing.T) {
	t.Helper()
	oldDB, oldLogDB, oldPath := model.DB, model.LOG_DB, common.SQLitePath
	t.Setenv("LOG_SQL_DSN", "")
	var db *gorm.DB
	var err error
	if dsn := os.Getenv("CLEANUP_WORKFLOW_TEST_DSN"); dsn != "" {
		isolated, cleanup, isolateErr := isolatedServiceTestDSN(dsn, "cleanup")
		require.NoError(t, isolateErr)
		t.Cleanup(cleanup)
		t.Setenv("SQL_DSN", isolated)
		db, _, err = model.OpenDataCleanupDB(context.Background(), false)
	} else {
		common.SQLitePath = filepath.Join(t.TempDir(), "cleanup.db")
		t.Setenv("SQL_DSN", "local")
		db, err = gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	}
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	// This fixture represents the application pool, not the single-connection
	// maintenance pool opened separately by previews and workers.
	pool.SetMaxOpenConns(8)
	require.NoError(t, db.AutoMigrate(&model.DataCleanupRun{}, &model.FinancialDetailTombstone{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.SystemInstance{}, &model.ConsumptionCost{}, &model.EmployeeCommissionLog{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.SQLitePath = oldDB, oldLogDB, oldPath
		pool, e := db.DB()
		require.NoError(t, e)
		require.NoError(t, pool.Close())
	})
}

func TestDataCleanupWorkflow(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	before := time.Now().Add(-3 * 24 * time.Hour).Unix()
	id := 123
	require.NoError(t, model.DB.Create(&model.ConsumptionCost{LogId: &id, CreatedAt: before - 1, RevenueQuota: 50}).Error)
	id2 := 124
	require.NoError(t, model.DB.Create(&model.ConsumptionCost{LogId: &id2, CreatedAt: before, RevenueQuota: 50}).Error)
	require.NoError(t, model.DB.Create(&model.ConsumptionCost{CreatedAt: before - 1, RevenueQuota: 20}).Error)
	preview, err := PreviewDataCleanup(ctx, 1, model.CleanupLedger, before)
	require.NoError(t, err)
	require.Equal(t, 1, preview.Matched)
	require.True(t, preview.CountExact)
	_, err = StartDataCleanup(ctx, 2, preview.ID)
	require.ErrorIs(t, err, ErrCleanupPreview)
	run, err := StartDataCleanup(ctx, 1, preview.ID)
	require.NoError(t, err)
	repeated, err := StartDataCleanup(ctx, 1, preview.ID)
	require.NoError(t, err)
	require.Equal(t, run.TaskID, repeated.TaskID)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "cleanup-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	dataCleanupHandler{}.Run(ctx, claimed, "cleanup-test")
	require.NoError(t, model.DB.First(run, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", run.Status)
	require.EqualValues(t, 1, run.Deleted)
	var remaining []model.ConsumptionCost
	require.NoError(t, model.DB.Order("id").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	require.Equal(t, &id2, remaining[0].LogId)
	require.Nil(t, remaining[1].LogId)
	// The independent maintenance connection must use this isolated database.
	var markers []model.FinancialDetailTombstone
	require.NoError(t, model.DB.Find(&markers).Error)
	require.Len(t, markers, 1)
	require.Equal(t, id, markers[0].LogID)
}

func TestDataCleanupPreviewExpiryAndGlobalConflict(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	preview, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(preview).Update("expires_at", time.Now().Unix()-1).Error)
	_, err = StartDataCleanup(ctx, 1, preview.ID)
	require.ErrorIs(t, err, ErrCleanupPreview)
	preview, err = PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	_, err = model.CreateSystemTask(model.SystemTaskTypeLogCleanup, nil, nil)
	require.NoError(t, err)
	_, err = StartDataCleanup(ctx, 1, preview.ID)
	require.ErrorIs(t, err, ErrCleanupBusy)
	require.NoError(t, model.DB.First(preview, "id = ?", preview.ID).Error)
	require.Equal(t, "preview", preview.Status)
	require.Nil(t, preview.TaskID)
}

func TestLogCleanupSharedActiveKeyConflict(t *testing.T) {
	for _, taskType := range []string{model.SystemTaskTypeDataCleanup, model.SystemTaskTypeErrorLogCleanup} {
		for _, status := range []model.SystemTaskStatus{model.SystemTaskStatusPending, model.SystemTaskStatusRunning} {
			t.Run(taskType+"/"+string(status), func(t *testing.T) {
				cleanupWorkflowDB(t)
				activeKey := model.SystemTaskTypeLogCleanup
				existing := model.SystemTask{
					TaskID: "existing-cleanup", Type: taskType, ActiveKey: &activeKey,
					Status: model.SystemTaskStatusPending, Payload: `{"run_id":"private-cleanup-receipt"}`,
				}
				require.NoError(t, model.DB.Create(&existing).Error)
				if status == model.SystemTaskStatusRunning {
					_, claimed, err := model.ClaimSystemTask(existing.ID, taskType, "cleanup-owner", common.GetTimestamp()+60)
					require.NoError(t, err)
					require.True(t, claimed)
				}

				task, err := StartLogCleanupTask(1234, "")
				require.ErrorIs(t, err, ErrCleanupBusy)
				require.Nil(t, task, "another cleanup's receipt must not be returned as a log cleanup")
				var count int64
				require.NoError(t, model.DB.Model(&model.SystemTask{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
				stored, err := model.GetSystemTaskByTaskID(existing.TaskID)
				require.NoError(t, err)
				require.Equal(t, status, stored.Status)
				require.Equal(t, existing.Payload, stored.Payload)
				require.Equal(t, &activeKey, stored.ActiveKey)

				if status == model.SystemTaskStatusPending {
					_, claimed, err := model.ClaimSystemTask(existing.ID, taskType, "cleanup-owner", common.GetTimestamp()+60)
					require.NoError(t, err)
					require.True(t, claimed)
				}
				terminalStatus := model.SystemTaskStatusSucceeded
				if status == model.SystemTaskStatusRunning {
					terminalStatus = model.SystemTaskStatusFailed
				}
				require.NoError(t, model.FinishSystemTask(existing.TaskID, "cleanup-owner", terminalStatus, nil, ""))
				task, err = StartLogCleanupTask(1234, "")
				require.NoError(t, err)
				require.Equal(t, model.SystemTaskTypeLogCleanup, task.Type)
				var payload LogCleanupPayload
				require.NoError(t, task.DecodePayload(&payload))
				require.EqualValues(t, 1234, payload.TargetTimestamp)
				repeated, err := StartLogCleanupTask(5678, "")
				require.NoError(t, err)
				require.Equal(t, task.TaskID, repeated.TaskID)
				require.Equal(t, task.Payload, repeated.Payload, "reusing a task must preserve its original cutoff")
			})
		}
	}
}

func TestLogCleanupConcurrentReservation(t *testing.T) {
	for _, taskType := range []string{model.SystemTaskTypeLogCleanup, model.SystemTaskTypeDataCleanup, model.SystemTaskTypeErrorLogCleanup, model.SystemTaskTypeChannelTest} {
		t.Run(taskType, func(t *testing.T) {
			cleanupWorkflowDB(t)
			activeKey := model.SystemTaskTypeLogCleanup
			if taskType == model.SystemTaskTypeChannelTest {
				activeKey = taskType
			}
			winner := model.SystemTask{
				TaskID: "concurrent-cleanup", Type: taskType, ActiveKey: &activeKey,
				Status: model.SystemTaskStatusPending, Payload: `{"target_timestamp":1000}`,
			}
			// Commit a competing task after the active-type lookup but before the
			// INSERT. Both writes use the real database and its unique constraint.
			require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:cleanup_reservation", func(tx *gorm.DB) {
				task, ok := tx.Statement.Dest.(*model.SystemTask)
				if !ok || task.Type != model.SystemTaskTypeLogCleanup || task.TaskID == winner.TaskID {
					return
				}
				if taskType == model.SystemTaskTypeChannelTest {
					// A task ID collision without a shared reservation remains a
					// database failure; it must not be disguised as cleanup busy.
					winner.TaskID = task.TaskID
				}
				tx.AddError(model.DB.Create(&winner).Error)
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Create().Remove("test:cleanup_reservation")) })
			task, err := StartLogCleanupTask(1234, "")
			if taskType == model.SystemTaskTypeLogCleanup {
				require.NoError(t, err)
				require.Equal(t, winner.TaskID, task.TaskID)
				require.Equal(t, winner.Payload, task.Payload)
			} else if taskType == model.SystemTaskTypeChannelTest {
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrCleanupBusy)
				require.Nil(t, task)
			} else {
				require.ErrorIs(t, err, ErrCleanupBusy)
				require.Nil(t, task)
			}
			var count int64
			require.NoError(t, model.DB.Model(&model.SystemTask{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestFinancialCleanupRejectsOldNodes(t *testing.T) {
	cleanupWorkflowDB(t)
	require.NoError(t, model.DB.Create(&model.SystemInstance{NodeName: "old", Info: "{}"}).Error)
	_, err := PreviewDataCleanup(context.Background(), 1, model.CleanupLedger, time.Now().Add(-90*24*time.Hour).Unix())
	require.ErrorIs(t, err, ErrCleanupNodes)
}

func TestErrorLogCleanupPreviewWithoutDedicatedIndex(t *testing.T) {
	cleanupWorkflowDB(t)
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_cleanup_created_id"))
	before := time.Now().Add(-48 * time.Hour).Unix()
	require.NoError(t, model.LOG_DB.Create(&model.Log{Id: 1, Type: model.LogTypeError, CreatedAt: before - 1}).Error)
	run, err := PreviewDataCleanup(context.Background(), 1, model.CleanupErrorLogs, before)
	require.NoError(t, err)
	require.Equal(t, "time", run.ScanMode)
	require.Equal(t, 1, run.Matched)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "preview must not delete")
}

func TestErrorLogCleanupResumeKeepsSnapshotAndProgress(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	before := time.Now().Add(-48 * time.Hour).Unix()
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_cleanup_created_id"))
	logs := []model.Log{{Id: 1, Type: model.LogTypeConsume, CreatedAt: before - 1}, {Id: 2, Type: model.LogTypeError, CreatedAt: before - 1}}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	preview, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, before)
	require.NoError(t, err)
	run, err := StartDataCleanup(ctx, 1, preview.ID)
	require.NoError(t, err)
	oldTask, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskTypeErrorLogCleanup, oldTask.Type)
	_, ok, err := model.ClaimSystemTask(oldTask.ID, oldTask.Type, "resume-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, oldTask, "resume-test", run))
	var savedPlan errorLogCleanupPayload
	require.NoError(t, common.UnmarshalJsonStr(oldTask.Payload, &savedPlan))
	require.NotNil(t, savedPlan.ScanPlan)
	require.Equal(t, preview.ErrorLogCleanupPlan, *savedPlan.ScanPlan)
	batch, err := model.DeleteDataCleanupBatch(ctx, model.LOG_DB, run, 1)
	require.NoError(t, err)
	require.Equal(t, 1, batch.Scanned)
	require.Zero(t, batch.Deleted)
	require.NoError(t, model.DB.Model(run).Updates(map[string]any{"status": "failed", "cursor_at": run.CursorAt, "cursor_id": run.CursorID, "scanned": run.Scanned}).Error)
	require.NoError(t, model.FinishSystemTask(oldTask.TaskID, "resume-test", model.SystemTaskStatusFailed, nil, "test interruption"))
	// A lease expiry can leave the receipt pending even though the task failed.
	require.NoError(t, model.DB.Model(run).Updates(map[string]any{"status": "pending", "expires_at": 0}).Error)
	_, err = ResumeDataCleanup(ctx, 2, run.ID)
	require.ErrorIs(t, err, ErrCleanupPreview)
	resumed, err := ResumeDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	require.NotEqual(t, oldTask.TaskID, *resumed.TaskID)
	require.Equal(t, 1, resumed.CursorID)
	require.EqualValues(t, 1, resumed.Scanned)
	require.Equal(t, preview.MaxID, resumed.MaxID)
	require.Empty(t, resumed.ScanMode, "scan state is not a database column")
	repeated, err := ResumeDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, resumed.TaskID, repeated.TaskID)
	require.NoError(t, model.LOG_DB.Create(&model.Log{Id: 3, Type: model.LogTypeError, CreatedAt: before - 2}).Error)
	task, err := model.GetSystemTaskByTaskID(*resumed.TaskID)
	require.NoError(t, err)
	require.Equal(t, oldTask.Payload, task.Payload, "continuation must copy the persisted plan")
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "resume-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	errorLogCleanupHandler{}.Run(ctx, claimed, "resume-test")
	require.NoError(t, model.DB.First(resumed, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", resumed.Status)
	require.EqualValues(t, 2, resumed.Scanned)
	require.EqualValues(t, 1, resumed.Deleted)
	_, err = ResumeDataCleanup(ctx, 1, run.ID)
	require.ErrorIs(t, err, ErrCleanupPreview)
	var ids []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []int{1, 3}, ids)
}

func TestErrorLogCleanupResumeRejectsOtherCleanupAndConflict(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	financial, err := PreviewDataCleanup(ctx, 1, model.CleanupLedger, time.Now().Add(-90*24*time.Hour).Unix())
	require.NoError(t, err)
	_, err = ResumeDataCleanup(ctx, 1, financial.ID)
	require.ErrorIs(t, err, ErrCleanupPreview)
	run, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	_, err = ResumeDataCleanup(ctx, 1, run.ID)
	require.ErrorIs(t, err, ErrCleanupPreview, "an unconfirmed preview is not a stopped task")
	run, err = StartDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	oldID := *run.TaskID
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", oldID).Updates(map[string]any{"status": model.SystemTaskStatusFailed, "active_key": nil}).Error)
	require.NoError(t, model.DB.Model(run).Update("status", "failed").Error)
	_, err = model.CreateSystemTask(model.SystemTaskTypeLogCleanup, nil, nil)
	require.NoError(t, err)
	_, err = ResumeDataCleanup(ctx, 1, run.ID)
	require.ErrorIs(t, err, ErrCleanupBusy)
	require.NoError(t, model.DB.First(run, "id = ?", run.ID).Error)
	require.Equal(t, "failed", run.Status, "a conflict must roll back the resume claim")
	require.Equal(t, oldID, *run.TaskID)
}

func TestErrorLogCleanupLostIndexRequiresNewPreview(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_cleanup_created_id"))
	run, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	require.Equal(t, "time", run.ScanMode)
	run, err = StartDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "index-lost", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, claimed, "index-lost", run))
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_created_at_id"))
	errorLogCleanupHandler{}.Run(ctx, claimed, "index-lost")
	require.NoError(t, model.DB.First(run, "id = ?", run.ID).Error)
	require.Equal(t, "failed", run.Status)
	require.Equal(t, "cleanup.index_missing", run.ErrorKey)
	_, err = ResumeDataCleanup(ctx, 1, run.ID)
	require.ErrorIs(t, err, ErrCleanupIndex)
	newPreview, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, run.Before)
	require.NoError(t, err)
	require.Equal(t, "id", newPreview.ScanMode)
}

func TestErrorLogCleanupPlanPersistence(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	before := time.Now().Add(-48 * time.Hour).Unix()
	require.NoError(t, model.LOG_DB.Create(&model.Log{Id: 1, Type: model.LogTypeError, CreatedAt: before - 1}).Error)
	run, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, before)
	require.NoError(t, err)
	run, err = StartDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "plan-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	originalPayload := claimed.Payload
	// A late insert may extend the scan endpoint, but cannot extend deletion scope.
	require.NoError(t, model.LOG_DB.Create(&model.Log{Id: 2, Type: model.LogTypeError, CreatedAt: before - 1}).Error)
	require.ErrorIs(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, claimed, "wrong-runner", run), model.ErrSystemTaskLockLost)
	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, originalPayload, stored.Payload)
	require.NoError(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, claimed, "plan-test", run))
	require.Equal(t, 1, run.MaxID)
	plan := run.ErrorLogCleanupPlan
	stored, err = model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, claimed.Payload, stored.Payload)
	// Reload from the database as a restarted worker would; no plan columns exist.
	restored := &model.DataCleanupRun{}
	require.NoError(t, model.DB.First(restored, "id = ?", run.ID).Error)
	require.Empty(t, restored.ScanMode)
	require.NoError(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, stored, "plan-test", restored))
	require.Equal(t, plan, restored.ErrorLogCleanupPlan)
	badTask := *stored
	badTask.Payload = originalPayload
	restored.Scanned = 1
	require.Error(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, &badTask, "plan-test", restored), "lost plan cannot restart after progress")
	badTask.Payload = "{"
	require.Error(t, prepareErrorLogCleanupTask(ctx, model.LOG_DB, &badTask, "plan-test", restored))
	errorLogCleanupHandler{}.Run(ctx, stored, "plan-test")
	require.NoError(t, model.DB.First(restored, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", restored.Status)
	require.EqualValues(t, 1, restored.Deleted)
	var ids []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []int{2}, ids)
}

func TestDataCleanupDates(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2028, 3, 3, 12, 0, 0, 0, time.UTC),
	} {
		t.Run(now.Format("2006-01-02"), func(t *testing.T) {
			for _, kind := range []string{model.CleanupLedger, model.CleanupCommission, model.CleanupErrorLogs} {
				limit := now.Add(-3 * 24 * time.Hour).Unix()
				if kind == model.CleanupErrorLogs {
					limit = now.Add(-24 * time.Hour).Unix()
				}
				for _, before := range []int64{-1, 0, now.Unix(), now.Unix() + 1} {
					require.Error(t, ValidateDataCleanupDate(kind, before, now))
				}
				require.NoError(t, ValidateDataCleanupDate(kind, limit, now))
				require.NoError(t, ValidateDataCleanupDate(kind, limit-1, now))
				require.Error(t, ValidateDataCleanupDate(kind, limit+1, now))
			}
			require.Error(t, ValidateDataCleanupDate("all", 1, now))
		})
	}
}

func TestErrorLogCleanupDirectStart(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	before := time.Now().Add(-48 * time.Hour).Unix()
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_cleanup_created_id"))
	require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, "idx_created_at_id"))
	logs := []model.Log{
		{Id: 1, Type: model.LogTypeConsume, CreatedAt: before - 1},
		{Id: 2, Type: model.LogTypeError, CreatedAt: before - 1},
		{Id: 3, Type: model.LogTypeError, CreatedAt: before},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	run, err := StartErrorLogCleanup(ctx, 1, before)
	require.NoError(t, err)
	require.Equal(t, "pending", run.Status)
	require.Equal(t, 3, run.MaxID)
	require.Zero(t, run.Matched, "direct submission must not count matches")
	require.False(t, run.CountExact)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 3, count, "submitting a task must not delete synchronously")
	_, err = StartErrorLogCleanup(ctx, 1, before)
	require.ErrorIs(t, err, ErrCleanupBusy)
	require.NoError(t, model.DB.Model(&model.DataCleanupRun{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "conflicting submissions must not leave orphan runs")
	require.NoError(t, model.LOG_DB.Create(&model.Log{Id: 4, Type: model.LogTypeError, CreatedAt: before - 2}).Error)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "direct-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	errorLogCleanupHandler{}.Run(ctx, claimed, "direct-test")
	require.NoError(t, model.DB.First(run, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", run.Status)
	require.EqualValues(t, 1, run.Deleted)
	var ids []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []int{1, 3, 4}, ids)
}

func TestErrorLogCleanupLeaseGuards(t *testing.T) {
	cleanupWorkflowDB(t)
	ctx := context.Background()
	run, err := StartErrorLogCleanup(ctx, 1, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	_, ok, err := model.ClaimSystemTask(task.ID, task.Type, "lease-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	updates := map[string]any{"status": "running", "scanned": 0}
	for range 2 {
		require.NoError(t, model.UpdateLeasedDataCleanupRun(ctx, run.ID, task.TaskID, "lease-test", updates), "no-op updates must not lose an active MySQL lease")
	}
	require.ErrorIs(t, model.UpdateLeasedDataCleanupRun(ctx, run.ID, task.TaskID, "wrong-runner", updates), model.ErrSystemTaskLockLost)
	callback := "cleanup_terminal_receipt_failure"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "data_cleanup_runs" {
			tx.AddError(fmt.Errorf("injected terminal receipt failure"))
		}
	}))
	require.Error(t, model.FinishDataCleanupRun(ctx, run, "lease-test", model.SystemTaskStatusSucceeded, ""))
	require.NoError(t, model.DB.Callback().Update().Remove(callback))
	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusRunning, stored.Status, "receipt failure must roll back task completion")
	require.NotNil(t, stored.ActiveKey)
	var held int64
	require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Count(&held).Error)
	require.EqualValues(t, 1, held, "receipt failure must not release the task lease")
	require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Update("locked_until", time.Now().Unix()-1).Error)
	require.ErrorIs(t, model.UpdateLeasedDataCleanupRun(ctx, run.ID, task.TaskID, "lease-test", map[string]any{"scanned": 123}), model.ErrSystemTaskLockLost)
	require.ErrorIs(t, model.FinishDataCleanupRun(ctx, run, "lease-test", model.SystemTaskStatusSucceeded, ""), model.ErrSystemTaskLockLost)
	require.NoError(t, model.DB.First(run, "id = ?", run.ID).Error)
	require.Equal(t, "running", run.Status)
	require.Zero(t, run.Scanned)
}

func TestErrorLogCleanupStaleWorkerCannotOverwriteResumedProgress(t *testing.T) {
	cleanupWorkflowDB(t)
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_size": "100"})
	ctx := context.Background()
	before := time.Now().Add(-48 * time.Hour).Unix()
	logs := make([]model.Log, 250)
	for i := range logs {
		logs[i] = model.Log{Id: i + 1, Type: model.LogTypeError, CreatedAt: before - 1}
	}
	require.NoError(t, model.LOG_DB.CreateInBatches(logs, 100).Error)
	run, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, before)
	require.NoError(t, err)
	run, err = StartDataCleanup(ctx, 1, run.ID)
	require.NoError(t, err)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "stale-runner", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	interleaved := false
	var resumedResult model.DataCleanupRun
	callback := "audit_cleanup_resume_before_progress"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:begin_transaction").Register(callback, func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if !ok || interleaved || tx.Statement.Table != "data_cleanup_runs" {
			return
		}
		if _, ok := values["cursor_id"]; !ok {
			return
		}
		interleaved = true
		// The old worker has committed its log deletion and is paused before
		// saving its cursor. Another node expires its lease and resumes the run.
		now := time.Now().Unix()
		require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Update("locked_until", now-1).Error)
		require.NoError(t, model.ExpireStaleSystemTaskLocks(now))
		resumed, err := ResumeDataCleanup(ctx, 1, run.ID)
		require.NoError(t, err)
		newTask, err := model.GetSystemTaskByTaskID(*resumed.TaskID)
		require.NoError(t, err)
		newClaim, ok, err := model.ClaimSystemTask(newTask.ID, newTask.Type, "current-runner", time.Now().Add(time.Minute).Unix())
		require.NoError(t, err)
		require.True(t, ok)
		errorLogCleanupHandler{}.Run(ctx, newClaim, "current-runner")
		require.NoError(t, model.DB.First(&resumedResult, "id = ?", run.ID).Error)
		require.Equal(t, "succeeded", resumedResult.Status)
		require.Equal(t, 250, resumedResult.CursorID)
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
	errorLogCleanupHandler{}.Run(ctx, claimed, "stale-runner")
	require.True(t, interleaved)
	var actual model.DataCleanupRun
	require.NoError(t, model.DB.First(&actual, "id = ?", run.ID).Error)
	t.Logf("resumed task completed at cursor=%d scanned=%d deleted=%d; after stale worker write cursor=%d scanned=%d deleted=%d status=%s", resumedResult.CursorID, resumedResult.Scanned, resumedResult.Deleted, actual.CursorID, actual.Scanned, actual.Deleted, actual.Status)
	require.Equal(t, resumedResult, actual, "an expired worker must not overwrite its replacement's progress or status")
}

func claimedErrorLogCleanupFixture(t *testing.T, logs []model.Log) (*model.DataCleanupRun, *model.SystemTask, operation_setting.DataCleanupSetting) {
	t.Helper()
	cleanupWorkflowDB(t)
	previous := operation_setting.GetDataCleanupSetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"enabled": "true"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"enabled": fmt.Sprint(previous.Enabled)}))
	})
	for _, index := range []string{"idx_cleanup_created_id", "idx_created_at_id"} {
		require.NoError(t, model.LOG_DB.Migrator().DropIndex(&model.Log{}, index))
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	run, err := StartErrorLogCleanup(context.Background(), 1, time.Now().Add(-48*time.Hour).Unix())
	require.NoError(t, err)
	task, err := model.GetSystemTaskByTaskID(*run.TaskID)
	require.NoError(t, err)
	task, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "bounded-cleanup", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, prepareErrorLogCleanupTask(context.Background(), model.LOG_DB, task, "bounded-cleanup", run))
	require.Equal(t, "id", run.ScanMode)
	cfg := operation_setting.GetDataCleanupSetting()
	cfg.BatchSleepMS = 100
	return run, task, cfg
}

func setErrorLogCleanupTestSettings(t *testing.T, values map[string]string) {
	t.Helper()
	encoded, err := common.Marshal(operation_setting.GetDataCleanupSetting())
	require.NoError(t, err)
	var old map[string]any
	require.NoError(t, common.Unmarshal(encoded, &old))
	restore := make(map[string]string, len(values))
	for key := range values {
		restore[key] = fmt.Sprint(old[key])
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", values))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", restore)) })
}

func TestErrorLogCleanupAutoContinuation(t *testing.T) {
	logs := make([]model.Log, 2501)
	for i := range logs {
		logs[i] = model.Log{Id: 1000000001 + i, Type: model.LogTypeError, CreatedAt: 10}
	}
	logs[len(logs)-1].Type = model.LogTypeConsume
	// Create in bounded inserts, then use the ordinary task creation and lease.
	run, task, cfg := claimedErrorLogCleanupFixture(t, logs[:1])
	require.NoError(t, model.LOG_DB.CreateInBatches(logs[1:], 200).Error)
	run.MaxID = logs[len(logs)-1].Id
	require.NoError(t, model.DB.Model(run).Update("max_id", run.MaxID).Error)
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_auto_continue": "true", "error_log_batch_size": "5000"})
	ctx, cancel := context.WithTimeout(context.Background(), 19*time.Second)
	defer cancel()
	require.ErrorIs(t, runErrorLogCleanup(ctx, model.LOG_DB, task, "bounded-cleanup", run, cfg), errErrorLogCleanupContinued)
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, "pending", saved.Status)
	require.NotEqual(t, task.TaskID, *saved.TaskID)
	require.EqualValues(t, 2000, saved.Deleted)
	require.Equal(t, run.Before, saved.Before)
	require.Equal(t, run.MaxID, saved.MaxID)
	require.ErrorIs(t, model.FinishDataCleanupRun(context.Background(), run, "bounded-cleanup", model.SystemTaskStatusFailed, "cleanup.failed"), model.ErrSystemTaskLockLost)
	next, err := model.GetSystemTaskByTaskID(*saved.TaskID)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(next.ID, next.Type, "next-cleanup", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	errorLogCleanupHandler{}.Run(context.Background(), claimed, "next-cleanup")
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", saved.Status)
	require.EqualValues(t, 2500, saved.Deleted)
	var remaining int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&remaining).Error)
	require.EqualValues(t, 1, remaining)
}

func TestErrorLogCleanupContinuationRollback(t *testing.T) {
	run, task, _ := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	injected := errors.New("continuation creation failed")
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:handoff_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_tasks" {
			tx.AddError(injected)
		}
	}))
	_, err := model.ContinueErrorLogCleanup(context.Background(), run, task, "bounded-cleanup")
	require.ErrorIs(t, err, injected)
	require.NoError(t, model.DB.Callback().Create().Remove("test:handoff_failure"))
	previous, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusRunning, previous.Status)
	require.NotNil(t, previous.ActiveKey)
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, task.TaskID, *saved.TaskID)
	require.NoError(t, model.UpdateSystemTaskState(task.TaskID, "bounded-cleanup", map[string]any{}))
}

func TestErrorLogCleanupSeekFallbackAndLeaseFence(t *testing.T) {
	for _, mode := range []string{"timeout", "lost_lease"} {
		t.Run(mode, func(t *testing.T) {
			run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 131073, Type: model.LogTypeError, CreatedAt: 10}})
			probes := 0
			require.NoError(t, model.LOG_DB.Callback().Query().After("gorm:query").Register("test:seek_failure", func(tx *gorm.DB) {
				if tx.Statement.Table != "logs" {
					return
				}
				limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
				if !ok || limit.Limit == nil || *limit.Limit != 1 {
					return
				}
				probes++
				if mode == "timeout" {
					tx.AddError(context.DeadlineExceeded)
					return
				}
				require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", task.TaskID).Update("locked_by", "replacement").Error)
			}))
			t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:seek_failure")) })
			err := runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, cfg)
			require.Equal(t, 1, probes, "failed probes must cool down instead of repeating on every empty window")
			if mode == "timeout" {
				require.NoError(t, err)
				require.EqualValues(t, 1, run.Deleted)
				require.EqualValues(t, 1, run.Scanned)
			} else {
				require.ErrorIs(t, err, model.ErrSystemTaskLockLost)
				require.Zero(t, run.CursorID)
				var count int64
				require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
			}
		})
	}
}

func TestErrorLogCleanupPreparationAndStateRetries(t *testing.T) {
	for _, mode := range []string{"state_timeout", "plan_commit_ack"} {
		t.Run(mode, func(t *testing.T) {
			run, task, _ := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
			// Exercise initial plan persistence again, as a newly submitted task.
			run.ErrorLogCleanupPlan = model.ErrorLogCleanupPlan{}
			payload, err := common.Marshal(errorLogCleanupPayload{RunID: run.ID})
			require.NoError(t, err)
			task.Payload = string(payload)
			require.NoError(t, model.DB.Model(task).Update("payload", task.Payload).Error)
			injected := false
			require.NoError(t, model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("test:preparation_retry", func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || injected || tx.Statement.Table != "system_tasks" {
					return
				}
				key := "state"
				if mode == "plan_commit_ack" {
					key = "payload"
				}
				if _, ok := values[key]; ok {
					injected = true
					tx.AddError(context.DeadlineExceeded)
				}
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:preparation_retry")) })
			require.NoError(t, prepareErrorLogCleanupWithRetry(context.Background(), model.LOG_DB, task, "bounded-cleanup", run))
			require.True(t, injected)
			require.Equal(t, "id", run.ScanMode)
			persisted, err := model.GetSystemTaskByTaskID(task.TaskID)
			require.NoError(t, err)
			require.Equal(t, task.Payload, persisted.Payload)
			var count int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
			require.EqualValues(t, 1, count, "preparation does not delete records")
		})
	}
}

func TestErrorLogCleanupRetryAndRecovery(t *testing.T) {
	logs := make([]model.Log, 30)
	for i := range logs {
		logs[i] = model.Log{Id: i + 1, Type: model.LogTypeError, CreatedAt: 10}
	}
	run, task, cfg := claimedErrorLogCleanupFixture(t, logs)
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_size": "4"})
	var limits []int
	require.NoError(t, model.LOG_DB.Callback().Query().Before("gorm:query").Register("test:cleanup_timeout", func(tx *gorm.DB) {
		if tx.Statement.Table != "logs" {
			return
		}
		limit := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
		if *limit.Limit == 1 {
			return
		} // The optional positioning probe is not a deletion attempt.
		limits = append(limits, *limit.Limit)
		if len(limits) == 1 {
			tx.AddError(&net.DNSError{Err: "injected transport timeout", IsTimeout: true})
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:cleanup_timeout")) })
	require.NoError(t, runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, cfg))
	require.Greater(t, len(limits), 11)
	require.Less(t, limits[1], limits[0], "retry must reduce work")
	require.Equal(t, limits[0], limits[len(limits)-1], "fast successful batches restore the configured workload after recovery")
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.EqualValues(t, 30, saved.Deleted)
	require.EqualValues(t, 30, saved.Scanned)
	require.Equal(t, run.MaxID, saved.CursorID)
}

func TestErrorLogCleanupSlowerSuccessfulBatchesReduceWork(t *testing.T) {
	logs := make([]model.Log, 14)
	for i := range logs {
		logs[i] = model.Log{Id: i + 1, Type: model.LogTypeError, CreatedAt: 10}
	}
	run, task, _ := claimedErrorLogCleanupFixture(t, logs)
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_size": "4", "error_log_batch_timeout_sec": "2"})
	var limits []int
	require.NoError(t, model.LOG_DB.Callback().Query().After("gorm:query").Register("test:slow_success", func(tx *gorm.DB) {
		if tx.Statement.Table != "logs" {
			return
		}
		limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
		if !ok || limit.Limit == nil || *limit.Limit == 1 {
			return
		}
		limits = append(limits, *limit.Limit)
		// Keep real commits successful, but cross half of the two-second budget.
		// There is almost a full second of headroom for a loaded CI database.
		time.Sleep(1100 * time.Millisecond)
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:slow_success")) })
	require.NoError(t, runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, operation_setting.GetDataCleanupSetting()))
	require.Equal(t, []int{40, 40, 40, 20}, limits)
	require.EqualValues(t, 14, run.Deleted)
}

func TestErrorLogCleanupOnlineLimits(t *testing.T) {
	run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	logs := make([]model.Log, 2500)
	for i := range logs {
		logs[i] = model.Log{Id: i + 2, Type: model.LogTypeError, CreatedAt: 10}
	}
	require.NoError(t, model.LOG_DB.CreateInBatches(logs, 200).Error)
	run.MaxID = 2501
	require.NoError(t, model.DB.Model(run).Update("max_id", run.MaxID).Error)
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_size": "5000", "error_log_batch_timeout_sec": "5"})
	var limits []int
	var deadlines []time.Duration
	require.NoError(t, model.LOG_DB.Callback().Query().Before("gorm:query").Register("test:online_batch", func(tx *gorm.DB) {
		if tx.Statement.Table != "logs" {
			return
		}
		limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
		if !ok || limit.Limit == nil || *limit.Limit == 1 {
			return
		}
		limits = append(limits, *limit.Limit)
		deadline, ok := tx.Statement.Context.Deadline()
		require.True(t, ok)
		deadlines = append(deadlines, time.Until(deadline))
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:online_batch")) })
	require.NoError(t, model.LOG_DB.Callback().Delete().After("gorm:delete").Register("test:online_config", func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" {
			require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"error_log_batch_size": "1000", "error_log_batch_timeout_sec": "2"}))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Delete().Remove("test:online_config")) })
	require.NoError(t, runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, cfg))
	require.Equal(t, []int{20000, 10000}, limits)
	require.Greater(t, deadlines[0], 4*time.Second)
	require.LessOrEqual(t, deadlines[0], 5*time.Second)
	require.Greater(t, deadlines[1], time.Second)
	require.LessOrEqual(t, deadlines[1], 2*time.Second)
	require.EqualValues(t, 2501, run.Deleted)
}

func TestErrorLogCleanupRetryStops(t *testing.T) {
	for _, mode := range []string{"persistent_timeout", "permanent_error", "cancel", "disabled", "lost_lease"} {
		t.Run(mode, func(t *testing.T) {
			run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			queries := 0
			failure := errors.New("injected permanent query failure")
			require.NoError(t, model.LOG_DB.Callback().Query().Before("gorm:query").Register("test:cleanup_stop", func(tx *gorm.DB) {
				if tx.Statement.Table != "logs" {
					return
				}
				if limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit); ok && limit.Limit != nil && *limit.Limit == 1 {
					return
				}
				queries++
				switch mode {
				case "permanent_error":
					tx.AddError(failure)
					return
				case "cancel":
					cancel()
				case "disabled":
					require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"enabled": "false"}))
				case "lost_lease":
					require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", task.TaskID).Update("locked_by", "replacement-worker").Error)
				}
				tx.AddError(context.DeadlineExceeded)
			}))
			t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:cleanup_stop")) })
			err := runErrorLogCleanup(ctx, model.LOG_DB, task, "bounded-cleanup", run, cfg)
			require.Error(t, err)
			expectedQueries := 1
			if mode == "persistent_timeout" {
				expectedQueries = 4
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			if mode == "permanent_error" {
				require.ErrorIs(t, err, failure)
			}
			if mode == "lost_lease" {
				require.ErrorIs(t, err, model.ErrSystemTaskLockLost)
			}
			require.Equal(t, expectedQueries, queries)
			require.Zero(t, run.CursorID)
			require.Zero(t, run.Deleted)
			var count int64
			require.NoError(t, model.LOG_DB.Raw("SELECT COUNT(*) FROM logs").Scan(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestErrorLogCleanupProgressRetryDoesNotRepeatDelete(t *testing.T) {
	run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	deletes, receipts := 0, 0
	require.NoError(t, model.LOG_DB.Callback().Delete().After("gorm:delete").Register("test:cleanup_delete_count", func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" {
			deletes++
		}
	}))
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:cleanup_receipt_timeout", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_cleanup_runs" {
			receipts++
			if receipts == 1 {
				tx.AddError(context.DeadlineExceeded)
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.LOG_DB.Callback().Delete().Remove("test:cleanup_delete_count"))
		require.NoError(t, model.DB.Callback().Update().Remove("test:cleanup_receipt_timeout"))
	})
	require.NoError(t, runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, cfg))
	require.Equal(t, 1, deletes)
	require.Equal(t, 2, receipts)
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.EqualValues(t, 1, saved.Deleted)
	require.Equal(t, 1, saved.CursorID)
}

func TestErrorLogCleanupWorkerEmptyWindowsAndStateDeadline(t *testing.T) {
	run, task, _ := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 131073, Type: model.LogTypeError, CreatedAt: 10}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, model.UpdateSystemTaskStateContext(ctx, task.TaskID, "bounded-cleanup", map[string]any{"deleted": 123}), context.Canceled)
	errorLogCleanupHandler{}.Run(context.Background(), task, "bounded-cleanup")
	var saved model.DataCleanupRun
	require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
	require.Equal(t, "succeeded", saved.Status)
	require.EqualValues(t, 1, saved.Deleted)
	require.EqualValues(t, 1, saved.Scanned)
	require.Equal(t, saved.MaxID, saved.CursorID)
}

func TestErrorLogCleanupPostgresLockTimeoutRecovery(t *testing.T) {
	run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	if model.LOG_DB.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL table-lock cancellation and retry")
	}
	pool, err := model.LOG_DB.DB()
	require.NoError(t, err)
	lock, err := pool.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer lock.Rollback()
	_, err = lock.Exec("LOCK TABLE logs IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	failed := make(chan struct{}, 1)
	require.NoError(t, model.LOG_DB.Callback().Query().After("gorm:query").Register("test:cleanup_lock_timeout", func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" && tx.Error != nil {
			select {
			case failed <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Query().Remove("test:cleanup_lock_timeout")) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	released := make(chan error, 1)
	go func() {
		select {
		case <-failed:
		case <-ctx.Done():
		}
		released <- lock.Rollback()
	}()
	setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_timeout_sec": "1"})
	require.NoError(t, runErrorLogCleanup(ctx, model.LOG_DB, task, "bounded-cleanup", run, cfg))
	require.NoError(t, <-released)
	require.EqualValues(t, 1, run.Deleted)
	require.Equal(t, run.MaxID, run.CursorID)
}

func TestErrorLogCleanupProgressFailureKeepsSavedCursor(t *testing.T) {
	run, task, cfg := claimedErrorLogCleanupFixture(t, []model.Log{{Id: 1, Type: model.LogTypeError, CreatedAt: 10}})
	deletes, receipts := 0, 0
	require.NoError(t, model.LOG_DB.Callback().Delete().After("gorm:delete").Register("test:cleanup_delete_count", func(tx *gorm.DB) {
		if tx.Statement.Table == "logs" {
			deletes++
		}
	}))
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:cleanup_receipt_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_cleanup_runs" {
			receipts++
			tx.AddError(context.DeadlineExceeded)
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Delete().Remove("test:cleanup_delete_count")) })
	err := runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", run, cfg)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, deletes)
	require.Equal(t, 4, receipts)
	require.NoError(t, model.DB.Callback().Update().Remove("test:cleanup_receipt_failure"))
	var restored model.DataCleanupRun
	require.NoError(t, model.DB.First(&restored, "id = ?", run.ID).Error)
	require.Zero(t, restored.Deleted)
	require.Zero(t, restored.CursorID)
	restored.ErrorLogCleanupPlan = run.ErrorLogCleanupPlan
	require.NoError(t, runErrorLogCleanup(context.Background(), model.LOG_DB, task, "bounded-cleanup", &restored, cfg))
	require.Equal(t, 1, deletes, "replaying the saved cursor must not delete an already removed log again")
	require.Equal(t, restored.MaxID, restored.CursorID)
	require.Zero(t, restored.Deleted, "lost receipts retain lower-bound counts")
}

type cleanupScaleTrace struct {
	gormlogger.Interface
	samples     map[string][]float64
	started     time.Time
	firstCommit float64
	batchSizes  map[int64]int
}

func (trace *cleanupScaleTrace) Trace(_ context.Context, start time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	phase := "other"
	switch {
	case strings.HasPrefix(sql, "SELECT id,created_at"):
		phase = "scan"
	case strings.HasPrefix(sql, "DELETE FROM"):
		phase = "delete"
	case strings.HasPrefix(sql, "UPDATE"):
		phase = "state_or_receipt"
	}
	trace.samples[phase] = append(trace.samples[phase], float64(time.Since(start).Microseconds())/1000)
}

// Opt-in scale acceptance uses a labelled disposable fixture, never a project
// database. The normal suite exercises the same worker with private small data.
func TestErrorLogCleanupScaleWorker(t *testing.T) {
	dsn := os.Getenv("CLEANUP_SCALE_DSN")
	if dsn == "" {
		t.Skip("requires the isolated error-cleanup-bounded-20261121 scale fixture")
	}
	data, err := os.ReadFile(os.Getenv("CLEANUP_SCALE_CASES"))
	require.NoError(t, err)
	var cases []struct {
		Label      string
		Before     int64
		MaxID      int
		CursorID   int
		CursorAt   int64
		Seconds    int
		BatchLimit int
	}
	require.NoError(t, common.Unmarshal(data, &cases))
	trace := &cleanupScaleTrace{Interface: gormlogger.Default.LogMode(gormlogger.Silent), samples: make(map[string][]float64)}
	logDB, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: trace})
	require.NoError(t, err)
	pool, err := logDB.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	logDB.ConnPool = cleanupScalePool{DB: pool, trace: trace}
	logDB.Statement.ConnPool = logDB.ConnPool
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var identity string
	require.NoError(t, logDB.Raw("SELECT purpose FROM fixture_identity").Scan(&identity).Error)
	require.Equal(t, "error-cleanup-bounded-20261121", identity)
	for _, c := range cases {
		t.Run(c.Label, func(t *testing.T) {
			cleanupWorkflowDB(t)
			if c.BatchLimit > 0 {
				setErrorLogCleanupTestSettings(t, map[string]string{"error_log_batch_size": fmt.Sprint(c.BatchLimit)})
			}
			id, err := common.GenerateRandomCharsKey(32)
			require.NoError(t, err)
			run := &model.DataCleanupRun{ID: id, UserID: 1, Kind: model.CleanupErrorLogs, Before: c.Before, MaxID: c.MaxID, Status: "preview", ExpiresAt: time.Now().Add(time.Minute).Unix()}
			require.NoError(t, model.DB.Create(run).Error)
			run, err = StartDataCleanup(context.Background(), 1, id)
			require.NoError(t, err)
			task, err := model.GetSystemTaskByTaskID(*run.TaskID)
			require.NoError(t, err)
			task, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "scale-worker", time.Now().Add(time.Duration(c.Seconds+60)*time.Second).Unix())
			require.NoError(t, err)
			require.True(t, claimed)
			require.NoError(t, prepareErrorLogCleanupTask(context.Background(), logDB, task, "scale-worker", run))
			run.CursorID, run.CursorAt = c.CursorID, c.CursorAt
			require.NoError(t, model.UpdateLeasedDataCleanupRun(context.Background(), run.ID, task.TaskID, "scale-worker", map[string]any{"cursor_id": run.CursorID, "cursor_at": run.CursorAt, "status": "running"}))
			originalLogger := model.DB.Logger
			model.DB.Logger = trace
			defer func() { model.DB.Logger = originalLogger }()
			trace.samples = make(map[string][]float64)
			trace.batchSizes = make(map[int64]int)
			trace.started, trace.firstCommit = time.Now(), 0
			t.Logf("SCALE_READY %s", c.Label)
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Seconds)*time.Second)
			started := time.Now()
			err = runErrorLogCleanup(ctx, logDB, task, "scale-worker", run, operation_setting.GetDataCleanupSetting())
			elapsed := time.Since(started).Seconds()
			cancel()
			require.True(t, err == nil || errors.Is(err, context.DeadlineExceeded), "unexpected worker error: %v", err)
			var saved model.DataCleanupRun
			require.NoError(t, model.DB.First(&saved, "id = ?", run.ID).Error)
			require.GreaterOrEqual(t, saved.CursorID, c.CursorID)
			phases := make(map[string]map[string]float64)
			for phase, samples := range trace.samples {
				sort.Float64s(samples)
				stats := map[string]float64{"count": float64(len(samples))}
				for name, percentile := range map[string]float64{"p50": .5, "p95": .95, "p99": .99, "max": 1} {
					stats[name] = samples[int(float64(len(samples)-1)*percentile)]
				}
				phases[phase] = stats
			}
			result, marshalErr := common.Marshal(map[string]any{"label": c.Label, "mode": run.ScanMode, "deleted": saved.Deleted, "scanned": saved.Scanned, "cursor_id": saved.CursorID, "cursor_at": saved.CursorAt, "elapsed_s": elapsed, "deleted_per_second": float64(saved.Deleted) / elapsed, "phase_ms": phases, "first_commit_s": trace.firstCommit, "batch_sizes": trace.batchSizes, "task_budget_reached": errors.Is(err, context.DeadlineExceeded)})
			require.NoError(t, marshalErr)
			t.Logf("SCALE_RESULT %s", result)
			status := model.SystemTaskStatusSucceeded
			if err != nil {
				status = model.SystemTaskStatusFailed
			}
			require.NoError(t, model.FinishDataCleanupRun(context.Background(), run, "scale-worker", status, ""))
		})
	}
}

func TestErrorLogCleanupConnectHonorsDeadline(t *testing.T) {
	cleanupWorkflowDB(t)
	for _, protocol := range []string{"mysql", "postgres"} {
		t.Run(protocol, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				connection, err := listener.Accept()
				if err == nil {
					accepted <- connection
				}
			}()
			dsn := fmt.Sprintf("root@tcp(%s)/unreachable?timeout=10s&readTimeout=10s", listener.Addr())
			if protocol == "postgres" {
				dsn = fmt.Sprintf("postgres://postgres@%s/unreachable?sslmode=disable&connect_timeout=10", listener.Addr())
			}
			t.Setenv("LOG_SQL_DSN", dsn)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := PreviewDataCleanup(ctx, 1, model.CleanupErrorLogs, time.Now().Add(-48*time.Hour).Unix())
				done <- err
			}()
			var connection net.Conn
			select {
			case connection = <-accepted:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "preview did not connect to the isolated test listener")
			}
			defer connection.Close()
			<-ctx.Done()
			returned := false
			select {
			case <-done:
				returned = true
			case <-time.After(time.Second):
			}
			// Release the blocked handshake and join the goroutine before test cleanup.
			require.NoError(t, listener.Close())
			require.NoError(t, connection.Close())
			if !returned {
				<-done
			}
			require.True(t, returned, "preview was still blocked in database initialization one second after its request deadline")
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := model.OpenDataCleanupDB(ctx, true)
	require.ErrorIs(t, err, context.Canceled)
}
