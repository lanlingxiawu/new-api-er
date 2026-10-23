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
	age := 30 * 24 * time.Hour
	if kind == model.CleanupErrorLogs {
		age = 24 * time.Hour
	}
	if before <= 0 || before > now.Add(-age).Unix() {
		return ErrCleanupDate
	}
	if kind != model.CleanupErrorLogs && before > model.ResolveCommissionMonthlyPeriod(now.Unix()).PeriodStartAt {
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
	db := model.DB
	if kind == model.CleanupErrorLogs {
		db = model.LOG_DB
	}
	if db.Dialector.Name() == "clickhouse" {
		return nil, ErrCleanupUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if kind != model.CleanupErrorLogs {
		if err := checkFinancialCleanupNodes(ctx); err != nil {
			return nil, err
		}
	}
	table, _ := model.DataCleanupTable(kind)
	index := "idx_cleanup_created_id"
	if kind == model.CleanupLedger {
		index = "idx_cost_cleanup_created_id"
	}
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
	if kind == model.CleanupErrorLogs {
		query = query.Select("id").Where("type = ?", model.LogTypeError)
	} else {
		query = query.Select("id, log_id")
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	matched := 0
	for _, row := range rows {
		if kind == model.CleanupErrorLogs || (row.LogID != nil && *row.LogID > 0) {
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
	if !operation_setting.GetDataCleanupSetting().Enabled {
		return nil, ErrCleanupDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var run model.DataCleanupRun
	taskCreateFailed := false
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCleanupPreview
			}
			return err
		}
		if run.TaskID != nil {
			return nil
		}
		if run.Status != "preview" || run.ExpiresAt < time.Now().Unix() {
			return ErrCleanupPreview
		}
		if err := ValidateDataCleanupDate(run.Kind, run.Before, time.Now()); err != nil {
			return err
		}
		if run.Kind != model.CleanupErrorLogs {
			if err := checkFinancialCleanupNodes(ctx); err != nil {
				return err
			}
		}
		claimed := tx.Model(&model.DataCleanupRun{}).Where("id = ? AND status = ?", id, "preview").Update("status", "pending")
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
		payload, err := common.Marshal(map[string]string{"run_id": id})
		if err != nil {
			return err
		}
		// All cleanup tasks, including the existing full-log cleanup, share the
		// unique active key. No check-then-create race between multiple nodes.
		activeKey := model.SystemTaskTypeLogCleanup
		task := model.SystemTask{TaskID: taskID, Type: model.SystemTaskTypeDataCleanup, Status: model.SystemTaskStatusPending, ActiveKey: &activeKey, Payload: string(payload), State: "{}", Result: "{}"}
		if err := tx.Create(&task).Error; err != nil {
			taskCreateFailed = true
			return err
		}
		run.TaskID = &taskID
		run.TaskSequence = task.ID
		run.Status = "pending"
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

type dataCleanupHandler struct{}

func (dataCleanupHandler) Type() string { return model.SystemTaskTypeDataCleanup }
func init()                             { RegisterSystemTaskHandler(dataCleanupHandler{}) }

func (dataCleanupHandler) Run(parent context.Context, task *model.SystemTask, runnerID string) {
	var run model.DataCleanupRun
	var runErr error
	defer func() {
		if p := recover(); p != nil {
			runErr = fmt.Errorf("cleanup panic: %v", p)
		}
		status := model.SystemTaskStatusSucceeded
		if runErr != nil {
			status = model.SystemTaskStatusFailed
			common.SysError(fmt.Sprintf("data cleanup %s: %v", task.TaskID, runErr))
			run.ErrorKey = "cleanup.failed"
		}
		run.Status = string(status)
		run.UpdatedAt = time.Now().Unix()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := model.DB.WithContext(ctx).Model(&model.DataCleanupRun{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"status": run.Status, "error_key": run.ErrorKey, "updated_at": run.UpdatedAt}).Error; err != nil {
			common.SysError(fmt.Sprintf("cleanup receipt: %v", err))
		}
		if err := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]any{"deleted": run.Deleted}, run.ErrorKey); err != nil {
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
	if runErr = model.DB.WithContext(ctx).Model(&run).Update("status", "running").Error; runErr != nil {
		return
	}
	db, closeDB, err := model.OpenDataCleanupDB(run.Kind == model.CleanupErrorLogs)
	if err != nil {
		runErr = err
		return
	}
	defer closeDB()
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
			if runErr = model.DB.WithContext(ctx).Model(&run).Updates(map[string]any{"cursor_at": run.CursorAt, "cursor_id": run.CursorID, "deleted": run.Deleted, "scanned": run.Scanned, "updated_at": run.UpdatedAt}).Error; runErr != nil {
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
