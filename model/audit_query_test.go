package model

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAuditQueryCurrentRoleCeiling(t *testing.T) {
	dialects := map[string]gorm.Dialector{"sqlite": sqlite.Open(filepath.Join(t.TempDir(), "roles.db"))}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["postgres"] = postgres.Open(dsn)
	}
	for name, dialect := range dialects {
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(dialect, &gorm.Config{})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			require.NoError(t, db.AutoMigrate(&AuditLog{}))
			prefix := common.NewRequestId()
			rows := []AuditLog{}
			ids := []string{}
			for _, role := range []int{0, 1, 10, 100, 200} {
				id := fmt.Sprintf("%s-%d", prefix, role)
				ids = append(ids, id)
				rows = append(rows, AuditLog{EventId: id, Username: prefix, UserId: 31, ActorRole: role, CreatedAt: 100, Route: fmt.Sprintf("/role/%d", role)})
			}
			rows = append(rows, AuditLog{EventId: prefix + "-other", Username: prefix, UserId: 32, ActorRole: 1, CreatedAt: 100, Route: "/other"})
			require.NoError(t, db.Create(&rows).Error)
			t.Cleanup(func() { require.NoError(t, db.Where("username = ?", prefix).Delete(&AuditLog{}).Error) })
			for _, tc := range []struct {
				role   int
				actor  string
				routes []string
			}{
				{0, "", nil},
				{1, "", []string{"/role/1"}},
				{1, "admin", nil}, {1, "root", nil},
				{10, "", []string{"/role/1", "/role/10"}},
				{10, "root", nil}, {10, "unknown", nil},
				{100, "", []string{"/role/0", "/role/1", "/role/10", "/role/100", "/role/200"}},
				{100, "root", []string{"/role/100"}},
				{100, "unknown", []string{"/role/0", "/role/200"}},
				{100, "invalid", nil},
			} {
				t.Run(fmt.Sprintf("%d/%s", tc.role, tc.actor), func(t *testing.T) {
					f := AuditLogFilter{Username: prefix, UserId: 31, SelfView: true, ActorType: tc.actor, StartTimestamp: 99, EndTimestamp: 101}
					logs, count, err := QueryAuditLogs(db, f, 0, 20, tc.role)
					require.NoError(t, err)
					require.EqualValues(t, len(tc.routes), count)
					require.Len(t, logs, len(tc.routes))
					routes, err := QueryAuditRoutes(db, f, tc.role, "", "", 100)
					require.NoError(t, err)
					require.ElementsMatch(t, tc.routes, routes.Items)
					events, err := QueryAuditEvents(db, f, tc.role, append(ids, prefix+"-other"))
					require.NoError(t, err)
					actual := []string{}
					for _, row := range events {
						require.Equal(t, 31, row.UserId)
						actual = append(actual, row.Route)
					}
					require.ElementsMatch(t, tc.routes, actual)
				})
			}
		})
	}
}

func TestAuditEmployeeSnapshotIsRequestScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "employees.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&EmployeeProfile{}))
	require.NoError(t, db.Create(&EmployeeProfile{UserId: 71, Status: 1}).Error)
	previous := auditReaders.Swap(&auditReadPools{employees: db})
	t.Cleanup(func() { auditReaders.Store(previous) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/user/self", nil)
	require.NotNil(t, AuditEmployeeSnapshot(c, 71))
	require.True(t, *AuditEmployeeSnapshot(c, 71))
	require.NoError(t, db.Model(&EmployeeProfile{}).Where("user_id = ?", 71).Update("status", 0).Error)
	require.True(t, *AuditEmployeeSnapshot(c, 71), "retain the status observed during this request")
	require.False(t, *AuditEmployeeSnapshot(c, 72), "a different actor cannot reuse the first snapshot")
	next, _ := gin.CreateTestContext(httptest.NewRecorder())
	next.Request = httptest.NewRequest("GET", "/api/user/self", nil)
	require.False(t, *AuditEmployeeSnapshot(next, 71))
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		next.Request = httptest.NewRequest("POST", path, nil)
		require.Nil(t, AuditEmployeeSnapshot(next, 71), "relay must never access employee storage")
	}
	require.Nil(t, AuditEmployeeSnapshot(nil, 71))
}

func TestAuditEmployeeClickHouseRoundTrip(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_CH_DSN")
	if dsn == "" {
		t.Skip("isolated ClickHouse required")
	}
	require.Contains(t, dsn, "/audit_test")
	t.Setenv("LOG_SQL_DSN", dsn)
	db, err := OpenAuditSecurityDB(t.Context(), true)
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	previous, kind := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	t.Cleanup(func() { LOG_DB = previous; common.SetLogDatabaseType(kind) })
	require.NoError(t, MigrateAuditLogs())
	prefix := common.NewRequestId()
	t.Cleanup(func() {
		require.NoError(t, db.Exec("ALTER TABLE audit_logs DELETE WHERE username = ? SETTINGS mutations_sync = 1", prefix).Error)
	})
	yes, no := true, false
	for _, value := range []*bool{nil, &no, &yes} {
		RecordAuditLog(nil, AuditLog{Username: prefix, UserId: 31, ActorRole: 1, ActorIsEmployee: value})
	}
	for _, actor := range []string{"employee", "user", "unclassified_user"} {
		rows, total, err := QueryAuditLogs(db, AuditLogFilter{Username: prefix, ActorType: actor}, 0, 20, 100)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		if actor == "unclassified_user" {
			require.Nil(t, rows[0].ActorIsEmployee)
		} else {
			require.NotNil(t, rows[0].ActorIsEmployee)
			require.Equal(t, actor == "employee", *rows[0].ActorIsEmployee)
		}
	}
}
