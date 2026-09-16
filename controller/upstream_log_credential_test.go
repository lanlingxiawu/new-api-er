package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUpstreamRecordingServer records the path and credential headers of the last
// request, so a test can assert WHICH upstream endpoint was called and WHICH
// credential was used — the two things that distinguish the site-wide admin query
// from the token-scoped one.
func newUpstreamRecordingServer(t *testing.T, path, auth, apiUser *string, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		*path = r.URL.Path
		*auth = r.Header.Get("Authorization")
		*apiUser = r.Header.Get("New-Api-User")
		// Like a real upstream: the account is an ordinary user, so the admin endpoint refuses it.
		if r.URL.Path == "/api/log" || r.URL.Path == "/api/log/" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, insufficient privileges"}`))
			return
		}
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}}`))
	}))
}

// A channel whose upstream is a new-api gateway is very often typed as an
// OpenAI-compatible channel (or Gemini, Anthropic…), because that is what the
// relay adapter needs. Probing the HK test server proved such channels answer
// /api/log/token/query with HTTP 200 and the filters-v1 capability header, so the
// channel TYPE is not what decides whether the upstream speaks the log API.
// Refusing them purely on type makes a working configuration unreachable.
func TestQueryUpstreamLog_NonNewAPITypeWithNewAPIUpstream(t *testing.T) {
	requireDB(t)

	var gotPath, gotAuth, gotUser string
	var hits int32
	srv := newUpstreamRecordingServer(t, &gotPath, &gotAuth, &gotUser, &hits)
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "sk-gemini-typed"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini // 24 — upstream is still a new-api gateway
		c.Key = channelKey
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success,
		"a channel whose upstream speaks the new-api log API must be queryable regardless of channel type: %s",
		resp.Message)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(1), "the upstream must actually be contacted")
	assert.Equal(t, "Bearer "+channelKey, gotAuth, "the channel key must be forwarded")
}

// The channel key comes first. Even with an account token configured, a working key
// must be used and the account token left untouched.
func TestQueryUpstreamLog_PrefersChannelKeyOverAccountToken(t *testing.T) {
	requireDB(t)

	var gotPath, gotAuth, gotUser string
	srv := newUpstreamRecordingServer(t, &gotPath, &gotAuth, &gotUser, nil)
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "sk-preferred"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = channelKey
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  "pat-must-not-be-used",
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "channel key must be usable: %s", resp.Message)
	assert.Equal(t, "/api/log/token/query", gotPath, "the channel key path must be tried first")
	assert.Equal(t, "Bearer "+channelKey, gotAuth, "the channel key must win over the account token")
	assert.Empty(t, gotUser, "no New-Api-User header on the channel-key path")
}

// "sk 不到就用个人令牌": when the upstream rejects the channel key, the query retries
// with the account token instead of surfacing the failure.
func TestQueryUpstreamLog_FallsBackToAccountTokenWhenKeyRejected(t *testing.T) {
	requireDB(t)

	var paths []string
	var lastAuth, lastUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		lastAuth = r.Header.Get("Authorization")
		lastUser = r.Header.Get("New-Api-User")
		if strings.HasPrefix(r.URL.Path, "/api/log/token") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false}`))
			return
		}
		if r.URL.Path == "/api/log" || r.URL.Path == "/api/log/" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, insufficient privileges"}`))
			return
		}
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":2,"items":[{"id":1,"request_id":"a"}]}}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	const accountToken = "pat-upstream-admin"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = "sk-rejected-by-upstream"
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  accountToken,
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"model_name": "gpt-5"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "a rejected key must fall back to the account token: %s", resp.Message)
	require.NotEmpty(t, paths)
	assert.Equal(t, "/api/log/self", paths[len(paths)-1], "the fallback must read the account's own logs, not the admin endpoint")
	assert.Equal(t, "Bearer "+accountToken, lastAuth, "the account token must be used for the retry")
	assert.Equal(t, "7", lastUser, "New-Api-User must be sent on the retry")
	assert.NotContains(t, rec.Body.String(), accountToken,
		"the account token must never appear in the browser-facing response")
}

// A multi-key channel with no key index used to dead-end on "pick a key index".
// With an account token available it must simply use that instead.
func TestQueryUpstreamLog_MultiKeyWithoutIndexUsesAccountToken(t *testing.T) {
	requireDB(t)

	var gotPath, gotAuth, gotUser string
	srv := newUpstreamRecordingServer(t, &gotPath, &gotAuth, &gotUser, nil)
	defer srv.Close()

	baseURL := srv.URL
	const accountToken = "pat-multi-key"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = "k0\nk1\nk2"
		c.BaseURL = &baseURL
		c.ChannelInfo.IsMultiKey = true
		c.ChannelInfo.MultiKeySize = 3
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  accountToken,
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"model_name": "gpt-5"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success,
		"a multi-key channel with an account token must not dead-end on the key index: %s", resp.Message)
	assert.Equal(t, "/api/log/self", gotPath)
	assert.Equal(t, "Bearer "+accountToken, gotAuth)
	assert.Equal(t, "7", gotUser)
}

// Regression guard for the existing behaviour: with no account token configured the
// query must keep using the relay key against the token-scoped endpoint. This test
// passes today and must keep passing after the change.
func TestQueryUpstreamLog_FallsBackToChannelKeyWithoutAccountToken(t *testing.T) {
	requireDB(t)

	var gotPath, gotAuth, gotUser string
	srv := newUpstreamRecordingServer(t, &gotPath, &gotAuth, &gotUser, nil)
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "sk-relay-only"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeNewAPI
		c.Key = channelKey
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "existing relay-key path must keep working: %s", resp.Message)
	assert.Equal(t, "/api/log/token/query", gotPath,
		"without an account token the query must stay on the token-scoped endpoint")
	assert.Equal(t, "Bearer "+channelKey, gotAuth, "the relay key must be used")
	assert.Empty(t, gotUser, "no New-Api-User header without a configured account user id")
}
