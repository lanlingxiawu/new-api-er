package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var errErrorLogCleanupContinued = errors.New("error log cleanup continued")

// ErrorLogCleanupProgress is a response/state DTO, never a database model.
type ErrorLogCleanupProgress struct {
	Phase         string `json:"phase"`
	CheckpointAt  int64  `json:"checkpoint_at"`
	HeartbeatAt   int64  `json:"heartbeat_at"`
	LeaseValid    bool   `json:"lease_valid"`
	ScanMode      string `json:"scan_mode,omitempty"`
	CursorID      int    `json:"cursor_id"`
	SnapshotMaxID int    `json:"snapshot_max_id"`
}

func saveErrorLogCleanupState(ctx context.Context, task *model.SystemTask, runnerID string, run *model.DataCleanupRun, phase string) error {
	state := ErrorLogCleanupProgress{Phase: phase, ScanMode: run.ScanMode, CursorID: run.CursorID, SnapshotMaxID: run.MaxID}
	if run.Scanned > 0 || run.CursorID > 0 {
		state.CheckpointAt = run.UpdatedAt
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg := operation_setting.GetDataCleanupSetting()
		if !cfg.Enabled {
			return ErrCleanupDisabled
		}
		stateCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := model.UpdateSystemTaskStateContext(stateCtx, task.TaskID, runnerID, state)
		contextErr := stateCtx.Err()
		cancel()
		if err == nil {
			return nil
		}
		if attempt == 3 || ctx.Err() != nil || !isErrorLogCleanupTimeout(err, contextErr) {
			return fmt.Errorf("error log cleanup state: %w", err)
		}
		common.SysError(fmt.Sprintf("data cleanup %s state retry: %v", task.TaskID, err))
		state.Phase = "retrying"
		if err := waitForErrorLogCleanup(ctx, max(time.Duration(cfg.BatchSleepMS)*time.Millisecond, 500*time.Millisecond<<attempt)); err != nil {
			return err
		}
	}
}

func prepareErrorLogCleanupWithRetry(ctx context.Context, db *gorm.DB, task *model.SystemTask, runnerID string, run *model.DataCleanupRun) error {
	for attempt := 0; ; attempt++ {
		if err := saveErrorLogCleanupState(ctx, task, runnerID, run, "preparing"); err != nil {
			return err
		}
		prepareCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := prepareErrorLogCleanupTask(prepareCtx, db, task, runnerID, run)
		contextErr := prepareCtx.Err()
		cancel()
		if err == nil {
			return nil
		}
		if attempt == 3 || ctx.Err() != nil || !isErrorLogCleanupTimeout(err, contextErr) {
			return err
		}
		common.SysError(fmt.Sprintf("data cleanup %s preparation retry: %v", task.TaskID, err))
		if err := waitForErrorLogCleanup(ctx, 500*time.Millisecond<<attempt); err != nil {
			return err
		}
	}
}
