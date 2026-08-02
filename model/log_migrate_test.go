package model

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type logTableMigrationRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *logTableMigrationRecorder) LogMode(gormlogger.LogLevel) gormlogger.Interface { return r }
func (r *logTableMigrationRecorder) Info(context.Context, string, ...interface{})     {}
func (r *logTableMigrationRecorder) Warn(context.Context, string, ...interface{})     {}
func (r *logTableMigrationRecorder) Error(context.Context, string, ...interface{})    {}
func (r *logTableMigrationRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	r.mu.Unlock()
}

func (r *logTableMigrationRecorder) reset() {
	r.mu.Lock()
	r.statements = nil
	r.mu.Unlock()
}

func (r *logTableMigrationRecorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.statements, "\n")
}

func newLogMigrationTestDB(t *testing.T) (*gorm.DB, *logTableMigrationRecorder) {
	t.Helper()
	recorder := &logTableMigrationRecorder{}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: recorder,
	})
	require.NoError(t, err)
	return db, recorder
}

func TestMigrateLogTableCreatesMissingTable(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)

	require.NoError(t, migrateLogTable(db))

	assert.True(t, db.Migrator().HasTable(&Log{}))
	assert.True(t, db.Migrator().HasColumn(&Log{}, "upstream_request_id"))
	assert.True(t, db.Migrator().HasIndex(&Log{}, "idx_logs_upstream_request_id"))
}

func TestMigrateLogTableUpToDateEmitsNoDDLOrDataProbe(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, migrateLogTable(db))
	recorder.reset()

	require.NoError(t, migrateLogTable(db))

	sql := strings.ToUpper(recorder.joined())
	assert.NotContains(t, sql, "ALTER TABLE")
	assert.NotContains(t, sql, "CREATE INDEX")
	assert.NotContains(t, sql, "SELECT * FROM")
}

// 只补真正缺失的那一列，不碰其余列。
//
// 场景刻意设成"整表齐全、只少一列"——这才是一次正常版本升级的形态。
// 早先这里建的是一张只有 id 的表（缺 20 列），那种形态会被
// guardLogColumnAdditions 判定为"目录读坏了"并拒绝启动，
// 因为在一张持续写入的大表上连做 20 次改表几乎必然是故障而非升级。
func TestMigrateLogTableAddsOnlyMissingColumn(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	require.NoError(t, db.Migrator().DropColumn(&Log{}, "upstream_request_id"))
	require.False(t, db.Migrator().HasColumn(&Log{}, "upstream_request_id"))
	recorder.reset()

	require.NoError(t, migrateLogTable(db))

	assert.True(t, db.Migrator().HasColumn(&Log{}, "upstream_request_id"))
	sql := strings.ToUpper(recorder.joined())
	assert.Contains(t, sql, "ALTER TABLE")
	// 只应当出现一次改表：其余 20 列本来就在，不该被碰
	assert.Equal(t, 1, strings.Count(sql, "ADD"), "补列时动了不该动的列")
	assert.NotContains(t, sql, "SELECT * FROM")
}

func TestMigrateLogTableMissingIndexDoesNotBuildOnExistingTable(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_created_at_type"))
	recorder.reset()

	require.NoError(t, migrateLogTable(db))

	assert.False(t, db.Migrator().HasIndex(&Log{}, "idx_created_at_type"))
	sql := strings.ToUpper(recorder.joined())
	assert.NotContains(t, sql, "CREATE INDEX")
}

func TestMigrateLogTableRejectsColumnTypeDrift(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	require.NoError(t, db.Exec("ALTER TABLE logs RENAME COLUMN request_id TO request_id_original").Error)
	require.NoError(t, db.Exec("ALTER TABLE logs ADD COLUMN request_id integer").Error)
	recorder.reset()
	metadata, metadataErr := loadLogColumnMetadata(db)
	require.NoError(t, metadataErr)
	assert.Equal(t, "integer", metadata["request_id"].DataType)

	err := migrateLogTable(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request_id")
	// 运维要能从这条信息直接知道下一步做什么，而不只是"启动失败了"。
	assert.Contains(t, err.Error(), "拒绝启动")
	assert.Contains(t, err.Error(), "在线迁移")
	assert.Contains(t, err.Error(), "数据库实际为")
	assert.NotContains(t, strings.ToUpper(recorder.joined()), "SELECT * FROM")
}

// TestMigrateLogTableStaysWithinRoundTripBudget 是启动耗时的回归护栏。
//
// 最初的实现逐列 HasColumn（21 次）加逐索引 HasIndex（14 次），加上表探测和列目录
// 共 37 条语句；在真实 PostgreSQL 上实测 35ms，其中 27.5ms 花在那 21 次 HasColumn 上，
// 而同样的信息一次读全表目录只要 2.1ms。改成"一次列目录 + 一次 GetIndexes"之后
// 降到 16 条、3.0ms。
//
// 预算定在 20：足以容纳方言实现的小幅波动，但只要有人把按列或按索引的循环写回来，
// 数量就会重新逼近 37 而被这条用例拦下。
func TestMigrateLogTableStaysWithinRoundTripBudget(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	recorder.reset()

	require.NoError(t, migrateLogTable(db))

	recorder.mu.Lock()
	issued := len(recorder.statements)
	recorder.mu.Unlock()

	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&Log{}))
	// 护栏只有在"字段/索引数量远多于语句数"时才有意义，先把这个前提断言出来。
	require.Greater(t, len(stmt.Schema.DBNames)+len(stmt.Schema.ParseIndexes()), 20)

	assert.LessOrEqual(t, issued, 20,
		"logs 表结构校验发出了 %d 条语句；不要按列或按索引循环探测，改用整表目录读取", issued)
}

func TestMigrateLogTableReturnsCatalogFailure(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	require.Error(t, migrateLogTable(db))
}
