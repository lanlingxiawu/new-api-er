package auditsecurity

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsProofBindsExactValues(t *testing.T) {
	first, err := service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopeAuditSecuritySettings, Context: []byte(`{"values":{"enabled":"true"}}`)})
	require.NoError(t, err)
	second, err := service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopeAuditSecuritySettings, Context: []byte(`{"values":{"enabled":"false"}}`)})
	require.NoError(t, err)
	require.NotEqual(t, first.ContextHash, second.ContextHash)
	_, err = service.BindVerificationOperation(service.VerificationOperation{Scope: service.VerificationScopeAuditSecuritySettings, Context: []byte(`{"values":{"arbitrary":"true"}}`)})
	require.Error(t, err)
}

func TestCaptureIsBoundedAndSuspensionStopsEnqueue(t *testing.T) {
	for {
		_, ok := model.TakeSecurityCapture()
		if !ok {
			break
		}
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "false", "suspended": "false"})
		for {
			_, ok := model.TakeSecurityCapture()
			if !ok {
				break
			}
		}
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/user/login", nil)
	before := model.SecurityCaptureDropped.Load()
	started := time.Now()
	for range 1100 {
		model.CaptureSecurityEvent(c, "auth.login_failed", "example-user", "invalid_credentials", false)
	}
	require.Less(t, time.Since(started), time.Second)
	require.Greater(t, model.SecurityCaptureDropped.Load(), before)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"suspended": "true"}))
	entry, ok := model.TakeSecurityCapture()
	require.True(t, ok)
	encoded, err := common.Marshal(entry)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"attempted_account":"example-user"`)
	require.Empty(t, entry.Username)
	require.Zero(t, entry.UserId)
	model.CaptureSecurityEvent(c, "auth.login_failed", "suspended", "invalid_credentials", false)
	count := 0
	for {
		_, ok := model.TakeSecurityCapture()
		if !ok {
			break
		}
		count++
	}
	require.Equal(t, 1023, count)
}

func TestSuspendedDeliveryDoesNotAccessDestination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, sendWebhook(ctx, "https://127.0.0.1/private", []byte(`{}`), 1))
}
