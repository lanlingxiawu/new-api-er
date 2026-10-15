package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestPolicyCannotWriteOutsideAuthorizedScope(t *testing.T) {
	for _, scope := range []string{"site.notice", "request-policies.filtering", "request-policies.health"} {
		t.Run(scope, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set(middleware.SystemSettingsScopeContextKey, scope)
			c.Request = httptest.NewRequest(http.MethodPatch, "/api/option/request_policy", strings.NewReader(`{"scope":"request-policies.routing","options":{"RetryTimes":"9"}}`))
			UpdateRequestPolicy(c)
			require.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

func TestRequestPolicyReadFiltersAuthorizedOptions(t *testing.T) {
	for _, scope := range []string{"site.notice", "request-policies.filtering", "request-policies.routing"} {
		t.Run(scope, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set(middleware.SystemSettingsScopeContextKey, scope)
			GetRequestPolicy(c)
			var response struct {
				Data struct {
					Options map[string]string `json:"options"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			for key := range response.Data.Options {
				require.True(t, settingsaccess.AllowsOption(scope, key), key)
			}
			if scope == "site.notice" {
				require.Empty(t, response.Data.Options)
			}
		})
	}
}
