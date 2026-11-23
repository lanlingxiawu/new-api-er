package controller

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuditHTTPCursorsRestoreDefaultWindow(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	previous := common.SQLitePath
	common.SQLitePath = filepath.Join(t.TempDir(), "audit-cursors.db")
	t.Cleanup(func() { model.CloseAuditReadPools(); common.SQLitePath = previous })
	require.NoError(t, model.InitAuditReadPools(t.Context()))
	db, err := model.AuditReadDB(false)
	require.NoError(t, err)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.AuditLog{}))
	require.NoError(t, db.Create(&[]model.User{{Id: 1, Username: "employee", Role: 1, Status: 1}, {Id: 3, Username: "customer", InviterId: 1, Role: 1, Status: 1}}).Error)
	require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 1, Status: 1}).Error)
	end := time.Now().Unix() - 60
	for i := range 21 {
		alert := model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: end - 1, UpdatedAt: end - 1, Visibility: 100, SubjectRef: "cursor-customer", Status: "open"}
		require.NoError(t, db.Create(&alert).Error)
		require.NoError(t, db.Create(&model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: alert.Rule, RuleVersion: 2, ActorID: 3, ActorRole: 1, SubjectRef: alert.SubjectRef, TriggerAt: end - 1, TriggerEventID: fmt.Sprintf("event-%02d", i)}).Error)
		require.NoError(t, db.Create(&model.AuditLog{EventId: fmt.Sprintf("event-%02d", i), UserId: 3, ActorRole: 1, Action: "login", CreatedAt: end - 1}).Error)
	}
	for _, tc := range []struct {
		name, query string
		id, role    int
		params      gin.Params
		handler     gin.HandlerFunc
	}{
		{"self", "", 3, 1, nil, GetSelfSecurityAlerts},
		{"customer audit", "", 1, 1, gin.Params{{Key: "id", Value: "3"}}, EmployeeCustomerAuditLogs},
		{"customer risk", "", 1, 1, gin.Params{{Key: "id", Value: "3"}}, EmployeeCustomerSecurityAlerts},
		{"activity", "&view=activity&limit=20", 1, 100, nil, GetSecurityAlerts},
		{"relations", "&relation=account&limit=20", 1, 100, gin.Params{{Key: "id", Value: "1"}}, GetSecurityAlertRelations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cursor string
			// The original query predates this request. Subsequent requests omit
			// bounds, so recomputing now would invalidate the binding.
			queries := []string{fmt.Sprintf("&start=%d&end=%d", end-86400, end), "", ""}
			for index, bounds := range queries {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", tc.id)
				c.Set("role", tc.role)
				c.Set("session_id", "window-test")
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
				c.Params = tc.params
				if index == 2 {
					bounds = fmt.Sprintf("&end=%d", end+1)
				}
				c.Request = httptest.NewRequest("GET", "/?cursor="+cursor+tc.query+bounds, nil)
				tc.handler(c)
				var result struct {
					Success bool
					Data    struct {
						Items      []map[string]any
						NextCursor string `json:"next_cursor"`
					}
				}
				require.Equal(t, 200, recorder.Code)
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
				require.Equal(t, index != 2, result.Success, recorder.Body.String())
				if index == 0 {
					require.Len(t, result.Data.Items, 20)
					require.NotEmpty(t, result.Data.NextCursor)
					cursor = result.Data.NextCursor
				} else if index == 1 {
					require.Len(t, result.Data.Items, 1)
					require.Empty(t, result.Data.NextCursor)
				}
			}
		})
	}
}
