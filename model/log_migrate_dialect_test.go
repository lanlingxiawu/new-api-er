package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 方言分支
//
// loadLogColumnMetadata / runLogMigrationDDL 都按方言分支，而单元测试只跑 SQLite。
// 这里补两条：一条用真实日志库（.env 配置的 MySQL / PostgreSQL）验证目录查询的
// 列名映射与类型解析，一条验证 PostgreSQL 专有的 SET LOCAL lock_timeout 路径。
// ---------------------------------------------------------------------------

// TestStartupGateAcceptsEveryConfiguredDatabase 是两个"MySQL 起不来"的回归。
//
// 这个闸门原先只在 PostgreSQL 日志库上验证过，MySQL 分支带着两个致命缺陷：
//
//  1. information_schema 在 MySQL 8 返回**大写**列标签（COLUMN_NAME），而结构体
//     tag 是小写、GORM 映射大小写敏感 —— 一列都扫不进来，于是每列都被判"缺失"，
//     启动时对已存在的列 AddColumn，撞上 "Duplicate column name"。
//  2. MySQL 没有原生布尔类型，BOOLEAN 就是 TINYINT(1)；漂移检测不认 tinyint，
//     把完全正常的 is_stream 判成定义不一致。
//
// 两个都会让**所有单库 MySQL 部署**无法启动。这条用例对 .env 里配置的每一个真实
// 库跑一遍完整闸门，任何方言上的误判都会在这里暴露。
func TestStartupGateAcceptsEveryConfiguredDatabase(t *testing.T) {
	checked := 0
	for label, db := range map[string]*gorm.DB{"主库": DB, "日志库": LOG_DB} {
		if db == nil || !db.Migrator().HasTable(&Log{}) {
			continue
		}
		checked++
		require.NoErrorf(t, migrateLogTable(db),
			"%s（方言 %s）会被启动闸门拒绝", label, db.Dialector.Name())
	}
	if checked == 0 {
		t.Skip("没有配置带 logs 表的真实数据库")
	}
	t.Logf("已验证 %d 个真实数据库的启动闸门", checked)
}

// ---------------------------------------------------------------------------
// 启动期改表的两道防护
//
// logs 是持续写入的大表，启动路径上的 ADD COLUMN 既慢又危险。
// ---------------------------------------------------------------------------

// TestGuardRefusesImplausibleColumnAdditions：缺失列数不合理时必须拒绝启动，
// 而不是真的去改表。
//
// 这条用例直接对应此前那个 MySQL 缺陷：目录读坏 → 21 列全被判缺失 →
// 对已存在的列 ADD COLUMN。当时是 MySQL 用 "Duplicate column name" 挡住的，
// 属于运气；有了这道防护，那种情况在碰到表之前就会被拦下。
func TestGuardRefusesImplausibleColumnAdditions(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	recorder.reset()

	// 模拟"目录读坏"：所有列都被判缺失
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&Log{}))
	allColumns := append([]string(nil), stmt.Schema.DBNames...)

	err := guardLogColumnAdditions(db, allColumns, len(allColumns))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "拒绝启动")
	assert.Contains(t, err.Error(), "读取表结构本身出了问题")
	assert.NotContains(t, strings.ToUpper(recorder.joined()), "ADD COLUMN",
		"防护应当在任何改表之前生效")
}

// 少量缺列是正常升级，放行。
func TestGuardAllowsSmallColumnAdditions(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	require.NoError(t, guardLogColumnAdditions(db, nil, 21))
	require.NoError(t, guardLogColumnAdditions(db, []string{"a"}, 21))
	require.NoError(t, guardLogColumnAdditions(db, []string{"a", "b", "c"}, 21))
	require.Error(t, guardLogColumnAdditions(db, []string{"a", "b", "c", "d"}, 21),
		"超过阈值必须拒绝")
}

// 运维可以完全禁止启动期改表：缺列一律转为拒绝启动并列出待补字段。
func TestGuardHonoursAutoAddColumnSwitch(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	t.Setenv("LOG_MIGRATE_AUTO_ADD_COLUMN", "false")

	require.NoError(t, guardLogColumnAdditions(db, nil, 21), "没有缺列时不该报错")

	err := guardLogColumnAdditions(db, []string{"new_col"}, 21)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new_col")
	assert.Contains(t, err.Error(), "禁止启动期改表")
}

// 开关关闭时，完整的迁移闸门也必须在改表之前停下。
func TestMigrateLogTableRespectsAutoAddColumnSwitch(t *testing.T) {
	db, recorder := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	require.NoError(t, db.Migrator().DropColumn(&Log{}, "other"))
	recorder.reset()
	t.Setenv("LOG_MIGRATE_AUTO_ADD_COLUMN", "false")

	err := migrateLogTable(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "other")
	assert.NotContains(t, strings.ToUpper(recorder.joined()), "ADD COLUMN",
		"开关关闭后不该执行任何改表")
}

// MySQL 用 tinyint 存布尔是正常形态，不能判为漂移。
func TestBoolColumnAcceptsMySQLTinyint(t *testing.T) {
	stmt := &gorm.Statement{DB: DB}
	if DB == nil {
		t.Skip("未配置主库")
	}
	require.NoError(t, stmt.Parse(&Log{}))
	field := stmt.Schema.FieldsByDBName["is_stream"]
	require.NotNil(t, field)

	assert.Empty(t, logColumnDrift(field, logColumnMetadata{
		Name: "is_stream", DataType: "tinyint",
	}, "mysql"), "MySQL 的 tinyint 是布尔的正常存储形态")
	assert.Empty(t, logColumnDrift(field, logColumnMetadata{
		Name: "is_stream", DataType: "boolean",
	}, "postgres"))
	assert.NotEmpty(t, logColumnDrift(field, logColumnMetadata{
		Name: "is_stream", DataType: "varchar",
	}, "mysql"), "真正的类型不一致仍要被检出")
}

// TestLoadLogColumnMetadataOnRealDialect 验证 information_schema 查询在真实方言上
// 能正确映射到结构体字段。列名大小写、驱动的 NULL 处理都只有真库能暴露。
func TestLoadLogColumnMetadataOnRealDialect(t *testing.T) {
	requireLogDB(t)
	dialect := LOG_DB.Dialector.Name()

	columns, err := loadLogColumnMetadata(LOG_DB)
	require.NoError(t, err, "方言 %s 的目录查询失败", dialect)
	require.NotEmpty(t, columns)
	require.NotContains(t, columns, "", "列名为空说明扫描映射失效")

	// 模型声明的每一列都必须能在目录里找到，且类型非空——
	// 若列名映射错了（比如 MySQL 8 的 COLUMN_NAME 大小写），这里会全部落空
	stmt := &gorm.Statement{DB: LOG_DB}
	require.NoError(t, stmt.Parse(&Log{}))
	for _, fieldName := range stmt.Schema.DBNames {
		actual, ok := columns[fieldName]
		require.True(t, ok, "方言 %s 的目录里找不到列 %s", dialect, fieldName)
		assert.Equal(t, fieldName, actual.Name)
		assert.NotEmpty(t, actual.DataType, "列 %s 的类型为空，说明字段映射没生效", fieldName)
	}

	// 主键必须被识别为非空；SQLite 分支靠 pk 列判断，MySQL/PG 靠 is_nullable
	idColumn, ok := columns["id"]
	require.True(t, ok)
	assert.False(t, idColumn.Nullable, "主键 id 不该被判定为可空")

	// 带 type:varchar(N) 标签的列必须解析出长度，否则漂移检测形同虚设
	if requestID, exists := columns["request_id"]; exists && requestID.CharLength != nil {
		assert.Positive(t, *requestID.CharLength)
	}
}

// 主库通常与日志库方言不同（本环境是 MySQL + PostgreSQL）。
// 只测其中一个就会漏掉另一个方言的映射缺陷——这正是大写列标签那个 bug
// 能一路活到现在的原因。
func TestLoadLogColumnMetadataOnMainDatabase(t *testing.T) {
	requireDB(t)
	if !DB.Migrator().HasTable(&Log{}) {
		t.Skip("主库没有 logs 表（配置了独立日志库）")
	}
	columns, err := loadLogColumnMetadata(DB)
	require.NoError(t, err, "方言 %s 的目录查询失败", DB.Dialector.Name())
	require.NotContains(t, columns, "", "列名为空说明扫描映射失效")

	stmt := &gorm.Statement{DB: DB}
	require.NoError(t, stmt.Parse(&Log{}))
	for _, fieldName := range stmt.Schema.DBNames {
		require.Containsf(t, columns, fieldName,
			"方言 %s 的目录里找不到列 %s", DB.Dialector.Name(), fieldName)
	}
}

func TestLoadLogColumnMetadataRejectsUnsupportedDialect(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	// 伪造一个不认识的方言名
	previous := db.Config.Dialector
	db.Config.Dialector = unsupportedDialector{Dialector: previous}
	t.Cleanup(func() { db.Config.Dialector = previous })

	_, err := loadLogColumnMetadata(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不支持的日志库方言")
}

type unsupportedDialector struct{ gorm.Dialector }

func (unsupportedDialector) Name() string { return "cockroachdb" }

// ---------------------------------------------------------------------------
// runLogMigrationDDL
// ---------------------------------------------------------------------------

// 非 PostgreSQL 走 context 超时；超时预算配成 0 时直接执行、不加任何包装。
func TestRunLogMigrationDDLHonoursDisabledTimeout(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	t.Setenv("LOG_MIGRATE_LOCK_TIMEOUT_MS", "0")

	var received *gorm.DB
	require.NoError(t, runLogMigrationDDL(db, func(tx *gorm.DB) error {
		received = tx
		return nil
	}))
	assert.Same(t, db, received, "预算为 0 时应当原样传入，不包 context")
}

func TestRunLogMigrationDDLPropagatesError(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	sentinel := errors.New("ddl exploded")

	err := runLogMigrationDDL(db, func(*gorm.DB) error { return sentinel })

	require.ErrorIs(t, err, sentinel)
}

// PostgreSQL 分支必须在事务里先下 SET LOCAL lock_timeout：
// 这是"等锁最多 N 毫秒"的唯一实现方式，漏了就会在热表上无限等待。
func TestRunLogMigrationDDLSetsPostgresLockTimeout(t *testing.T) {
	requireLogDB(t)
	if LOG_DB.Dialector.Name() != "postgres" {
		t.Skip("非 PostgreSQL 日志库，跳过锁超时分支")
	}
	t.Setenv("LOG_MIGRATE_LOCK_TIMEOUT_MS", "1234")

	var observed string
	err := runLogMigrationDDL(LOG_DB, func(tx *gorm.DB) error {
		return tx.Raw("SHOW lock_timeout").Scan(&observed).Error
	})
	require.NoError(t, err)
	assert.Equal(t, "1234ms", observed, "SET LOCAL lock_timeout 没有生效")

	// SET LOCAL 只在事务内有效，事务外必须已经恢复
	var outside string
	require.NoError(t, LOG_DB.Raw("SHOW lock_timeout").Scan(&outside).Error)
	assert.NotEqual(t, "1234ms", outside, "锁超时泄漏到了事务之外")
}

// ---------------------------------------------------------------------------
// missingLogIndexes 的退回分支
// ---------------------------------------------------------------------------

// SQLite 驱动（glebarez/sqlite）不支持 GetIndexes，直接返回 "not support"，
// 而 SQLite 是 SQL_DSN 未配置时的默认库——所以退回逐个 HasIndex 是**常态路径**，
// 不是异常路径。这条用例先把这个前提钉住，避免将来有人误以为退路只在边缘方言上跑。
func TestSQLiteDoesNotSupportBulkIndexLookup(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))

	_, err := db.Migrator().GetIndexes(&Log{})
	require.Error(t, err, "若 SQLite 驱动开始支持 GetIndexes，这里的前提就变了")
}

// 退回分支：不能把"读不到索引目录"当成"没有索引"，否则每次启动都误报缺失全部索引。
func TestMissingLogIndexesFallbackDetectsComplete(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&Log{}))

	// db.Migrator() 在 SQLite 上本身就走退回分支；再叠一个显式失败的实现，
	// 覆盖"其他方言也不支持"的情形，两者结论必须一致
	assert.Empty(t, missingLogIndexes(db.Migrator(), stmt), "索引齐全却报了缺失")
	failing := failingGetIndexesMigrator{Migrator: db.Migrator()}
	assert.Empty(t, missingLogIndexes(failing, stmt), "退回分支把已存在的索引误判为缺失了")
}

func TestMissingLogIndexesFallbackDetectsMissing(t *testing.T) {
	db, _ := newLogMigrationTestDB(t)
	require.NoError(t, db.Migrator().CreateTable(&Log{}))
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&Log{}))
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_created_at_type"))

	missing := missingLogIndexes(db.Migrator(), stmt)
	require.Len(t, missing, 1)
	assert.Equal(t, "idx_created_at_type", missing[0])

	failing := failingGetIndexesMigrator{Migrator: db.Migrator()}
	fallbackMissing := missingLogIndexes(failing, stmt)
	require.Len(t, fallbackMissing, 1)
	assert.Equal(t, "idx_created_at_type", fallbackMissing[0])
}

// GetIndexes 成功路径只有支持该能力的方言才走得到，因此必须用真实日志库验。
// SQLite 上这条路径永远不会执行，用 SQLite 测等于什么都没测。
func TestMissingLogIndexesUsesBulkLookupOnRealDialect(t *testing.T) {
	requireLogDB(t)
	if _, err := LOG_DB.Migrator().GetIndexes(&Log{}); err != nil {
		t.Skipf("方言 %s 不支持 GetIndexes，成功路径不适用", LOG_DB.Dialector.Name())
	}
	stmt := &gorm.Statement{DB: LOG_DB}
	require.NoError(t, stmt.Parse(&Log{}))

	missing := missingLogIndexes(LOG_DB.Migrator(), stmt)
	assert.Empty(t, missing,
		"真实日志库缺少索引 %v；若确属缺失请补迁移，否则说明批量比对有问题", missing)

	// 与逐个探测的结论必须一致，否则批量路径的名称匹配有问题
	failing := failingGetIndexesMigrator{Migrator: LOG_DB.Migrator()}
	assert.Equal(t, missing, missingLogIndexes(failing, stmt),
		"批量读取与逐个探测得出了不同结论")
}

type failingGetIndexesMigrator struct{ gorm.Migrator }

func (failingGetIndexesMigrator) GetIndexes(interface{}) ([]gorm.Index, error) {
	return nil, errors.New("dialect does not support GetIndexes")
}
