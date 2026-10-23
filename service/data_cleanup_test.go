package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func cleanupWorkflowDB(t *testing.T) {
	t.Helper()
	oldDB, oldLogDB, oldPath := model.DB, model.LOG_DB, common.SQLitePath
	common.SQLitePath = filepath.Join(t.TempDir(), "cleanup.db")
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "")
	db, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	require.NoError(t, err)
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
	before := time.Now().Add(-90 * 24 * time.Hour).Unix()
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

func TestFinancialCleanupRejectsOldNodes(t *testing.T) {
	cleanupWorkflowDB(t)
	require.NoError(t, model.DB.Create(&model.SystemInstance{NodeName: "old", Info: "{}"}).Error)
	_, err := PreviewDataCleanup(context.Background(), 1, model.CleanupLedger, time.Now().Add(-90*24*time.Hour).Unix())
	require.ErrorIs(t, err, ErrCleanupNodes)
}

func TestDataCleanupDates(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{model.CleanupLedger, model.CleanupCommission, model.CleanupErrorLogs} {
		limit := now.Add(-30 * 24 * time.Hour).Unix()
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
}
