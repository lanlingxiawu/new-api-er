package middleware

import (
	"context"
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

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Shared test harness for the whole `middleware` package suite.
//
// This is the SINGLE place that owns TestMain, DB bootstrap, gin/session
// helpers, and generic fixture factories. Per-file test files MUST NOT
// redefine TestMain or the helpers below; they reuse the factories / id
// generators here and add only file-local fixtures (prefixed by file name to
// avoid symbol collisions across the package's single test binary).
//
// Design (mirrors docs/design/testing/model.md, Rule 15.5):
//   - Real MySQL (SQL_DSN) -> model.DB ; real PG (LOG_SQL_DSN) -> model.LOG_DB.
//   - Never mock GORM. Never global-truncate a table another test might use;
//     each factory registers a row-scoped t.Cleanup hard-delete.
//   - Redis disabled by default; a test enables what it needs via enableRedis.
//   - Middleware are exercised via gin.CreateTestContext or a real gin.Engine
//     (for session-based auth) + httptest recorder (Rule 15 / Rule 11). We
//     never call real upstream AI / payment gateways (Rule 15.4).
// ---------------------------------------------------------------------------

func middlewareTestFindRoot() string {
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

	// Initialize the i18n bundle so middleware that call i18n.T / TranslateMessage
	// resolve keys instead of panicking on a nil localizer.
	if err := i18n.Init(); err != nil {
		panic("failed to init i18n: " + err.Error())
	}

	root := middlewareTestFindRoot()
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
		fmt.Println("[TEST] SQL_DSN not set; DB-backed middleware tests will skip")
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
		&model.Task{},
	); err != nil {
		panic("failed to migrate main db: " + err.Error())
	}
	if model.LOG_DB != nil && model.LOG_DB != db {
		_ = model.LOG_DB.AutoMigrate(&model.Log{}, &model.RequestLog{})
	} else {
		_ = db.AutoMigrate(&model.Log{}, &model.RequestLog{})
	}

	fmt.Println("[TEST] Middleware tests - Main DB type:", common.MainDatabaseType())
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

// ---------------------------------------------------------------------------
// Collision-free identifier generators (high id range, process-unique).
// ---------------------------------------------------------------------------

const testIDBase = 830_000_000

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

// uniqKey returns a 32-char alphanumeric token key/access-token (no dashes, so
// the auth `strings.Split(key, "-")[0]` keeps the whole value).
func uniqKey() string {
	raw := strings.ReplaceAll(uniq("k"), "_", "")
	for len(raw) < 32 {
		raw += "0"
	}
	return raw[:32]
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
// gin helpers
// ---------------------------------------------------------------------------

// newCtx builds a bare gin context + recorder with a request for the given
// method/target (no session middleware). Suitable for middleware that only
// read headers / context keys.
func newCtx(method, target string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	if body != "" {
		ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		ctx.Request = httptest.NewRequest(method, target, nil)
	}
	return ctx, rec
}

// sessionSecret keeps the cookie store key stable across the suite.
var sessionSecret = []byte("middleware-test-secret")

// newSessionRouter returns a fresh gin.Engine wired with the cookie session
// store, plus a /login route that stamps the provided session values so
// subsequent authenticated requests carry a valid session cookie.
func newSessionRouter() *gin.Engine {
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore(sessionSecret)))
	return r
}

// loginSession hits a login route on the router that writes the given session
// values and returns the resulting cookies.
func loginSession(t *testing.T, r *gin.Engine, values map[string]interface{}) []*http.Cookie {
	t.Helper()
	r.GET("/__login", func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range values {
			s.Set(k, v)
		}
		if err := s.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__login", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	return rec.Result().Cookies()
}

// ---------------------------------------------------------------------------
// Generic fixture factories. Each inserts a minimal valid row and registers a
// row-scoped hard-delete cleanup.
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
		Key:         uniqKey(),
		Name:        uniq("tok"),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 0,
		CreatedTime: time.Now().Unix(),
		Group:       "",
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
