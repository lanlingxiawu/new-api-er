package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// withHeaderNavModules swaps the HeaderNavModules option for the test duration.
func withHeaderNavModules(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev, had := common.OptionMap["HeaderNavModules"]
	common.OptionMap["HeaderNavModules"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if had {
			common.OptionMap["HeaderNavModules"] = prev
			return
		}
		delete(common.OptionMap, "HeaderNavModules")
	})
}

// ---------------------------------------------------------------------------
// parseHeaderNavBool — pure logic, all input classes
// ---------------------------------------------------------------------------

func TestParseHeaderNavBool(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		fallback bool
		want     bool
	}{
		{"bool true", true, false, true},
		{"bool false", false, true, false},
		{"string true", "true", false, true},
		{"string 1", "1", false, true},
		{"string false", "false", true, false},
		{"string 0", "0", true, false},
		{"string mixed case", "TrUe", false, true},
		{"string unknown -> fallback", "maybe", true, true},
		{"float 1", float64(1), false, true},
		{"float 0", float64(0), true, false},
		{"float other -> fallback", float64(2), true, true},
		{"int 1", 1, false, true},
		{"int 0", 0, true, false},
		{"int other -> fallback", 5, true, true},
		{"nil -> fallback", nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseHeaderNavBool(tc.value, tc.fallback))
		})
	}
}

// ---------------------------------------------------------------------------
// parseHeaderNavAccess — type variants
// ---------------------------------------------------------------------------

func TestParseHeaderNavAccess(t *testing.T) {
	fb := headerNavAccess{Enabled: true, RequireAuth: false}

	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess(false, fb))
	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess("false", fb))
	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess(float64(0), fb))

	got := parseHeaderNavAccess(map[string]any{"enabled": true, "requireAuth": true}, fb)
	require.Equal(t, headerNavAccess{Enabled: true, RequireAuth: true}, got)

	// unknown type -> fallback
	require.Equal(t, fb, parseHeaderNavAccess(12345, fb))
}

// ---------------------------------------------------------------------------
// getHeaderNavAccess — option parsing + fallback
// ---------------------------------------------------------------------------

func TestGetHeaderNavAccess_EmptyOptionFallback(t *testing.T) {
	withHeaderNavModules(t, "")
	acc := getHeaderNavAccess("pricing")
	require.True(t, acc.Enabled)
	require.False(t, acc.RequireAuth)
}

func TestGetHeaderNavAccess_InvalidJSONFallback(t *testing.T) {
	withHeaderNavModules(t, "{not-json")
	acc := getHeaderNavAccess("pricing")
	require.True(t, acc.Enabled)
}

func TestGetHeaderNavAccess_ModuleDisabled(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false}}`)
	acc := getHeaderNavAccess("pricing")
	require.False(t, acc.Enabled)
}

// ---------------------------------------------------------------------------
// HeaderNavModuleAuth / HeaderNavModulePublicOrUserAuth — full flow
// ---------------------------------------------------------------------------

// runHeaderNav performs a request against the given module middleware,
// optionally carrying a valid user session.
func runHeaderNav(t *testing.T, handler gin.HandlerFunc, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	r := newSessionRouter()
	var cookies []*http.Cookie
	if authenticated {
		cookies = loginSession(t, r, map[string]interface{}{
			"username": "tester", "role": common.RoleCommonUser,
			"id": 1, "status": common.UserStatusEnabled, "group": "default",
		})
	}
	r.GET("/api/test", handler, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if authenticated {
		req.Header.Set("New-Api-User", "1")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestHeaderNavModuleAuth_DefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModuleAuth_DisabledForbidden(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHeaderNavModuleAuth_RequireAuthRejectsAnonymous(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":true,"requireAuth":true}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHeaderNavModuleAuth_RequireAuthAllowsLoggedIn(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":true,"requireAuth":true}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), true)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_PublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), false)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_DisabledRequiresLogin(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), false)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_DisabledAllowsLoggedIn(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), true)
	require.Equal(t, http.StatusOK, rec.Code)
}
