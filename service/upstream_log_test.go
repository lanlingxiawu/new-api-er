package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUpstreamLogURL(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{"root", "https://host", "https://host/api/log/token/query", false},
		{"v1 suffix", "https://host/v1", "https://host/api/log/token/query", false},
		{"prefix", "https://host/prefix", "https://host/prefix/api/log/token/query", false},
		{"prefix v1", "https://host/prefix/v1", "https://host/prefix/api/log/token/query", false},
		{"trailing slash", "https://host/", "https://host/api/log/token/query", false},
		{"http ok", "http://host", "http://host/api/log/token/query", false},
		{"empty", "", "", true},
		{"non http", "ftp://host", "", true},
		{"userinfo rejected", "https://user:pass@host", "", true},
		{"fragment rejected", "https://host/#frag", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeUpstreamLogURL(tc.base, upstreamLogQueryPath)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildUpstreamLogQuery_OmitsEmpty(t *testing.T) {
	q := buildUpstreamLogQuery(UpstreamLogFilters{RequestId: "req_1", Type: 2}, 1, 50)
	assert.Equal(t, "req_1", q.Get("request_id"))
	assert.Equal(t, "2", q.Get("type"))
	assert.Equal(t, "1", q.Get("p"))
	assert.Equal(t, "50", q.Get("page_size"))
	// empty optional filters must not be sent
	assert.False(t, q.Has("username"))
	assert.False(t, q.Has("group"))
	assert.False(t, q.Has("channel"))
}

func TestProjectUpstreamLogItem_OtherAllowlist(t *testing.T) {
	r := upstreamRawLog{
		Id:        7,
		RequestId: "req_x",
		Other:     []byte(`{"frt":0.4,"is_stream":true,"admin_info":{"secret":"x"},"api_key":"sk-leak"}`),
	}
	item := projectUpstreamLogItem(r)
	assert.Equal(t, 7, item.Id)
	assert.Contains(t, item.Other, "frt")
	assert.Contains(t, item.Other, "is_stream")
	assert.NotContains(t, item.Other, "admin_info")
	assert.NotContains(t, item.Other, "api_key")
}

// The upstream log detail reuses the local log detail layout, so the projection must keep
// every field that layout renders. Probing a real nexaxis.ai user log showed the upstream
// does return them (request conversion, billing, cache tiers, tiered pricing, stream
// settlement ...); dropping them here is what made the upstream pane look bare.
// What must still never pass: admin/audit internals and anything credential-shaped.
func TestProjectUpstreamLogItem_KeepsDetailLayoutFields(t *testing.T) {
	raw := `{"id":9,"type":2,"request_id":"req","channel":12,"channel_name":"up-ch","group":"vip","ip":"1.2.3.4",
	  "other":{"request_conversion":["OpenAI Compatible","Google Gemini"],"request_path":"/pg/chat/completions",
	  "billing_mode":"ratio","billing_source":"wallet","model_price":-1,"user_group_ratio":0.8,
	  "cache_creation_tokens_5m":3,"cache_creation_ratio_1h":2,"expr_b64":"e30=","matched_tier":"t1",
	  "stream_result":{"usage_source":"upstream","settlement_state":"settled","effective_content":true},
	  "admin_info":{"use_channel":["1"]},"audit_info":{"route":"/x"},"api_key":"sk-leak",
	  "stream_diagnostic_available":true}}`
	var r upstreamRawLog
	require.NoError(t, common.Unmarshal([]byte(raw), &r))

	out, err := common.Marshal(projectUpstreamLogItem(r))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, common.Unmarshal(out, &got))

	for _, k := range []string{"channel", "channel_name", "group", "ip"} {
		assert.Contains(t, got, k, "top-level %q is rendered by the log detail layout", k)
	}
	other, _ := got["other"].(map[string]any)
	for _, k := range []string{"request_conversion", "request_path", "billing_mode", "billing_source",
		"model_price", "user_group_ratio", "cache_creation_tokens_5m", "cache_creation_ratio_1h",
		"expr_b64", "matched_tier", "stream_result"} {
		assert.Contains(t, other, k, "other.%s is rendered by the log detail layout", k)
	}
	// stream_diagnostic_available would make the layout look up OUR server's stream
	// diagnostic by the upstream's request id, which can only ever miss.
	for _, k := range []string{"admin_info", "audit_info", "api_key", "stream_diagnostic_available"} {
		assert.NotContains(t, other, k, "other.%s must never reach the browser", k)
	}
}

// A real new-api upstream stores Log.Other as a string, so its log APIs return `other` as a
// JSON string holding the object, not as an object. Dropping that shape empties every detail
// section fed by `other` (conversion chain, billing, cache) against any real upstream.
func TestProjectUpstreamLogItem_OtherAsJSONString(t *testing.T) {
	cases := []struct {
		name      string
		other     string
		wantKeys  []string
		wantEmpty bool
	}{
		{
			name:     "json string with allowlisted and forbidden keys",
			other:    `"{\"request_conversion\":[\"OpenAI Compatible\",\"Google Gemini\"],\"frt\":412,\"model_ratio\":1.5,\"admin_info\":{\"use_channel\":[\"1\"]},\"po\":[{\"header\":\"x\"}]}"`,
			wantKeys: []string{"request_conversion", "frt", "model_ratio"},
		},
		{name: "empty string", other: `""`, wantEmpty: true},
		{name: "string that is not json", other: `"not json"`, wantEmpty: true},
		{name: "string holding a non-object", other: `"[1,2]"`, wantEmpty: true},
		{name: "null", other: `null`, wantEmpty: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r upstreamRawLog
			require.NoError(t, common.Unmarshal([]byte(`{"id":1,"other":`+tc.other+`}`), &r))
			item := projectUpstreamLogItem(r)
			if tc.wantEmpty {
				assert.Empty(t, item.Other)
				return
			}
			for _, k := range tc.wantKeys {
				assert.Contains(t, item.Other, k)
			}
			assert.NotContains(t, item.Other, "admin_info")
			assert.NotContains(t, item.Other, "po")
		})
	}
}

func newUpstreamServer(t *testing.T, capable bool, handler func(r *http.Request) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := handler(r)
		if capable && r.URL.Path == "/api/log/token/query" {
			w.Header().Set(upstreamLogCapabilityHeader, upstreamLogCapabilityValue)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestQueryUpstreamLogs_ExactScopeAndAuth(t *testing.T) {
	var gotAuth string
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		gotAuth = r.Header.Get("Authorization")
		assert.Equal(t, "req_abc", r.URL.Query().Get("request_id"))
		return http.StatusOK, `{"success":true,"data":{"total":1,"items":[{"id":9,"request_id":"req_abc","model_name":"gpt-5","quota":100,"other":{"frt":0.4,"api_key":"leak"}}]}}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "sk-secret"}, UpstreamLogFilters{RequestId: "req_abc"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, UpstreamLogScopeExact, res.Scope)
	assert.True(t, res.SupportsExact)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "req_abc", res.Items[0].RequestId)
	assert.NotContains(t, res.Items[0].Other, "api_key")
	assert.Equal(t, "Bearer sk-secret", gotAuth)
	// token must never appear in returned data
	assert.NotContains(t, res.Items[0].Content, "sk-secret")
}

func TestQueryUpstreamLogs_FilteredScopeNoRequestId(t *testing.T) {
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":{"total":0,"items":[]}}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{ModelName: "gpt-5"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, UpstreamLogScopeFiltered, res.Scope)
}

func TestQueryUpstreamLogs_RecentFallback(t *testing.T) {
	srv := newUpstreamServer(t, false, func(r *http.Request) (int, string) {
		if r.URL.Path == "/api/log/token/query" {
			return http.StatusNotFound, `not found`
		}
		// legacy /api/log/token returns a recent array
		return http.StatusOK, `{"success":true,"data":[{"id":1,"request_id":"req_a","model_name":"gpt-5"},{"id":2,"request_id":"req_b","model_name":"gpt-4"}]}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, UpstreamLogScopeRecentFallback, res.Scope)
	assert.False(t, res.SupportsExact)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "req_a", res.Items[0].RequestId)
}

// The legacy /api/log/token endpoint takes no page parameter, so it can only
// ever return one page of recent logs. Pretending page 2 exists would replay
// page 1 to the caller, so later pages must come back empty.
func TestQueryUpstreamLogs_RecentFallbackHasNoSecondPage(t *testing.T) {
	srv := newUpstreamServer(t, false, func(r *http.Request) (int, string) {
		if r.URL.Path == "/api/log/token/query" {
			return http.StatusNotFound, `not found`
		}
		return http.StatusOK, `{"success":true,"data":[{"id":1,"request_id":"req_a"},{"id":2,"request_id":"req_b"}]}`
	})
	defer srv.Close()

	first, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	assert.Equal(t, 2, first.Total)

	second, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 2, 50)
	require.NoError(t, err)
	assert.Empty(t, second.Items)
	assert.Equal(t, UpstreamLogScopeRecentFallback, second.Scope)
}

func TestQueryUpstreamLogs_Unauthorized(t *testing.T) {
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		return http.StatusUnauthorized, `{"success":false}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogUnauthorized)
}

func TestQueryUpstreamLogs_InvalidResponse(t *testing.T) {
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":"not-an-array-or-object"}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogInvalidResp)
}

func TestQueryUpstreamLogs_BusyWhenSaturated(t *testing.T) {
	// fully saturate the semaphore, next call must fail fast.
	for i := 0; i < upstreamLogMaxConcurrency; i++ {
		upstreamLogSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < upstreamLogMaxConcurrency; i++ {
			<-upstreamLogSem
		}
	}()

	_, err := QueryUpstreamLogs(context.Background(), "https://host", UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogBusy)
}

// An upstream account token belongs to an ordinary user of the upstream site (the
// reseller's customer), not an admin: the admin endpoint /api/log answers 403. Probing
// nexaxis.ai with a real customer account confirmed it (/api/log 403, /api/log/self 200,
// request_id filter honoured). So the account token must query /api/log/self.
func TestQueryUpstreamLogs_AccountTokenUsesSelfLogEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotUser string
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUser = r.Header.Get("New-Api-User")
		if r.URL.Path == "/api/log" || r.URL.Path == "/api/log/" {
			return http.StatusForbidden, `{"success":false,"message":"Unauthorized, insufficient privileges"}`
		}
		return http.StatusOK, `{"success":true,"data":{"total":3,"items":[{"id":1,"request_id":"a","model_name":"gpt-5"}]}}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat-token", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{ModelName: "gpt-5"}, 1, 50)
	require.NoError(t, err, "an ordinary upstream account must be able to read its own logs")
	assert.Equal(t, "/api/log/self", gotPath, "the account token must use the user log endpoint, never the admin one")
	assert.Equal(t, "Bearer pat-token", gotAuth)
	assert.Equal(t, "7", gotUser, "New-Api-User must be sent, mirroring the balance query")
	assert.Equal(t, UpstreamLogScopeFiltered, res.Scope)
	assert.True(t, res.SupportsExact)
	assert.Equal(t, 3, res.Total)
}

// The token-scoped endpoint returns only ONE token's logs. Falling back to it after an
// account-scoped 404 would silently turn "every token of this account" into "this one
// key", changing what the row count and contents mean with nothing to signal it. So an
// account-scoped query must fail loudly instead of degrading.
func TestQueryUpstreamLogs_AccountScopeNeverFallsBackToTokenScope(t *testing.T) {
	var paths []string
	srv := newUpstreamServer(t, false, func(r *http.Request) (int, string) {
		paths = append(paths, r.URL.Path)
		return http.StatusNotFound, `not found`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat-token", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogUnavailable)
	assert.Equal(t, []string{"/api/log/self"}, paths,
		"an account-scoped query must not retry the token-scoped endpoint")
}

func TestFilterRecentItems(t *testing.T) {
	items := []UpstreamLogItem{
		{Id: 1, Type: 2, RequestId: "a", ModelName: "gpt-5", CreatedAt: 100},
		{Id: 2, Type: 5, RequestId: "b", ModelName: "gpt-4", CreatedAt: 200},
	}
	out := filterRecentItems(items, UpstreamLogFilters{Type: 2})
	require.Len(t, out, 1)
	assert.Equal(t, "a", out[0].RequestId)

	out = filterRecentItems(items, UpstreamLogFilters{StartTimestamp: 150})
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].Id)

	_ = strings.TrimSpace("")
}
