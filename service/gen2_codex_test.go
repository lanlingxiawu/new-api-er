package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT builds a well-formed 3-part JWT with the given claims payload.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

// ---------------------------------------------------------------------------
// codex_oauth.go
// ---------------------------------------------------------------------------

func TestCodexOAuth_RefreshToken(t *testing.T) {
	ctx := context.Background()

	// empty refresh token
	_, err := refreshCodexOAuthToken(ctx, http.DefaultClient, "url", "cid", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty refresh_token")

	// success
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "rt-1", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt2","expires_in":3600}`))
	}))
	defer srv.Close()

	res, err := refreshCodexOAuthToken(ctx, srv.Client(), srv.URL, "cid", "rt-1")
	require.NoError(t, err)
	assert.Equal(t, "at", res.AccessToken)
	assert.Equal(t, "rt2", res.RefreshToken)
	assert.True(t, res.ExpiresAt.After(res.ExpiresAt.Add(-1)))

	// non-2xx status
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"b","expires_in":10}`))
	}))
	defer srvErr.Close()
	_, err = refreshCodexOAuthToken(ctx, srvErr.Client(), srvErr.URL, "cid", "rt-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status=401")

	// missing fields
	srvMissing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"","refresh_token":"","expires_in":0}`))
	}))
	defer srvMissing.Close()
	_, err = refreshCodexOAuthToken(ctx, srvMissing.Client(), srvMissing.URL, "cid", "rt-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing fields")

	// transport error (connection refused)
	_, err = refreshCodexOAuthToken(ctx, &http.Client{}, "http://127.0.0.1:1/", "cid", "rt-1")
	require.Error(t, err)
}

func TestCodexOAuth_GetHTTPClient(t *testing.T) {
	InitHttpClient()
	// empty proxy => copy of base client with codex timeout
	cl, err := getCodexOAuthHTTPClient("")
	require.NoError(t, err)
	require.NotNil(t, cl)
	assert.Equal(t, defaultHTTPTimeout, cl.Timeout)

	// invalid proxy scheme => error
	_, err = getCodexOAuthHTTPClient("ftp://bad")
	require.Error(t, err)
}

func TestCodexOAuth_ExtractFromJWT(t *testing.T) {
	// account id
	tok := makeJWT(t, map[string]any{
		codexJWTClaimPath: map[string]any{"chatgpt_account_id": " acc-1 "},
		"email":           " e@x.com ",
	})
	acc, ok := ExtractCodexAccountIDFromJWT(tok)
	require.True(t, ok)
	assert.Equal(t, "acc-1", acc)
	email, ok := ExtractEmailFromJWT(tok)
	require.True(t, ok)
	assert.Equal(t, "e@x.com", email)

	// malformed token (not 3 parts)
	_, ok = ExtractCodexAccountIDFromJWT("a.b")
	assert.False(t, ok)
	_, ok = ExtractEmailFromJWT("a.b")
	assert.False(t, ok)

	// claim path missing
	tok2 := makeJWT(t, map[string]any{"other": 1})
	_, ok = ExtractCodexAccountIDFromJWT(tok2)
	assert.False(t, ok)
	_, ok = ExtractEmailFromJWT(tok2)
	assert.False(t, ok)

	// auth claim wrong type
	tok3 := makeJWT(t, map[string]any{codexJWTClaimPath: "notanobject"})
	_, ok = ExtractCodexAccountIDFromJWT(tok3)
	assert.False(t, ok)

	// account id present but empty
	tok4 := makeJWT(t, map[string]any{codexJWTClaimPath: map[string]any{"chatgpt_account_id": "  "}})
	_, ok = ExtractCodexAccountIDFromJWT(tok4)
	assert.False(t, ok)

	// email empty
	tok5 := makeJWT(t, map[string]any{"email": ""})
	_, ok = ExtractEmailFromJWT(tok5)
	assert.False(t, ok)

	// email wrong type
	tok6 := makeJWT(t, map[string]any{"email": 123})
	_, ok = ExtractEmailFromJWT(tok6)
	assert.False(t, ok)

	// bad base64 payload
	_, ok = ExtractEmailFromJWT("h.@@@.s")
	assert.False(t, ok)
}

func TestCodexOAuth_RefreshWithProxyInvalidProxy(t *testing.T) {
	InitHttpClient()
	_, err := RefreshCodexOAuthTokenWithProxy(context.Background(), "rt", "ftp://bad")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// codex_wham_usage.go
// ---------------------------------------------------------------------------

func TestCodexWham_GuardBranches(t *testing.T) {
	ctx := context.Background()
	fns := []func(client *http.Client, base, at, aid string) (int, []byte, error){
		func(c *http.Client, b, a, i string) (int, []byte, error) {
			return FetchCodexWhamUsage(ctx, c, b, a, i)
		},
		func(c *http.Client, b, a, i string) (int, []byte, error) {
			return FetchCodexWhamRateLimitResetCredits(ctx, c, b, a, i)
		},
		func(c *http.Client, b, a, i string) (int, []byte, error) {
			return ConsumeCodexWhamRateLimitResetCredit(ctx, c, b, a, i)
		},
	}
	for i, fn := range fns {
		_, _, err := fn(nil, "http://x", "at", "aid")
		require.Error(t, err, "nil client %d", i)
		_, _, err = fn(http.DefaultClient, "  ", "at", "aid")
		require.Error(t, err, "empty base %d", i)
		_, _, err = fn(http.DefaultClient, "http://x", " ", "aid")
		require.Error(t, err, "empty token %d", i)
		_, _, err = fn(http.DefaultClient, "http://x", "at", " ")
		require.Error(t, err, "empty aid %d", i)
	}
}

func TestCodexWham_Success(t *testing.T) {
	ctx := context.Background()
	var gotPath, gotAuth, gotAccount, gotOriginator string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sc, body, err := FetchCodexWhamUsage(ctx, srv.Client(), srv.URL+"/", "at-1", "acc-1")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, sc)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Equal(t, "/backend-api/wham/usage", gotPath)
	assert.Equal(t, "Bearer at-1", gotAuth)
	assert.Equal(t, "acc-1", gotAccount)
	assert.Equal(t, "codex_cli_rs", gotOriginator)

	_, _, err = FetchCodexWhamRateLimitResetCredits(ctx, srv.Client(), srv.URL, "at", "aid")
	require.NoError(t, err)
	assert.Equal(t, "/backend-api/wham/rate-limit-reset-credits", gotPath)

	_, _, err = ConsumeCodexWhamRateLimitResetCredit(ctx, srv.Client(), srv.URL, "at", "aid")
	require.NoError(t, err)
	assert.Equal(t, "/backend-api/wham/rate-limit-reset-credits/consume", gotPath)
}

// ---------------------------------------------------------------------------
// codex_credential_refresh.go
// ---------------------------------------------------------------------------

func TestCodex_ParseOAuthKey(t *testing.T) {
	_, err := parseCodexOAuthKey("   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty oauth key")

	_, err = parseCodexOAuthKey("{not json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid oauth key json")

	key, err := parseCodexOAuthKey(`{"refresh_token":"rt","access_token":"at"}`)
	require.NoError(t, err)
	assert.Equal(t, "rt", key.RefreshToken)
	assert.Equal(t, "at", key.AccessToken)
}

func TestCodex_RefreshChannelCredential_Guards(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// channel not found
	_, _, err := RefreshCodexChannelCredential(ctx, 999999, CodexCredentialRefreshOptions{})
	require.Error(t, err)

	// wrong channel type
	chWrong := &model.Channel{Id: 7001, Name: "notcodex", Type: constant.ChannelTypeOpenAI, Key: "k", Status: 1}
	require.NoError(t, model.DB.Create(chWrong).Error)
	model.InitChannelCache()
	_, _, err = RefreshCodexChannelCredential(ctx, 7001, CodexCredentialRefreshOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not Codex")

	// codex type but invalid key json
	chBadKey := &model.Channel{Id: 7002, Name: "codexbad", Type: constant.ChannelTypeCodex, Key: "not-json", Status: 1}
	require.NoError(t, model.DB.Create(chBadKey).Error)
	model.InitChannelCache()
	_, _, err = RefreshCodexChannelCredential(ctx, 7002, CodexCredentialRefreshOptions{})
	require.Error(t, err)

	// codex type, valid json but empty refresh_token
	chNoRT := &model.Channel{Id: 7003, Name: "codexnort", Type: constant.ChannelTypeCodex, Key: `{"access_token":"at"}`, Status: 1}
	require.NoError(t, model.DB.Create(chNoRT).Error)
	model.InitChannelCache()
	_, _, err = RefreshCodexChannelCredential(ctx, 7003, CodexCredentialRefreshOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh_token is required")
}
