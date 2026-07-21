package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// applyCreatedAtTimeRange conditionally appends created_at bounds. We inspect
// the generated SQL (DryRun) so the assertions do not depend on any table
// having data, and cover all four branch combinations (start present/absent x
// end present/absent).

func timeRangeSQL(t *testing.T, start, end int64) string {
	t.Helper()
	requireDB(t)
	sql := DB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		tx = tx.Model(&PerfMetric{})
		tx = applyCreatedAtTimeRange(tx, start, end)
		return tx.Find(&[]PerfMetric{})
	})
	return sql
}

func TestApplyCreatedAtTimeRange_NoBounds(t *testing.T) {
	sql := timeRangeSQL(t, 0, 0)
	assert.NotContains(t, sql, "created_at >=")
	assert.NotContains(t, sql, "created_at <=")
}

func TestApplyCreatedAtTimeRange_StartOnly(t *testing.T) {
	sql := timeRangeSQL(t, 100, 0)
	assert.Contains(t, sql, "created_at >=")
	assert.NotContains(t, sql, "created_at <=")
}

func TestApplyCreatedAtTimeRange_EndOnly(t *testing.T) {
	sql := timeRangeSQL(t, 0, 200)
	assert.NotContains(t, sql, "created_at >=")
	assert.Contains(t, sql, "created_at <=")
}

func TestApplyCreatedAtTimeRange_BothBounds(t *testing.T) {
	sql := timeRangeSQL(t, 100, 200)
	assert.Contains(t, sql, "created_at >=")
	assert.Contains(t, sql, "created_at <=")
	// both predicates AND-combined
	assert.True(t, strings.Contains(sql, "WHERE"))
}

// The helper must return the same *gorm.DB it received when no bounds apply
// (it just returns tx unchanged), and a chainable tx otherwise.
func TestApplyCreatedAtTimeRange_ReturnsChainable(t *testing.T) {
	requireDB(t)
	tx := DB.Session(&gorm.Session{DryRun: true}).Model(&PerfMetric{})
	got := applyCreatedAtTimeRange(tx, 0, 0)
	require.NotNil(t, got)
}
