package service

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"gorm.io/gorm"
)

// The opt-in fixture measures real confirmed transactions, including COMMIT.
// SQL statement timings alone exclude fsync/replication and are not throughput.
type cleanupScalePool struct {
	*sql.DB
	trace *cleanupScaleTrace
}

func (pool cleanupScalePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	started := time.Now()
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &cleanupScaleTx{Tx: tx, started: started, trace: pool.trace}, nil
}

type cleanupScaleTx struct {
	*sql.Tx
	started time.Time
	trace   *cleanupScaleTrace
	deleted int64
}

func (tx *cleanupScaleTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := tx.Tx.ExecContext(ctx, query, args...)
	if err == nil && strings.HasPrefix(query, "DELETE FROM") {
		count, countErr := result.RowsAffected()
		if countErr == nil {
			tx.deleted += count
		}
	}
	return result, err
}

func (tx *cleanupScaleTx) Commit() error {
	err := tx.Tx.Commit()
	if err == nil && tx.deleted > 0 {
		if tx.trace.firstCommit == 0 {
			tx.trace.firstCommit = time.Since(tx.trace.started).Seconds()
		}
		tx.trace.samples["committed_log_transaction"] = append(tx.trace.samples["committed_log_transaction"], float64(time.Since(tx.started).Microseconds())/1000)
		tx.trace.batchSizes[tx.deleted]++
	}
	return err
}
