package controller

// 上游测试 helper 的补丁层。
//
// 上游把 setupModelListControllerTestDB / initModelListColumnNames 定义在
// controller/model_list_test.go 里，而那个文件依赖的其余部分与本仓库的
// 测试重建冲突，因此没有携带。被保留的上游测试仍然引用这两个 helper，
// 按 CLAUDE.md Rule 6 的约定，把 helper 放到本文件，而不是去改上游测试
// ——改了它们，下次同步必然冲突。
//
// 两个 helper 与上游逐字节一致，只是换了个文件。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelListControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	// 本包其余测试共用 harness 建立的 model.DB。上游 helper 会把 model.DB 换成
	// 自建的 SQLite 并在 cleanup 里关闭它，若不还原，后续测试拿到的就是已关闭的
	// 句柄（表现为 "sql: database is closed"）。这里前后成对保存/还原。
	prevDB, prevLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
	})

	initModelListColumnNames(t)

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func initModelListColumnNames(t *testing.T) {
	t.Helper()

	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	defer func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	}()

	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s_init?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	require.NoError(t, model.InitDB())
	if model.DB != nil {
		sqlDB, err := model.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
}

// ---------------------------------------------------------------------------
// controller/token_test.go 的助手。
//
// 与 model_list 同一情况：上游把它们放在 token_test.go 里，而那个文件的其余部分
// （跨 MySQL/PostgreSQL 的迁移兼容测试）会替换 model.DB 并在 cleanup 里关闭，
// 与本仓 harness 的共享连接冲突，因此没有携带；token_auto_groups_test.go 仍要
// 用这几个助手，按 Rule 6 放在这里而不是去改上游测试。
//
// 与上游的唯一差异：openTokenControllerTestDB 前后成对保存/还原 model.DB 和
// model.LOG_DB。上游版本换掉全局句柄后不还原，后续用 harness 连接的测试会拿到
// 已关闭的句柄（表现为 "sql: database is closed"）。
// ---------------------------------------------------------------------------

type tokenAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func openTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	prevDB, prevLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
	})

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func migrateTokenControllerTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.AutoMigrate(&model.Token{}))
}

func setupTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTokenControllerTestDB(t)
	migrateTokenControllerTestDB(t, db)
	return db
}

func seedToken(t *testing.T, db *gorm.DB, userID int, name string, rawKey string) *model.Token {
	t.Helper()

	token := &model.Token{
		UserId:         userID,
		Name:           name,
		Key:            rawKey,
		Status:         common.TokenStatusEnabled,
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
		Group:          "default",
	}
	require.NoError(t, db.Create(token).Error)
	return token
}

func newAuthenticatedContext(t *testing.T, method string, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var requestBody *bytes.Reader
	if body != nil {
		payload, err := common.Marshal(body)
		require.NoError(t, err)
		requestBody = bytes.NewReader(payload)
	} else {
		requestBody = bytes.NewReader(nil)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, requestBody)
	if body != nil {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Set("id", userID)
	return ctx, recorder
}

func decodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) tokenAPIResponse {
	t.Helper()

	var response tokenAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

// ---------------------------------------------------------------------------
// controller/model_list_test.go 的 withTieredBillingConfig。
//
// relay_task_plugin_test.go 仍要用它；与上游逐字节一致。
// ---------------------------------------------------------------------------

func withTieredBillingConfig(t *testing.T, modes map[string]string, exprs map[string]string) {
	t.Helper()

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		model.InvalidatePricingCache()
	})

	modeBytes, err := common.Marshal(modes)
	require.NoError(t, err)
	exprBytes, err := common.Marshal(exprs)
	require.NoError(t, err)

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(modeBytes),
		"billing_setting.billing_expr": string(exprBytes),
	}))
	model.InvalidatePricingCache()
}

// closeDBHandlesOnCleanup closes database pools opened inside a test (for example by
// model.InitDB against a t.TempDir() SQLite file). Windows cannot remove an open
// SQLite file, so TempDir cleanup fails the test unless these pools are closed first.
func closeDBHandlesOnCleanup(t *testing.T, handles ...*gorm.DB) {
	t.Helper()
	t.Cleanup(func() {
		for _, handle := range handles {
			if handle == nil {
				continue
			}
			if connection, err := handle.DB(); err == nil {
				_ = connection.Close()
			}
		}
	})
}

// mutatePasskeySettings applies a change to the passkey settings through the
// fork's snapshot API. Upstream tests write through GetPasskeySettings(), but the
// fork returns an immutable published snapshot: such writes are lost on the next
// republish (e.g. any option update), so tests must replace the settings instead.
func mutatePasskeySettings(mutate func(*system_setting.PasskeySettings)) {
	settings := *system_setting.GetPasskeySettings()
	mutate(&settings)
	system_setting.ReplacePasskeySettings(settings)
}

// enableRelayLogPipelineForTest starts the fork's asynchronous relay-log pipeline.
// The fork writes consume/error logs (and their accounting continuations) through
// a bounded buffer that drops events until the pipeline is started; upstream tests
// assume synchronous log rows. Starting is process-wide and idempotent.
func enableRelayLogPipelineForTest(t *testing.T) {
	t.Helper()
	model.StartRelayLogFlushLoop()
}

// drainRelayLogsForTest synchronously persists buffered relay logs and runs their
// accounting continuations so assertions observe the rows immediately.
func drainRelayLogsForTest(t *testing.T) {
	t.Helper()
	model.DrainRelayLogsSync(5 * time.Second)
}
