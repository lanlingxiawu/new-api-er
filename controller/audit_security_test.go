package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecurityAlertQueryBoundaries(t *testing.T) {
	cases := []struct {
		query string
		valid bool
	}{
		{"", true},
		{"?start=1&end=2678401", true},
		{"?start=1&end=2678402", false},
		{"?limit=0", false},
		{"?limit=1", true},
		{"?limit=100", true},
		{"?limit=101", false},
		{"?before=-1", false},
		{"?status=deleted", false},
		{"?resource=users", false},
		{"?start=not-a-time", false},
		{"?view=recent_hits&limit=20", true},
		{"?view=recent_hits&limit=50", false},
		{"?view=recent_hits&before=0", false},
		{"?view=recent_hits", true},
		{"?view=activity", true},
		{"?view=activity&before=0", false},
		{"?view=activity&cursor=abc", false},
		{"?view=unresolved", true},
		{"?view=handled", true},
		{"?view=handled&status=resolved", true},
		{"?view=handled&status=false_positive", true},
		{"?view=handled&status=open", false},
		{"?view=handled&status=investigating", false},
		{"?view=archived&status=resolved", true},
		{"?view=archived&status=open", false},
	}
	for _, tc := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/audit/security/alerts"+tc.query, nil)
		_, ok := securityAlertFilter(c)
		require.Equal(t, tc.valid, ok, tc.query)
	}
}

func TestSecurityAlertRejectsInvalidMultipleAssigneesBeforeStorage(t *testing.T) {
	for _, fields := range []string{`"assignee_ids":[0]`, `"assignee_ids":[-1]`, `"assignee_ids":[2,2]`, `"assignee_ids":[1,2,3,4,5,6,7,8,9,10,11]`, `"assignee_ids":[2],"assignee_id":2`, `"assignee_ids":"2"`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", 1)
		c.Set("role", 100)
		c.Set("session_id", "test-session")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/audit/security/alerts/1/actions", strings.NewReader(`{"version":1,`+fields+`}`))
		ActOnSecurityAlert(c)
		require.Contains(t, recorder.Body.String(), `"success":false`)
		require.NotContains(t, recorder.Body.String(), "unavailable")
	}
}

func TestSecuritySettingsRejectNonRootAndAccessTokens(t *testing.T) {
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("role", role)
		c.Set("use_access_token", true)
		c.Request = httptest.NewRequest("PUT", "/api/option/group", strings.NewReader(`{"module":"audit_security_setting","values":{"enabled":"true"}}`))
		UpdateOptionGroup(c)
		if role == common.RoleRootUser {
			require.Equal(t, http.StatusForbidden, recorder.Code)
		} else {
			require.Contains(t, recorder.Body.String(), `"success":false`)
		}
	}
}

func TestSecurityAccountResponseRequiresDashboardSession(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("role", role)
		c.Set("use_access_token", true)
		c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/1/responses", strings.NewReader(`{"version":1,"action":"disable_account","request_id":"fixture-request"}`))
		RespondToSecurityAlert(c)
		require.Equal(t, http.StatusForbidden, recorder.Code)
	}
}

func TestSecurityAccountResponseMarksPreflightRejection(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	c.Set("session_id", "response-rejection-fixture")
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/audit/security/alerts/1/responses", strings.NewReader(`{"version":0,"action":"disable_account","request_id":"fixture-request"}`))
	RespondToSecurityAlert(c)
	var result struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	require.False(t, result.Success)
	require.Equal(t, "AUDIT_SECURITY_RESPONSE_REJECTED", result.Code)
}

func TestSecuritySettingsProofRoundTrip(t *testing.T) {
	operator, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.Model(operator).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.PublishUserAuthCache(operator.Id))
	previousPassword := common.PasswordLoginEnabled
	previousThreshold := operation_setting.GetAuditSecuritySetting().FailureThreshold
	const optionKey = "audit_security_setting.failure_threshold"
	common.OptionMapRWMutex.RLock()
	previousOption, existed := common.OptionMap[optionKey]
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPassword
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"failure_threshold": strconv.Itoa(previousThreshold)}))
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[optionKey] = previousOption
		} else {
			delete(common.OptionMap, optionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
	common.PasswordLoginEnabled = true
	proof, err := service.VerifySecurityInput(identity, service.VerificationInput{
		Scope: service.VerificationScopeAuditSecuritySettings, Method: service.VerificationMethodPassword,
		Password: "enrollment-password", Context: []byte(`{"values":{"failure_threshold":"6"}}`),
	})
	require.NoError(t, err)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", identity.UserID)
		c.Set("role", common.RoleRootUser)
		c.Set("session_id", identity.SessionID)
		c.Set("auth_version", identity.UserAuthVersion)
		c.Set("session_version", identity.SessionVersion)
		c.Next()
	})
	router.PUT("/api/option/group", middleware.RequireSystemSettingsScope(authz.ActionEdit), UpdateOptionGroup)
	for _, tc := range []struct {
		name, value, token string
		success            bool
	}{
		{"missing proof", "6", "", false},
		{"changed values", "7", proof.ProofToken, false},
		{"bound values", "6", proof.ProofToken, true},
		{"replayed proof", "6", proof.ProofToken, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/option/group", strings.NewReader(`{"scope":"security.audit-monitor","module":"audit_security_setting","values":{"failure_threshold":"`+tc.value+`"}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Security-Proof", tc.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
			require.Equal(t, tc.success, result.Success, response.Body.String())
		})
	}
	require.Equal(t, 6, operation_setting.GetAuditSecuritySetting().FailureThreshold)
	var persisted model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: optionKey}).Take(&persisted).Error)
	require.Equal(t, "6", persisted.Value)

	common.PasswordLoginEnabled = false
	requirements, err := service.GetVerificationRequirements(identity, service.VerificationScopeAuditSecuritySettings)
	require.NoError(t, err)
	require.Len(t, requirements.Methods, 1)
	require.False(t, requirements.Methods[0].Available, "disabled password authentication must not authorize monitoring changes")
}

func TestChannelSecurityChangesContainOnlyFieldNames(t *testing.T) {
	before := &model.Channel{Key: "old-secret", Name: "old", Status: 1}
	after := *before
	after.Name = "new"
	require.Empty(t, ChannelSecurityChanges(before, &after))
	after.Key = "new-secret"
	after.Status = 2
	header := "Authorization: secret"
	after.HeaderOverride = &header
	require.Equal(t, []string{"header_override", "key", "status"}, ChannelSecurityChanges(before, &after))
}

func TestMultiKeyFailureAuditIsNotSuccessful(t *testing.T) {
	if model.LOG_DB == nil {
		t.Skip("isolated log database required")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/channel/multi_key", nil)
	c.Set("id", 984129)
	c.Set("role", 10)
	c.Set(common.RequestIdKey, "security-test-failed-multi-key")
	t.Cleanup(func() {
		model.LOG_DB.Where("request_id = ?", "security-test-failed-multi-key").Delete(&model.AuditLog{})
	})
	writeManageAudit(c, "channel.multi_key_manage", map[string]any{"id": 42, "success": false})
	var entry model.AuditLog
	require.NoError(t, model.LOG_DB.Where("request_id = ?", "security-test-failed-multi-key").Take(&entry).Error)
	require.False(t, entry.Success)
}
