package model

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// migrateLogTable deliberately avoids AutoMigrate and ColumnTypes. The latter
// issues SELECT * FROM logs LIMIT 1 on some dialects, while AutoMigrate may
// repeatedly request DDL for equivalent PostgreSQL defaults/types. Since logs
// is written by the relay path, startup must only touch its schema when a
// column or index is genuinely missing.
// 诊断信息一律用中文：logs 是生产热表，这些错误会直接阻止进程启动，
// 读者是需要立刻决定怎么处置的运维，不是开发。
func migrateLogTable(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("日志库未初始化，无法校验 logs 表结构")
	}
	migrator := db.Migrator()
	if !migrator.HasTable(&Log{}) {
		if err := runLogMigrationDDL(db, func(tx *gorm.DB) error {
			return tx.Migrator().CreateTable(&Log{})
		}); err != nil {
			return fmt.Errorf("创建 logs 表失败，relay 消费日志将完全无法落库：%w", err)
		}
		return nil
	}

	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&Log{}); err != nil {
		return fmt.Errorf("解析 Log 模型结构失败：%w", err)
	}

	// 一次读全表目录，而不是逐列 HasColumn。两者信息量相同，但 HasColumn 每列一次
	// 往返：21 个字段实测 27.5ms（真实 PostgreSQL），而整表目录一次只要 2.1ms。
	// 目录本来就要为下面的定义校验读一次，这里直接复用。
	columns, err := loadLogColumnMetadata(db)
	if err != nil {
		return err
	}

	missingColumns := make([]string, 0)
	for _, fieldName := range stmt.Schema.DBNames {
		if field := stmt.Schema.FieldsByDBName[fieldName]; field == nil {
			continue
		}
		if _, exists := columns[fieldName]; !exists {
			missingColumns = append(missingColumns, fieldName)
		}
	}

	if err := guardLogColumnAdditions(db, missingColumns, len(stmt.Schema.DBNames)); err != nil {
		return err
	}

	columnAdded := false
	for _, fieldName := range missingColumns {
		field := stmt.Schema.FieldsByDBName[fieldName]
		startedAt := time.Now()
		common.SysLog(fmt.Sprintf(
			"logs 表缺少字段 %s，即将在其上执行 ADD COLUMN（这是一张持续写入的大表，"+
				"如需完全禁止启动期改表请设置 LOG_MIGRATE_AUTO_ADD_COLUMN=false）", fieldName))
		err := runLogMigrationDDL(db, func(tx *gorm.DB) error {
			return tx.Migrator().AddColumn(&Log{}, field.Name)
		})
		if err != nil {
			return fmt.Errorf(
				"为 logs 表补充缺失字段 %s 失败：%w；"+
					"当前写入使用完整模型，缺字段会导致每一条消费日志写入失败，因此拒绝启动。"+
					"请手动执行加列迁移后重启（若加列锁等待超时，可调大 LOG_MIGRATE_LOCK_TIMEOUT_MS）",
				fieldName, err,
			)
		}
		common.SysLog(fmt.Sprintf("已为 logs 表补充缺失字段 %s，耗时 %s", fieldName, time.Since(startedAt)))
		columnAdded = true
	}

	// 只有真的补过列时目录才会过期，需要重读一次拿到新列的定义。
	if columnAdded {
		if columns, err = loadLogColumnMetadata(db); err != nil {
			return err
		}
	}
	if err := validateLogColumnDefinitions(db, stmt, columns); err != nil {
		return err
	}

	missingIndexes := missingLogIndexes(migrator, stmt)
	if len(missingIndexes) > 0 {
		sort.Strings(missingIndexes)
		common.SysError(fmt.Sprintf(
			"logs 表缺少索引 [%s]（数据库方言：%s）。写入仍然正确，因此启动继续；"+
				"但相关查询会退化为全表扫描，必须用显式的在线索引迁移补齐"+
				"（PostgreSQL 用 CREATE INDEX CONCURRENTLY，在事务外、独立连接上执行），"+
				"启动路径不会自动创建，以免在热表上长时间占用日志库 I/O",
			strings.Join(missingIndexes, ", "),
			db.Dialector.Name(),
		))
		return nil
	}
	common.SysLog("logs 表结构校验通过")
	return nil
}

// missingLogIndexes 优先用一次 GetIndexes 代替逐个 HasIndex：实测真实 PostgreSQL 上
// 14 次 HasIndex 要 5.8ms，一次 GetIndexes 只要 0.5ms。
//
// 但**不是所有方言都支持**：SQLite 驱动（glebarez/sqlite）直接返回 "not support"，
// 而 SQLite 正是 SQL_DSN 未配置时的默认库。所以这条退路是常态而不是异常，
// 日志用陈述语气写清楚"该方言不支持批量读取"，不要写成"失败"——
// 否则每个 SQLite 部署每次启动都会看到一行像故障一样的输出。
func missingLogIndexes(migrator gorm.Migrator, stmt *gorm.Statement) []string {
	declared := stmt.Schema.ParseIndexes()
	missing := make([]string, 0)

	existing, err := migrator.GetIndexes(&Log{})
	if err != nil {
		common.SysLog(fmt.Sprintf(
			"当前方言不支持批量读取索引目录（%s），改为逐个索引探测；这不影响结果，只是多几次元数据查询",
			err.Error(),
		))
		for name := range declared {
			if !migrator.HasIndex(&Log{}, name) {
				missing = append(missing, name)
			}
		}
		return missing
	}

	present := make(map[string]struct{}, len(existing))
	for _, index := range existing {
		present[index.Name()] = struct{}{}
	}
	for name := range declared {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// guardLogColumnAdditions 在启动期对 logs 执行任何 ADD COLUMN 之前做两道拦截。
//
// logs 是持续写入的大表（生产上 4 亿行量级），启动路径上的改表既慢又危险，
// 所以这里宁可拒绝启动让人来处理，也不擅自动表。
//
// 拦截一：数量不合理即判定为"读坏了"而不是"真缺列"。
// 一次正常的版本升级最多新增一两列；如果目录说缺很多列，几乎必然是目录读取本身
// 出了问题——历史上就发生过：MySQL 的 information_schema 返回大写列标签、
// 而扫描 tag 是小写，导致一列都没扫进来、21 列全被判缺失，
// 启动时对已存在的列 ADD COLUMN，撞上 "Duplicate column name" 才暴露。
// 那次是 MySQL 恰好挡住了；不能指望下一次还有人挡。
//
// 拦截二：LOG_MIGRATE_AUTO_ADD_COLUMN=false 时完全禁止启动期改表，
// 缺列一律转为拒绝启动并给出待补字段，由运维用显式在线迁移处理。
func guardLogColumnAdditions(db *gorm.DB, missing []string, totalColumns int) error {
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	// 阈值取 3：版本升级一次加一两列是常态，加三列以上已经罕见，
	// 而"读坏了"的典型表现是缺失数接近甚至等于总列数。
	const implausibleMissingCount = 3
	if len(missing) > implausibleMissingCount {
		return fmt.Errorf(
			"logs 表被判定缺少 %d/%d 个字段 [%s]（方言 %s）。"+
				"一次正常升级不会缺这么多列，这几乎必然是读取表结构本身出了问题；"+
				"若真按此结果执行 ADD COLUMN，会在一张持续写入的大表上做多次改表。"+
				"因此拒绝启动，请先人工核对 logs 表结构与数据库账号的元数据读取权限",
			len(missing), totalColumns, strings.Join(missing, ", "), db.Dialector.Name(),
		)
	}

	if !common.GetEnvOrDefaultBool("LOG_MIGRATE_AUTO_ADD_COLUMN", true) {
		return fmt.Errorf(
			"logs 表缺少字段 [%s]，而 LOG_MIGRATE_AUTO_ADD_COLUMN=false 禁止启动期改表。"+
				"请用显式的在线迁移补齐这些字段后重启",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

type logColumnMetadata struct {
	Name       string
	DataType   string
	Nullable   bool
	Default    *string
	CharLength *int64
}

func loadLogColumnMetadata(db *gorm.DB) (map[string]logColumnMetadata, error) {
	columns := make(map[string]logColumnMetadata)
	switch db.Dialector.Name() {
	case "postgres":
		var rows []struct {
			Name       string  `gorm:"column:column_name"`
			DataType   string  `gorm:"column:data_type"`
			IsNullable string  `gorm:"column:is_nullable"`
			Default    *string `gorm:"column:column_default"`
			CharLength *int64  `gorm:"column:character_maximum_length"`
		}
		// 显式小写别名：结构体 tag 是小写的，而 GORM 按列标签大小写敏感地映射。
		// PostgreSQL 默认就返回小写，但写死别名可以让两个方言的行为一致，
		// 也不会因为将来换驱动或改大小写设置而悄悄失配。
		err := db.Raw(`
SELECT column_name AS column_name, data_type AS data_type, is_nullable AS is_nullable,
       column_default AS column_default, character_maximum_length AS character_maximum_length
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = ?`, "logs").Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("读取 PostgreSQL 的 logs 表结构失败，无法证明日志表可用，因此拒绝启动：%w", err)
		}
		for _, row := range rows {
			columns[row.Name] = logColumnMetadata{
				Name: row.Name, DataType: row.DataType, Nullable: row.IsNullable == "YES",
				Default: row.Default, CharLength: row.CharLength,
			}
		}
	case "mysql":
		var rows []struct {
			Name       string  `gorm:"column:column_name"`
			DataType   string  `gorm:"column:data_type"`
			IsNullable string  `gorm:"column:is_nullable"`
			Default    *string `gorm:"column:column_default"`
			CharLength *int64  `gorm:"column:character_maximum_length"`
		}
		// 必须写显式小写别名。MySQL 8 的 information_schema 返回的列标签是**大写**
		// （COLUMN_NAME / DATA_TYPE / ...），而结构体 tag 是小写、GORM 的映射大小写
		// 敏感——不加别名会一列都扫不进来，map 里只剩一个 key="" 的条目，
		// 于是每一列都被判定为"缺失"，启动时去 AddColumn 又撞上
		// "Duplicate column name"，所有单库 MySQL 部署都起不来。
		err := db.Raw(`
SELECT COLUMN_NAME AS column_name, DATA_TYPE AS data_type, IS_NULLABLE AS is_nullable,
       COLUMN_DEFAULT AS column_default, CHARACTER_MAXIMUM_LENGTH AS character_maximum_length
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = ?`, "logs").Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("读取 MySQL 的 logs 表结构失败，无法证明日志表可用，因此拒绝启动：%w", err)
		}
		for _, row := range rows {
			columns[row.Name] = logColumnMetadata{
				Name: row.Name, DataType: row.DataType, Nullable: row.IsNullable == "YES",
				Default: row.Default, CharLength: row.CharLength,
			}
		}
	case "sqlite":
		var rows []struct {
			Name       string
			Type       string
			NotNull    int     `gorm:"column:not_null"`
			Default    *string `gorm:"column:default_value"`
			PrimaryKey int     `gorm:"column:primary_key"`
		}
		if err := db.Raw(`
SELECT name, type, "notnull" AS not_null, dflt_value AS default_value, pk AS primary_key
FROM pragma_table_info(?)`, "logs").Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("读取 SQLite 的 logs 表结构失败，无法证明日志表可用，因此拒绝启动：%w", err)
		}
		for _, row := range rows {
			dataType, charLength := splitSQLType(row.Type)
			columns[row.Name] = logColumnMetadata{
				Name: row.Name, DataType: dataType, Nullable: row.NotNull == 0 && row.PrimaryKey == 0,
				Default: row.Default, CharLength: charLength,
			}
		}
	default:
		return nil, fmt.Errorf("不支持的日志库方言 %q，无法校验 logs 表结构", db.Dialector.Name())
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("查询 logs 表结构返回空结果，通常说明表不存在或数据库账号无权读取表结构，因此拒绝启动")
	}
	// 空列名说明查询结果没能映射进结构体（历史上就是 MySQL 返回大写列标签、
	// 而 tag 是小写导致的）。这种情况下 map 里全是垃圾，会把每一列都误判成缺失，
	// 进而在启动时对已存在的列执行 AddColumn。必须在这里就明确失败，
	// 而不是让错误以 "Duplicate column name" 的形式在下游冒出来。
	if _, broken := columns[""]; broken {
		return nil, fmt.Errorf(
			"读取 logs 表结构时列名为空，说明查询结果未能映射到字段（方言 %s）；"+
				"这是代码缺陷而非数据库问题，请检查目录查询的列别名大小写",
			db.Dialector.Name(),
		)
	}
	return columns, nil
}

func splitSQLType(value string) (string, *int64) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	open := strings.IndexByte(normalized, '(')
	if open < 0 {
		return normalized, nil
	}
	close := strings.IndexByte(normalized[open+1:], ')')
	if close < 0 {
		return normalized, nil
	}
	length, err := strconv.ParseInt(strings.TrimSpace(normalized[open+1:open+1+close]), 10, 64)
	if err != nil {
		return normalized[:open], nil
	}
	return normalized[:open], &length
}

// validateLogColumnDefinitions 在字段定义漂移时拒绝启动，这是设计决策
// （docs/design/startup-log-schema-migration.md §11.3）：启动时自动改类型会重写
// 一张 5000 亿字节量级的热表，带着漂移的定义继续跑又无法保证 relay 的写入契约，
// 因此只能停在这里，交给显式的运维迁移处理。
func validateLogColumnDefinitions(db *gorm.DB, stmt *gorm.Statement, columns map[string]logColumnMetadata) error {
	for _, fieldName := range stmt.Schema.DBNames {
		field := stmt.Schema.FieldsByDBName[fieldName]
		actual, ok := columns[fieldName]
		if field == nil || !ok {
			return fmt.Errorf(
				"logs 表字段 %s 在补列之后仍未出现在数据库目录中，无法证明日志表可写，因此拒绝启动。"+
					"请确认该字段是否被其他会话并发改动，或数据库账号是否有读取表结构的权限",
				fieldName,
			)
		}
		if reason := logColumnDrift(field, actual, db.Dialector.Name()); reason != "" {
			return fmt.Errorf(
				"logs 表字段 %s 的定义与程序模型不一致（数据库方言：%s）：%s。"+
					"启动时自动改类型会重写整张热表并长时间锁表，带着不一致的定义继续运行又可能导致写入被截断或直接失败，"+
					"因此拒绝启动。请用显式的在线迁移把该字段改成模型期望的定义后重启；"+
					"若确认当前定义可以兼容写入，也可以先把模型标签调整为与数据库一致",
				fieldName, db.Dialector.Name(), reason,
			)
		}
	}
	return nil
}

func logColumnDrift(field *schema.Field, actual logColumnMetadata, dialect string) string {
	actualType := strings.ToLower(actual.DataType)
	typeMatches := false
	expectedDataType := field.GORMDataType
	if expectedDataType == "" {
		expectedDataType = field.DataType
	}
	switch expectedDataType {
	case schema.Bool:
		// MySQL 没有原生布尔类型，BOOLEAN 只是 TINYINT(1) 的别名，
		// 所以 tinyint 是它存布尔的**正常**形态，不是漂移。
		// SQLite 无类型亲和时会落到 numeric。
		typeMatches = actualType == "boolean" || actualType == "bool" ||
			(dialect == "mysql" && actualType == "tinyint") ||
			(dialect == "sqlite" && actualType == "numeric")
	case schema.Int, schema.Uint:
		typeMatches = strings.Contains(actualType, "int")
	case schema.Float:
		typeMatches = strings.Contains(actualType, "real") || strings.Contains(actualType, "float") ||
			strings.Contains(actualType, "double") || strings.Contains(actualType, "numeric") ||
			strings.Contains(actualType, "decimal")
	case schema.String:
		typeMatches = strings.Contains(actualType, "char") || strings.Contains(actualType, "text") ||
			(dialect == "sqlite" && actualType == "")
	default:
		typeMatches = true
	}
	if !typeMatches {
		return fmt.Sprintf("模型期望 %s 兼容类型，数据库实际为 %s", field.DataType, actual.DataType)
	}
	if size, ok := field.TagSettings["SIZE"]; ok && actual.CharLength != nil {
		expected, err := strconv.ParseInt(size, 10, 64)
		if err == nil && *actual.CharLength != expected {
			return fmt.Sprintf("模型期望长度 %d，数据库实际为 %d", expected, *actual.CharLength)
		}
	}
	if declaredType := field.TagSettings["TYPE"]; declaredType != "" && actual.CharLength != nil {
		_, expectedLength := splitSQLType(declaredType)
		if expectedLength != nil && *actual.CharLength != *expectedLength {
			return fmt.Sprintf("模型期望长度 %d，数据库实际为 %d", *expectedLength, *actual.CharLength)
		}
	}
	if field.NotNull && actual.Nullable {
		return "模型期望 NOT NULL，数据库实际可空"
	}
	return ""
}

func runLogMigrationDDL(db *gorm.DB, ddl func(*gorm.DB) error) error {
	timeoutMs := common.GetEnvOrDefault("LOG_MIGRATE_LOCK_TIMEOUT_MS", 3000)
	if timeoutMs <= 0 {
		return ddl(db)
	}

	if db.Dialector.Name() != "postgres" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
		return ddl(db.WithContext(ctx))
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", timeoutMs)).Error; err != nil {
			return err
		}
		return ddl(tx)
	})
}
