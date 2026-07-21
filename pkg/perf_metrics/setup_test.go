package perfmetrics

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// dbReady / redisReady gate the integration tests. When the backing service is
// not reachable the corresponding tests t.Skip instead of failing, so the pure
// unit tests still run on a bare CI box (Rule 15.5).
var (
	dbReady    bool
	redisReady bool
)

func findRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func TestMain(m *testing.M) {
	if root := findRoot(); root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	setupDB()
	setupRedis()
	os.Exit(m.Run())
}

func setupDB() {
	dsn := os.Getenv("SQL_DSN")
	if dsn == "" {
		return
	}
	var db *gorm.DB
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		db, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	} else {
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	}
	if err != nil {
		return
	}
	model.InitColumnNames() // resolve commonGroupCol for the chosen dialect
	model.DB = db
	if err := db.AutoMigrate(&model.PerfMetric{}); err != nil {
		return
	}
	dbReady = true
}

func setupRedis() {
	conn := os.Getenv("REDIS_CONN_STRING")
	if conn == "" {
		return
	}
	opt, err := redis.ParseURL(conn)
	if err != nil {
		return
	}
	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return
	}
	common.RDB = client
	common.RedisEnabled = true
	redisReady = true
}

func requireDB(t *testing.T) {
	t.Helper()
	if !dbReady {
		t.Skip("SQL_DSN not set or DB unreachable; skipping perf_metrics DB integration test")
	}
	// Isolate each DB test from residue.
	model.DB.Exec("DELETE FROM perf_metrics")
}

func requireRedis(t *testing.T) {
	t.Helper()
	if !redisReady {
		t.Skip("REDIS_CONN_STRING not set or Redis unreachable; skipping perf_metrics Redis test")
	}
}
