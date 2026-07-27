package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared DB harness for the whole `model` package test suite.
//
// This is the SINGLE place that owns TestMain, DB bootstrap, and generic
// fixture factories. Per-file test files must NOT redefine TestMain or the
// helpers below; they should use the factories / id generators here and add
// only file-local fixtures (prefixed by the file name to avoid collisions).
//
// DB rules (see CLAUDE.md Rule 15.5):
//   - Real MySQL (SQL_DSN) -> model.DB ; real PG (LOG_SQL_DSN) -> model.LOG_DB.
//   - Never mock GORM. Never global-truncate a table another test might use;
//     clean up only the rows you insert (factories register t.Cleanup).
//   - Redis disabled by default; a test that needs Redis sets it locally.
// ---------------------------------------------------------------------------

// modelTestFindRoot walks up from this file to find the directory containing .env.
func modelTestFindRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func TestMain(m *testing.M) {
	root := modelTestFindRoot()
	if root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}

	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true

	sqlDSN := os.Getenv("SQL_DSN")
	if sqlDSN == "" {
		// No DB configured: run only the pure-logic tests; DB tests self-skip.
		fmt.Println("[TEST] SQL_DSN not set; DB-backed model tests will skip")
		os.Exit(m.Run())
	}

	var db *gorm.DB
	var err error

	if strings.HasPrefix(sqlDSN, "postgres://") || strings.HasPrefix(sqlDSN, "postgresql://") {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		db, err = gorm.Open(postgres.New(postgres.Config{
			DSN:                  sqlDSN,
			PreferSimpleProtocol: true,
		}), &gorm.Config{})
	} else {
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		if !strings.Contains(sqlDSN, "parseTime") {
			if strings.Contains(sqlDSN, "?") {
				sqlDSN += "&parseTime=true"
			} else {
				sqlDSN += "?parseTime=true"
			}
		}
		db, err = gorm.Open(mysql.Open(sqlDSN), &gorm.Config{})
	}
	if err != nil {
		panic("failed to open main db: " + err.Error())
	}
	DB = db

	logDSN := os.Getenv("LOG_SQL_DSN")
	if logDSN != "" {
		if strings.HasPrefix(logDSN, "postgres://") || strings.HasPrefix(logDSN, "postgresql://") {
			common.SetLogDatabaseType(common.DatabaseTypePostgreSQL)
			logDB, logErr := gorm.Open(postgres.New(postgres.Config{
				DSN:                  logDSN,
				PreferSimpleProtocol: true,
			}), &gorm.Config{})
			if logErr != nil {
				panic("failed to open log db: " + logErr.Error())
			}
			LOG_DB = logDB
		} else {
			common.SetLogDatabaseType(common.DatabaseTypeMySQL)
			logDB, logErr := gorm.Open(mysql.Open(logDSN), &gorm.Config{})
			if logErr != nil {
				panic("failed to open log db: " + logErr.Error())
			}
			LOG_DB = logDB
		}
	} else {
		common.SetLogDatabaseType(common.MainDatabaseType())
		LOG_DB = db
	}

	initCol()

	if err := db.AutoMigrate(
		&Channel{},
		&Token{},
		&User{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
		&PasskeyCredential{},
		&Option{},
		&Redemption{},
		&Task{},
		&TwoFA{},
		&TwoFABackupCode{},
		&Log{},
		&QuotaData{},
		&Ability{},
		&Midjourney{},
		&TopUp{},
		&Model{},
		&Vendor{},
		&PrefillGroup{},
		&Setup{},
		&Checkin{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
		&PerfMetric{},
		&UserExtension{},
		&EmployeeProfile{},
		&ChannelCostConfig{},
		&EmployeeCommissionLog{},
		&ConsumptionCost{},
		&PlatformChannelDailyStat{},
		&EmployeeCommissionDailyStat{},
		&EmployeeCustomerCommissionDailyStat{},
		&EmployeeCommissionResetPeriodDailyStat{},
		&BusinessStatsAppliedBatch{},
		&BusinessDailyStatsCoverage{},
		&CustomerProfile{},
		&CustomerQuotaLog{},
		&EmployeeCommissionTier{},
		&EmployeeTierLevel{},
		&EmployeeTierLog{},
		&SystemInstance{},
		&SystemTask{},
		&SystemTaskLock{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	if LOG_DB != nil && LOG_DB != DB {
		_ = LOG_DB.AutoMigrate(&Log{})
	}

	fmt.Println("[TEST] Model tests - Main DB type:", common.MainDatabaseType())
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// DB availability guard
// ---------------------------------------------------------------------------

// requireDB skips the test when no real main DB is configured.
func requireDB(t *testing.T) {
	t.Helper()
	if DB == nil {
		t.Skip("main DB not configured (SQL_DSN unset); skipping DB-backed test")
	}
}

// requireLogDB skips the test when no log DB is configured.
func requireLogDB(t *testing.T) {
	t.Helper()
	if LOG_DB == nil {
		t.Skip("log DB not configured; skipping log-DB-backed test")
	}
}

func allowTestDBCleanup() bool {
	return strings.ToLower(os.Getenv("TEST_DB_CLEANUP")) == "true"
}

// ---------------------------------------------------------------------------
// Collision-free identifier generators.
//
// Test rows live in a high id range unlikely to overlap production or other
// concurrent suites. Ids are process-unique via an atomic counter.
// ---------------------------------------------------------------------------

const testIDBase = 800_000_000

var testIDCounter int64

// nextTestID returns a process-unique id in the high test range.
func nextTestID() int {
	return testIDBase + int(atomic.AddInt64(&testIDCounter, 1))
}

// uniq returns a short unique token for names/keys/groups, based on prefix + a
// monotonically increasing counter.
func uniq(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano()%1_000_000, atomic.AddInt64(&testIDCounter, 1))
}

// uniqCode returns a unique alphanumeric code (<=32 chars) suitable for
// columns with a UNIQUE index such as users.aff_code.
func uniqCode() string {
	return fmt.Sprintf("aff%d%d", time.Now().UnixNano()%1_000_000_000, atomic.AddInt64(&testIDCounter, 1))
}

// deleteByID registers a cleanup that hard-deletes the row(s) with the given id.
// It always runs (independent of TEST_DB_CLEANUP) so each test cleans exactly
// what it created, keeping the shared DB tidy without global truncation.
func deleteByID(t *testing.T, model interface{}, id int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Delete(model, id)
		}
	})
}

// ---------------------------------------------------------------------------
// Redis harness
//
// enableRedis lazily connects the shared common.RDB client from
// REDIS_CONN_STRING and flips common.RedisEnabled on for the duration of the
// calling test (restored via t.Cleanup). Tests do not run in parallel, so the
// global toggle is safe. If Redis is unreachable the test is skipped.
// ---------------------------------------------------------------------------

var redisProbeDone bool
var redisProbeOK bool

func enableRedis(t *testing.T) {
	t.Helper()
	if !redisProbeDone {
		redisProbeDone = true
		cs := os.Getenv("REDIS_CONN_STRING")
		if cs != "" {
			if opt, err := redis.ParseURL(cs); err == nil {
				client := redis.NewClient(opt)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if _, err := client.Ping(ctx).Result(); err == nil {
					common.RDB = client
					common.RequestLogRDB = client
					redisProbeOK = true
				}
			}
		}
	}
	if !redisProbeOK {
		t.Skip("Redis not reachable (REDIS_CONN_STRING); skipping Redis-backed test")
	}
	prev := common.RedisEnabled
	common.RedisEnabled = true
	prevSync := common.SyncFrequency
	if common.SyncFrequency <= 0 {
		// Cached keys need a positive TTL, otherwise RedisHIncrBy (which only
		// acts on keys with ttl>0) becomes a no-op.
		common.SyncFrequency = 60
	}
	t.Cleanup(func() {
		common.RedisEnabled = prev
		common.SyncFrequency = prevSync
	})
}

// ---------------------------------------------------------------------------
// Generic fixture factories. Each inserts a valid minimal row, registers a
// hard-delete cleanup, and applies an optional mutator before insert.
// ---------------------------------------------------------------------------

// mkUser inserts a User with unique username/email and returns it.
func mkUser(t *testing.T, mut func(u *User)) *User {
	t.Helper()
	requireDB(t)
	id := nextTestID()
	u := &User{
		Id:       id,
		Username: uniq("u"),
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    0,
		// aff_code has a UNIQUE index; empty strings collide across users, so
		// every factory user gets a distinct code (<=32 chars).
		AffCode: uniqCode(),
	}
	if mut != nil {
		mut(u)
	}
	require.NoError(t, DB.Create(u).Error)
	deleteByID(t, &User{}, u.Id)
	return u
}

// mkToken inserts a Token for the given user and returns it.
func mkToken(t *testing.T, userID int, mut func(tk *Token)) *Token {
	t.Helper()
	requireDB(t)
	tk := &Token{
		UserId:      userID,
		Key:         strings.ReplaceAll(uniq("k"), "_", "") + "aaaaaaaaaaaaaaaaaaaa",
		Name:        uniq("tok"),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 0,
		CreatedTime: time.Now().Unix(),
	}
	if mut != nil {
		mut(tk)
	}
	require.NoError(t, DB.Create(tk).Error)
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Delete(&Token{}, tk.Id)
		}
	})
	return tk
}

// mkChannel inserts a Channel and returns it.
func mkChannel(t *testing.T, mut func(ch *Channel)) *Channel {
	t.Helper()
	requireDB(t)
	id := nextTestID()
	ch := &Channel{
		Id:          id,
		Type:        1,
		Key:         uniq("ck"),
		Name:        uniq("chan"),
		Status:      common.ChannelStatusEnabled,
		Models:      "gpt-4o",
		Group:       "default",
		CreatedTime: time.Now().Unix(),
	}
	if mut != nil {
		mut(ch)
	}
	require.NoError(t, DB.Create(ch).Error)
	deleteByID(t, &Channel{}, ch.Id)
	return ch
}
