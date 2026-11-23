package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

var ErrCleanupScanIndex = errors.New("cleanup scan index unavailable")

const errorLogCleanupIDSpan = 65536

const MaxErrorLogCleanupBatchSize = 10000

// ErrorLogCleanupBatchOptions is transient worker state, not a database model.
type ErrorLogCleanupBatchOptions struct {
	DeleteLimit int
	IDSpan      int
}

func DefaultErrorLogCleanupBatchOptions(deleteLimit int) ErrorLogCleanupBatchOptions {
	return ErrorLogCleanupBatchOptions{DeleteLimit: deleteLimit, IDSpan: errorLogCleanupIDSpan}
}

// SeekErrorLogCleanupCursor skips only a proven empty primary-key range. It
// never mutates the receipt: the worker must persist this cursor before use.
func SeekErrorLogCleanupCursor(ctx context.Context, db *gorm.DB, run *DataCleanupRun) (int, error) {
	if err := ctx.Err(); err != nil {
		return run.CursorID, err
	}
	if run.Kind != CleanupErrorLogs || run.ScanMode != "id" || run.CursorID < 0 || run.MaxID < run.CursorID {
		return run.CursorID, errors.New("invalid error cleanup seek")
	}
	cursor := run.CursorID
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := configureCleanupIndexScan(tx); err != nil {
			return err
		}
		var rows []cleanupLogCandidate
		if err := tx.Model(&Log{}).Select("id,created_at,type").Where("id > ? AND id <= ?", run.CursorID, run.MaxID).Order("id").Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		cursor = run.MaxID
		if len(rows) != 0 {
			cursor = rows[0].ID - 1
		}
		return nil
	})
	if err != nil {
		return run.CursorID, err
	}
	return cursor, nil
}

// ErrorLogCleanupPlan is persisted in SystemTask.Payload, never as table columns.
type ErrorLogCleanupPlan struct {
	ScanMode  string `json:"mode"`
	ScanEndAt int64  `json:"end_at"`
	ScanEndID int    `json:"end_id"`
}

type cleanupLogCandidate struct {
	ID        int
	CreatedAt int64
	Type      int
}

// errorLogCleanupIndexes reads only catalogs. Predicates, expressions, invalid
// indexes and non-leading columns cannot establish a safe ordered scan.
func errorLogCleanupIndexes(ctx context.Context, db *gorm.DB) (map[string]bool, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&Log{}); err != nil {
		return nil, err
	}
	var query string
	switch db.Dialector.Name() {
	case "postgres":
		query = `SELECT ci.relname AS name, a.attname AS column_name, k.ord AS position, ix.indisprimary AS primary_index
FROM pg_index ix JOIN pg_class t ON t.oid=ix.indrelid
JOIN pg_class ci ON ci.oid=ix.indexrelid
JOIN pg_am am ON am.oid=ci.relam
JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum,ord) ON true
JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.attnum
WHERE t.oid=to_regclass(?) AND ix.indisvalid AND ix.indisready
AND ix.indpred IS NULL AND ix.indexprs IS NULL AND am.amname='btree'
AND pg_get_indexdef(ix.indexrelid) NOT LIKE '% INCLUDE (%'
AND NOT EXISTS (SELECT 1 FROM unnest(ix.indoption) AS opt WHERE opt <> 0)
ORDER BY ci.relname,k.ord`
	case "mysql":
		var visibilityColumn int64
		if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM information_schema.columns
WHERE TABLE_SCHEMA='information_schema' AND TABLE_NAME='STATISTICS' AND COLUMN_NAME='IS_VISIBLE'`).Scan(&visibilityColumn).Error; err != nil {
			return nil, err
		}
		query = `SELECT INDEX_NAME AS name, COLUMN_NAME AS column_name, SEQ_IN_INDEX AS position,
(INDEX_NAME='PRIMARY') AS primary_index FROM information_schema.statistics
WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_TYPE='BTREE' AND SUB_PART IS NULL AND COLLATION='A'`
		if visibilityColumn > 0 {
			query += " AND IS_VISIBLE='YES'"
		}
		query += " ORDER BY INDEX_NAME,SEQ_IN_INDEX"
	case "sqlite":
		query = `SELECT l.name AS name, i.name AS column_name, i.seqno+1 AS position, 0 AS primary_index
FROM pragma_index_list(?) l JOIN pragma_index_xinfo(l.name) i
WHERE l.partial=0 AND i.key=1 AND i.desc=0
UNION ALL SELECT 'PRIMARY',name,pk,1 FROM pragma_table_info(?) WHERE pk>0
ORDER BY name,position`
	default:
		return nil, ErrCleanupScanIndex
	}
	var columns []struct {
		Name         string
		ColumnName   string
		Position     int
		PrimaryIndex bool
	}
	args := []any{stmt.Table}
	if db.Dialector.Name() == "sqlite" {
		args = append(args, stmt.Table)
	}
	if err := db.WithContext(ctx).Raw(query, args...).Scan(&columns).Error; err != nil {
		return nil, err
	}
	modes := map[string]bool{}
	var name string
	var prefix []string
	primaryColumns := 0
	primaryID := false
	for _, column := range columns {
		if column.PrimaryIndex {
			primaryColumns++
			primaryID = column.ColumnName == "id"
		}
		if column.Name != name {
			name, prefix = column.Name, nil
		}
		if column.Position != len(prefix)+1 {
			continue
		}
		prefix = append(prefix, column.ColumnName)
		if len(prefix) == 2 && prefix[0] == "created_at" && prefix[1] == "id" {
			modes["time"] = true
		}
		if len(prefix) == 3 && prefix[0] == "type" && prefix[1] == "created_at" && prefix[2] == "id" {
			modes["error_time"] = true
		}
	}
	modes["id"] = primaryColumns == 1 && primaryID
	return modes, nil
}

func ValidateErrorLogCleanupPlan(ctx context.Context, db *gorm.DB, run *DataCleanupRun) error {
	modes, err := errorLogCleanupIndexes(ctx, db)
	if err != nil {
		return err
	}
	if !modes[run.ScanMode] || !modes["id"] {
		return ErrCleanupScanIndex
	}
	return nil
}

// Cleanup needs ordered index scans, even before statistics catch up with a
// restored or rapidly growing table. These settings last only for this short
// maintenance transaction; they never change the relay connection pool.
func configureCleanupIndexScan(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec(`SELECT set_config('enable_seqscan','off',true),
set_config('enable_bitmapscan','off',true),set_config('enable_sort','off',true)`).Error
}

func InitializeErrorLogCleanupPlan(ctx context.Context, db *gorm.DB, run *DataCleanupRun) error {
	if run.Kind != CleanupErrorLogs || run.Before <= 0 || run.MaxID < 0 || run.Scanned != 0 || run.Deleted != 0 || run.CursorAt != 0 || run.CursorID != 0 {
		return errors.New("invalid initial cleanup state")
	}
	modes, err := errorLogCleanupIndexes(ctx, db)
	if err != nil {
		return err
	}
	if !modes["id"] {
		return ErrCleanupScanIndex
	}
	run.ErrorLogCleanupPlan = ErrorLogCleanupPlan{ScanMode: "id", ScanEndID: run.MaxID}
	if modes["error_time"] {
		run.ScanMode = "error_time"
	} else if modes["time"] {
		run.ScanMode = "time"
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := configureCleanupIndexScan(tx); err != nil {
			return err
		}
		if run.ScanMode != "id" {
			var last []cleanupLogCandidate
			query := tx.Model(&Log{}).Select("id,created_at").Where("created_at >= ? AND created_at < ?", 0, run.Before)
			if run.ScanMode == "error_time" {
				query = query.Where("type = ?", LogTypeError)
			}
			if err := query.Order("created_at DESC,id DESC").Limit(1).Find(&last).Error; err != nil {
				return err
			}
			run.ScanEndID = 0
			if len(last) > 0 {
				run.ScanEndAt, run.ScanEndID = last[0].CreatedAt, last[0].ID
			}
		}
		return nil
	})
}

func PrepareErrorLogCleanup(ctx context.Context, db *gorm.DB, before int64) (*DataCleanupRun, error) {
	if before <= 0 {
		return nil, errors.New("invalid cleanup cutoff")
	}
	run := &DataCleanupRun{Kind: CleanupErrorLogs, Before: before}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := configureCleanupIndexScan(tx); err != nil {
			return err
		}
		if err := tx.Model(&Log{}).Select("COALESCE(MAX(id),0)").Scan(&run.MaxID).Error; err != nil {
			return err
		}
		if err := InitializeErrorLogCleanupPlan(ctx, tx, run); err != nil {
			return err
		}
		rows, err := scanErrorLogCleanupCandidates(ctx, tx, run, 1001, errorLogCleanupIDSpan)
		if err != nil {
			return err
		}
		run.CountExact = len(rows) < 1001
		if run.ScanMode == "id" && run.MaxID > errorLogCleanupIDSpan {
			run.CountExact = false
		}
		// Point lookups only, below the oldest supported SQLite bind limit.
		for start := 0; start < len(rows); start += 400 {
			ids := make([]int, 0, min(400, len(rows)-start))
			for _, row := range rows[start:min(start+400, len(rows))] {
				ids = append(ids, row.ID)
			}
			var count int64
			if err := tx.Model(&Log{}).Where("id IN ? AND (type + 0) = ? AND (created_at + 0) >= ? AND (created_at + 0) < ? AND id <= ?", ids, LogTypeError, 0, before, run.MaxID).Count(&count).Error; err != nil {
				return err
			}
			run.Matched += int(count)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

func scanErrorLogCleanupCandidates(ctx context.Context, db *gorm.DB, run *DataCleanupRun, limit, idSpan int) ([]cleanupLogCandidate, error) {
	var rows []cleanupLogCandidate
	query := db.WithContext(ctx).Model(&Log{}).Select("id,created_at")
	if run.ScanMode == "id" {
		endID := run.CursorID + min(idSpan, run.MaxID-run.CursorID)
		err := query.Select("id,created_at,type").Where("id > ? AND id <= ?", run.CursorID, endID).Order("id").Limit(limit).Find(&rows).Error
		return rows, err
	}
	if run.ScanMode != "time" && run.ScanMode != "error_time" {
		return nil, ErrCleanupScanIndex
	}
	if run.ScanEndID == 0 || run.CursorAt > run.ScanEndAt || (run.CursorAt == run.ScanEndAt && run.CursorID >= run.ScanEndID) {
		return rows, nil
	}
	if run.ScanMode == "error_time" {
		query = query.Where("type = ?", LogTypeError)
	}
	query = query.Where("created_at >= ? AND created_at < ?", 0, run.Before)
	// Split the tuple seek so MySQL 5.7 and PostgreSQL do not have to filter
	// an OR cursor while walking the already-scanned historical range.
	same := query.Session(&gorm.Session{}).Where("created_at = ? AND id > ?", run.CursorAt, run.CursorID)
	if run.CursorAt == run.ScanEndAt {
		same = same.Where("id <= ?", run.ScanEndID)
	}
	if err := same.Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) < limit && run.CursorAt < run.ScanEndAt {
		var later []cleanupLogCandidate
		if err := query.Session(&gorm.Session{}).Where("created_at > ? AND created_at <= ?", run.CursorAt, run.ScanEndAt).Order("created_at,id").Limit(limit - len(rows)).Find(&later).Error; err != nil {
			return nil, err
		}
		for _, row := range later {
			if row.CreatedAt == run.ScanEndAt && row.ID > run.ScanEndID {
				break
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// DeleteErrorLogCleanupBatch bounds both the key range and returned candidates.
// Failed or unconfirmed commits leave the caller's checkpoint untouched.
func DeleteErrorLogCleanupBatch(ctx context.Context, db *gorm.DB, run *DataCleanupRun, options ErrorLogCleanupBatchOptions) (DataCleanupBatch, error) {
	if run.Kind != CleanupErrorLogs || run.Before <= 0 || run.MaxID < 0 || options.DeleteLimit < 1 || options.DeleteLimit > MaxErrorLogCleanupBatchSize || options.IDSpan < 1 || options.IDSpan > errorLogCleanupIDSpan {
		return DataCleanupBatch{}, errors.New("invalid error cleanup bounds")
	}
	if run.ScanMode == "id" && (run.CursorID < 0 || run.CursorID > run.MaxID) {
		return DataCleanupBatch{}, errors.New("invalid error cleanup cursor")
	}
	if err := ctx.Err(); err != nil {
		return DataCleanupBatch{}, err
	}
	if run.ScanMode == "id" && run.CursorID == run.MaxID {
		return DataCleanupBatch{Done: true}, nil
	}
	scanLimit := options.DeleteLimit
	if run.ScanMode == "id" {
		scanLimit = min(10*options.DeleteLimit, 20000)
	}
	var result DataCleanupBatch
	next := *run
	phase := "begin"
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		phase = "configure"
		if err := configureCleanupIndexScan(tx); err != nil {
			return err
		}
		phase = "scan"
		rows, err := scanErrorLogCleanupCandidates(ctx, tx, run, scanLimit, options.IDSpan)
		if err != nil {
			return err
		}
		ids := make([]int, 0, min(len(rows), options.DeleteLimit))
		for _, row := range rows {
			result.Scanned++
			next.CursorAt, next.CursorID = row.CreatedAt, row.ID
			if run.ScanMode != "id" || (row.Type == LogTypeError && row.CreatedAt >= 0 && row.CreatedAt < run.Before && row.ID <= run.MaxID) {
				ids = append(ids, row.ID)
			}
			if len(ids) == options.DeleteLimit {
				break // The unread tail must remain ahead of the persisted cursor.
			}
		}
		if run.ScanMode == "id" {
			if len(rows) < scanLimit && result.Scanned == len(rows) && len(ids) < options.DeleteLimit {
				next.CursorID = run.CursorID + min(options.IDSpan, run.MaxID-run.CursorID)
			}
			result.Done = next.CursorID >= run.MaxID
		} else {
			result.Done = len(rows) == 0
		}
		chunkSize := 5000
		if tx.Dialector.Name() == "sqlite" {
			chunkSize = 400
		}
		for start := 0; start < len(ids); start += chunkSize {
			phase = "delete"
			// Keep type/time as residual guards. In particular, SQLite may
			// otherwise prefer a broad type/time index over these primary keys.
			deleted := tx.Where("id IN ? AND (type + 0) = ? AND (created_at + 0) >= ? AND (created_at + 0) < ? AND id <= ?", ids[start:min(start+chunkSize, len(ids))], LogTypeError, 0, run.Before, run.MaxID).Delete(&Log{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.Deleted += deleted.RowsAffected
		}
		next.Deleted += result.Deleted
		next.Scanned += int64(result.Scanned)
		next.UpdatedAt = time.Now().Unix()
		phase = "commit"
		return nil
	})
	if err != nil {
		return DataCleanupBatch{}, fmt.Errorf("error log cleanup %s: %w", phase, err)
	}
	*run = next
	return result, nil
}
