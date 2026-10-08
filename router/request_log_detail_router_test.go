package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Through the real router and middleware: the request-log list needs the menu's
// view grant, a single log's full record needs view_detail on top of it, and
// only root sees credential headers unmasked. Granting view_detail alone
// implies view (the permission is normalized like view/edit).
//
// The router package has no database harness, so this uses SQLite in memory
// (Rule 15.5 fallback). Nothing here is dialect specific: users are found by
// access token and permissions are casbin rows.
func TestRequestLogDetailRequiresItsOwnGrantAndMasksCredentials(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open("file:request-log-detail-routes?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.CasbinRule{}, &model.AuthzRole{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMaster, previousRedis := common.IsMasterNode, common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	common.IsMasterNode, common.RedisEnabled = true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.IsMasterNode, common.RedisEnabled = previousMaster, previousRedis
	})
	require.NoError(t, authz.Init(db))

	// Not t.TempDir/t.Setenv: the shared request-log writer keeps its segment
	// open, which Windows refuses to delete. Re-initialising the store after
	// restoring the variable closes the writer and restores the original root.
	dir, err := os.MkdirTemp("", "request-log-detail-*")
	require.NoError(t, err)
	previousDir, hadDir := os.LookupEnv("REQUEST_LOG_DIR")
	require.NoError(t, os.Setenv("REQUEST_LOG_DIR", dir))
	t.Cleanup(func() {
		if hadDir {
			_ = os.Setenv("REQUEST_LOG_DIR", previousDir)
		} else {
			_ = os.Unsetenv("REQUEST_LOG_DIR")
		}
		model.InitRequestLogStore()
		_ = os.RemoveAll(dir)
	})
	model.InitRequestLogStore()
	require.True(t, model.RequestLogStoreReady())

	requestHeaders, err := common.Marshal(http.Header{
		"Authorization":  {"Bearer sk-detail-secret"},
		"X-Goog-Api-Key": {"AIza-detail-secret"},
		"Content-Type":   {"application/json"},
	})
	require.NoError(t, err)
	responseHeaders, err := common.Marshal(http.Header{
		"Set-Cookie":   {"session=detail-secret"},
		"Content-Type": {"application/json"},
	})
	require.NoError(t, err)
	complete := &model.RequestLog{
		CreatedAt: time.Now().Unix(), Method: http.MethodPost, Url: "/v1/chat/completions",
		RequestHeaders: string(requestHeaders), ResponseHeaders: string(responseHeaders),
		RequestBody:  `{"model":"gpt","messages":[{"role":"user","content":"hello detail"}]}`,
		ResponseBody: `{"error":{"message":"No available channel for model gemini?key=sk-body-secret"}}`,
	}
	// Headers cut at the size limit are no longer valid JSON.
	truncated := &model.RequestLog{
		CreatedAt: time.Now().Unix(), Method: http.MethodPost, Url: "/v1/chat/completions",
		RequestHeaders: `{"Authorization":["Bearer sk-cut-sec` + "\n...[truncated]",
	}
	model.RecordRequestLog(complete)
	model.RecordRequestLog(truncated)
	require.Positive(t, complete.Id)
	require.Positive(t, truncated.Id)

	users := map[string]*model.User{}
	for name, role := range map[string]int{
		"rld-list": common.RoleAdminUser, "rld-detail": common.RoleAdminUser,
		"rld-only": common.RoleAdminUser, "rld-root": common.RoleRootUser,
	} {
		token := strings.Repeat("t", 32-len(name)) + name
		user := &model.User{Username: name, Password: "unused-password", Role: role, Status: common.UserStatusEnabled, AccessToken: &token, AuthVersion: 1}
		require.NoError(t, db.Create(user).Error)
		users[name] = user
	}
	require.NoError(t, authz.SetUserPermissions(users["rld-list"].Id, authz.PermissionsMap{
		authz.ResourceAdminMenuRequestLogs: {authz.ActionView: true},
	}))
	require.NoError(t, authz.SetUserPermissions(users["rld-detail"].Id, authz.PermissionsMap{
		authz.ResourceAdminMenuRequestLogs: {authz.ActionView: true, authz.ActionViewDetail: true},
	}))
	require.NoError(t, authz.SetUserPermissions(users["rld-only"].Id, authz.PermissionsMap{
		authz.ResourceAdminMenuRequestLogs: {authz.ActionViewDetail: true},
	}))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	type detail struct {
		RequestHeaders  string `json:"request_headers"`
		ResponseHeaders string `json:"response_headers"`
		RequestBody     string `json:"request_body"`
		ResponseBody    string `json:"response_body"`
		HeadersWithheld bool   `json:"headers_withheld"`
	}
	serve := func(user, path string) (int, bool, detail) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+*users[user].AccessToken)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		var out struct {
			Success bool   `json:"success"`
			Data    detail `json:"data"`
		}
		_ = common.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Success, out.Data
	}
	detailPath := func(log *model.RequestLog) string { return "/api/request-log/" + strconv.Itoa(log.Id) }

	// The list stays open to the menu grant alone; the detail does not.
	status, success, _ := serve("rld-list", "/api/request-log/")
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, success)
	status, success, _ = serve("rld-list", detailPath(complete))
	assert.Equal(t, http.StatusForbidden, status)
	assert.False(t, success)

	for _, user := range []string{"rld-detail", "rld-only"} {
		status, success, got := serve(user, detailPath(complete))
		require.Equal(t, http.StatusOK, status, user)
		require.True(t, success, user)
		assert.Contains(t, got.RequestBody, "hello detail", user)
		for _, headers := range []string{got.RequestHeaders, got.ResponseHeaders} {
			assert.NotContains(t, headers, "detail-secret", user)
			assert.Contains(t, headers, "***", user)
			assert.Contains(t, headers, "application/json", user)
		}
		assert.False(t, got.HeadersWithheld, user)
		assert.NotContains(t, got.ResponseBody, "sk-body-secret", "credentials echoed into bodies are masked for non-root")
		assert.Contains(t, got.ResponseBody, "key=***", user)

		status, success, got = serve(user, detailPath(truncated))
		require.Equal(t, http.StatusOK, status, user)
		require.True(t, success, user)
		assert.Empty(t, got.RequestHeaders, "a cut header block cannot be masked and is withheld")
		assert.True(t, got.HeadersWithheld, user)
	}

	status, success, got := serve("rld-root", detailPath(complete))
	require.Equal(t, http.StatusOK, status)
	require.True(t, success)
	assert.Contains(t, got.RequestHeaders, "sk-detail-secret", "root sees the stored headers")
	assert.Contains(t, got.ResponseHeaders, "session=detail-secret")
	assert.Contains(t, got.ResponseBody, "sk-body-secret", "root sees the stored body")
	status, success, got = serve("rld-root", detailPath(truncated))
	require.Equal(t, http.StatusOK, status)
	require.True(t, success)
	assert.Contains(t, got.RequestHeaders, "sk-cut-sec")
	assert.False(t, got.HeadersWithheld)
}
