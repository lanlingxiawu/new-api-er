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
