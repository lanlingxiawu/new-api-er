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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmployeeCustomerAuditHTTPBoundary(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	previous := common.SQLitePath
	common.SQLitePath = filepath.Join(t.TempDir(), "employee-customer.db")
	t.Cleanup(func() { model.CloseAuditReadPools(); common.SQLitePath = previous })
	require.NoError(t, model.InitAuditReadPools(t.Context()))
	db, err := model.AuditReadDB(false)
	require.NoError(t, err)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.AuditLog{}))
	require.NoError(t, db.Create(&[]model.User{{Id: 1, Username: "employee", Status: 1}, {Id: 3, Username: "customer", InviterId: 1, Status: 1}, {Id: 4, Username: "foreign", Status: 1}}).Error)
	require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 1, Status: 1}).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.AuditLog{EventId: "owned-audit", UserId: 3, ActorRole: 1, Action: "login", CreatedAt: now, Ip: "192.0.2.1", RequestId: "customer-request", Content: "private-content", Route: "/internal", TokenRef: "private-fingerprint", Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"subject_ref": "private-subject"}}}}).Error)
	require.NoError(t, db.Create(&model.SecurityAlert{ID: 7, Rule: "session_reuse", RuleVersion: 2, ActorID: 3, ActorRole: 1, Visibility: 100, Status: "open", Version: 1, LastSeen: now}).Error)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("role", 1)
		if c.GetHeader("X-Test-Session") == "yes" {
			c.Set("session_id", "employee-test-session")
			c.Set("auth_version", int64(1))
			c.Set("session_version", int64(1))
		}
	})
	router.GET("/:id/audit", EmployeeCustomerAuditLogs)
	router.GET("/audit", EmployeeCustomersAuditLogs)
	router.GET("/security/alerts", EmployeeCustomersSecurityAlerts)
	router.GET("/security/pending-count", EmployeeCustomersSecurityPendingCount)
	router.GET("/:id/security/alerts", EmployeeCustomerSecurityAlerts)
	router.GET("/:id/security/alerts/:alert_id", EmployeeCustomerSecurityAlert)
	router.POST("/:id/security/alerts/:alert_id/actions", EmployeeCustomerSecurityAction)
	for _, tc := range []struct {
		path, method, body string
		session            bool
		code               int
		success            bool
	}{
		{"/audit", "GET", "", false, 403, false},
		{"/security/alerts", "GET", "", false, 403, false},
		{"/security/pending-count", "GET", "", false, 403, false},
		{"/security/pending-count", "GET", "", true, 200, true},
		{"/security/pending-count?customer_id=4", "GET", "", true, 200, false},
		{"/security/alerts?view=unresolved", "GET", "", true, 200, true},
		{"/security/alerts?view=unresolved&start=1", "GET", "", true, 200, false},
		{"/security/alerts?view=unresolved&unknown=1", "GET", "", true, 200, false},
		{"/security/alerts?view=unresolved&status=resolved", "GET", "", true, 200, false},
		{"/security/alerts?view=handled", "GET", "", true, 200, true},
		{"/security/alerts?view=unknown", "GET", "", true, 200, false},
		{"/3/security/alerts?view=unresolved", "GET", "", true, 200, true},
		{"/4/security/alerts?view=unresolved", "GET", "", true, 403, false},
		{"/audit?view=handled", "GET", "", true, 200, false},
		{"/3/audit?view=unresolved", "GET", "", true, 200, false},
		{"/audit", "GET", "", true, 200, true},
		{"/audit?user_id=4&employee_id=99&actor_role=100", "GET", "", true, 200, true},
		{"/audit?actor_type=root", "GET", "", true, 200, false},
		{"/audit?ip=invalid", "GET", "", true, 200, false},
		{"/audit?cursor=invalid", "GET", "", true, 200, false},
		{"/security/alerts", "GET", "", true, 200, true},
		{"/security/alerts?request_id=customer-request", "GET", "", true, 200, false},
		{"/3/audit", "GET", "", false, 403, false},
		{"/3/security/alerts", "GET", "", false, 403, false},
		{"/3/security/alerts/7", "GET", "", false, 403, false},
		{"/3/security/alerts/7/actions", "POST", `{"version":1,"status":"resolved"}`, false, 403, false},
		{"/4/audit", "GET", "", true, 403, false},
		{"/3/audit?user_id=4&actor_role=100&self_view=false", "GET", "", true, 200, true},
		{"/3/audit?ip=192.0.2.1&request_id=customer-request", "GET", "", true, 200, true},
		{"/3/audit?route=/internal", "GET", "", true, 200, false},
		{"/3/audit?actor_type=root", "GET", "", true, 200, false},
		{"/3/security/alerts?ip=192.0.2.1", "GET", "", true, 200, false},
		{"/3/security/alerts?request_id=customer-request", "GET", "", true, 200, false},
		{"/3/audit?start=1&end=" + strconv.FormatInt(now, 10), "GET", "", true, 200, false},
		{"/3/security/alerts/7", "GET", "", true, 200, true},
		{"/3/security/alerts/7/actions", "POST", `{"version":1,"status":"resolved","assignee_id":99}`, true, 200, false},
		{"/3/security/alerts/7/actions", "POST", `{"version":1,"status":"deleted"}`, true, 200, false},
		{"/3/security/alerts/7/actions", "POST", `{"version":1,"status":"resolved","note":"Customer confirmed"}`, true, 200, true},
		{"/3/security/alerts/7/actions", "POST", `{"version":1,"status":"open"}`, true, 200, false},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.session {
			request.Header.Set("X-Test-Session", "yes")
		}
		router.ServeHTTP(response, request)
		require.Equal(t, tc.code, response.Code, tc.path)
		if tc.code == http.StatusOK {
			var result struct{ Success bool }
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
			require.Equal(t, tc.success, result.Success, "%s %s", tc.path, response.Body.String())
			if tc.success && (strings.HasPrefix(tc.path, "/3/audit") || strings.HasPrefix(tc.path, "/audit")) {
				require.Contains(t, response.Body.String(), "owned-audit")
				for _, forbidden := range []string{`"other"`, `"actor_role"`, `"user_id"`, `"route"`, `"token_ref"`, "private-"} {
					require.NotContains(t, response.Body.String(), forbidden)
				}
				if strings.HasPrefix(tc.path, "/audit") {
					require.Contains(t, response.Body.String(), `"customer":{"id":3,"username":"customer"`)
				}
			}
		}
	}
}
