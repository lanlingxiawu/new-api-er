package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopUpPlatformStatusRouteIsAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	_, hasAdminRoute := routes[http.MethodPost+" /api/user/topup/platform-status"]
	_, hasSelfRoute := routes[http.MethodPost+" /api/user/topup/self/platform-status"]
	assert.True(t, hasAdminRoute)
	assert.False(t, hasSelfRoute)

	req := httptest.NewRequest(http.MethodPost, "/api/user/topup/platform-status", strings.NewReader(`{"trade_nos":["ALI-1"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
