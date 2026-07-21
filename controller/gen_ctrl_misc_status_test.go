package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// misc.go: status/notice/content endpoints and password-reset / email-verification
// input guards (crypto verification itself is exercised only via the failure path).

func TestGetStatus_Success(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/status", nil)
	GetStatus(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
	assert.NotEmpty(t, resp.Data)
}

func TestTestStatus_DBReachable(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, "GET", "/api/status/test", nil)
	TestStatus(ctx)
	// PingDB should succeed against the configured test DB.
	assert.Equal(t, 200, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, true, out["success"])
}

func TestOptionMapContentEndpoints(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap["Notice"] = "hello-notice"
	common.OptionMap["About"] = "about-body"
	common.OptionMap["HomePageContent"] = "home"
	common.OptionMap["Midjourney"] = "mj"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "Notice")
		delete(common.OptionMap, "About")
		delete(common.OptionMap, "HomePageContent")
		delete(common.OptionMap, "Midjourney")
		common.OptionMapRWMutex.Unlock()
	})

	for _, tc := range []struct {
		h    func(*gin.Context)
		want string
	}{
		{GetNotice, "hello-notice"},
		{GetAbout, "about-body"},
		{GetHomePageContent, "home"},
		{GetMidjourney, "mj"},
	} {
		ctx, rec := newCtx(t, "GET", "/x", nil)
		tc.h(ctx)
		resp := decodeResp(t, rec)
		assert.True(t, resp.Success)
		assert.JSONEq(t, `"`+tc.want+`"`, string(resp.Data))
	}
}

func TestResetPassword_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, "POST", "/api/user/reset", "not-json")
	ResetPassword(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestResetPassword_MissingFields(t *testing.T) {
	ctx, rec := newCtx(t, "POST", "/api/user/reset", PasswordResetRequest{Email: "", Token: ""})
	ResetPassword(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestResetPassword_InvalidToken(t *testing.T) {
	// A random unregistered code fails verification -> link-invalid message.
	ctx, rec := newCtx(t, "POST", "/api/user/reset", PasswordResetRequest{
		Email: "nobody@example.com",
		Token: "totally-wrong-token",
	})
	ResetPassword(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestSendEmailVerification_InvalidEmail(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/verification?email=not-an-email", nil)
	SendEmailVerification(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}
