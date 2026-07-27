package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 后台鉴权契约：登录返回的 access_token 必须能让后续请求通过 UserAuth，
// 且缺少 Authorization 头时必须被拒。
//
// 这条契约是前端 lib/auth-session.ts + lib/api.ts 拦截器存在的全部理由：
// 合并上游后后端从 cookie session 改成了 bearer token（middleware/auth.go 的
// classifyDashboardCredential 只读 Authorization 头），前端若不带这个头，
// 登录会「成功」但紧接着的每个接口都 401。
func TestDashboardAuth_LoginTokenIsAcceptedByUserAuth(t *testing.T) {
	requireDB(t)

	const password = "contract-pass-123"
	hashed, err := common.Password2Hash(password)
	require.NoError(t, err)
	user := mkUser(t, func(u *model.User) {
		u.Password = hashed
		u.Status = common.UserStatusEnabled
	})

	// ── 登录 ──────────────────────────────────────────────
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/login?turnstile=", map[string]any{
		"username": user.Username,
		"password": password,
	})
	Login(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "login failed: %s", resp.Message)

	var bundle struct {
		AccessToken     string `json:"access_token"`
		TokenType       string `json:"token_type"`
		AccessExpiresAt int64  `json:"access_expires_at"`
		Session         struct {
			SID     string `json:"sid"`
			Current bool   `json:"current"`
		} `json:"session"`
		User struct {
			Id int `json:"id"`
		} `json:"user"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &bundle))

	// 前端 isAuthBundle() 校验的就是这几项，缺一它就不会保存凭证。
	assert.NotEmpty(t, bundle.AccessToken, "login must return an access token")
	assert.Equal(t, "Bearer", bundle.TokenType)
	assert.Greater(t, bundle.AccessExpiresAt, int64(0))
	assert.NotEmpty(t, bundle.Session.SID)
	assert.True(t, bundle.Session.Current)
	assert.Equal(t, user.Id, bundle.User.Id)

	// ── 带上 token 访问受保护接口 ──────────────────────────
	authed, authedRec := newCtx(t, http.MethodGet, "/api/user/self", nil)
	authed.Request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	middleware.UserAuth()(authed)
	assert.False(t, authed.IsAborted(), "a valid bearer token must pass UserAuth")
	assert.NotEqual(t, http.StatusUnauthorized, authedRec.Code)
	assert.Equal(t, user.Id, authed.GetInt("id"))

	// ── 不带 token（合并前前端的行为）必须被拒 ──────────────
	anon, anonRec := newCtx(t, http.MethodGet, "/api/user/self", nil)
	middleware.UserAuth()(anon)
	assert.True(t, anon.IsAborted(), "requests without Authorization must be rejected")
	assert.Equal(t, http.StatusUnauthorized, anonRec.Code)

	// ── 无效 token 同样被拒 ────────────────────────────────
	bad, badRec := newCtx(t, http.MethodGet, "/api/user/self", nil)
	bad.Request.Header.Set("Authorization", "Bearer not-a-real-token")
	middleware.UserAuth()(bad)
	assert.True(t, bad.IsAborted())
	assert.Equal(t, http.StatusUnauthorized, badRec.Code)

	// ── 刷新：access token 只存在内存，页面重载后靠 refresh cookie 换新的 ──
	// 这是前端 bootstrapAuthentication() 依赖的接口，换不出来就会被踢回登录页。
	refreshCookie := findRefreshCookie(t, rec.Result().Cookies())
	refresh, refreshRec := newCtx(t, http.MethodPost, "/api/user/auth/refresh", nil)
	refresh.Request.AddCookie(refreshCookie)
	refresh.Request.Header.Set("X-Auth-Session", bundle.Session.SID)
	RefreshAuth(refresh)

	refreshResp := decodeResp(t, refreshRec)
	require.True(t, refreshResp.Success, "refresh failed: %s", refreshResp.Message)

	var rotated struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Session     struct {
			SID string `json:"sid"`
		} `json:"session"`
	}
	require.NoError(t, common.Unmarshal(refreshResp.Data, &rotated))
	assert.NotEmpty(t, rotated.AccessToken)
	assert.Equal(t, "Bearer", rotated.TokenType)
	assert.Equal(t, bundle.Session.SID, rotated.Session.SID, "refresh must stay on the same session")

	// 换来的新 token 同样可用。
	renewed, renewedRec := newCtx(t, http.MethodGet, "/api/user/self", nil)
	renewed.Request.Header.Set("Authorization", "Bearer "+rotated.AccessToken)
	middleware.UserAuth()(renewed)
	assert.False(t, renewed.IsAborted(), "refreshed token must pass UserAuth")
	assert.NotEqual(t, http.StatusUnauthorized, renewedRec.Code)
}

func findRefreshCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, ck := range cookies {
		if ck.Name == service.RefreshCookieName {
			return ck
		}
	}
	t.Fatalf("login response did not set the %s cookie", service.RefreshCookieName)
	return nil
}
