package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkUpstreamTraceLog(t *testing.T, requestID string, channelID int, upstreamRequestID string, other string) *model.Log {
	t.Helper()
	requireDB(t)
	log := &model.Log{
		Id:                nextTestID(),
		UserId:            nextTestID(),
		CreatedAt:         time.Now().Unix(),
		Type:              model.LogTypeConsume,
		RequestId:         requestID,
		UpstreamRequestId: upstreamRequestID,
		ChannelId:         channelID,
		Other:             other,
	}
	require.NoError(t, model.LOG_DB.Create(log).Error)
	t.Cleanup(func() {
		model.LOG_DB.Where("request_id = ?", requestID).Delete(&model.Log{})
	})
	return log
}

// newUpstreamCountingServer returns a test upstream that records how many times
// it was contacted and echoes a minimal valid New API log-query response.
func newUpstreamCountingServer(t *testing.T, hits *int32, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if r.URL.Path == "/api/log/token/query" {
			w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req_x","model_name":"gpt-5"}]}}`))
	}))
}

// TestQueryUpstreamLog_GuardBlocksNonAdminAndSpareUpstream verifies the admin
// guard: unauthenticated and common-user callers are rejected before any
// upstream request is made. This is the security contract from the design doc
// (§9): a non-admin must never be able to reach the upstream through this route.
func TestQueryUpstreamLog_GuardBlocksNonAdminAndSpareUpstream(t *testing.T) {
	requireDB(t)

	var hits int32
	srv := newUpstreamCountingServer(t, &hits, nil)
	defer srv.Close()

	// A real New API channel exists and points at the test upstream, so the only
	// thing standing between a caller and the upstream is AdminAuth.
	baseURL := srv.URL
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeNewAPI
		c.Key = "sk-secret-guard"
		c.BaseURL = &baseURL
	})

	body := map[string]any{
		"channel_id": ch.Id,
		"filters":    map[string]any{"request_id": "req_x"},
	}

	// ── no credential → 401, handler never runs ────────────────────────────
	anon, anonRec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	middleware.AdminAuth()(anon)
	if !anon.IsAborted() {
		QueryUpstreamLog(anon)
	}
	assert.True(t, anon.IsAborted(), "request without Authorization must be rejected")
	assert.Equal(t, http.StatusUnauthorized, anonRec.Code)

	// ── common user → 403, handler never runs ──────────────────────────────
	const password = "guard-pass-123"
	hashed, err := common.Password2Hash(password)
	require.NoError(t, err)
	commonUser := mkUser(t, func(u *model.User) {
		u.Password = hashed
		u.Role = common.RoleCommonUser
		u.Status = common.UserStatusEnabled
	})
	loginCtx, loginRec := newCtx(t, http.MethodPost, "/api/user/login?turnstile=", map[string]any{
		"username": commonUser.Username,
		"password": password,
	})
	Login(loginCtx)
	loginResp := decodeResp(t, loginRec)
	require.True(t, loginResp.Success, "login failed: %s", loginResp.Message)
	var bundle struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, common.Unmarshal(loginResp.Data, &bundle))
	require.NotEmpty(t, bundle.AccessToken)

	commonCtx, commonRec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	commonCtx.Request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	middleware.AdminAuth()(commonCtx)
	if !commonCtx.IsAborted() {
		QueryUpstreamLog(commonCtx)
	}
	assert.True(t, commonCtx.IsAborted(), "common user must be rejected by AdminAuth")
	assert.Equal(t, http.StatusForbidden, commonRec.Code)

	// The upstream must not have been contacted by any blocked request.
	assert.EqualValues(t, 0, atomic.LoadInt32(&hits),
		"blocked requests must never reach the upstream")
}

// TestQueryUpstreamLog_AdminReachesUpstream verifies the happy path: an admin
// caller reaches the upstream, the channel key is forwarded as a Bearer token
// (server-side only), and the key never appears in the browser-facing response.
func TestQueryUpstreamLog_AdminReachesUpstream(t *testing.T) {
	requireDB(t)

	var hits int32
	var gotAuth string
	srv := newUpstreamCountingServer(t, &hits, &gotAuth)
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "sk-secret-admin"
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeNewAPI
		c.Key = channelKey
		c.BaseURL = &baseURL
	})

	body := map[string]any{
		"channel_id": ch.Id,
		"filters":    map[string]any{"request_id": "req_x"},
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	// The handler itself only trusts AdminAuth's context; drive it directly.
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "admin query failed: %s", resp.Message)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(1),
		"admin query must reach the upstream")
	assert.Equal(t, "Bearer "+channelKey, gotAuth,
		"channel key must be forwarded as a Bearer token")
	// The channel key must never be echoed back to the browser.
	assert.NotContains(t, rec.Body.String(), channelKey,
		"channel key must never appear in the response body")
}

// TestQueryUpstreamLog_UnreachableUpstreamFailsCleanly verifies that an upstream we
// cannot reach produces a clean, translated failure rather than a panic or a raw
// network error leaking to the browser.
//
// This test used to be TestQueryUpstreamLog_RejectsNonNewAPIChannel, asserting that a
// non-New-API channel was refused *before* any upstream request. That contract is gone:
// the channel type says nothing about whether the upstream speaks the new-api log API
// (see TestQueryUpstreamLog_NonNewAPITypeWithNewAPIUpstream). Left as it was, the test
// still passed — but only because the dial to port 1 fails, i.e. it silently stopped
// testing what its name claimed.
func TestQueryUpstreamLog_UnreachableUpstreamFailsCleanly(t *testing.T) {
	requireDB(t)

	baseURL := "http://127.0.0.1:1" // nothing listens here
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = 1 // OpenAI-typed channel: no longer refused on type grounds
		c.Key = "sk-x"
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "r"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.NotContains(t, resp.Message, "127.0.0.1",
		"the internal upstream address must not leak to the browser")
	assert.NotContains(t, resp.Message, "dial",
		"raw network errors must not leak to the browser")
}

func TestQueryUpstreamLog_LocalRequestIdResolvesTrustedRoute(t *testing.T) {
	requireDB(t)

	var hits int32
	var gotRequestID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotRequestID = r.URL.Query().Get("request_id")
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req-upstream"}]}}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	const channelKey = "trusted-route-key"
	channel := mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeNewAPI
		c.Key = channelKey
		c.BaseURL = &baseURL
	})
	localRequestID := uniq("local-request")
	mkUpstreamTraceLog(t, localRequestID, channel.Id, "req-upstream", "")

	body := map[string]any{
		"local_request_id": localRequestID,
		"channel_id":       nextTestID(),
		"key_index":        99,
		"filters":          map[string]any{"request_id": "client-override"},
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "local trace query failed: %s", resp.Message)
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
	assert.Equal(t, "req-upstream", gotRequestID)
	assert.Contains(t, rec.Body.String(), localRequestID)
	assert.Contains(t, rec.Body.String(), "req-upstream")
	assert.NotContains(t, rec.Body.String(), channelKey)
	assert.NotContains(t, rec.Body.String(), "client-override")
}

func TestQueryUpstreamLog_LocalTraceFailuresDoNotReachUpstream(t *testing.T) {
	requireDB(t)

	var hits int32
	srv := newUpstreamCountingServer(t, &hits, nil)
	defer srv.Close()
	baseURL := srv.URL

	t.Run("request not found", func(t *testing.T) {
		ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
			"local_request_id": uniq("missing-local"),
		})
		asAdmin(ctx, 1)
		QueryUpstreamLog(ctx)
		assert.False(t, decodeResp(t, rec).Success)
	})

	t.Run("upstream request id missing", func(t *testing.T) {
		channel := mkChannel(t, func(c *model.Channel) {
			c.Type = constant.ChannelTypeNewAPI
			c.Key = "unused-key"
			c.BaseURL = &baseURL
		})
		requestID := uniq("missing-upstream")
		mkUpstreamTraceLog(t, requestID, channel.Id, "", "")
		ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
			"local_request_id": requestID,
		})
		asAdmin(ctx, 1)
		QueryUpstreamLog(ctx)
		assert.False(t, decodeResp(t, rec).Success)
	})

	// A multi-key trace whose log lacks the key index used to be a third case here:
	// it was refused without contacting the upstream. It now probes the channel's keys
	// instead, so it lives in TestQueryUpstreamLog_TraceProbesKeysWhenLogLacksIndex and
	// the cases that remain genuinely never reach the upstream.

	assert.EqualValues(t, 0, atomic.LoadInt32(&hits))
}

// TestQueryUpstreamLog_TraceKeyIndexFallback covers the recovery path for logs
// written before the channel key index was recorded: the trace still resolves
// the channel and upstream request ID from the local log, but the admin may
// supply the key index that the log is missing instead of hitting a dead end.
func TestQueryUpstreamLog_TraceKeyIndexFallback(t *testing.T) {
	requireDB(t)

	var hits int32
	var gotAuth string
	var gotRequestID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth = r.Header.Get("Authorization")
		gotRequestID = r.URL.Query().Get("request_id")
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":7,"request_id":"req-upstream"}]}}`))
	}))
	defer srv.Close()
	baseURL := srv.URL

	newMultiKeyChannel := func(t *testing.T) *model.Channel {
		return mkChannel(t, func(c *model.Channel) {
			c.Type = constant.ChannelTypeNewAPI
			c.Key = "key-0\nkey-1"
			c.BaseURL = &baseURL
			c.ChannelInfo.IsMultiKey = true
			c.ChannelInfo.MultiKeySize = 2
		})
	}

	t.Run("client key index is used when the log has none", func(t *testing.T) {
		channel := newMultiKeyChannel(t)
		requestID := uniq("fallback-key-index")
		mkUpstreamTraceLog(t, requestID, channel.Id, "req-upstream", `{}`)

		ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
			"local_request_id": requestID,
			"key_index":        1,
		})
		asAdmin(ctx, 1)
		QueryUpstreamLog(ctx)

		resp := decodeResp(t, rec)
		require.True(t, resp.Success, "fallback trace query failed: %s", resp.Message)
		assert.Equal(t, "Bearer key-1", gotAuth)
		assert.Equal(t, "req-upstream", gotRequestID)
		// The UI needs to know the key index was guessed, not recorded.
		assert.Contains(t, rec.Body.String(), `"key_index_from_log":false`)
	})

	t.Run("log key index still wins over the client value", func(t *testing.T) {
		channel := newMultiKeyChannel(t)
		requestID := uniq("recorded-key-index")
		mkUpstreamTraceLog(t, requestID, channel.Id, "req-upstream",
			`{"admin_info":{"multi_key_index":0}}`)

		ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
			"local_request_id": requestID,
			"key_index":        1,
		})
		asAdmin(ctx, 1)
		QueryUpstreamLog(ctx)

		resp := decodeResp(t, rec)
		require.True(t, resp.Success, "recorded trace query failed: %s", resp.Message)
		assert.Equal(t, "Bearer key-0", gotAuth)
		assert.Contains(t, rec.Body.String(), `"key_index_from_log":true`)
	})

	assert.EqualValues(t, 2, atomic.LoadInt32(&hits))
}

// newKeyProbeServer answers the token-scoped query with the log only for requests
// authenticated with hitKey, and with an empty page for every other key. It records
// each request's Authorization header in order, so a test can assert exactly which
// keys were probed and where probing stopped.
func newKeyProbeServer(t *testing.T, hitKey string, seen *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		*seen = append(*seen, auth)
		mu.Unlock()
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		if auth == "Bearer "+hitKey {
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[{"id":9,"request_id":"req-probe"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":0,"items":[]}}`))
	}))
}

func mkProbeChannel(t *testing.T, baseURL string, statuses map[int]int) *model.Channel {
	t.Helper()
	return mkChannel(t, func(c *model.Channel) {
		c.Type = constant.ChannelTypeNewAPI
		c.Key = "key-0\nkey-1\nkey-2"
		c.BaseURL = &baseURL
		c.ChannelInfo.IsMultiKey = true
		c.ChannelInfo.MultiKeySize = 3
		c.ChannelInfo.MultiKeyStatusList = statuses
	})
}

// An old log that never recorded which key served the request used to dead-end on
// "trace key index missing". A log belongs to exactly one key, so the trace now tries
// the channel's keys in order and stops at the first one that has it.
func TestQueryUpstreamLog_TraceProbesKeysWhenLogLacksIndex(t *testing.T) {
	requireDB(t)

	var mu sync.Mutex
	var seen []string
	srv := newKeyProbeServer(t, "key-1", &seen, &mu)
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, nil)
	requestID := uniq("probe-key-index")
	mkUpstreamTraceLog(t, requestID, channel.Id, "req-probe", `{}`)

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"local_request_id": requestID,
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "a trace without a recorded key index must probe the keys: %s", resp.Message)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Bearer key-0", "Bearer key-1"}, seen,
		"probing must stop at the first key that has the log and never touch key-2")
	assert.Contains(t, rec.Body.String(), "req-probe")
	// Found by an exact request ID, so the result is precise: no "may not match" warning.
	assert.Contains(t, rec.Body.String(), `"key_index_from_log":true`)
}

// A disabled key is skipped rather than probed.
func TestQueryUpstreamLog_TraceProbeSkipsDisabledKeys(t *testing.T) {
	requireDB(t)

	var mu sync.Mutex
	var seen []string
	srv := newKeyProbeServer(t, "key-2", &seen, &mu)
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, map[int]int{0: common.ChannelStatusEnabled + 1})
	requestID := uniq("probe-skip-disabled")
	mkUpstreamTraceLog(t, requestID, channel.Id, "req-probe", `{}`)

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"local_request_id": requestID,
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "probing must still find the log on an enabled key: %s", resp.Message)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Bearer key-1", "Bearer key-2"}, seen, "a disabled key must not be probed")
}

// When no key has the log, every enabled key is tried and the answer is "not found
// upstream" — a result, not an error.
func TestQueryUpstreamLog_TraceProbeExhaustsKeysWhenNoneHasTheLog(t *testing.T) {
	requireDB(t)

	var mu sync.Mutex
	var seen []string
	srv := newKeyProbeServer(t, "no-such-key", &seen, &mu)
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, nil)
	requestID := uniq("probe-not-found")
	mkUpstreamTraceLog(t, requestID, channel.Id, "req-probe", `{}`)

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"local_request_id": requestID,
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "not finding the log on any key is an answer, not an error: %s", resp.Message)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Bearer key-0", "Bearer key-1", "Bearer key-2"}, seen,
		"every enabled key must be tried before concluding the log is absent")
}

// newRejectingProbeServer answers with an empty page for every key except those in
// rejected, which get 401 — an upstream that revoked some of the channel's keys.
func newRejectingProbeServer(t *testing.T, rejected map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-NewAPI-Log-Query", "filters-v1")
		if rejected[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":0,"items":[]}}`))
	}))
}

// Earlier keys answered "no such log" and only the last key was rejected. The rejected key
// cannot see anything, so the honest answer is still "not found upstream" — reporting
// "credential rejected" sends the admin chasing a key problem for a log that is simply absent.
func TestQueryUpstreamLog_TraceProbeLastKeyRejectedAfterEmptyAnswersIsNotFound(t *testing.T) {
	requireDB(t)

	srv := newRejectingProbeServer(t, map[string]bool{"key-2": true})
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, nil)
	requestID := uniq("probe-last-rejected")
	mkUpstreamTraceLog(t, requestID, channel.Id, "req-probe", `{}`)

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"local_request_id": requestID,
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "an empty answer from a usable key must win over a later rejection: %s", resp.Message)
	assert.Contains(t, rec.Body.String(), `"items":[]`)
}

// When no key could query at all there is no answer to fall back on: the rejection is the
// real problem and must still be reported.
func TestQueryUpstreamLog_TraceProbeAllKeysRejectedStillReportsRejection(t *testing.T) {
	requireDB(t)

	srv := newRejectingProbeServer(t, map[string]bool{"key-0": true, "key-1": true, "key-2": true})
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, nil)
	requestID := uniq("probe-all-rejected")
	mkUpstreamTraceLog(t, requestID, channel.Id, "req-probe", `{}`)

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"local_request_id": requestID,
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgUpstreamLogUnauthorized), resp.Message)
}

// Browsing a multi-key channel is unchanged: probing only applies to request-ID traces,
// where a hit is unambiguous. Without a key index the admin still picks one.
func TestQueryUpstreamLog_BrowseMultiKeyWithoutIndexStillRequiresSelection(t *testing.T) {
	requireDB(t)

	var hits int32
	srv := newUpstreamCountingServer(t, &hits, nil)
	defer srv.Close()

	channel := mkProbeChannel(t, srv.URL, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
		"channel_id": channel.Id,
		"filters":    map[string]any{"model_name": "gpt-5"},
	})
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	assert.False(t, decodeResp(t, rec).Success, "browsing a multi-key channel still needs an explicit key")
	assert.EqualValues(t, 0, atomic.LoadInt32(&hits), "browsing must never probe keys")
}
