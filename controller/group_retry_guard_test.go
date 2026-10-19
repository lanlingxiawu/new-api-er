package controller

import (
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestGroupRetryGuardStatusScope(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, scope := range []string{"site.system-info", "system-tuning.group-retry-times"} {
		c, rec := newCtx(t, http.MethodGet, "/api/option/group-retry/status", nil)
		c.Set(middleware.SystemSettingsScopeContextKey, scope)
		GetGroupRetryGuardStatus(c)
		require.Equal(t, scope == "system-tuning.group-retry-times", decodeResp(t, rec).Success)
	}
}

func TestGroupRetryPolicyInvalidRequestIsSanitized(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, rec := newCtx(t, http.MethodPut, "/api/option/group", map[string]any{"module": "group_retry_policy", "values": map[string]string{"times": "null"}})
	asRoot(c, nextTestID())
	UpdateOptionGroup(c)
	response := decodeResp(t, rec)
	require.False(t, response.Success)
	require.Equal(t, i18n.T(c, i18n.MsgGroupRetryGuardInvalid), response.Message)
}
