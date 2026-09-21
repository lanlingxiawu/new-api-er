package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUpstreamRecordingServer mimics an official new-api upstream: the token-scoped
// endpoint answers a bare recent array, the account endpoint answers a paged object,
// and the admin endpoint refuses an ordinary account. It records the path and
// credential headers of the last request, so a test can assert WHICH endpoint was
// called and WHICH credential was used.
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
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/log/token" {
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}}`))
	}))
}

// A channel whose upstream is a new-api gateway is very often typed as an
// OpenAI-compatible channel (or Gemini, Anthropic…), because that is what the
// relay adapter needs. The channel TYPE is not what decides whether the upstream
// speaks the log API; refusing them purely on type makes a working configuration
// unreachable.
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

// The account access token comes first: on official new-api it reads /api/log/self,
// which covers the account's whole history and is rate limited far more loosely than
// the relay-key endpoint (same IP, 20 requests per 20 minutes).
func TestQueryUpstreamLog_PrefersAccountTokenOverChannelKey(t *testing.T) {
	requireDB(t)

	var gotPath, gotAuth, gotUser string
	srv := newUpstreamRecordingServer(t, &gotPath, &gotAuth, &gotUser, nil)
	defer srv.Close()

	baseURL := srv.URL
	const accountToken = "pat-preferred"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = "sk-should-not-be-needed"
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  accountToken,
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "the account token must be usable: %s", resp.Message)
	assert.Equal(t, "/api/log/self", gotPath, "the account token path must be tried first")
	assert.Equal(t, "Bearer "+accountToken, gotAuth, "the account token must win over the channel key")
	assert.Equal(t, "7", gotUser, "New-Api-User must be sent on the account path")
	assert.NotContains(t, rec.Body.String(), accountToken,
		"the account token must never appear in the browser-facing response")
	assert.Contains(t, rec.Body.String(), `"checked_account":true`,
		"the response must state that the account's full history was searched")
}

// When the upstream rejects the account token, the query retries with the channel key
// instead of surfacing the failure.
func TestQueryUpstreamLog_FallsBackToChannelKeyWhenAccountTokenRejected(t *testing.T) {
	requireDB(t)

	var paths []string
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		lastAuth = r.Header.Get("Authorization")
		if strings.HasPrefix(r.URL.Path, "/api/log/self") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "sk-still-works"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = channelKey
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  "pat-rejected-by-upstream",
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "a rejected account token must fall back to the channel key: %s", resp.Message)
	require.NotEmpty(t, paths)
	assert.Equal(t, "/api/log/token", paths[len(paths)-1], "the fallback must use the official token endpoint")
	assert.Equal(t, "Bearer "+channelKey, lastAuth, "the channel key must be used for the retry")
}

// A single-key channel whose key returns nothing must keep going and ask the account
// token, which is the only credential that can see the upstream's whole history.
// Stopping at the first empty answer reported "the upstream has no log for this
// request" while the answer was one request away.
func TestQueryUpstreamLog_EmptyKeyResultStillTriesAccountToken(t *testing.T) {
	requireDB(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/log/self" {
			// the account token sees the log; it is simply not in the key's recent page
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":9,"request_id":"req_other"}]}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = "sk-recent-only"
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  "pat-full-history",
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	assert.Contains(t, paths, "/api/log/self")
	assert.Contains(t, rec.Body.String(), "req_x", "the matching upstream log must be returned")
}

// Official /api/log/token is rate limited per IP (20 requests / 20 minutes by default),
// so every key of a multi-key channel draws from the same bucket. Once the upstream
// answers 429, trying the remaining keys can only deepen the limit and fail the same way.
func TestQueryUpstreamLog_RateLimitStopsKeyProbing(t *testing.T) {
	requireDB(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"success":false,"message":"too many requests"}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeGemini
		c.Key = "k0\nk1\nk2"
		c.BaseURL = &baseURL
		c.ChannelInfo.IsMultiKey = true
		c.ChannelInfo.MultiKeySize = 3
	})

	body := map[string]any{
		"channel_id": ch.Id,
		"key_index":  0,
		"filters":    map[string]any{"request_id": "req_x"},
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.False(t, resp.Success, "a rate limited upstream must not be reported as success")
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits), "probing must stop at the first 429")
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

// Without an account token the query uses the relay key against the official
// token-scoped endpoint, and says so: "checked_account" false tells the page that a
// miss only means "not in this key's recent logs", not "the upstream deleted it".
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
	assert.Equal(t, "/api/log/token", gotPath,
		"without an account token the query must stay on the official token endpoint")
	assert.Equal(t, "Bearer "+channelKey, gotAuth, "the relay key must be used")
	assert.Empty(t, gotUser, "no New-Api-User header without a configured account user id")
	assert.Contains(t, rec.Body.String(), `"checked_account":false`)
}

// 上游不是 new-api（例如 sub2api：它的逐请求明细只在自己的管理端，路径与鉴权都不同），
// 两个官方接口都会 404。此时提示必须是「该上游没有这个接口，去它自己的后台查」，
// 而不是「上游不可用，请重试或排查渠道连通性」——上游其实活得好好的。
// 两套凭证仍要各试一次：404 是按路径返回的，换凭证前无从判断另一条路是否存在。
func TestQueryUpstreamLog_NonNewAPIUpstreamReportsMissingEndpoint(t *testing.T) {
	requireDB(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found"))
	}))
	defer srv.Close()

	baseURL := srv.URL
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeOpenAI
		c.Key = "sk-key"
		c.BaseURL = &baseURL
		c.SetSetting(dto.ChannelSettings{
			AccountBalanceToken:  "pat-account",
			AccountBalanceUserID: "7",
		})
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.True(t, strings.HasPrefix(resp.Message, i18n.T(ctx, i18n.MsgUpstreamLogEndpointMissing)),
		"the capability-missing hint must lead the message, got %q", resp.Message)
	assert.NotContains(t, resp.Message, i18n.T(ctx, i18n.MsgUpstreamLogUnavailable))
	assert.Contains(t, resp.Message, "404", "the upstream status code must be visible")
	assert.Equal(t, []string{"/api/log/self", "/api/log/token"}, paths,
		"both credentials must be tried once before reporting the capability as missing")
}

// 上游失败时提示必须带上它真实的状态码与正文：否则管理员只看到一句「上游不可用」，
// 无从判断是网关拦截、鉴权失败还是上游自己报错。同时上游回显的凭证不得外泄。
func TestQueryUpstreamLog_ErrorMessageCarriesUpstreamResponse(t *testing.T) {
	requireDB(t)

	const channelKey = "sk-channel-secret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// 上游把我们发过去的密钥原样回显——真实网关常见行为。
		_, _ = w.Write([]byte(`{"success":false,"message":"无效的令牌 ` + channelKey + `"}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeOpenAI
		c.Key = channelKey
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "req_x"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, i18n.T(ctx, i18n.MsgUpstreamLogUnauthorized),
		"the actionable hint must still lead the message")
	assert.Contains(t, resp.Message, "401", "the upstream status code must be visible")
	assert.Contains(t, resp.Message, "无效的令牌", "the upstream body must be visible")
	assert.NotContains(t, rec.Body.String(), channelKey,
		"a credential echoed back by the upstream must never reach the browser")
}

// 连不上时没有上游响应可展示，也不能把 dial 细节和上游地址漏出去。
func TestQueryUpstreamLog_ConnectionFailureHasNoUpstreamDetail(t *testing.T) {
	requireDB(t)

	baseURL := "http://127.0.0.1:1"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeOpenAI
		c.Key = "sk-x"
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "r"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgUpstreamLogUnavailable), resp.Message,
		"without an upstream response the message must stay the plain hint")
	assert.NotContains(t, resp.Message, "127.0.0.1")
	assert.NotContains(t, resp.Message, "dial")
}
