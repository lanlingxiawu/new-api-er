// Regression tests (branch audit H1): scoped system-settings writes must be
// authorized and applied against one and the same scope.
//
// The attack: a body that is valid JSON without a "scope" member, but whose
// form (or multipart) reading carries "scope=<granted scope>", used to pass
// RequireSystemSettingsScope as the granted scope and reach UpdateOption /
// UpdateOptionGroup as an unscoped (root-only) write. The middleware now only
// accepts JSON bodies for writes and the handlers act on the scope it recorded
// in the gin context.

package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

const formContentType = "application/x-www-form-urlencoded"

func TestBranchAuditRegressionScopedAdminFormBodyWritesAnyOption(t *testing.T) {
	engine := setupScopedOptionAudit(t)

	// Control: the same payload as JSON is rejected by the allowlist.
	assertScopedRejected(t, serveScoped(engine, http.MethodPut, "/api/option/", "application/json",
		`{"scope":"site.notice","key":"About","value":"pwned"}`))

	rec := serveScoped(engine, http.MethodPut, "/api/option/", formContentType,
		`{"key":"About","value":"pwned","x":"&scope=site.notice&y="}`)

	assertScopedRejected(t, rec)
	assert.Equal(t, "about-before", scopedOption("About"),
		"an admin granted only site.notice rewrote a site.system-info option: %s", rec.Body.String())
}

// Same bypass through multipart/form-data: the JSON object sits in the MIME
// preamble (skipped by the multipart reader, read by json.Decoder, which stops
// after the first value), and the scope travels as a form part. Rejecting only
// urlencoded bodies would not close the hole.
func TestBranchAuditRegressionScopedAdminMultipartPreambleWritesAnyOption(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	body := "{\"key\":\"About\",\"value\":\"pwned-multipart\"}\r\n" +
		"--AUDITB\r\nContent-Disposition: form-data; name=\"scope\"\r\n\r\nsite.notice\r\n--AUDITB--\r\n"

	rec := serveScoped(engine, http.MethodPut, "/api/option/", "multipart/form-data; boundary=AUDITB", body)

	assertScopedRejected(t, rec)
	assert.Equal(t, "about-before", scopedOption("About"), rec.Body.String())
}

func TestBranchAuditRegressionScopedAdminFormBodyWritesAnyConfigGroup(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	before := operation_setting.GetUserSessionSetting()
	body := fmt.Sprintf(`{"module":"user_session_setting","values":{"hourly_alert_threshold":"%d"},"x":"&scope=site.notice&y="}`,
		before.HourlyAlertThreshold+1)

	rec := serveScoped(engine, http.MethodPut, "/api/option/group", formContentType, body)

	assertScopedRejected(t, rec)
	assert.Equal(t, before, operation_setting.GetUserSessionSetting(),
		"an admin granted only site.notice changed the login-session policy: %s", rec.Body.String())
}

// settingsWriteScope decision table: the handler follows the scope recorded by
// the middleware, refuses a body that names a different scope, and without the
// middleware only lets root write.
func TestSettingsWriteScope(t *testing.T) {
	cases := []struct {
		name      string
		recorded  any // nil: middleware did not run
		role      int
		bodyScope string
		wantScope string
		wantOK    bool
	}{
		{"recorded scope matches body", "site.notice", common.RoleAdminUser, " site.notice ", "site.notice", true},
		{"recorded scope, body names none", "site.notice", common.RoleAdminUser, "", "site.notice", false},
		{"recorded scope, body names another", "site.notice", common.RoleAdminUser, "billing.payment", "site.notice", false},
		{"root recorded empty, body empty", "", common.RoleRootUser, "", "", true},
		{"root recorded empty, body names scope", "", common.RoleRootUser, "site.notice", "", false},
		{"recorded non-string value", 1, common.RoleRootUser, "", "", false},
		{"no middleware, root", nil, common.RoleRootUser, "site.notice", "site.notice", true},
		{"no middleware, root unscoped", nil, common.RoleRootUser, "", "", true},
		{"no middleware, admin", nil, common.RoleAdminUser, "site.notice", "site.notice", false},
		{"no middleware, admin unscoped", nil, common.RoleAdminUser, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("role", tc.role)
			if tc.recorded != nil {
				ctx.Set(middleware.SystemSettingsScopeContextKey, tc.recorded)
			}
			scope, ok := settingsWriteScope(ctx, tc.bodyScope)
			assert.Equal(t, tc.wantOK, ok)
			if ok {
				assert.Equal(t, tc.wantScope, scope)
			}
		})
	}
}
