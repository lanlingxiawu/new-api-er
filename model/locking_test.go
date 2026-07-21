package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// lockForUpdate must attach a `FOR UPDATE` clause on non-SQLite dialects and
// leave the query untouched on SQLite (which has no such syntax). We branch on
// common.MainDatabaseType() only, so we can exercise both branches by
// temporarily flipping the configured type (restored after) while inspecting
// the generated SQL via DryRun.

func lockForUpdateSQL(t *testing.T) string {
	t.Helper()
	requireDB(t)
	return DB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		tx = tx.Model(&PerfMetric{}).Where("id = ?", 1)
		tx = lockForUpdate(tx)
		return tx.Find(&[]PerfMetric{})
	})
}

func withMainDBType(t *testing.T, dbType common.DatabaseType) {
	t.Helper()
	prev := common.MainDatabaseType()
	common.SetMainDatabaseType(dbType)
	t.Cleanup(func() { common.SetMainDatabaseType(prev) })
}

func TestLockForUpdate_SQLiteSkips(t *testing.T) {
	requireDB(t)
	withMainDBType(t, common.DatabaseTypeSQLite)
	sql := lockForUpdateSQL(t)
	assert.NotContains(t, sql, "FOR UPDATE")
}

func TestLockForUpdate_MySQLLocks(t *testing.T) {
	requireDB(t)
	withMainDBType(t, common.DatabaseTypeMySQL)
	sql := lockForUpdateSQL(t)
	assert.Contains(t, sql, "FOR UPDATE")
}

func TestLockForUpdate_PostgresLocks(t *testing.T) {
	requireDB(t)
	withMainDBType(t, common.DatabaseTypePostgreSQL)
	sql := lockForUpdateSQL(t)
	assert.Contains(t, sql, "FOR UPDATE")
}

// The SQLite branch returns the exact same *gorm.DB pointer it was given.
func TestLockForUpdate_SQLiteReturnsSameTx(t *testing.T) {
	requireDB(t)
	withMainDBType(t, common.DatabaseTypeSQLite)
	tx := DB.Session(&gorm.Session{DryRun: true}).Model(&PerfMetric{})
	got := lockForUpdate(tx)
	require.Same(t, tx, got)
}
