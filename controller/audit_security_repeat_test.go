package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRepeatOperationQuotaAuditCapturesVerifiedCredential(t *testing.T) {
	db := setupManageUserTestDB(t)
	logDB := model.LOG_DB
	actor, _ := initializeAccessTokenAudit(t, db)
	model.LOG_DB = logDB
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{
		authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionUsersQuota: true},
	}))
	user := model.User{Username: "repeat-quota-target", Role: common.RoleCommonUser, Quota: 1000, AffCode: "repeat-quota-target"}
	require.NoError(t, db.Create(&user).Error)
	raw, _ := createScopedAccessToken(t, actor.Id, common.GetTimestamp()+3600, "user:quota_write")
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	router.POST("/api/user/manage", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuUsersView), ManageUser)
	request := httptest.NewRequest("POST", "/api/user/manage", strings.NewReader(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":100}`, user.Id)))
	request.RemoteAddr = "8.8.8.8:1234"
	request.Header.Set("User-Agent", "quota-audit-test")
	request.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	require.Contains(t, rec.Body.String(), `"success":true`)
	var row model.AuditLog
	require.NoError(t, model.LOG_DB.Where("user_id = ? AND action = ?", actor.Id, "user.quota_add").Take(&row).Error)
	ref := model.AccessTokenFingerprint(raw)
	require.Equal(t, ref, row.TokenRef)
	event := auditsecurity.Normalize(row)
	var facts auditsecurity.Facts
	require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
	require.Equal(t, common.RoleCommonUser, facts.TargetRole)
	require.EqualValues(t, 100, *facts.To-*facts.From)
	require.NotEmpty(t, facts.ClientRef)
	require.Equal(t, "pat", facts.Identity.CredentialKind)
	require.Equal(t, ref, facts.Identity.CredentialRef)
	require.NotEmpty(t, facts.Identity.CredentialGrant)
	require.EqualValues(t, actor.Id, facts.Identity.ActorID)
	rows, _, err := model.GetAuditLogs(model.AuditLogFilter{RequestId: row.RequestId}, 0, 20, common.RoleRootUser)
	require.NoError(t, err)
	public, err := common.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(public), "credential_ref")
	require.NotContains(t, string(public), "credential_grant")
	require.NotContains(t, string(public), raw)
}

func TestRepeatOperationConfirmationRequiresSessionAndValidSnapshot(t *testing.T) {
	for _, body := range []string{
		`{"version":3,"status":"resolved","observed_occurrences":0}`,
		`{"version":3,"status":"resolved","observed_occurrences":4}`,
		`{"version":3,"status":"open","observed_occurrences":1}`,
		`{"version":3,"status":"resolved","observed_occurrences":1,"note":"review"}`,
		`{"version":3,"status":"resolved","observed_occurrences":1,"assignee_id":0}`,
		`{"version":3,"status":"resolved","observed_occurrences":1,"assignee_ids":[]}`,
		`{"version":3,"status":"resolved","observed_occurrences":1,"conclusion":"authorized"}`,
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("id", 1)
		c.Set("role", 100)
		c.Set("session_id", "test-session")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Params = gin.Params{{Key: "id", Value: "7"}}
		c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/7/actions", strings.NewReader(body))
		ActOnSecurityAlert(c)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"success":false`)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("id", 1)
	c.Set("role", 100)
	c.Set("use_access_token", true)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/7/actions", strings.NewReader(`{"version":3,"status":"resolved","observed_occurrences":1}`))
	ActOnSecurityAlert(c)
	require.Equal(t, http.StatusForbidden, rec.Code, "a calling token cannot confirm its own operations")
}
