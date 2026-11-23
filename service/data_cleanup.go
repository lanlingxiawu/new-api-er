package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var (
	ErrCleanupDate        = errors.New("cleanup.invalid_date")
	ErrCleanupDisabled    = errors.New("cleanup.disabled")
	ErrCleanupPreview     = errors.New("cleanup.preview_expired")
	ErrCleanupBusy        = errors.New("cleanup.busy")
	ErrCleanupUnsupported = errors.New("cleanup.unsupported")
	ErrCleanupIndex       = errors.New("cleanup.index_missing")
	ErrCleanupNodes       = errors.New("cleanup.upgrade_nodes")
)

// Old writers do not recognize permanent deletion receipts. Refuse financial
// cleanup until every registered node has upgraded (including stale nodes that
// might return). An intentionally retired node can be removed via node management.
func checkFinancialCleanupNodes(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var nodes []model.SystemInstance
	if err := model.DB.WithContext(ctx).Select("node_name, info").Limit(1001).Find(&nodes).Error; err != nil {
		return err
	}
	if len(nodes) > 1000 {
		return ErrCleanupNodes
	}
	for _, node := range nodes {
		var info struct {
			Receipts bool `json:"financial_cleanup_receipts"`
		}
		if err := common.UnmarshalJsonStr(node.Info, &info); err != nil || !info.Receipts {
			return ErrCleanupNodes
		}
	}
	return nil
}

func ValidateDataCleanupDate(kind string, before int64, now time.Time) error {
	if _, err := model.DataCleanupTable(kind); err != nil {
		return ErrCleanupDate
	}
	age := 3 * 24 * time.Hour
	if kind == model.CleanupErrorLogs {
		age = 24 * time.Hour
	}
	if before <= 0 || before > now.Add(-age).Unix() {
		return ErrCleanupDate
	}
	return nil
}

func PreviewDataCleanup(ctx context.Context, userID int, kind string, before int64) (*model.DataCleanupRun, error) {
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return nil, ErrCleanupDisabled
	}
	now := time.Now()
	if err := ValidateDataCleanupDate(kind, before, now); err != nil {
		return nil, err
	}
	if kind == model.CleanupErrorLogs {
		if model.LOG_DB.Dialector.Name() == "clickhouse" {
			return nil, ErrCleanupUnsupported
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		db, closeDB, err := model.OpenDataCleanupDB(ctx, true)
		if err != nil {
			return nil, err
		}
		defer closeDB()
		run, err := model.PrepareErrorLogCleanup(ctx, db, before)
		if errors.Is(err, model.ErrCleanupScanIndex) {
			return nil, ErrCleanupIndex
		}
		if err != nil {
			return nil, err
		}
		run.ID, err = common.GenerateRandomCharsKey(32)
		if err != nil {
			return nil, err
		}
		run.UserID, run.Status = userID, "preview"
		run.CreatedAt, run.UpdatedAt, run.ExpiresAt = now.Unix(), now.Unix(), now.Add(10*time.Minute).Unix()
		if err := model.DB.WithContext(ctx).Create(run).Error; err != nil {
			return nil, err
		}
		return run, nil
	}
	db := model.DB
	if db.Dialector.Name() == "clickhouse" {
		return nil, ErrCleanupUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := checkFinancialCleanupNodes(ctx); err != nil {
		return nil, err
	}
	table, _ := model.DataCleanupTable(kind)
	index := "idx_cost_cleanup_created_id"
	if kind == model.CleanupCommission {
		index = "idx_commission_cleanup_created_id"
	}
	if !db.WithContext(ctx).Migrator().HasIndex(table, index) {
		return nil, ErrCleanupIndex
	}
	var maxID int
	if err := db.WithContext(ctx).Model(table).Select("COALESCE(MAX(id),0)").Scan(&maxID).Error; err != nil {
		return nil, err
	}
	var rows []struct {
		ID    int
		LogID *int
	}
	query := db.WithContext(ctx).Model(table).Where("created_at >= ? AND created_at < ? AND id <= ?", 0, before, maxID).Order("created_at, id").Limit(1001)
	query = query.Select("id, log_id")
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	matched := 0
	for _, row := range rows {
		if row.LogID != nil && *row.LogID > 0 {
			matched++
		}
	}
	id, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return nil, err
	}
	run := &model.DataCleanupRun{ID: id, UserID: userID, Kind: kind, Before: before, MaxID: maxID, Matched: matched, CountExact: len(rows) < 1001, Status: "preview", CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix()}
	if err := model.DB.WithContext(ctx).Create(run).Error; err != nil {
		return nil, err
	}
	return run, nil
}

func StartDataCleanup(ctx context.Context, userID int, id string) (*model.DataCleanupRun, error) {
	return submitDataCleanup(ctx, userID, id, false, nil)
}

func ResumeDataCleanup(ctx context.Context, userID int, id string) (*model.DataCleanupRun, error) {
	return submitDataCleanup(ctx, userID, id, true, nil)
}

// StartErrorLogCleanup captures only the deletion fence. Candidate scans and
// index selection belong to the bounded background worker, after confirmation.
func StartErrorLogCleanup(ctx context.Context, userID int, before int64) (*model.DataCleanupRun, error) {
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return nil, ErrCleanupDisabled
	}
	now := time.Now()
	if err := ValidateDataCleanupDate(model.CleanupErrorLogs, before, now); err != nil {
		return nil, err
	}
	if model.LOG_DB.Dialector.Name() == "clickhouse" {
		return nil, ErrCleanupUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db, closeDB, err := model.OpenDataCleanupDB(ctx, true)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	var maxID int
	if err := db.WithContext(ctx).Model(&model.Log{}).Select("COALESCE(MAX(id),0)").Scan(&maxID).Error; err != nil {
		return nil, err
	}
	id, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return nil, err
	}
	run := &model.DataCleanupRun{ID: id, UserID: userID, Kind: model.CleanupErrorLogs, Before: before, MaxID: maxID, Status: "preview", CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix()}
	return submitDataCleanup(ctx, userID, id, false, run)
}

type errorLogCleanupPayload struct {
	RunID    string                     `json:"run_id"`
	ScanPlan *model.ErrorLogCleanupPlan `json:"scan_plan,omitempty"`
}

func submitDataCleanup(ctx context.Context, userID int, id string, resume bool, newRun *model.DataCleanupRun) (*model.DataCleanupRun, error) {
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return nil, ErrCleanupDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var run model.DataCleanupRun
	taskCreateFailed := false
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if newRun != nil {
			run = *newRun
			if err := tx.Create(&run).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&run).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrCleanupPreview
				}
				return err
			}
		}
		previousTaskID := run.TaskID
		payload := errorLogCleanupPayload{RunID: id}
		if resume {
			if run.Kind != model.CleanupErrorLogs || previousTaskID == nil {
				return ErrCleanupPreview
			}
			if run.ErrorKey == "cleanup.index_missing" {
				return ErrCleanupIndex
			}
			var previous model.SystemTask
			if err := tx.Select("status", "type", "payload").Where("task_id = ?", *previousTaskID).First(&previous).Error; err != nil {
				return err
			}
			if previous.Type != model.SystemTaskTypeErrorLogCleanup {
				return ErrCleanupPreview
			}
			if previous.Status == model.SystemTaskStatusPending || previous.Status == model.SystemTaskStatusRunning {
				run.Status = string(previous.Status)
				return nil
			}
			if previous.Status != model.SystemTaskStatusFailed {
				return ErrCleanupPreview
			}
			if err := common.UnmarshalJsonStr(previous.Payload, &payload); err != nil {
				return err
			}
			if payload.RunID != id {
				return ErrCleanupPreview
			}
		} else {
			if run.TaskID != nil {
				return nil
			}
			if run.Status != "preview" || run.ExpiresAt < time.Now().Unix() {
				return ErrCleanupPreview
			}
		}
		if err := ValidateDataCleanupDate(run.Kind, run.Before, time.Now()); err != nil {
			return err
		}
		if run.Kind != model.CleanupErrorLogs {
			if err := checkFinancialCleanupNodes(ctx); err != nil {
				return err
			}
		}
		claim := tx.Model(&model.DataCleanupRun{}).Where("id = ?", id)
		if resume {
			claim = claim.Where("task_id = ? AND status = ?", *previousTaskID, run.Status)
		} else {
			claim = claim.Where("status = ? AND task_id IS NULL", "preview")
		}
		updates := map[string]any{"status": "pending", "error_key": ""}
		if resume {
			updates["task_id"] = nil
		}
		claimed := claim.Updates(updates)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected != 1 {
			return ErrCleanupBusy
		}
		taskID, err := model.GenerateSystemTaskID()
		if err != nil {
			return err
		}
		payloadText, err := common.Marshal(payload)
		if err != nil {
			return err
		}
		// All cleanup tasks, including the existing full-log cleanup, share the
		// unique active key. No check-then-create race between multiple nodes.
		activeKey := model.SystemTaskTypeLogCleanup
		taskType := model.SystemTaskTypeDataCleanup
		if run.Kind == model.CleanupErrorLogs {
			taskType = model.SystemTaskTypeErrorLogCleanup
		}
		task := model.SystemTask{TaskID: taskID, Type: taskType, Status: model.SystemTaskStatusPending, ActiveKey: &activeKey, Payload: string(payloadText), State: "{}", Result: "{}"}
		if err := tx.Create(&task).Error; err != nil {
			taskCreateFailed = true
			return err
		}
		run.TaskID = &taskID
		run.TaskSequence = task.ID
		run.Status = "pending"
		run.ErrorKey = ""
		run.UpdatedAt = time.Now().Unix()
		return tx.Model(&model.DataCleanupRun{}).Where("id = ?", id).Updates(map[string]any{"task_id": taskID, "task_sequence": task.ID, "status": "pending", "updated_at": run.UpdatedAt}).Error
	})
	if err != nil {
		// Check after rollback: PostgreSQL rejects queries inside a transaction
		// after a unique-key violation. Other database failures remain errors.
		if taskCreateFailed {
			var active model.SystemTask
			if checkErr := model.DB.WithContext(ctx).Select("id").Where("active_key = ?", model.SystemTaskTypeLogCleanup).Take(&active).Error; checkErr == nil {
				return nil, fmt.Errorf("%w: %v", ErrCleanupBusy, err)
			}
		}
		return nil, err
	}
	notifySystemTaskRunner()
	return &run, nil
}

// Save the scan coordinates before any deletion. A resumed task restores this
// JSON plan alongside the existing receipt cursor, without schema changes.
func prepareErrorLogCleanupTask(ctx context.Context, db *gorm.DB, task *model.SystemTask, runnerID string, run *model.DataCleanupRun) error {
	var payload errorLogCleanupPayload
	if err := common.UnmarshalJsonStr(task.Payload, &payload); err != nil {
		return err
	}
	if payload.RunID != run.ID || run.Kind != model.CleanupErrorLogs {
		return ErrCleanupPreview
	}
	if payload.ScanPlan != nil {
		run.ErrorLogCleanupPlan = *payload.ScanPlan
		return model.ValidateErrorLogCleanupPlan(ctx, db, run)
	}
	if err := model.InitializeErrorLogCleanupPlan(ctx, db, run); err != nil {
		return err
	}
	payload.ScanPlan = &run.ErrorLogCleanupPlan
	payloadText, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	updated := model.DB.WithContext(ctx).Model(&model.SystemTask{}).
		Where("task_id = ? AND status = ? AND locked_by = ? AND payload = ?", task.TaskID, model.SystemTaskStatusRunning, runnerID, task.Payload).
		Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id = system_tasks.task_id AND system_task_locks.locked_by = ? AND system_task_locks.locked_until >= ?)", runnerID, now).
		Updates(map[string]any{"payload": string(payloadText), "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		// A previous attempt may have committed the plan before losing its
		// acknowledgement. Accept only this exact plan under the same live lease.
		var held int64
		err := model.DB.WithContext(ctx).Model(&model.SystemTask{}).
			Where("task_id = ? AND status = ? AND locked_by = ? AND payload = ?", task.TaskID, model.SystemTaskStatusRunning, runnerID, string(payloadText)).
			Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id = system_tasks.task_id AND system_task_locks.locked_by = ? AND system_task_locks.locked_until >= ?)", runnerID, now).Count(&held).Error
		if err != nil {
			return err
		}
		if held != 1 {
			return model.ErrSystemTaskLockLost
		}
	}
	task.Payload = string(payloadText)
	return nil
}

type dataCleanupHandler struct{}

type errorLogCleanupHandler struct{ dataCleanupHandler }

func (errorLogCleanupHandler) Type() string { return model.SystemTaskTypeErrorLogCleanup }

func (dataCleanupHandler) Type() string { return model.SystemTaskTypeDataCleanup }
func init() {
	RegisterSystemTaskHandler(dataCleanupHandler{})
	RegisterSystemTaskHandler(errorLogCleanupHandler{})
}

func (dataCleanupHandler) Run(parent context.Context, task *model.SystemTask, runnerID string) {
	var run model.DataCleanupRun
	var runErr error
	defer func() {
		if p := recover(); p != nil {
			runErr = fmt.Errorf("cleanup panic: %v", p)
		}
		if errors.Is(runErr, errErrorLogCleanupContinued) {
			notifySystemTaskRunner()
			return
		}
		status := model.SystemTaskStatusSucceeded
		if runErr != nil {
			status = model.SystemTaskStatusFailed
			common.SysError(fmt.Sprintf("data cleanup %s: %v", task.TaskID, runErr))
			run.ErrorKey = "cleanup.failed"
			if errors.Is(runErr, model.ErrCleanupScanIndex) {
				run.ErrorKey = "cleanup.index_missing"
			}
		}
		run.Status = string(status)
		run.UpdatedAt = time.Now().Unix()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if run.ID == "" {
			if err := model.FinishSystemTask(task.TaskID, runnerID, status, nil, run.ErrorKey); err != nil {
				common.SysError(fmt.Sprintf("cleanup task completion: %v", err))
			}
			return
		}
		if err := model.FinishDataCleanupRun(ctx, &run, runnerID, status, run.ErrorKey); err != nil {
			common.SysError(fmt.Sprintf("cleanup task completion: %v", err))
		}
	}()
	cfg := operation_setting.GetDataCleanupSetting()
	ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.TaskTimeoutSec)*time.Second)
	defer cancel()
	if runErr = model.DB.WithContext(ctx).Where("task_id = ?", task.TaskID).First(&run).Error; runErr != nil {
		return
	}
	run.Status = "running"
	if runErr = model.UpdateLeasedDataCleanupRun(ctx, run.ID, task.TaskID, runnerID, map[string]any{"status": "running"}); runErr != nil {
		return
	}
	db, closeDB, err := model.OpenDataCleanupDB(ctx, run.Kind == model.CleanupErrorLogs)
	if err != nil {
		runErr = err
		return
	}
	defer closeDB()
	if task.Type == model.SystemTaskTypeErrorLogCleanup {
		runErr = prepareErrorLogCleanupWithRetry(ctx, db, task, runnerID, &run)
		if runErr != nil {
			return
		}
		runErr = runErrorLogCleanup(ctx, db, task, runnerID, &run, cfg)
		return
	}
	for {
		if run.Kind != model.CleanupErrorLogs {
			if runErr = checkFinancialCleanupNodes(ctx); runErr != nil {
				return
			}
			queues := model.GetLedgerPipelineStatusSnapshot()
			if queues.CostRetry.Backlog > 0 || queues.PairRetry.Backlog > 0 || queues.Cost.Backlog+queues.Pair.Backlog > operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries()/2 {
				runErr = ErrCleanupBusy
				return
			}
		}
		if !operation_setting.GetDataCleanupSetting().Enabled {
			runErr = ErrCleanupDisabled
			return
		}
		if runErr = ctx.Err(); runErr != nil {
			return
		}
		// Lease verification precedes every bounded batch; no relay lock is used.
		if runErr = model.UpdateSystemTaskState(task.TaskID, runnerID, map[string]any{"deleted": run.Deleted, "scanned": run.Scanned}); runErr != nil {
			return
		}
		batchCtx, batchCancel := context.WithTimeout(ctx, time.Duration(cfg.BatchTimeoutSec)*time.Second)
		batch, err := model.DeleteDataCleanupBatch(batchCtx, db, &run, cfg.BatchSize)
		batchCancel()
		if err != nil {
			runErr = err
			return
		}
		if run.Kind == model.CleanupErrorLogs {
			if runErr = model.UpdateLeasedDataCleanupRun(ctx, run.ID, task.TaskID, runnerID, map[string]any{"cursor_at": run.CursorAt, "cursor_id": run.CursorID, "deleted": run.Deleted, "scanned": run.Scanned, "updated_at": run.UpdatedAt}); runErr != nil {
				return
			}
		}
		if batch.Scanned == 0 {
			return
		}
		timer := time.NewTimer(time.Duration(cfg.BatchSleepMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			runErr = ctx.Err()
			return
		case <-timer.C:
		}
	}
}

// runErrorLogCleanup keeps log commits and receipt retries separate: a receipt
// timeout must not start another deletion or publish an unconfirmed checkpoint.
func runErrorLogCleanup(ctx context.Context, db *gorm.DB, task *model.SystemTask, runnerID string, run *model.DataCleanupRun, cfg operation_setting.DataCleanupSetting) error {
	initial := model.DefaultErrorLogCleanupBatchOptions(min(2000, cfg.ErrorLogBatchSize))
	options := initial
	fastBatches := 0
	slowBatches := 0
	seekNext := run.ScanMode == "id"
	var nextSeekAt time.Time
	for {
		cfg = operation_setting.GetDataCleanupSetting()
		if err := ctx.Err(); err != nil {
			return err
		}
		if !cfg.Enabled {
			return ErrCleanupDisabled
		}
		options.DeleteLimit = min(options.DeleteLimit, cfg.ErrorLogBatchSize)
		batchTimeout := time.Duration(cfg.ErrorLogBatchTimeoutSec) * time.Second
		if seekNext && !time.Now().Before(nextSeekAt) {
			if err := saveErrorLogCleanupState(ctx, task, runnerID, run, "locating"); err != nil {
				return err
			}
			seekCtx, cancel := context.WithTimeout(ctx, time.Second)
			cursor, err := model.SeekErrorLogCleanupCursor(seekCtx, db, run)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				common.SysError(fmt.Sprintf("data cleanup %s seek fallback: %v", task.TaskID, err))
				nextSeekAt = time.Now().Add(30 * time.Second)
			} else if cursor > run.CursorID {
				next := *run
				next.CursorID, next.UpdatedAt = cursor, time.Now().Unix()
				if err := saveErrorLogCleanupProgress(ctx, task, runnerID, &next, cfg.BatchSleepMS); err != nil {
					return err
				}
				*run = next
			}
			if run.CursorID == run.MaxID {
				return nil
			}
		}
		workStarted := time.Now()
		var batch model.DataCleanupBatch
		var elapsed time.Duration
		var err error
		for attempt := 0; ; attempt++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !operation_setting.GetDataCleanupSetting().Enabled {
				return ErrCleanupDisabled
			}
			// Fence every attempt, including retries after an ambiguous commit.
			phase := "scanning"
			if attempt > 0 {
				phase = "retrying"
			}
			if err := saveErrorLogCleanupState(ctx, task, runnerID, run, phase); err != nil {
				return err
			}
			batchCtx, batchCancel := context.WithTimeout(ctx, batchTimeout)
			started := time.Now()
			batch, err = model.DeleteErrorLogCleanupBatch(batchCtx, db, run, options)
			elapsed = time.Since(started)
			contextErr := batchCtx.Err()
			batchCancel()
			if err == nil {
				break
			}
			if parentErr := ctx.Err(); parentErr != nil {
				return parentErr
			}
			failure := fmt.Errorf("mode=%s delete_limit=%d id_span=%d attempt=%d elapsed=%s: %w", run.ScanMode, options.DeleteLimit, options.IDSpan, attempt+1, elapsed, err)
			if attempt == 3 || !isErrorLogCleanupTimeout(err, contextErr) {
				return failure
			}
			common.SysError(fmt.Sprintf("data cleanup %s retry: %v", task.TaskID, failure))
			options.DeleteLimit = max(1, options.DeleteLimit/2)
			options.IDSpan = max(1024, options.IDSpan/2)
			fastBatches = 0
			delay := max(time.Duration(cfg.BatchSleepMS)*time.Millisecond, 500*time.Millisecond<<attempt)
			if err := waitForErrorLogCleanup(ctx, delay); err != nil {
				return err
			}
		}
		if err := saveErrorLogCleanupProgress(ctx, task, runnerID, run, cfg.BatchSleepMS); err != nil {
			return err
		}
		if batch.Done {
			return nil
		}
		cfg = operation_setting.GetDataCleanupSetting()
		if !cfg.Enabled {
			return ErrCleanupDisabled
		}
		seekNext = run.ScanMode == "id" && batch.Scanned == 0
		if cfg.ErrorLogAutoContinue {
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 20*time.Second {
				if _, err := model.ContinueErrorLogCleanup(ctx, run, task, runnerID); err != nil {
					return err
				}
				return errErrorLogCleanupContinued
			}
		}
		if batch.Scanned > 0 && elapsed < min(250*time.Millisecond, batchTimeout/4) && time.Since(workStarted) < 500*time.Millisecond {
			fastBatches++
		} else {
			fastBatches = 0
		}
		if fastBatches == 10 {
			options.DeleteLimit = min(cfg.ErrorLogBatchSize, options.DeleteLimit*2)
			options.IDSpan = min(initial.IDSpan, options.IDSpan*2)
			fastBatches = 0
		}
		if elapsed > batchTimeout/2 {
			slowBatches++
		} else {
			slowBatches = 0
		}
		if slowBatches == 3 {
			options.DeleteLimit = max(1, options.DeleteLimit/2)
			fastBatches, slowBatches = 0, 0
		}
		if err := waitForErrorLogCleanup(ctx, time.Duration(cfg.BatchSleepMS)*time.Millisecond); err != nil {
			return err
		}
	}
}

func saveErrorLogCleanupProgress(ctx context.Context, task *model.SystemTask, runnerID string, run *model.DataCleanupRun, sleepMS int) error {
	updates := map[string]any{"cursor_at": run.CursorAt, "cursor_id": run.CursorID, "deleted": run.Deleted, "scanned": run.Scanned, "updated_at": run.UpdatedAt}
	for attempt := 0; ; attempt++ {
		// Always try to save a confirmed deletion, even if cleanup was disabled
		// during its commit. Further retries observe the switch in the wait.
		progressCtx, progressCancel := context.WithTimeout(ctx, 3*time.Second)
		started := time.Now()
		err := model.UpdateLeasedDataCleanupRun(progressCtx, run.ID, task.TaskID, runnerID, updates)
		elapsed := time.Since(started)
		contextErr := progressCtx.Err()
		progressCancel()
		if err == nil {
			return nil
		}
		if parentErr := ctx.Err(); parentErr != nil {
			return parentErr
		}
		failure := fmt.Errorf("error log cleanup progress attempt=%d elapsed=%s: %w", attempt+1, elapsed, err)
		if attempt == 3 || !isErrorLogCleanupTimeout(err, contextErr) {
			return failure
		}
		common.SysError(fmt.Sprintf("data cleanup %s retry: %v", task.TaskID, failure))
		if attempt == 0 {
			// Publish the waiting phase without claiming an unsaved checkpoint.
			stateCtx, stateCancel := context.WithTimeout(ctx, 3*time.Second)
			stateErr := model.UpdateSystemTaskStateContext(stateCtx, task.TaskID, runnerID, ErrorLogCleanupProgress{Phase: "saving"})
			stateCancel()
			if stateErr != nil {
				common.SysError(fmt.Sprintf("data cleanup %s progress phase: %v", task.TaskID, stateErr))
			}
		}
		delay := max(time.Duration(sleepMS)*time.Millisecond, 500*time.Millisecond<<attempt)
		if err := waitForErrorLogCleanup(ctx, delay); err != nil {
			return err
		}
	}
}

func isErrorLogCleanupTimeout(err, contextErr error) bool {
	if errors.Is(err, model.ErrSystemTaskLockLost) || errors.Is(err, model.ErrCleanupScanIndex) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(contextErr, context.DeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func waitForErrorLogCleanup(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return ErrCleanupDisabled
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return ErrCleanupDisabled
	}
	return ctx.Err()
}
