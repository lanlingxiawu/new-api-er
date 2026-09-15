package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// auditResponseSuccess
// ---------------------------------------------------------------------------

func TestAuditResponseSuccess(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"http error status", 500, `{"success":true}`, false},
		{"json success true", 200, `{"success":true}`, true},
		{"json success false", 200, `{"success":false}`, false},
		{"json without success field", 200, `{"data":1}`, true},
		{"non-json body", 200, `OK`, true},
		{"empty body ok status", 200, ``, true},
		{"whitespace body", 200, `   `, true},
		{"malformed json falls back to status", 200, `{bad`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, auditResponseSuccess(tc.status, []byte(tc.body)))
		})
	}
}

// ---------------------------------------------------------------------------
// auditAuthMethod
// ---------------------------------------------------------------------------

func TestAuditAuthMethod(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Equal(t, "session", auditAuthMethod(ctx))
	ctx.Set("use_access_token", true)
	require.Equal(t, "access_token", auditAuthMethod(ctx))
}

// ---------------------------------------------------------------------------
// auditResponseWriter — buffer capped at maxSize
// ---------------------------------------------------------------------------

func TestAuditResponseWriter_CapturesUpToMax(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &auditResponseWriter{
		ResponseWriter: ctx.Writer,
		body:           bytes.NewBuffer(nil),
		maxSize:        4,
	}
	n, err := w.Write([]byte("abcdef")) // longer than maxSize
	require.NoError(t, err)
	require.Equal(t, 6, n) // returns full underlying write count
	require.Equal(t, "abcd", w.body.String())

	// A subsequent write is dropped (buffer already at max).
	_, _ = w.WriteString("gh")
	require.Equal(t, "abcd", w.body.String())
	require.Equal(t, "abcdefgh", rec.Body.String()) // underlying gets everything
}

func TestAuditResponseWriter_PartialRemaining(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &auditResponseWriter{
		ResponseWriter: ctx.Writer,
		body:           bytes.NewBuffer(nil),
		maxSize:        5,
	}
	_, _ = w.Write([]byte("abc"))
	_, _ = w.Write([]byte("xyz")) // only 2 bytes fit
	require.Equal(t, "abcxy", w.body.String())
}

// ---------------------------------------------------------------------------
// beginAdminAudit — only wraps write methods
// ---------------------------------------------------------------------------

func TestBeginAdminAudit_ReadMethodReturnsNil(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user", nil)
	require.Nil(t, beginAdminAudit(ctx))
}

func TestBeginAdminAudit_WriteMethodWrapsWriter(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user", nil)
	w := beginAdminAudit(ctx)
	require.NotNil(t, w)
	// c.Writer is now the wrapping writer.
	_, ok := ctx.Writer.(*auditResponseWriter)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// finishAdminAudit — nil writer and already-logged short-circuits
// ---------------------------------------------------------------------------

func TestFinishAdminAudit_NilWriterNoOp(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user", nil)
	require.NotPanics(t, func() { finishAdminAudit(ctx, nil) })
}

func TestFinishAdminAudit_AlreadyLoggedSkips(t *testing.T) {
	requireDB(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user", nil)
	w := beginAdminAudit(ctx)
	ctx.Set(string(constant.ContextKeyAuditLogged), true)
	require.NotPanics(t, func() { finishAdminAudit(ctx, w) })
}

func TestFinishAdminAudit_GenericAction(t *testing.T) {
	requireDB(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/unknown-route", nil)
	ctx.Set("id", nextTestID())
	ctx.Set("username", "audit-tester")
	ctx.Set("role", 10)
	w := beginAdminAudit(ctx)
	_, _ = ctx.Writer.Write([]byte(`{"success":true}`))
	// No FullPath (not routed) -> action falls back to "generic"; recorded async.
	require.NotPanics(t, func() { finishAdminAudit(ctx, w) })
}

// ---------------------------------------------------------------------------
// auditTargetUserID — 兜底路径从路由参数解析被操作用户
// ---------------------------------------------------------------------------

func TestAuditTargetUserID(t *testing.T) {
	cases := []struct {
		name   string
		method string
		route  string
		params gin.Params
		want   int
	}{
		{
			name:   "registered user route",
			method: http.MethodDelete,
			route:  "/api/user/:id/oauth/bindings/:provider_id",
			params: gin.Params{{Key: "id", Value: "7"}, {Key: "provider_id", Value: "3"}},
			want:   7,
		},
		{
			// 该路由的用户在 :user_id 而非 :id（:id 是员工记录 ID）。
			name:   "reads the registered param, not :id",
			method: http.MethodDelete,
			route:  "/api/admin/employee/:id/customer/:user_id",
			params: gin.Params{{Key: "id", Value: "3"}, {Key: "user_id", Value: "9"}},
			want:   9,
		},
		{
			// :id 是客户档案 ID，真正的用户在档案的 CustomerUserId 上
			// （controller.AdminUpdateCustomerUser 用它解析用户）。把 :id 当用户 ID
			// 写进审计，记录到的是另一个账号。
			name:   "customer profile id is not a user id",
			method: http.MethodPut,
			route:  "/api/admin/customer/:id/user",
			params: gin.Params{{Key: "id", Value: "7"}},
			want:   0,
		},
		{
			// 未登记的路由不能把 :id 当成用户 ID——这里的 :id 是订阅 ID。
			name:   "unregistered route yields nothing",
			method: http.MethodPost,
			route:  "/api/subscription/admin/user_subscriptions/:id/invalidate",
			params: gin.Params{{Key: "id", Value: "7"}},
			want:   0,
		},
		{
			name:   "registered route but wrong method",
			method: http.MethodPost,
			route:  "/api/user/:id/2fa",
			params: gin.Params{{Key: "id", Value: "7"}},
			want:   0,
		},
		{
			name:   "non-numeric param",
			method: http.MethodDelete,
			route:  "/api/user/:id",
			params: gin.Params{{Key: "id", Value: "abc"}},
			want:   0,
		},
		{
			name:   "zero is not a user id",
			method: http.MethodDelete,
			route:  "/api/user/:id",
			params: gin.Params{{Key: "id", Value: "0"}},
			want:   0,
		},
		{
			name:   "negative is not a user id",
			method: http.MethodDelete,
			route:  "/api/user/:id",
			params: gin.Params{{Key: "id", Value: "-1"}},
			want:   0,
		},
		{
			name:   "missing param",
			method: http.MethodDelete,
			route:  "/api/user/:id",
			params: gin.Params{},
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Params = tc.params
			require.Equal(t, tc.want, auditTargetUserID(ctx, tc.method, tc.route))
		})
	}
}

// 登记表里的每条路由都必须真实存在于路由表中，否则是静默失效的死配置。
func TestAuditRouteTargetUserParam_RoutesAreRegisteredElsewhere(t *testing.T) {
	for key, param := range auditRouteTargetUserParam {
		require.NotEmpty(t, param, key)
		require.Contains(t, key, " ", "key must be \"METHOD /route\": %s", key)
		require.Contains(t, key, ":"+param, "route must contain the :%s param: %s", param, key)
	}
}
