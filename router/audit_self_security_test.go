package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSelfSecurityRoutesRequireSessionAndRemainReadOnly(t *testing.T) {
	engine := gin.New()
	SetApiRouter(engine)
	for _, path := range []string{"/api/audit/self/security/alerts", "/api/audit/self/security/alerts/:id"} {
		rule, ok := middleware.AccessTokenRouteRule("GET " + path)
		require.True(t, ok)
		require.Equal(t, "session", rule.Kind())
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest("GET", strings.ReplaceAll(path, ":id", "1"), nil))
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	}
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/audit/self/security/") {
			require.Equal(t, "GET", route.Method, "self security must not expose management mutations")
		}
	}
}

func TestEmployeeCustomerAuditListsRequireSession(t *testing.T) {
	engine := gin.New()
	SetApiRouter(engine)
	for _, path := range []string{"/api/user/employee/customers/audit", "/api/user/employee/customers/security/alerts"} {
		rule, ok := middleware.AccessTokenRouteRule("GET " + path)
		require.True(t, ok)
		require.Equal(t, "session", rule.Kind())
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	}
}
