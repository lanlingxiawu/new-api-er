package controller

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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

// TestQueryUpstreamLog_RejectsNonNewAPIChannel verifies a non-New-API channel is
// rejected before any upstream request.
func TestQueryUpstreamLog_RejectsNonNewAPIChannel(t *testing.T) {
	requireDB(t)

	baseURL := "http://127.0.0.1:1" // must never be dialed
	ch := mkChannel(t, func(c *model.Channel) {
		c.Type = 1 // OpenAI, not New API
		c.Key = "sk-x"
		c.BaseURL = &baseURL
	})

	body := map[string]any{"channel_id": ch.Id, "filters": map[string]any{"request_id": "r"}}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", body)
	asAdmin(ctx, 1)
	QueryUpstreamLog(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
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

	t.Run("multi key index missing", func(t *testing.T) {
		channel := mkChannel(t, func(c *model.Channel) {
			c.Type = constant.ChannelTypeNewAPI
			c.Key = "key-0\nkey-1"
			c.BaseURL = &baseURL
			c.ChannelInfo.IsMultiKey = true
		})
		requestID := uniq("missing-key-index")
		mkUpstreamTraceLog(t, requestID, channel.Id, "req-never-sent", `{}`)
		ctx, rec := newCtx(t, http.MethodPost, "/api/log/upstream/query", map[string]any{
			"local_request_id": requestID,
		})
		asAdmin(ctx, 1)
		QueryUpstreamLog(ctx)
		assert.False(t, decodeResp(t, rec).Success)
	})

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
