package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSelfSecurityHTTPIdentityBoundary(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	previous := common.SQLitePath
	common.SQLitePath = filepath.Join(t.TempDir(), "self-security.db")
	t.Cleanup(func() { model.CloseAuditReadPools(); common.SQLitePath = previous })
	require.NoError(t, model.InitAuditReadPools(t.Context()))
	db, err := model.AuditReadDB(false)
	require.NoError(t, err)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]model.SecurityAlert{
		{ID: 1, ActorID: 31, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: now, Visibility: 100},
		{ID: 2, ActorID: 32, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: now, Visibility: 100},
	}).Error)
	for _, role := range []int{1, 10, 100} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("id", 31)
			c.Set("role", role)
			if c.GetHeader("X-Test-Session") == "yes" {
				c.Set("session_id", "self-fixture")
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
			}
		})
		router.GET("/alerts", GetSelfSecurityAlerts)
		router.GET("/alerts/:id", GetSelfSecurityAlert)
		for _, path := range []string{"/alerts", "/alerts/1"} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			require.Equal(t, http.StatusForbidden, response.Code)
		}
		for _, tc := range []struct {
			path    string
			success bool
		}{
			{"/alerts?user_id=32&actor_id=32&role=100", true},
			{"/alerts/1?user_id=32", true}, {"/alerts/2", false}, {"/alerts/999", false},
			{"/alerts/0", false}, {"/alerts?start=bad", false},
			{"/alerts?start=1&end=" + strconv.FormatInt(now, 10), false},
		} {
			response := httptest.NewRecorder()
			request := httptest.NewRequest("GET", tc.path, nil)
			request.Header.Set("X-Test-Session", "yes")
			router.ServeHTTP(response, request)
			var result struct {
				Success bool
				Data    auditsecurity.SelfSecurityPage
			}
			require.Equal(t, 200, response.Code)
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, tc.success, result.Success, tc.path)
			if tc.success && strings.HasPrefix(tc.path, "/alerts?") {
				require.Len(t, result.Data.Items, 1)
				require.EqualValues(t, 1, result.Data.Items[0].ID)
			}
		}
	}
}

func TestSelfSecurityHTTPCursorKeepsOriginalWindow(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	previous := common.SQLitePath
	common.SQLitePath = filepath.Join(t.TempDir(), "self-cursor.db")
	t.Cleanup(func() { model.CloseAuditReadPools(); common.SQLitePath = previous })
	require.NoError(t, model.InitAuditReadPools(t.Context()))
	db, err := model.AuditReadDB(false)
	require.NoError(t, err)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	// Use an earlier window to model a later HTTP request deterministically.
	end := time.Now().Unix() - 60
	for i := range 21 {
		require.NoError(t, db.Create(&model.SecurityAlert{ID: int64(i + 1), ActorID: 31, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: end - 1, Visibility: 100}).Error)
	}
	first, err := auditsecurity.ListSelfSecurityAlerts(t.Context(), db, auditsecurity.SelfSecurityFilter{UserID: 31, Start: end - 86400, End: end})
	require.NoError(t, err)
	require.Len(t, first.Items, 20)
	require.NotEmpty(t, first.NextCursor)
	for _, tc := range []struct {
		query string
		user  int
		ok    bool
	}{
		{"", 31, true},
		{"&start=" + strconv.FormatInt(end-86400, 10), 31, true},
		{"&end=" + strconv.FormatInt(end+1, 10), 31, false},
		{"&start=0", 31, false},
		{"", 32, false},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", tc.user)
		c.Set("role", 1)
		c.Set("session_id", "self-cursor")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Request = httptest.NewRequest("GET", "/alerts?cursor="+first.NextCursor+tc.query, nil)
		GetSelfSecurityAlerts(c)
		var result struct {
			Success bool
			Data    auditsecurity.SelfSecurityPage
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(t, tc.ok, result.Success)
		if tc.ok {
			require.Len(t, result.Data.Items, 1)
			require.EqualValues(t, 1, result.Data.Items[0].ID)
			require.Empty(t, result.Data.NextCursor)
		}
	}
}
