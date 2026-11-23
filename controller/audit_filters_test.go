package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuditCursorTimeRange(t *testing.T) {
	raw, err := common.Marshal(struct{ Start, End int64 }{100, 200})
	require.NoError(t, err)
	cursor := base64.RawURLEncoding.EncodeToString(raw)
	for _, tc := range []struct {
		query      string
		start, end int64
		valid      bool
	}{
		{"cursor=" + cursor, 100, 200, true},
		{"cursor=" + cursor + "&start=101", 101, 200, true},
		{"cursor=" + cursor + "&end=201", 100, 201, true},
		{"start=100&end=200", 100, 200, true},
		{"cursor=e30&start=100&end=200", 100, 200, true}, // Old cursor, explicit bounds.
		{"cursor=broken", 0, 0, false},
		{"cursor=" + strings.Repeat("a", 3001), 0, 0, false},
		{"cursor=" + cursor + "&start=0", 0, 0, false},
		{"cursor=" + cursor + "&end=bad", 0, 0, false},
		{"cursor=" + cursor + "&end=99", 0, 0, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+tc.query, nil)
		start, end, ok := auditCursorTimeRange(c, 86400)
		require.Equal(t, tc.valid, ok, tc.query)
		if ok {
			require.Equal(t, tc.start, start)
			require.Equal(t, tc.end, end)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	before := time.Now().Unix()
	start, end, ok := auditCursorTimeRange(c, 86400)
	require.True(t, ok)
	require.GreaterOrEqual(t, end, before)
	require.LessOrEqual(t, end, time.Now().Unix())
	require.EqualValues(t, 86400, end-start)
}

// Fixtures replace DB/LOG_DB per case; the server uses dedicated pools.
func init() {
	auditReadDatabase = func(logStore bool) (*gorm.DB, error) {
		if logStore {
			return model.LOG_DB, nil
		}
		return model.DB, nil
	}
}

func TestAuditBatchVisibilityAndBounds(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "batch.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	previous := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previous })
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, db.Create(&[]model.AuditLog{
		{EventId: "own", UserId: 31, ActorRole: 10, CreatedAt: 100},
		{EventId: "other", UserId: 32, ActorRole: 10, CreatedAt: 100},
		{EventId: "root", UserId: 31, ActorRole: 100, CreatedAt: 100},
	}).Error)
	for _, tc := range []struct {
		name, ids string
		valid     bool
	}{
		{"visible subset", "own,other,root", true},
		{"limit 100", strings.Repeat("own,", 99) + "own", true},
		{"limit 101", strings.Repeat("own,", 100) + "own", false},
		{"empty", "", false}, {"oversized ID", strings.Repeat("a", 65), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/api/audit/self/routes", func(c *gin.Context) {
				c.Set("id", 31)
				c.Set("role", 10)
				rows, ok := auditBatchRecords(c, context.Background())
				require.Equal(t, tc.valid, ok)
				if ok {
					require.Len(t, rows, 1)
					require.Equal(t, "own", rows[0].EventId)
				}
			})
			engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/audit/self/routes?start_timestamp=99&end_timestamp=101&user_id=32&event_ids="+tc.ids, nil))
		})
	}
}

func TestAuditFiltersKeepLegacyTimeContract(t *testing.T) {
	for _, tc := range []struct {
		query          string
		bounded, valid bool
	}{
		{"", false, true}, {"start_timestamp=1", false, true}, {"end_timestamp=1", false, true},
		{"start_timestamp=1&end_timestamp=4000000", false, true},
		{"", true, false}, {"start_timestamp=1&end_timestamp=2678401", true, true},
		{"start_timestamp=1&end_timestamp=2678402", true, false},
		{"start_timestamp=-1", false, false}, {"start_timestamp=2&end_timestamp=1", false, false},
		{"ip=2001:db8::1&actor_type=employee&auth_method=access_token", false, true},
		{"ip=203.0.113.0/24", false, false}, {"ip=fe80::1%25eth0", false, false},
		{"actor_type=superuser", false, false}, {"auth_method=bearer", false, false},
		{"actor_type=root%7Cadmin", false, false},
		{"route_missing=true&route=/api/user/self", false, false}, {"route_missing=true", false, true},
		{"route=https://example.com", false, false}, {"route=/api/user?key=x", false, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/api/audit?"+tc.query, nil)
			c.Set("role", common.RoleAdminUser)
			_, ok := parseAuditFilter(c, tc.bounded)
			require.Equal(t, tc.valid, ok)
		})
	}
	engine := gin.New()
	engine.GET("/api/audit/self/routes", func(c *gin.Context) {
		c.Set("id", 31)
		filter, ok := parseAuditFilter(c, false)
		require.True(t, ok)
		require.Equal(t, 31, filter.UserId)
		require.Empty(t, filter.Username)
		require.True(t, filter.SelfView)
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/audit/self/routes?user_id=99&username=root", nil))
}

func TestAuditMineKeepsOwnOperationDetails(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "mine.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	previous := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previous })
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	own := model.NewAuditChanges()
	own.Add(model.AuditTarget("channel", 42, "sentinel-channel"), model.AuditFields{"status": 2}, model.AuditFields{"status": 1})
	adminChanges := model.NewAuditChanges()
	adminChanges.Add(model.AuditTarget("channel", 7, "admin-channel"), model.AuditFields{"status": 1}, model.AuditFields{"status": 2})
	require.NoError(t, db.Create(&[]model.AuditLog{
		{EventId: "own-root", UserId: 7, Username: "root-self", ActorRole: common.RoleRootUser, CreatedAt: 50, Category: model.AuditCategoryOperation, Action: "channel.status_update", Success: true, Content: "status-detail-sentinel", Other: model.AuditOther{Changes: own, Op: &model.AuditOperation{Action: "channel.status_update", Params: model.AuditFields{"id": 42, "status": 1, "changed": true}}, RootInfo: model.AuditFields{"note": "root-only-note"}}},
		{EventId: "other-root", UserId: 8, Username: "other-root", ActorRole: common.RoleRootUser, CreatedAt: 50, Category: model.AuditCategoryOperation, Action: "channel.status_update", Success: true, Content: "other-account-sentinel"},
		{EventId: "own-admin", UserId: 9, Username: "admin-self", ActorRole: common.RoleAdminUser, CreatedAt: 50, Category: model.AuditCategoryOperation, Action: "channel.status_update", Success: true, Content: "admin-detail-sentinel", Other: model.AuditOther{Changes: adminChanges, RootInfo: model.AuditFields{"note": "admin-root-secret"}}},
	}).Error)

	body := auditMineBody(t, "/api/audit?mine=true&start_timestamp=1&end_timestamp=100&p=1&page_size=20", 7, common.RoleRootUser)
	require.Contains(t, body, "status-detail-sentinel")
	require.Contains(t, body, "sentinel-channel")
	require.Contains(t, body, "root-only-note")
	require.NotContains(t, body, "other-account-sentinel")
	require.NotContains(t, body, "admin-channel")

	self := auditMineBody(t, "/api/audit/self?mine=true&start_timestamp=1&end_timestamp=100&p=1&page_size=20", 7, common.RoleRootUser)
	require.Contains(t, self, "channel.status_update")
	for _, hidden := range []string{"status-detail-sentinel", "sentinel-channel", "root-only-note", "other-account-sentinel"} {
		require.NotContains(t, self, hidden)
	}

	admin := auditMineBody(t, "/api/audit?mine=true&start_timestamp=1&end_timestamp=100&p=1&page_size=20", 9, common.RoleAdminUser)
	require.Contains(t, admin, "admin-channel")
	require.Contains(t, admin, "admin-detail-sentinel")
	require.NotContains(t, admin, "admin-root-secret")
	require.NotContains(t, admin, "sentinel-channel")

	for _, tc := range []struct {
		query string
		id    int
	}{
		{"mine=false", 7},
		{"mine=true&username=other-root", 7},
		{"mine=true", 0},
	} {
		rejected := auditMineBody(t, "/api/audit?"+tc.query+"&start_timestamp=1&end_timestamp=100", tc.id, common.RoleRootUser)
		var payload struct {
			Success bool `json:"success"`
		}
		require.NoError(t, json.Unmarshal([]byte(rejected), &payload))
		require.False(t, payload.Success, tc.query)
		require.NotContains(t, rejected, "sentinel-channel")
	}
}

func auditMineBody(t *testing.T, target string, id, role int) string {
	t.Helper()
	path := target
	if cut := strings.IndexByte(target, '?'); cut >= 0 {
		path = target[:cut]
	}
	engine := gin.New()
	engine.GET(path, func(c *gin.Context) {
		c.Set("id", id)
		c.Set("role", role)
		GetAuditLogs(c)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response.Body.String()
}
