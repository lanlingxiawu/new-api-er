package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Shared test harness for the whole `controller` package suite.
//
// This is the SINGLE place that owns TestMain, DB bootstrap, gin context
// helpers, and generic fixture factories. Per-file test files MUST NOT
// redefine TestMain or the helpers below. They reuse the factories / id
// generators here and add only file-local fixtures (prefixed by file name to
// avoid symbol collisions across the package's single test binary).
//
// Design (mirrors docs/design/testing/model.md, Rule 15.5):
//   - Real MySQL (SQL_DSN) -> model.DB ; real PG (LOG_SQL_DSN) -> model.LOG_DB.
//   - Never mock GORM. Never global-truncate a table another test might use;
//     each factory registers a row-scoped t.Cleanup hard-delete.
//   - Redis + batch update disabled by default; a test enables what it needs.
//   - Controller handlers are exercised via gin.CreateTestContext + a recorder
//     (Rule 15 / Rule 11). We never spin the real router or call real upstream
//     AI / payment gateways (Rule 15.4) -- httptest mocks only.
// ---------------------------------------------------------------------------

func controllerTestFindRoot() string {
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
	gin.SetMode(gin.TestMode)

	// Initialize the i18n bundle so handlers that call i18n.T / ApiErrorI18n
	// resolve keys instead of panicking on a nil localizer.
	if err := i18n.Init(); err != nil {
		panic("failed to init i18n: " + err.Error())
	}

	root := controllerTestFindRoot()
	if root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}

	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}

	sqlDSN := os.Getenv("SQL_DSN")
	if sqlDSN == "" {
		fmt.Println("[TEST] SQL_DSN not set; DB-backed controller tests will skip")
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
	model.DB = db

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
			model.LOG_DB = logDB
		} else {
			common.SetLogDatabaseType(common.DatabaseTypeMySQL)
			logDB, logErr := gorm.Open(mysql.Open(logDSN), &gorm.Config{})
			if logErr != nil {
				panic("failed to open log db: " + logErr.Error())
			}
			model.LOG_DB = logDB
		}
	} else {
		common.SetLogDatabaseType(common.MainDatabaseType())
		model.LOG_DB = db
	}

	model.InitColumnNames()

	if err := db.AutoMigrate(
		&model.Channel{},
		&model.Token{},
		&model.User{},
		&model.Option{},
		&model.Redemption{},
		&model.Ability{},
		&model.TopUp{},
		&model.QuotaData{},
		&model.Model{},
		&model.Vendor{},
		&model.UserExtension{},
	); err != nil {
		panic("failed to migrate main db: " + err.Error())
	}
	if model.LOG_DB != nil && model.LOG_DB != db {
		_ = model.LOG_DB.AutoMigrate(&model.Log{})
	} else {
		_ = db.AutoMigrate(&model.Log{})
	}

	fmt.Println("[TEST] Controller tests - Main DB type:", common.MainDatabaseType())
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// DB availability guards
// ---------------------------------------------------------------------------

func requireDB(t *testing.T) {
	t.Helper()
	if model.DB == nil {
		t.Skip("main DB not configured (SQL_DSN unset); skipping DB-backed test")
	}
}

func requireLogDB(t *testing.T) {
	t.Helper()
	if model.LOG_DB == nil {
		t.Skip("log DB not configured; skipping log-DB-backed test")
	}
}

// ---------------------------------------------------------------------------
// Collision-free identifier generators (high id range, process-unique).
// ---------------------------------------------------------------------------

const testIDBase = 820_000_000

var testIDCounter int64

func nextTestID() int {
	return testIDBase + int(atomic.AddInt64(&testIDCounter, 1))
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano()%1_000_000, atomic.AddInt64(&testIDCounter, 1))
}

func uniqCode() string {
	return fmt.Sprintf("aff%d%d", time.Now().UnixNano()%1_000_000_000, atomic.AddInt64(&testIDCounter, 1))
}

func deleteByID(t *testing.T, m interface{}, id int) {
	t.Helper()
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(m, id)
		}
	})
}

// ---------------------------------------------------------------------------
// Redis harness (lazily connects the shared client; skips if unreachable).
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
		common.SyncFrequency = 60
	}
	t.Cleanup(func() {
		common.RedisEnabled = prev
		common.SyncFrequency = prevSync
	})
}

// ---------------------------------------------------------------------------
// gin context helpers
// ---------------------------------------------------------------------------

// newCtx builds a gin context + recorder for the given request. `body`, when
// non-nil, is JSON-marshalled and attached with a JSON content type.
func newCtx(t *testing.T, method, target string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := common.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, target, reader)
	if body != nil {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	return ctx, rec
}

// newRawCtx builds a gin context whose body is the exact bytes provided (used
// for malformed-JSON parse-error tests).
func newRawCtx(t *testing.T, method, target, rawBody string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, target, bytes.NewBufferString(rawBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, rec
}

// asUser marks the context as an authenticated common user with the given id.
func asUser(ctx *gin.Context, id int) *gin.Context {
	ctx.Set("id", id)
	ctx.Set("role", common.RoleCommonUser)
	return ctx
}

// asAdmin marks the context as an admin user with the given id.
func asAdmin(ctx *gin.Context, id int) *gin.Context {
	ctx.Set("id", id)
	ctx.Set("role", common.RoleAdminUser)
	return ctx
}

// asRoot marks the context as a root user with the given id.
func asRoot(ctx *gin.Context, id int) *gin.Context {
	ctx.Set("id", id)
	ctx.Set("role", common.RoleRootUser)
	return ctx
}

// ---------------------------------------------------------------------------
// Response envelope (Rule 9: HTTP 200 {success, message, data}).
// ---------------------------------------------------------------------------

type apiResp struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// decodeResp decodes the standard envelope and asserts HTTP 200.
func decodeResp(t *testing.T, rec *httptest.ResponseRecorder) apiResp {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var out apiResp
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

// ---------------------------------------------------------------------------
// Generic fixture factories. Each inserts a minimal valid row and registers a
// row-scoped hard-delete cleanup (see model harness for the same pattern).
// ---------------------------------------------------------------------------

func mkUser(t *testing.T, mut func(u *model.User)) *model.User {
	t.Helper()
	requireDB(t)
	id := nextTestID()
	u := &model.User{
		Id:       id,
		Username: uniq("u"),
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    0,
		AffCode:  uniqCode(),
	}
	if mut != nil {
		mut(u)
	}
	require.NoError(t, model.DB.Create(u).Error)
	deleteByID(t, &model.User{}, u.Id)
	return u
}

func mkToken(t *testing.T, userID int, mut func(tk *model.Token)) *model.Token {
	t.Helper()
	requireDB(t)
	tk := &model.Token{
		UserId:      userID,
		Key:         strings.ReplaceAll(uniq("k"), "_", "") + "aaaaaaaaaaaaaaaaaaaa",
		Name:        uniq("tok"),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 0,
		CreatedTime: time.Now().Unix(),
		Group:       "default",
	}
	if mut != nil {
		mut(tk)
	}
	require.NoError(t, model.DB.Create(tk).Error)
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.Token{}, tk.Id)
		}
	})
	return tk
}

// withPaymentComplianceConfirmed flips the payment-compliance gate on for the
// duration of the test (restored via t.Cleanup). Several admin endpoints
// (redemption creation, payment webhook availability) short-circuit when the
// operator has not confirmed the compliance terms.
func withPaymentComplianceConfirmed(t *testing.T) {
	t.Helper()
	ps := operation_setting.GetPaymentSetting()
	prevConfirmed := ps.ComplianceConfirmed
	prevVersion := ps.ComplianceTermsVersion
	ps.ComplianceConfirmed = true
	ps.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		ps.ComplianceConfirmed = prevConfirmed
		ps.ComplianceTermsVersion = prevVersion
	})
}

func mkChannel(t *testing.T, mut func(ch *model.Channel)) *model.Channel {
	t.Helper()
	requireDB(t)
	id := nextTestID()
	ch := &model.Channel{
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
	require.NoError(t, model.DB.Create(ch).Error)
	deleteByID(t, &model.Channel{}, ch.Id)
	return ch
}
