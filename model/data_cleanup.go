package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The maintenance pool has one connection and never borrows the relay writer's
// connection pool. Infrastructure DSNs are the same ones used at startup.
func OpenDataCleanupDB(ctx context.Context, logDB bool) (*gorm.DB, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	envName := "SQL_DSN"
	if logDB && os.Getenv("LOG_SQL_DSN") != "" {
		envName = "LOG_SQL_DSN"
	}
	dsn := os.Getenv(envName)
	var dialect gorm.Dialector
	var sqlitePool *sql.DB
	switch {
	case isClickHouseDSN(dsn):
		return nil, nil, errors.New("cleanup requires a relational database")
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		dialect = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	case dsn == "", strings.HasPrefix(dsn, "local"):
		var err error
		sqlitePool, err = sql.Open("sqlite", common.SQLitePath)
		if err != nil {
			return nil, nil, err
		}
		// SQLite's dialector probes its version with context.Background().
		// Constrain only that initialization probe, then restore the real pool.
		dialect = &sqlite.Dialector{Conn: cleanupInitPool{DB: sqlitePool, ctx: ctx}}
	default:
		if !strings.Contains(dsn, "parseTime") {
			separator := "?"
			if strings.Contains(dsn, "?") {
				separator = "&"
			}
			dsn += separator + "parseTime=true"
		}
		dialect = mysql.New(mysql.Config{DSN: dsn, SkipInitializeWithVersion: true})
	}
	cfg := newGormConfig(false)
	cfg.DisableAutomaticPing = true
	db, err := gorm.Open(dialect, cfg)
	if err != nil {
		if sqlitePool != nil {
			_ = sqlitePool.Close()
		}
		return nil, nil, err
	}
	if sqlitePool != nil {
		db.ConnPool, db.Statement.ConnPool = sqlitePool, sqlitePool
	}
	pool, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, nil, err
	}
	return db, func() { _ = pool.Close() }, nil
}

type cleanupInitPool struct {
	*sql.DB
	ctx context.Context
}

func (pool cleanupInitPool) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	return pool.DB.QueryRowContext(pool.ctx, query, args...)
}

const (
	CleanupLedger                 = "ledger"
	CleanupCommission             = "commission"
	CleanupErrorLogs              = "error_logs"
	SystemTaskTypeDataCleanup     = "data_cleanup"
	SystemTaskTypeErrorLogCleanup = "error_log_cleanup"
)

// DataCleanupRun is also the server-side preview. Keeping the snapshot here
// avoids trusting client-supplied filters and preserves the deletion receipt
// when ordinary system task history is removed.
type DataCleanupRun struct {
	ID           string  `json:"id" gorm:"type:varchar(64);primaryKey"`
	TaskID       *string `json:"task_id" gorm:"type:varchar(64);uniqueIndex"`
	TaskSequence int64   `json:"-" gorm:"index:idx_cleanup_kind_sequence,priority:2"`
	UserID       int     `json:"user_id"`
	Kind         string  `json:"kind" gorm:"type:varchar(32);index:idx_cleanup_kind_time,priority:1;index:idx_cleanup_kind_sequence,priority:1"`
	Before       int64   `json:"before" gorm:"column:before_time;index:idx_cleanup_kind_time,priority:2"`
	MaxID        int     `json:"-"`
	CursorAt     int64   `json:"-"`
	CursorID     int     `json:"-"`
	Matched      int     `json:"matched"`
	CountExact   bool    `json:"count_exact"`
	Deleted      int64   `json:"deleted"`
	Scanned      int64   `json:"scanned"`
	Status       string  `json:"status" gorm:"type:varchar(32)"`
	ErrorKey     string  `json:"error_key" gorm:"type:varchar(128)"`
	CreatedAt    int64   `json:"created_at" gorm:"index"`
	UpdatedAt    int64   `json:"updated_at"`
	ExpiresAt    int64   `json:"expires_at"`

	ErrorLogCleanupPlan `json:"-" gorm:"-"`
	CanResume           bool `json:"can_resume" gorm:"-"`
}

// UpdateLeasedDataCleanupRun fences both status and progress writes against a
// replaced task or expired runner. A log batch can commit before its receipt.
func UpdateLeasedDataCleanupRun(ctx context.Context, runID, taskID, runnerID string, updates map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	now := time.Now().Unix()
	query := DB.WithContext(ctx).Model(&DataCleanupRun{}).
		Where("id = ? AND task_id = ?", runID, taskID).
		Where(`EXISTS (SELECT 1 FROM system_tasks JOIN system_task_locks ON system_task_locks.task_id=system_tasks.task_id
WHERE system_tasks.task_id=data_cleanup_runs.task_id AND system_tasks.status=? AND system_tasks.locked_by=?
AND system_task_locks.locked_by=? AND system_task_locks.locked_until>=?)`, SystemTaskStatusRunning, runnerID, runnerID, now)
	result := query.Session(&gorm.Session{}).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// MySQL reports changed rows; an unchanged receipt can still hold its lease.
	var held int64
	if err := query.Count(&held).Error; err != nil {
		return err
	}
	if held == 0 {
		return ErrSystemTaskLockLost
	}
	return nil
}

// FinishDataCleanupRun commits the task, receipt and lock release together so
// a stopped runner cannot publish a terminal receipt for a resumed task.
func FinishDataCleanupRun(ctx context.Context, run *DataCleanupRun, runnerID string, status SystemTaskStatus, errorKey string) error {
	if run.TaskID == nil {
		return ErrSystemTaskLockLost
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resultText, err := marshalSystemTaskJSON(map[string]any{"deleted": run.Deleted})
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&SystemTask{}).
			Where("task_id = ? AND status = ? AND locked_by = ?", *run.TaskID, SystemTaskStatusRunning, runnerID).
			Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id=system_tasks.task_id AND system_task_locks.locked_by=? AND system_task_locks.locked_until>=?)", runnerID, now).
			Updates(map[string]any{"status": status, "active_key": nil, "result": resultText, "error": errorKey, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		receipt := tx.Model(&DataCleanupRun{}).Where("id = ? AND task_id = ?", run.ID, *run.TaskID).
			Updates(map[string]any{"status": status, "error_key": errorKey, "updated_at": now})
		if receipt.Error != nil {
			return receipt.Error
		}
		if receipt.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		return tx.Where("task_id = ? AND locked_by = ?", *run.TaskID, runnerID).Delete(&SystemTaskLock{}).Error
	})
}

// These compact receipts are permanent idempotency keys, not financial data.
// They must outlive both task history and the Redis deduplication TTL.
type FinancialDetailTombstone struct {
	Kind      string `gorm:"type:varchar(32);primaryKey"`
	LogID     int    `gorm:"primaryKey;autoIncrement:false"`
	CreatedAt int64
	RunID     string `gorm:"type:varchar(64)"`
}

type DataCleanupBatch struct {
	Scanned int
	Deleted int64
	// Done is explicit for planned error cleanup: an empty ID window may
	// advance the checkpoint while later windows still contain eligible rows.
	Done bool
}

// FinancialCleanupVersion makes historical query/export snapshots change when
// a relevant cleanup starts or advances, without invalidating relay caches.
func FinancialCleanupVersion(ctx context.Context, kind string, start int64) (string, error) {
	ctx, cancel := context.WithTimeout(safeDBContext(ctx), 3*time.Second)
	defer cancel()
	var run DataCleanupRun
	err := DB.WithContext(ctx).Select("id, deleted, scanned, status").Where("kind = ? AND before_time > ? AND task_id IS NOT NULL", kind, max(start, 0)).Order("task_sequence DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d:%s", run.ID, run.Deleted, run.Scanned, run.Status), nil
}

func DataCleanupTable(kind string) (any, error) {
	switch kind {
	case CleanupLedger:
		return &ConsumptionCost{}, nil
	case CleanupCommission:
		return &EmployeeCommissionLog{}, nil
	case CleanupErrorLogs:
		return &Log{}, nil
	default:
		return nil, errors.New("invalid cleanup target")
	}
}

// FilterRetainedFinancialDetails returns processed keys, first consulting live
// rows and then permanent receipts. Reversing that order creates a READ COMMITTED
// race when cleanup atomically replaces a live row with its receipt.
func FilterRetainedFinancialDetails(tx *gorm.DB, kind string, ids []int) (map[int]struct{}, error) {
	seen := make(map[int]struct{}, len(ids))
	if len(ids) == 0 {
		return seen, nil
	}
	table, err := DataCleanupTable(kind)
	if err != nil || kind == CleanupErrorLogs {
		return nil, errors.New("invalid financial target")
	}
	for start := 0; start < len(ids); start += 400 {
		batch := ids[start:min(start+400, len(ids))]
		var live []int
		if err := tx.Model(table).Where("log_id IN ?", batch).Pluck("log_id", &live).Error; err != nil {
			return nil, err
		}
		for _, id := range live {
			seen[id] = struct{}{}
		}
		var purged []int
		if err := tx.Model(&FinancialDetailTombstone{}).Where("kind = ? AND log_id IN ?", kind, batch).Pluck("log_id", &purged).Error; err != nil {
			return nil, err
		}
		for _, id := range purged {
			seen[id] = struct{}{}
		}
	}
	return seen, nil
}

// PrepareFinancialDetails is only called by database writers, never by the
// relay enqueue path. A deleted detail must neither reappear nor add statistics.
func PrepareFinancialDetails(tx *gorm.DB, costs []*ConsumptionCost, commissions []*EmployeeCommissionLog) ([]*ConsumptionCost, []*EmployeeCommissionLog, error) {
	ids := make([]int, 0, len(costs))
	for _, row := range costs {
		if row != nil && row.LogId != nil {
			ids = append(ids, *row.LogId)
		}
	}
	seenCosts, err := FilterRetainedFinancialDetails(tx, CleanupLedger, ids)
	if err != nil {
		return nil, nil, err
	}
	ids = make([]int, 0, len(commissions))
	for _, row := range commissions {
		if row != nil && row.LogId != nil {
			ids = append(ids, *row.LogId)
		}
	}
	seenCommissions, err := FilterRetainedFinancialDetails(tx, CleanupCommission, ids)
	if err != nil {
		return nil, nil, err
	}
	newCosts := make([]*ConsumptionCost, 0, len(costs))
	for _, row := range costs {
		if row == nil {
			continue
		}
		if row.LogId != nil {
			if _, found := seenCosts[*row.LogId]; found {
				continue
			}
			seenCosts[*row.LogId] = struct{}{}
		}
		newCosts = append(newCosts, row)
	}
	newCommissions := make([]*EmployeeCommissionLog, 0, len(commissions))
	for _, row := range commissions {
		if row == nil {
			continue
		}
		if row.LogId != nil {
			if _, found := seenCommissions[*row.LogId]; found {
				continue
			}
			seenCommissions[*row.LogId] = struct{}{}
		}
		newCommissions = append(newCommissions, row)
	}
	return newCosts, newCommissions, nil
}

// DeleteDataCleanupBatch bounds scanned financial rows, including manual rows.
// Planned error cleanup has separate bounded scan and deletion budgets.
// Cursor and receipts are committed with financial deletion. LOG_DB progress is
// persisted separately by the worker because it may be a different database.
func DeleteDataCleanupBatch(ctx context.Context, db *gorm.DB, run *DataCleanupRun, limit int) (DataCleanupBatch, error) {
	if run.Kind == CleanupErrorLogs && run.ScanMode != "" {
		if db.Dialector.Name() == "clickhouse" {
			return DataCleanupBatch{}, errors.New("cleanup requires a relational log database")
		}
		return DeleteErrorLogCleanupBatch(ctx, db, run, DefaultErrorLogCleanupBatchOptions(limit))
	}
	table, err := DataCleanupTable(run.Kind)
	if err != nil {
		return DataCleanupBatch{}, err
	}
	if run.Before <= 0 || run.MaxID < 0 || limit < 1 || limit > 500 {
		return DataCleanupBatch{}, errors.New("invalid cleanup bounds")
	}
	if db.Dialector.Name() == "clickhouse" {
		return DataCleanupBatch{}, errors.New("cleanup requires a relational log database")
	}
	var result DataCleanupBatch
	next := *run
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			ID        int
			CreatedAt int64
			LogID     *int
		}
		query := tx.Model(table).Where("created_at >= ? AND created_at < ? AND id <= ?", 0, run.Before, run.MaxID).
			Where("created_at > ? OR (created_at = ? AND id > ?)", run.CursorAt, run.CursorAt, run.CursorID).
			Order("created_at, id").Limit(limit)
		if run.Kind == CleanupErrorLogs {
			query = query.Select("id, created_at").Where("type = ?", LogTypeError)
		} else {
			query = query.Select("id, created_at, log_id")
		}
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		result.Scanned = len(rows)
		ids := make([]int, 0, len(rows))
		markers := make([]FinancialDetailTombstone, 0, len(rows))
		for _, row := range rows {
			next.CursorAt, next.CursorID = row.CreatedAt, row.ID
			if run.Kind != CleanupErrorLogs {
				if row.LogID == nil || *row.LogID <= 0 {
					continue
				}
				markers = append(markers, FinancialDetailTombstone{Kind: run.Kind, LogID: *row.LogID, CreatedAt: row.CreatedAt, RunID: run.ID})
			}
			ids = append(ids, row.ID)
		}
		if len(markers) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(markers, 100).Error; err != nil {
				return err
			}
		}
		if len(ids) > 0 {
			deletion := tx.Where("id IN ? AND created_at < ? AND id <= ?", ids, run.Before, run.MaxID)
			if run.Kind == CleanupErrorLogs {
				deletion = deletion.Where("type = ?", LogTypeError)
			} else {
				deletion = deletion.Where("log_id > 0")
			}
			deleted := deletion.Delete(table)
			if deleted.Error != nil {
				return deleted.Error
			}
			result.Deleted = deleted.RowsAffected
		}
		next.Deleted += result.Deleted
		next.Scanned += int64(result.Scanned)
		next.UpdatedAt = time.Now().Unix()
		if run.Kind != CleanupErrorLogs {
			return tx.Model(&DataCleanupRun{}).Where("id = ?", run.ID).Updates(map[string]any{"cursor_at": next.CursorAt, "cursor_id": next.CursorID, "deleted": next.Deleted, "scanned": next.Scanned, "updated_at": next.UpdatedAt}).Error
		}
		return nil
	})
	if err != nil {
		return DataCleanupBatch{}, err
	}
	*run = next
	return result, nil
}
