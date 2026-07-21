package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// validUserInfo — pure logic
// ---------------------------------------------------------------------------

func TestValidUserInfo(t *testing.T) {
	cases := []struct {
		name     string
		username string
		role     int
		want     bool
	}{
		{"valid common", "alice", common.RoleCommonUser, true},
		{"valid guest role", "bob", common.RoleGuestUser, true},
		{"valid admin", "carol", common.RoleAdminUser, true},
		{"valid root", "dave", common.RoleRootUser, true},
		{"empty username", "", common.RoleCommonUser, false},
		{"blank username", "   ", common.RoleCommonUser, false},
		{"invalid role (between valid)", "eve", 5, false},
		{"invalid role (negative)", "eve", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, validUserInfo(tc.username, tc.role))
		})
	}
}

// ---------------------------------------------------------------------------
// authHelper via UserAuth/AdminAuth/RootAuth — session path
// ---------------------------------------------------------------------------

// authRunner wires a session router with the middleware under test plus a
// terminal handler, performing a request with the given session values and
// New-Api-User header. Returns the terminal recorder.
func runAuth(t *testing.T, mw gin.HandlerFunc, sessionValues map[string]interface{}, newApiUser string, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	r := newSessionRouter()
	var cookies []*http.Cookie
	if sessionValues != nil {
		cookies = loginSession(t, r, sessionValues)
	}
	r.GET("/protected", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if newApiUser != "" {
		req.Header.Set("New-Api-User", newApiUser)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", accessToken)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthHelper_SessionValidCommonUser(t *testing.T) {
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "42", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "864b7076dbcd0a3c01b5520316720ebf", rec.Header().Get("Auth-Version"))
}

func TestAuthHelper_NoSessionNoAccessToken(t *testing.T) {
	// No session and no Authorization header -> 401 not-logged-in.
	rec := runAuth(t, UserAuth(), nil, "1", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHelper_MissingNewApiUser(t *testing.T) {
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHelper_NewApiUserNotNumber(t *testing.T) {
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "not-a-number", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHelper_NewApiUserMismatch(t *testing.T) {
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "99", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHelper_UserBanned(t *testing.T) {
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusDisabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "42", "")
	require.Equal(t, http.StatusOK, rec.Code) // banned -> 200 {success:false}
	require.Contains(t, rec.Body.String(), "false")
}

func TestAuthHelper_InsufficientPrivilege(t *testing.T) {
	// Common user hitting an AdminAuth-guarded route -> role < minRole -> denied.
	sv := map[string]interface{}{
		"username": "alice", "role": common.RoleCommonUser,
		"id": 42, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, AdminAuth(), sv, "42", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "false")
}

func TestAuthHelper_AdminAllowedForAdmin(t *testing.T) {
	sv := map[string]interface{}{
		"username": "root", "role": common.RoleAdminUser,
		"id": 43, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, AdminAuth(), sv, "43", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "true")
}

func TestAuthHelper_RootDeniedForAdmin(t *testing.T) {
	sv := map[string]interface{}{
		"username": "admin", "role": common.RoleAdminUser,
		"id": 44, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, RootAuth(), sv, "44", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "false")
}

func TestAuthHelper_RootAllowedForRoot(t *testing.T) {
	sv := map[string]interface{}{
		"username": "root", "role": common.RoleRootUser,
		"id": 45, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, RootAuth(), sv, "45", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "true")
}

func TestAuthHelper_InvalidUserInfoInSession(t *testing.T) {
	// role passes minRole but username blank -> validUserInfo fails.
	sv := map[string]interface{}{
		"username": "   ", "role": common.RoleCommonUser,
		"id": 46, "status": common.UserStatusEnabled, "group": "default",
	}
	rec := runAuth(t, UserAuth(), sv, "46", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "false")
}

// ---------------------------------------------------------------------------
// authHelper — access token path (no session)
// ---------------------------------------------------------------------------

func TestAuthHelper_AccessTokenValid(t *testing.T) {
	requireDB(t)
	at := uniqKey()
	u := mkUser(t, func(u *model.User) {
		u.Role = common.RoleAdminUser
		u.AccessToken = &at
	})
	rec := runAuth(t, AdminAuth(), nil, jsonInt(u.Id), at)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "true")
}

func TestAuthHelper_AccessTokenInvalid(t *testing.T) {
	requireDB(t)
	// No user has this access token -> ValidateAccessToken returns (nil,nil) ->
	// access-token-invalid -> 200 {success:false}.
	rec := runAuth(t, UserAuth(), nil, "1", uniqKey())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "false")
}

func TestAuthHelper_AccessTokenUserIdMismatch(t *testing.T) {
	requireDB(t)
	at := uniqKey()
	u := mkUser(t, func(u *model.User) { u.AccessToken = &at })
	// New-Api-User header does not match the token's user id.
	rec := runAuth(t, UserAuth(), nil, jsonInt(u.Id+1), at)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// ---------------------------------------------------------------------------
// TryUserAuth — optional auth
// ---------------------------------------------------------------------------

func TestTryUserAuth_WithSession(t *testing.T) {
	r := newSessionRouter()
	cookies := loginSession(t, r, map[string]interface{}{"id": 7})
	var gotID int
	var present bool
	r.GET("/opt", TryUserAuth(), func(c *gin.Context) {
		v, ok := c.Get("id")
		present = ok
		if ok {
			gotID = v.(int)
		}
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/opt", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, present)
	require.Equal(t, 7, gotID)
}

func TestTryUserAuth_WithoutSession(t *testing.T) {
	r := newSessionRouter()
	var present bool
	r.GET("/opt", TryUserAuth(), func(c *gin.Context) {
		_, present = c.Get("id")
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/opt", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, present)
}

// ---------------------------------------------------------------------------
// RequirePermission
// ---------------------------------------------------------------------------

func TestRequirePermission_AllowRoot(t *testing.T) {
	// Root bypasses RBAC checks (authz.Can returns true for root).
	r := gin.New()
	r.GET("/perm", func(c *gin.Context) {
		c.Set("role", common.RoleRootUser)
		c.Set("id", 1)
	}, RequirePermission(authz.ChannelRead), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/perm", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRequirePermission_DenyGuest(t *testing.T) {
	r := gin.New()
	r.GET("/perm", func(c *gin.Context) {
		c.Set("role", common.RoleGuestUser)
		c.Set("id", 0)
	}, RequirePermission(authz.ChannelRead), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/perm", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// TokenAuth — API key path
// ---------------------------------------------------------------------------

func runTokenAuth(t *testing.T, mw gin.HandlerFunc, method, target string, headers map[string]string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		ctx.Request.Header.Set(k, v)
	}
	mw(ctx)
	return rec, ctx
}

func TestTokenAuth_MissingKey(t *testing.T) {
	rec, _ := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuth_InvalidKey(t *testing.T) {
	requireDB(t)
	rec, _ := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer " + uniqKey()})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuth_ValidTokenEnabledUser(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.UnlimitedQuota = true })
	rec, ctx := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, u.Id, ctx.GetInt("id"))
	require.Equal(t, tk.Id, ctx.GetInt("token_id"))
}

func TestTokenAuth_DisabledToken(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) {
		tk.Status = common.TokenStatusDisabled
		tk.UnlimitedQuota = true
	})
	rec, _ := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuth_BannedUser(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *model.User) { u.Status = common.UserStatusDisabled })
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.UnlimitedQuota = true })
	rec, _ := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestTokenAuth_AnthropicHeaderKey(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.UnlimitedQuota = true })
	// x-api-key on /v1/messages is promoted to Authorization.
	rec, ctx := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/messages",
		map[string]string{"x-api-key": "sk-" + tk.Key})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, u.Id, ctx.GetInt("id"))
}

func TestTokenAuth_GeminiQueryKey(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.UnlimitedQuota = true })
	rec, ctx := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent?key=sk-"+tk.Key, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, u.Id, ctx.GetInt("id"))
}

func TestTokenAuth_IpRestrictionDenied(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	allow := "10.0.0.0/8"
	tk := mkToken(t, u.Id, func(tk *model.Token) {
		tk.UnlimitedQuota = true
		tk.AllowIps = &allow // test client IP is not in this CIDR
	})
	rec, _ := runTokenAuth(t, TokenAuth(), http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// TokenAuthReadOnly
// ---------------------------------------------------------------------------

func TestTokenAuthReadOnly_MissingKey(t *testing.T) {
	rec, _ := runTokenAuth(t, TokenAuthReadOnly(), http.MethodGet, "/api/log/token", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuthReadOnly_InvalidKey(t *testing.T) {
	requireDB(t)
	rec, _ := runTokenAuth(t, TokenAuthReadOnly(), http.MethodGet, "/api/log/token",
		map[string]string{"Authorization": "Bearer sk-" + uniqKey()})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuthReadOnly_ExhaustedTokenStillAllowed(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	// Exhausted (not disabled) token is still allowed for read-only queries.
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.Status = common.TokenStatusExhausted })
	rec, ctx := runTokenAuth(t, TokenAuthReadOnly(), http.MethodGet, "/api/log/token",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, u.Id, ctx.GetInt("id"))
}

func TestTokenAuthReadOnly_DisabledTokenDenied(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.Status = common.TokenStatusDisabled })
	rec, _ := runTokenAuth(t, TokenAuthReadOnly(), http.MethodGet, "/api/log/token",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTokenAuthReadOnly_BannedUser(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *model.User) { u.Status = common.UserStatusDisabled })
	tk := mkToken(t, u.Id, nil)
	rec, _ := runTokenAuth(t, TokenAuthReadOnly(), http.MethodGet, "/api/log/token",
		map[string]string{"Authorization": "Bearer sk-" + tk.Key})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// TokenOrUserAuth
// ---------------------------------------------------------------------------

func TestTokenOrUserAuth_SessionWins(t *testing.T) {
	r := newSessionRouter()
	cookies := loginSession(t, r, map[string]interface{}{
		"id": 55, "status": common.UserStatusEnabled,
	})
	r.GET("/dual", TokenOrUserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id")})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dual", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "55")
}

func TestTokenOrUserAuth_FallbackToToken(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.UnlimitedQuota = true })
	// No session cookie -> falls back to TokenAuth.
	r := newSessionRouter()
	r.GET("/dual", TokenOrUserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id")})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dual", nil)
	req.Header.Set("Authorization", "Bearer sk-"+tk.Key)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), jsonInt(u.Id))
}

// ---------------------------------------------------------------------------
// SetupContextForToken
// ---------------------------------------------------------------------------

func TestSetupContextForToken_NilToken(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	err := SetupContextForToken(ctx, nil)
	require.Error(t, err)
}

func TestSetupContextForToken_UnlimitedAndModelLimits(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	tk := &model.Token{
		Id: 1, UserId: 2, Key: "k", Name: "n",
		UnlimitedQuota:     false,
		RemainQuota:        123,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-4o,gpt-4",
		Group:              "default",
	}
	err := SetupContextForToken(ctx, tk)
	require.NoError(t, err)
	require.Equal(t, 2, ctx.GetInt("id"))
	require.Equal(t, 123, ctx.GetInt("token_quota"))
	require.True(t, ctx.GetBool("token_model_limit_enabled"))
	limits := ctx.MustGet("token_model_limit").(map[string]bool)
	require.True(t, limits["gpt-4o"])
}

func TestSetupContextForToken_CommonUserSpecificChannelForbidden(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil) // common user
	ctx, rec := newCtx(http.MethodGet, "/", "")
	tk := &model.Token{Id: 9, UserId: u.Id, Key: "k", Name: "n", UnlimitedQuota: true}
	// parts has >1 element -> triggers the specific-channel branch; common user denied.
	err := SetupContextForToken(ctx, tk, "keypart", "5")
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSetupContextForToken_AdminSpecificChannelAllowed(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	ctx, _ := newCtx(http.MethodGet, "/", "")
	tk := &model.Token{Id: 9, UserId: u.Id, Key: "k", Name: "n", UnlimitedQuota: true}
	err := SetupContextForToken(ctx, tk, "keypart", "5")
	require.NoError(t, err)
	require.Equal(t, "5", ctx.GetString("specific_channel_id"))
}

func TestWssAuth_NoOp(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	require.NotPanics(t, func() { WssAuth(ctx) })
}

// jsonInt renders an int as its decimal string.
func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

var _ = constant.ContextKeyTokenGroup
