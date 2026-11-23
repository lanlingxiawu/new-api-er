package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecurityDeletionRequiresSessionAndValidatedInput(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{DeleteSecurityAlert, CleanupSecurityAlerts} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("id", 1)
		c.Set("role", 100)
		c.Set("use_access_token", true)
		c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/cleanup", strings.NewReader(`{"closed_before":100}`))
		handler(c)
		require.Equal(t, http.StatusForbidden, rec.Code)
	}
	for _, tc := range []struct {
		handler gin.HandlerFunc
		body    string
	}{
		{DeleteSecurityAlert, `{}`}, {DeleteSecurityAlert, `{"version":0}`}, {DeleteSecurityAlert, `{"version":"3"}`},
		{CleanupSecurityAlerts, `{}`}, {CleanupSecurityAlerts, `{"closed_before":-1}`}, {CleanupSecurityAlerts, `{"closed_before":99999999999}`},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("id", 1)
		c.Set("role", 100)
		c.Set("session_id", "test-session")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Params = gin.Params{{Key: "id", Value: "7"}}
		c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/cleanup", strings.NewReader(tc.body))
		tc.handler(c)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"success":false`)
	}
}

func TestSecurityAssigneeQueryRequiresAnAlert(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, query := range []string{"", "?alert_id=0", "?alert_id=-1", "?alert_id=invalid"} {
		t.Run(query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("role", 100)
			c.Request = httptest.NewRequest("GET", "/api/audit/security/assignees"+query, nil)
			GetSecurityAssignees(c)
			require.Contains(t, recorder.Body.String(), `"success":false`)
			require.NotContains(t, recorder.Body.String(), `"data"`)
		})
	}
}

func TestRejectNegativeSecurityAssignee(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 1)
	c.Set("role", 100)
	c.Set("session_id", "test-session")
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest("POST", "/api/audit/security/alerts/7/actions", strings.NewReader(`{"version":1,"assignee_id":-1}`))
	ActOnSecurityAlert(c)
	require.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestSecurityProviderSecretsAreNotReturnedByOptions(t *testing.T) {
	const key = "audit_security_setting.ip_intelligence_providers_json"
	common.OptionMapRWMutex.Lock()
	previous, existed := common.OptionMap[key]
	common.OptionMap[key] = `[{"id":"ipapi","name":"ipapi","type":"ipapi_is","endpoint":"https://api.ipapi.is","enabled":true,"priority":100,"api_key":"never-return-this","daily_limit":1000,"capabilities":["geo","vpn"]}]`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[key] = previous
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
	})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("role", 100)
	c.Request = httptest.NewRequest("GET", "/api/option/?scope=security.audit-monitor", nil)
	GetOptions(c)
	body := recorder.Body.String()
	require.NotContains(t, body, "never-return-this")
	require.Contains(t, body, `"key_configured\":true`)
}
