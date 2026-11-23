package controller

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestErrorLogCleanupProgressUsesCurrentTask(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	id, err := common.GenerateRandomCharsKey(32)
	require.NoError(t, err)
	oldID, newID := "old-"+id, "new-"+id
	taskType := "progress-" + id // This fixture must not take another test's type lease.
	now := time.Now().Unix()
	old := model.SystemTask{TaskID: oldID, Type: taskType, Status: model.SystemTaskStatusSucceeded, State: `{"phase":"scanning","checkpoint_at":123}`, Payload: `{"scan_plan":{"mode":"id"}}`}
	next := model.SystemTask{TaskID: newID, Type: taskType, Status: model.SystemTaskStatusPending, State: "{}", Payload: old.Payload}
	require.NoError(t, model.DB.Create(&old).Error)
	require.NoError(t, model.DB.Create(&next).Error)
	run := model.DataCleanupRun{ID: id, UserID: nextTestID(), Kind: model.CleanupErrorLogs, Status: "pending", TaskID: &newID, Before: 1000, MaxID: 10000, CursorID: 2000, Scanned: 2000, Deleted: 1200}
	require.NoError(t, model.DB.Create(&run).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("task_id IN ?", []string{oldID, newID}).Delete(&model.SystemTaskLock{}).Error)
		require.NoError(t, model.DB.Where("id = ?", id).Delete(&model.DataCleanupRun{}).Error)
		require.NoError(t, model.DB.Where("task_id IN ?", []string{oldID, newID}).Delete(&model.SystemTask{}).Error)
	})
	previousLogDB := model.LOG_DB
	model.LOG_DB = nil // Polling must never query/count the log database.
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	progress, _, err := readErrorLogCleanupProgress(context.Background(), &run)
	require.NoError(t, err)
	require.Equal(t, "pending", run.Status, "the completed previous segment is not overall completion")
	require.Equal(t, "queued", progress.Phase)
	require.False(t, progress.LeaseValid)
	require.Equal(t, 2000, progress.CursorID)
	require.Equal(t, 10000, progress.SnapshotMaxID)
	require.Equal(t, "id", progress.ScanMode)
	require.NoError(t, model.DB.Model(&next).Updates(map[string]any{"status": model.SystemTaskStatusRunning, "locked_by": "worker", "state": `{"phase":"locating","checkpoint_at":123}`}).Error)
	lease := model.SystemTaskLock{Type: taskType, TaskID: newID, LockedBy: "worker", LockedUntil: now + 60, UpdatedAt: now}
	require.NoError(t, model.DB.Create(&lease).Error)
	progress, _, err = readErrorLogCleanupProgress(context.Background(), &run)
	require.NoError(t, err)
	require.True(t, progress.LeaseValid)
	require.Equal(t, "locating", progress.Phase)
	require.EqualValues(t, 123, progress.CheckpointAt, "heartbeat must not masquerade as saved progress")
	require.Equal(t, now, progress.HeartbeatAt)
	require.NoError(t, model.DB.Model(&lease).Where("task_id = ?", newID).Update("locked_until", now-1).Error)
	progress, _, err = readErrorLogCleanupProgress(context.Background(), &run)
	require.NoError(t, err)
	require.False(t, progress.LeaseValid)
	require.Equal(t, "running", run.Status, "a read does not mutate an expired task")
}

func TestErrorLogCleanupSettingAPIValidation(t *testing.T) {
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		for _, key := range []string{"error_log_batch_size", "error_log_batch_timeout_sec", "error_log_auto_continue"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("role", role)
			c.Set("id", nextTestID())
			c.Set(middleware.SystemSettingsScopeContextKey, "operations.logs")
			c.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(`{"scope":"operations.logs","key":"data_cleanup_setting.`+key+`","value":"invalid"}`))
			UpdateOption(c)
			if role == common.RoleAdminUser {
				require.Equal(t, 403, w.Code)
			} else {
				require.Equal(t, 200, w.Code)
				require.Contains(t, w.Body.String(), `"success":false`)
				require.NotContains(t, w.Body.String(), "invalid error log cleanup setting")
			}
		}
	}
}
