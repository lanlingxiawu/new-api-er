package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, "sk-secret", UpstreamLogFilters{RequestId: "req_abc"}, 1, 50)
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

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{ModelName: "gpt-5"}, 1, 50)
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

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
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

	first, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{}, 1, 50)
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	assert.Equal(t, 2, first.Total)

	second, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{}, 2, 50)
	require.NoError(t, err)
	assert.Empty(t, second.Items)
	assert.Equal(t, UpstreamLogScopeRecentFallback, second.Scope)
}

func TestQueryUpstreamLogs_Unauthorized(t *testing.T) {
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		return http.StatusUnauthorized, `{"success":false}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogUnauthorized)
}

func TestQueryUpstreamLogs_InvalidResponse(t *testing.T) {
	srv := newUpstreamServer(t, true, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":"not-an-array-or-object"}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, "k", UpstreamLogFilters{}, 1, 50)
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

	_, err := QueryUpstreamLogs(context.Background(), "https://host", "k", UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogBusy)
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
