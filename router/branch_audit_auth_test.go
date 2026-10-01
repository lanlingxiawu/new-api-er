package router

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise real middleware registration, not only the declarative permission
// tables: anonymous requests must stop before a handler reads or mutates data.
func TestBranchAuditPrivilegedRoutesRejectAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/request-log/"},
		{http.MethodGet, "/api/request-log/1"},
		{http.MethodDelete, "/api/request-log/"},
		{http.MethodDelete, "/api/request-log/all"},
		{http.MethodGet, "/api/system-info/instances"},
		{http.MethodDelete, "/api/system-info/stale-instances"},
		{http.MethodDelete, "/api/system-info/instances/audit-node"},
		{http.MethodGet, "/api/system-info/pprof-status"},
		{http.MethodGet, "/api/system-info/pprof/heap"},
		{http.MethodGet, "/api/price_monitor/status"},
		{http.MethodGet, "/api/price_monitor/results"},
		{http.MethodGet, "/api/price_monitor/inconsistencies"},
		{http.MethodGet, "/api/price_monitor/channels"},
		{http.MethodPut, "/api/price_monitor/settings"},
		{http.MethodPost, "/api/price_monitor/run"},
		{http.MethodPost, "/api/price_monitor/apply_price"},
		{http.MethodPut, "/api/price_monitor/channels/1/cost_ratio"},
		{http.MethodPost, "/api/user/topup/platform-status"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		})
	}
}

// auditRouteChain is the full handler chain gin resolved for one registered route.
type auditRouteChain struct {
	method, path string
	names        []string
}

var (
	auditEnforcingAuth = regexp.MustCompile(`\.(AdminAuth|RootAuth|UserAuth|TokenAuth|TokenAuthReadOnly|TokenOrUserAuth)\.func`)
	auditAdminAuth     = regexp.MustCompile(`\.(AdminAuth|RootAuth)\.func`)
	auditPermission    = regexp.MustCompile(`\.(RequirePermission|RequireSystemSettingsScope)\.func`)
	auditPathParam     = regexp.MustCompile(`[:*][A-Za-z_]+`)
)

// collectRouteChains registers every router that serves API traffic behind a
// probe that records c.HandlerNames() and aborts, so no handler, auth check or
// database access runs while the chains are walked.
func collectRouteChains(t *testing.T) []auditRouteChain {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var names []string
	var full string
	engine.Use(func(c *gin.Context) {
		names = append([]string(nil), c.HandlerNames()[1:]...)
		full = c.FullPath()
		c.AbortWithStatus(http.StatusNoContent)
	})
	SetApiRouter(engine)
	SetDashboardRouter(engine)
	SetRelayRouter(engine)
	SetVideoRouter(engine)

	var chains []auditRouteChain
	for _, route := range engine.Routes() {
		names, full = nil, ""
		req := httptest.NewRequest(route.Method, auditPathParam.ReplaceAllString(route.Path, "1"), nil)
		engine.ServeHTTP(httptest.NewRecorder(), req)
		require.Equal(t, route.Path, full, "probe request resolved to a different route")
		require.NotEmpty(t, names, route.Path)
		chains = append(chains, auditRouteChain{method: route.Method, path: route.Path, names: names})
	}
	require.Greater(t, len(chains), 100, "route walk found suspiciously few routes")
	return chains
}

func chainIndex(names []string, re *regexp.Regexp) int {
	for i, name := range names {
		if re.MatchString(name) {
			return i
		}
	}
	return -1
}

// Every route reachable without credentials. Each entry is a deliberate public
// surface (login/registration, signature-verified payment callbacks, public
// content, token-validated downloads). A new route that is neither
// authenticated nor listed here fails the walk below.
var auditPublicRoutes = map[string]string{
	"GET /api/user/epay/notify":                "payment callback (signature verified)",
	"POST /api/user/epay/notify":               "payment callback (signature verified)",
	"GET /api/alipay/notify":                   "payment callback (signature verified)",
	"POST /api/alipay/notify":                  "payment callback (signature verified)",
	"POST /api/wechat/notify":                  "payment callback (signature verified)",
	"POST /api/infini/webhook":                 "payment callback (signature verified)",
	"POST /api/stripe/webhook":                 "payment callback (signature verified)",
	"POST /api/creem/webhook":                  "payment callback (signature verified)",
	"POST /api/waffo/webhook":                  "payment callback (signature verified)",
	"POST /api/waffo-pancake/webhook/:env":     "payment callback (signature verified)",
	"GET /api/subscription/epay/notify":        "payment callback (signature verified)",
	"POST /api/subscription/epay/notify":       "payment callback (signature verified)",
	"GET /api/subscription/epay/return":        "payment return page",
	"POST /api/subscription/epay/return":       "payment return page",
	"GET /api/user/groups":                     "public group list",
	"GET /api/user-agreement":                  "public legal content",
	"GET /api/privacy-policy":                  "public legal content",
	"GET /api/uptime/status":                   "public status page",
	"GET /api/about":                           "public content",
	"GET /api/status":                          "public bootstrap status",
	"GET /api/setup":                           "first-run setup",
	"POST /api/setup":                          "first-run setup",
	"GET /api/notice":                          "public content",
	"GET /api/home_page_content":               "public content",
	"GET /api/pricing":                         "public pricing (optional auth)",
	"GET /api/perf-metrics":                    "public when the pricing nav module is public (upstream)",
	"GET /api/perf-metrics/summary":            "public when the pricing nav module is public (upstream)",
	"GET /api/ratio_config":                    "public ratio sync (feature-flagged)",
	"GET /api/rankings":                        "public rankings",
	"GET /api/exchange-rate/usd-cny":           "public FX rate for price display",
	"GET /api/oauth/telegram/login":            "OAuth login",
	"GET /api/oauth/telegram/bind/:flow_token": "OAuth bind via one-time flow token",
	"GET /api/oauth/wechat":                    "OAuth login",
	"GET /api/oauth/:provider":                 "OAuth login",
	"POST /api/oauth/state":                    "OAuth state issuance",
	"GET /api/reset_password":                  "password reset email",
	"POST /api/user/reset":                     "password reset",
	"GET /api/verification":                    "email verification",
	"POST /api/user/register":                  "registration",
	"POST /api/user/login":                     "login",
	"POST /api/user/login/2fa":                 "login second factor",
	"POST /api/user/passkey/login/begin":       "passkey login",
	"POST /api/user/passkey/login/finish":      "passkey login",
	"POST /api/user/auth/refresh":              "refresh-cookie rotation",
	"POST /api/user/auth/logout":               "logout",
	"POST /api/price_monitor/public_query":     "rotating-password share query (rate limited)",
	"GET /price_monitor/view":                  "public share page shell",
	"GET /dl/ledger/:token":                    "signed download token",
	"GET /dl/log-export/:token":                "signed download token",
	"GET /mj/image/:id":                        "Midjourney image proxy",
	"GET /:mode/mj/image/:id":                  "Midjourney image proxy",
}

func TestBranchAuditEveryRouteIsAuthenticatedOrAllowlisted(t *testing.T) {
	seenPublic := map[string]bool{}
	for _, chain := range collectRouteChains(t) {
		id := chain.method + " " + chain.path
		_, listed := auditPublicRoutes[id]
		if chainIndex(chain.names, auditEnforcingAuth) >= 0 {
			assert.False(t, listed, "%s is authenticated but still allowlisted as public", id)
			continue
		}
		assert.True(t, listed, "%s has no auth middleware and is not an allowlisted public route: %v", id, chain.names)
		seenPublic[id] = true
	}
	for id := range auditPublicRoutes {
		assert.True(t, seenPublic[id], "allowlisted public route %s is no longer registered", id)
	}
}

// Fine-grained admin permission checks read the id/role that AdminAuth or
// RootAuth put in the context; they must never be the first gate.
func TestBranchAuditPermissionGatesFollowAdminAuth(t *testing.T) {
	gated := 0
	for _, chain := range collectRouteChains(t) {
		permIdx := chainIndex(chain.names, auditPermission)
		if permIdx < 0 {
			continue
		}
		gated++
		adminIdx := chainIndex(chain.names, auditAdminAuth)
		assert.True(t, adminIdx >= 0 && adminIdx < permIdx,
			"%s %s: permission gate without a preceding AdminAuth/RootAuth: %v", chain.method, chain.path, chain.names)
	}
	assert.Greater(t, gated, 20)
}

// Every /api/option endpoint was RootAuth on main; the fork opens them to admins
// and must gate each one with a scope or an explicit permission check.
func TestBranchAuditOptionRoutesAreAdminAndPermissionGated(t *testing.T) {
	found := 0
	for _, chain := range collectRouteChains(t) {
		if !strings.HasPrefix(chain.path, "/api/option") {
			continue
		}
		found++
		assert.GreaterOrEqual(t, chainIndex(chain.names, auditAdminAuth), 0, "%s %s", chain.method, chain.path)
		assert.GreaterOrEqual(t, chainIndex(chain.names, auditPermission), 0, "%s %s", chain.method, chain.path)
	}
	assert.Greater(t, found, 5)
}
