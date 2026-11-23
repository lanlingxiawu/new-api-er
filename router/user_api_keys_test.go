package router

import (
	"testing"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserAPIKeysRouteRejectsEveryPAT(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	found := false
	for _, route := range engine.Routes() {
		if route.Method == "DELETE" && route.Path == "/api/user/:id/tokens" {
			found = true
		}
	}
	require.True(t, found)
	rule, exists := middleware.AccessTokenRouteRule("DELETE /api/user/:id/tokens")
	require.True(t, exists)
	assert.Equal(t, "session", rule.Kind())
	scope, exists := service.AccessTokenVerificationScope(service.VerificationScopeAdminUserAPIKeysDeleteAll)
	require.True(t, exists)
	assert.Empty(t, scope, "PATs must not obtain a clearing proof with any scope")
}

func TestUserAccessTokensClearRouteRejectsEveryPAT(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	found := false
	for _, route := range engine.Routes() {
		if route.Method == "DELETE" && route.Path == "/api/user/:id/access_tokens" {
			found = true
		}
	}
	require.True(t, found)
	rule, exists := middleware.AccessTokenRouteRule("DELETE /api/user/:id/access_tokens")
	require.True(t, exists)
	assert.Equal(t, "session", rule.Kind())
	scope, exists := service.AccessTokenVerificationScope(service.VerificationScopeAdminUserAccessTokensDeleteAll)
	require.True(t, exists)
	assert.Empty(t, scope)
}
