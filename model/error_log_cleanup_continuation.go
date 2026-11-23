package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ContinueErrorLogCleanup atomically transfers the shared reservation and the
// saved receipt to a new task. The old task ID remains unusable even when the
// commit acknowledgement is lost. No log database call occurs in this transaction.
func ContinueErrorLogCleanup(ctx context.Context, run *DataCleanupRun, task *SystemTask, runnerID string) (*SystemTask, error) {
	if run.Kind != CleanupErrorLogs || task.Type != SystemTaskTypeErrorLogCleanup || run.TaskID == nil || *run.TaskID != task.TaskID {
		return nil, ErrSystemTaskLockLost
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	id, err := GenerateSystemTaskID()
	if err != nil {
		return nil, err
	}
	resultJSON, err := marshalSystemTaskJSON(map[string]any{"deleted": run.Deleted, "continued": true})
	if err != nil {
		return nil, err
	}
	key := SystemTaskTypeLogCleanup
	next := &SystemTask{TaskID: id, Type: task.Type, Status: SystemTaskStatusPending, ActiveKey: &key, Payload: task.Payload, State: "{}", Result: "{}"}
	now := time.Now().Unix()
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		finished := tx.Model(&SystemTask{}).
			Where("task_id = ? AND type = ? AND status = ? AND locked_by = ? AND active_key = ? AND payload = ?", task.TaskID, task.Type, SystemTaskStatusRunning, runnerID, key, task.Payload).
			Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id=system_tasks.task_id AND system_task_locks.locked_by=? AND system_task_locks.locked_until>=?)", runnerID, now).
			Updates(map[string]any{"status": SystemTaskStatusSucceeded, "active_key": nil, "result": resultJSON, "updated_at": now})
		if finished.Error != nil {
			return finished.Error
		}
		if finished.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		if err := tx.Create(next).Error; err != nil {
			return err
		}
		receipt := tx.Model(&DataCleanupRun{}).
			Where("id = ? AND task_id = ? AND before_time = ? AND max_id = ? AND cursor_id = ? AND cursor_at = ? AND deleted = ? AND scanned = ?", run.ID, task.TaskID, run.Before, run.MaxID, run.CursorID, run.CursorAt, run.Deleted, run.Scanned).
			Updates(map[string]any{"task_id": next.TaskID, "task_sequence": next.ID, "status": "pending", "error_key": "", "updated_at": now})
		if receipt.Error != nil {
			return receipt.Error
		}
		if receipt.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		released := tx.Where("task_id = ? AND locked_by = ?", task.TaskID, runnerID).Delete(&SystemTaskLock{})
		if released.Error != nil {
			return released.Error
		}
		if released.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return next, nil
}
