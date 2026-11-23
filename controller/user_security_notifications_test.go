package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountSecurityNotificationPreferenceLifecycle(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{Username: "security-notification-owner", Role: 1, Status: 1}
	require.NoError(t, db.Create(&user).Error)
	for _, tc := range []struct {
		body string
		want *bool
	}{
		{`{"notify_type":"email"}`, nil},
		{`{"notify_type":"email","account_security_notify_enabled":true}`, boolPreference(true)},
		{`{"notify_type":"email"}`, boolPreference(true)},
		{`{"notify_type":"email","account_security_notify_enabled":false}`, boolPreference(false)},
		{`{"notify_type":"email"}`, boolPreference(false)},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", user.Id)
		UpdateUserSetting(c)
		require.Contains(t, recorder.Body.String(), `"success":true`)
		require.NoError(t, db.First(&user, user.Id).Error)
		require.Equal(t, tc.want, user.GetSetting().AccountSecurityNotifyEnabled)
	}
}

func boolPreference(value bool) *bool { return &value }
