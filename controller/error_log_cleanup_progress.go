package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

// Read the receipt, current task and lease in one statement, so an atomic
// handoff cannot make the old task's completion look like overall completion.
func readErrorLogCleanupProgress(ctx context.Context, run *model.DataCleanupRun) (*service.ErrorLogCleanupProgress, string, error) {
	var row struct {
		model.DataCleanupRun `gorm:"embedded"`
		TaskStatus           string
		TaskType             string
		TaskState            string
		TaskPayload          string
		TaskRunner           string
		LeaseRunner          string
		LockedUntil          int64
		HeartbeatAt          int64
	}
	result := model.DB.WithContext(ctx).Table("data_cleanup_runs AS cleanup").Select(`cleanup.id, cleanup.task_id, cleanup.task_sequence, cleanup.user_id, cleanup.kind,
cleanup.before_time, cleanup.max_id, cleanup.cursor_at, cleanup.cursor_id, cleanup.matched, cleanup.count_exact,
cleanup.deleted, cleanup.scanned, cleanup.status, cleanup.error_key, cleanup.created_at, cleanup.updated_at, cleanup.expires_at,
task.status AS task_status, task.type AS task_type, task.state AS task_state, task.payload AS task_payload,
task.locked_by AS task_runner, lease.locked_by AS lease_runner, lease.locked_until, lease.updated_at AS heartbeat_at`).
		Joins("LEFT JOIN system_tasks AS task ON task.task_id = cleanup.task_id").
		Joins("LEFT JOIN system_task_locks AS lease ON lease.type = task.type AND lease.task_id = task.task_id").
		Where("cleanup.id = ?", run.ID).Scan(&row)
	if result.Error != nil {
		return nil, "", result.Error
	}
	if result.RowsAffected != 1 {
		return nil, "", gorm.ErrRecordNotFound
	}
	*run = row.DataCleanupRun
	if row.TaskStatus != "" {
		run.Status = row.TaskStatus
	}
	if run.Status == "failed" && run.ErrorKey == "" {
		run.ErrorKey = "cleanup.failed"
	}
	var progress service.ErrorLogCleanupProgress
	if row.TaskState != "" {
		if err := common.UnmarshalJsonStr(row.TaskState, &progress); err != nil {
			return nil, "", err
		}
	}
	switch progress.Phase {
	case "preparing", "locating", "scanning", "retrying", "saving":
	default:
		progress.Phase = ""
	}
	if run.Status == "pending" {
		progress.Phase = "queued"
	}
	if run.Status != "pending" && run.Status != "running" {
		progress.Phase = ""
	}
	if row.TaskPayload != "" {
		var payload struct {
			Plan *model.ErrorLogCleanupPlan `json:"scan_plan"`
		}
		if err := common.UnmarshalJsonStr(row.TaskPayload, &payload); err != nil {
			return nil, "", err
		}
		if payload.Plan != nil {
			progress.ScanMode = payload.Plan.ScanMode
		}
	}
	progress.CursorID, progress.SnapshotMaxID = run.CursorID, run.MaxID
	progress.HeartbeatAt = row.HeartbeatAt
	progress.LeaseValid = run.Status == "running" && row.TaskRunner != "" && row.TaskRunner == row.LeaseRunner && row.LockedUntil >= time.Now().Unix()
	if run.Status == "succeeded" {
		progress.CheckpointAt = run.UpdatedAt
	}
	return &progress, row.TaskType, nil
}
