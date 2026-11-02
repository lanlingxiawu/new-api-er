package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Server DSNs must point to explicitly disposable exclusion_matrix databases.
// Production/development databases are never accepted by this schema test.
func TestUserChannelExclusionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dialector gorm.Dialector
			switch dialect {
			case "sqlite":
				dialector = sqlite.Open(filepath.Join(t.TempDir(), "exclusion_matrix.db"))
			case "mysql":
				dsn := os.Getenv("TEST_EXCLUSION_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_EXCLUSION_MYSQL_DSN not configured")
				}
				require.Contains(t, dsn, "/exclusion_matrix?")
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_EXCLUSION_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_EXCLUSION_POSTGRES_DSN not configured")
				}
				require.Contains(t, dsn, "/exclusion_matrix?")
				dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			recorder := &migrationSQLRecorder{}
			db, err := gorm.Open(dialector, &gorm.Config{Logger: recorder})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				// SQLite has one writer; serialize background deletion finalizers too.
				pool.SetMaxOpenConns(1)
			}
			t.Cleanup(func() { pool.Close() })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("%s %s", dialect, version)
			models := []any{&User{}, &Channel{}, &Ability{}, &ChannelCostConfig{}, &UserChannelExclusion{}, &UserChannelExclusionCleanup{}, &SystemTaskLock{}, &ChannelDailyUsage{}, &ChannelLimitPeriodUsage{}, &PlatformChannelDailyStat{}}
			t.Cleanup(func() {
				for i := len(models) - 1; i >= 0; i-- {
					require.NoError(t, db.Migrator().DropTable(models[i]))
				}
			})
			require.NoError(t, migrateUserChannelExclusionColumn(db))
			require.NoError(t, db.AutoMigrate(models...))
			recorder.reset()
			require.NoError(t, migrateUserChannelExclusionColumn(db))
			require.NoError(t, db.AutoMigrate(models...))
			for _, ddl := range recorder.schemaMutations() {
				// Existing PostgreSQL char(32) normalization predates this feature.
				if dialect == "postgres" && strings.Contains(ddl, `ALTER COLUMN "access_token" TYPE char(32)`) {
					t.Log("pre-existing access_token normalization DDL")
					continue
				}
				require.Empty(t, ddl, "new schema must not issue repeated DDL")
			}
			// The preceding User schema differs by exactly this newly added column.
			require.NoError(t, db.Migrator().DropColumn(&User{}, "excluded_channel_ids"))
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "preserve", "password": "migration-only", "quota": 724, "aff_code": "migration"}).Error)
			if dialect == "mysql" && !strings.HasPrefix(version, "5.7.") {
				// Match the reported production user cardinality before testing DDL.
				for start := 2; start <= 65000; start += 500 {
					rows := make([]map[string]any, 0, 500)
					for id := start; id < min(start+500, 65001); id++ {
						name := fmt.Sprintf("migration-%d", id)
						rows = append(rows, map[string]any{"id": id, "username": name, "password": "migration-only", "quota": 724, "aff_code": name})
					}
					require.NoError(t, db.Table("users").CreateInBatches(rows, 500).Error)
				}
			}
			if dialect == "mysql" && strings.HasPrefix(version, "5.7.") {
				started := time.Now()
				err := migrateUserChannelExclusionColumn(db)
				require.ErrorContains(t, err, "pre-deployment")
				require.Less(t, time.Since(started), 8*time.Second)
				require.False(t, db.Migrator().HasColumn(&User{}, "excluded_channel_ids"), "startup must not fall back to a table rebuild")
				// An explicit operator migration is only simulated in this disposable DB.
				require.NoError(t, db.Exec("ALTER TABLE users ADD COLUMN excluded_channel_ids TEXT NULL").Error)
			} else {
				// A real open transaction holds the table lock needed by ADD COLUMN.
				pool.SetMaxOpenConns(3)
				blocker := db.Begin()
				require.NoError(t, blocker.Error)
				var id int
				require.NoError(t, blocker.Raw("SELECT id FROM users WHERE id = 1").Scan(&id).Error)
				started := time.Now()
				err := migrateUserChannelExclusionColumn(db)
				require.NoError(t, blocker.Rollback().Error)
				require.Error(t, err, "DDL must not wait indefinitely on a production transaction")
				require.Less(t, time.Since(started), 8*time.Second)
				require.False(t, db.Migrator().HasColumn(&User{}, "excluded_channel_ids"))
				if dialect == "sqlite" {
					pool.SetMaxOpenConns(1)
				}
			}
			var previousLockTimeout int64
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			if dialect == "mysql" {
				require.NoError(t, db.Raw("SELECT @@SESSION.lock_wait_timeout").Scan(&previousLockTimeout).Error)
			}
			migrationStarted := time.Now()
			require.NoError(t, migrateUserChannelExclusionColumn(db))
			t.Logf("bounded column migration on %s: %s", version, time.Since(migrationStarted))
			if dialect == "mysql" {
				var restored int64
				require.NoError(t, db.Raw("SELECT @@SESSION.lock_wait_timeout").Scan(&restored).Error)
				require.Equal(t, previousLockTimeout, restored, "migration must not leak session settings to pooled relay connections")
				pool.SetMaxOpenConns(10)
			}
			if dialect == "postgres" {
				pool.SetMaxOpenConns(10)
			}
			require.NoError(t, db.AutoMigrate(&User{}))
			recorder.reset()
			require.NoError(t, migrateUserChannelExclusionColumn(db))
			require.NoError(t, db.AutoMigrate(models...))
			for _, ddl := range recorder.schemaMutations() {
				if dialect == "postgres" && strings.Contains(ddl, `ALTER COLUMN "access_token" TYPE char(32)`) {
					continue
				}
				require.Empty(t, ddl, "new schema must not issue repeated DDL after upgrade")
			}
			var preserved User
			require.NoError(t, db.First(&preserved, 1).Error)
			require.Equal(t, 724, preserved.Quota)
			require.Empty(t, preserved.ExcludedChannelIDs)
			require.True(t, db.Migrator().HasIndex(&UserChannelExclusion{}, "idx_exclusion_channel_user"))
			require.True(t, db.Migrator().HasIndex(&UserChannelExclusionCleanup{}, "idx_exclusion_cleanup_due"))
			row := UserChannelExclusion{UserID: 1, ChannelID: 3}
			require.NoError(t, db.Create(&row).Error)
			require.Error(t, db.Create(&row).Error)
			// EXPLAIN the exact reverse lookup used by cleanup, with representative cardinality.
			rows := make([]UserChannelExclusion, 1000)
			for i := range rows {
				rows[i] = UserChannelExclusion{UserID: i + 10, ChannelID: i%50 + 10}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
			prefix := "EXPLAIN "
			if dialect == "sqlite" {
				prefix = "EXPLAIN QUERY PLAN "
			}
			var plan []map[string]any
			require.NoError(t, db.Raw(prefix+"SELECT user_id FROM user_channel_exclusions WHERE channel_id = ? AND user_id > ? ORDER BY user_id LIMIT 100", 12, 0).Scan(&plan).Error)
			for _, step := range plan {
				for key, value := range step {
					if bytes, ok := value.([]byte); ok {
						step[key] = string(bytes)
					}
				}
			}
			encoded, err := common.Marshal(plan)
			require.NoError(t, err)
			t.Logf("reverse lookup plan: %s", encoded)
			require.Contains(t, strings.ToLower(string(encoded)), "idx_exclusion_channel_user")
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			defer func() { DB = previousDB; common.SetMainDatabaseType(previousType); initCol() }()
			t.Run("cleanup_replay", TestUserChannelExclusionCleanupReplay)
			t.Run("priority_selection", TestUserChannelExclusionSelectionPriorities)
			t.Run("transaction_rollback", TestUserChannelExclusionTransactionRollback)
			t.Run("concurrent_save_delete", TestUserChannelExclusionConcurrentSaveDelete)
		})
	}
}
