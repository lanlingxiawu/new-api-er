package model

import (
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserSessionRefreshHashSchemaUsesChar(t *testing.T) {
	statement := &gorm.Statement{DB: DB}
	require.NoError(t, statement.Parse(&UserSession{}))
	field := statement.Schema.LookUpField("RefreshHash")
	require.NotNil(t, field)
	assert.Equal(t, "char(64)", field.TagSettings["TYPE"])
	assert.True(t, field.NotNull)
}

// These DSNs must point to disposable databases without a user_sessions table.
// The package harness uses a separate database; no existing table is dropped.
func TestUserSessionRefreshHashPreservationConfiguredDatabases(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			env := "TEST_SESSION_MYSQL_DSN"
			if dialect == "postgres" {
				env = "TEST_SESSION_POSTGRES_DSN"
			}
			dsn := os.Getenv(env)
			if dsn == "" {
				t.Skipf("%s not configured", env)
			}
			var dialector gorm.Dialector
			if dialect == "mysql" {
				dialector = mysqlMigrationDialector{*mysql.Open(dsn).(*mysql.Dialector)}
			} else {
				dialector = postgresMigrationDialector{*postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}).(*postgres.Dialector)}
			}
			recorder := &migrationSQLRecorder{}
			db, err := gorm.Open(dialector, &gorm.Config{Logger: recorder})
			require.NoError(t, err)
			connection, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = connection.Close() })
			oldDB, oldRedis := DB, common.RedisEnabled
			DB, common.RedisEnabled = db, false
			t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis })
			for _, existingType := range []string{"fresh", "char", "varchar"} {
				t.Run(existingType, func(t *testing.T) {
					testRefreshHashPreservation(t, db, recorder, dialect, existingType)
				})
			}
		})
	}
}

func testRefreshHashPreservation(t *testing.T, db *gorm.DB, recorder *migrationSQLRecorder, dialect, existingType string) {
	t.Helper()
	require.False(t, db.Migrator().HasTable(&UserSession{}), "use a separate disposable database")
	require.NoError(t, db.AutoMigrate(&UserSession{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&UserSession{})) })
	if existingType != "fresh" {
		alter := "ALTER TABLE user_sessions MODIFY COLUMN refresh_hash " + existingType + "(64) NOT NULL"
		if dialect == "postgres" {
			alter = "ALTER TABLE user_sessions ALTER COLUMN refresh_hash TYPE " + existingType + "(64)"
		}
		require.NoError(t, db.Exec(alter).Error)
	}
	now := time.Now().Unix()
	current := common.GenerateHMACWithKey([]byte("test-signing-key"), "current-secret")
	next := common.GenerateHMACWithKey([]byte("test-signing-key"), "next-secret")
	unknown := common.GenerateHMACWithKey([]byte("test-signing-key"), "unknown-secret")
	require.Len(t, current, 64)
	for _, sid := range []string{"logout", "reuse"} {
		require.NoError(t, CreateUserSession(&UserSession{
			SID: sid, UserID: 1, Version: 1, UserAuthVersion: 1,
			Status: UserSessionStatusActive, RefreshHash: current,
			LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600,
		}))
	}
	for start := 1; start <= 2; start++ {
		recorder.reset()
		require.NoError(t, db.AutoMigrate(&UserSession{}))
		require.Empty(t, recorder.schemaMutations(), "start %d must preserve existing schema", start)
	}
	columns, err := db.Migrator().ColumnTypes(&UserSession{})
	require.NoError(t, err)
	found := false
	for _, column := range columns {
		if column.Name() != "refresh_hash" {
			continue
		}
		found = true
		want := "char"
		if existingType == "varchar" {
			want = "varchar"
		} else if dialect == "postgres" {
			want = "bpchar"
		}
		assert.Equal(t, want, column.DatabaseTypeName())
		length, ok := column.Length()
		assert.True(t, ok)
		assert.EqualValues(t, 64, length)
		nullable, ok := column.Nullable()
		assert.True(t, ok)
		assert.False(t, nullable)
	}
	require.True(t, found)
	for _, sid := range []string{"logout", "reuse"} {
		var stored UserSession
		require.NoError(t, db.First(&stored, "sid = ?", sid).Error)
		require.Equal(t, current, stored.RefreshHash)
		revoked, err := RevokeUserSessionByRefreshHash(sid, unknown, "logout")
		require.NoError(t, err)
		require.False(t, revoked)
		_, err = RotateUserSessionRefresh(1, sid, unknown, next, now, 10*time.Second)
		require.ErrorIs(t, err, ErrUserSessionRefreshInvalid)
		rotated, err := RotateUserSessionRefresh(1, sid, current, next, now, 10*time.Second)
		require.NoError(t, err)
		require.Equal(t, next, rotated.RefreshHash)
		_, err = RotateUserSessionRefresh(1, sid, current, unknown, now+1, 10*time.Second)
		require.ErrorIs(t, err, ErrUserSessionRefreshRace)
		if sid == "logout" {
			revoked, err = RevokeUserSessionByRefreshHash(sid, next, "logout")
			require.NoError(t, err)
			require.True(t, revoked)
		} else {
			_, err = RotateUserSessionRefresh(1, sid, current, unknown, now+11, 10*time.Second)
			require.ErrorIs(t, err, ErrUserSessionRefreshReuse)
		}
	}
}
