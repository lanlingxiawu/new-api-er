package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryPolicyRejectsInvalidCombinedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, module, counts, scope, message string
		values                               map[string]string
	}{
		{"invalid count", "group_retry_status_setting", `{"default":21}`, "", i18n.MsgSettingGroupRetryTimesInvalid, map[string]string{"enabled": "true", "rules": `{"default":"429"}`}},
		{"wrong module", "probe_routing_setting", `{}`, "", i18n.MsgInvalidParams, map[string]string{"max_input_chars": "128"}},
		{"incomplete form", "group_retry_status_setting", `{}`, "", i18n.MsgInvalidParams, map[string]string{"enabled": "true"}},
		{"wrong scope", "group_retry_status_setting", `{}`, "request-policies.routing", i18n.MsgInvalidParams, map[string]string{"enabled": "true", "rules": "{}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newCtx(t, http.MethodPut, "/api/option/group", map[string]any{"module": tc.module, "group_retry_times": tc.counts, "values": tc.values, "scope": tc.scope})
			c.Set("role", common.RoleRootUser)
			c.Set(middleware.SystemSettingsScopeContextKey, tc.scope)
			UpdateOptionGroup(c)
			response := decodeResp(t, rec)
			require.False(t, response.Success)
			require.Equal(t, i18n.T(c, tc.message), response.Message)
		})
	}
}
