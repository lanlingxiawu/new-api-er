package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"root", "https://host", "https://host/api/log/token", false},
		{"v1 suffix", "https://host/v1", "https://host/api/log/token", false},
		{"prefix", "https://host/prefix", "https://host/prefix/api/log/token", false},
		{"prefix v1", "https://host/prefix/v1", "https://host/prefix/api/log/token", false},
		{"trailing slash", "https://host/", "https://host/api/log/token", false},
		{"http ok", "http://host", "http://host/api/log/token", false},
		{"empty", "", "", true},
		{"non http", "ftp://host", "", true},
		{"userinfo rejected", "https://user:pass@host", "", true},
		{"fragment rejected", "https://host/#frag", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeUpstreamLogURL(tc.base, upstreamLogTokenRecentPath)
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

func newUpstreamServer(t *testing.T, handler func(r *http.Request) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := handler(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// 官方 new-api 的中转密钥只能访问 /api/log/token：该接口不接受任何筛选或分页参数，
// 密钥只走 Authorization 头（v0.10.8 起），绝不能出现在 URL 里被上游访问日志记录。
func TestQueryUpstreamLogs_TokenScopeUsesOfficialRecentEndpoint(t *testing.T) {
	var paths []string
	var gotAuth, gotQuery string
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		paths = append(paths, r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		return http.StatusOK, `{"success":true,"data":[{"id":1,"request_id":"req_a","model_name":"gpt-5","other":{"frt":0.4,"api_key":"leak"}}]}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "sk-secret"}, UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, []string{"/api/log/token"}, paths, "官方上游没有 /api/log/token/query，不得再请求它")
	assert.Equal(t, "Bearer sk-secret", gotAuth)
	assert.Empty(t, gotQuery, "密钥与筛选条件都不能写进 URL")
	assert.Equal(t, UpstreamLogScopeRecent, res.Scope)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "req_a", res.Items[0].RequestId)
	assert.NotContains(t, res.Items[0].Other, "api_key")
}

// 官方 /api/log/token 只返回最近 1000 条、无法筛选，因此匹配必须在整批数据上做，
// 再截到一页；先截到 page_size 会把稍早的请求误报成「上游没有这条日志」。
func TestQueryUpstreamLogs_RecentMatchesBeyondPageSize(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":[{"id":7,"request_id":"req_7"},{"id":6,"request_id":"req_6"},{"id":5,"request_id":"req_5"},{"id":4,"request_id":"req_4"},{"id":3,"request_id":"req_3"},{"id":2,"request_id":"req_target"},{"id":1,"request_id":"req_target"}]}`
	})
	defer srv.Close()

	matched, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{RequestId: "req_target"}, 1, 5)
	require.NoError(t, err)
	require.Len(t, matched.Items, 2)
	assert.Equal(t, 2, matched.Items[0].Id)
	assert.Equal(t, 2, matched.Total)

	unfiltered, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 5)
	require.NoError(t, err)
	assert.Len(t, unfiltered.Items, 5)
	assert.Equal(t, 7, unfiltered.Total)
}

// /api/log/token 不接受分页参数，只能返回一批近期日志。假装存在第 2 页会把第 1 页
// 的内容重放给用户。
func TestQueryUpstreamLogs_RecentHasNoSecondPage(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
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
	assert.Equal(t, UpstreamLogScopeRecent, second.Scope)
}

// v0.10.8 之前官方日志表没有 request_id 列，返回的条目一律没有该字段：按请求 ID
// 追溯在这些版本上不可能成立，必须如实告知升级，而不是报成「上游没有这条日志」。
func TestQueryUpstreamLogs_TokenScopeLegacyUpstreamWithoutRequestId(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":[{"id":1,"model_name":"gpt-5"},{"id":2,"model_name":"gpt-4"}]}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogVersionUnsupported)

	// 没有按请求 ID 追溯时，旧版上游仍可按其它条件浏览，不应报版本错误。
	res, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{ModelName: "gpt-5"}, 1, 50)
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
}

// 官方 /api/log/token 挂 CriticalRateLimit（默认同一 IP 20 分钟 20 次）。429 必须是
// 独立错误：控制器据此立即停止轮询其余密钥，页面提示「稍后再试」而不是「上游不可用」。
func TestQueryUpstreamLogs_RateLimited(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusTooManyRequests, `{"success":false,"message":"too many requests"}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogRateLimited)
}

func TestQueryUpstreamLogs_Unauthorized(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusUnauthorized, `{"success":false}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogUnauthorized)
}

func TestQueryUpstreamLogs_InvalidResponse(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":"not-an-array-or-object"}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL, UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogInvalidResp)
}

func TestQueryUpstreamLogs_BusyWhenSaturated(t *testing.T) {
	// fully saturate the semaphore, next call must fail fast.
	for range upstreamLogMaxConcurrency {
		upstreamLogSem <- struct{}{}
	}
	defer func() {
		for range upstreamLogMaxConcurrency {
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
	var gotPath, gotAuth, gotUser, gotModel string
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUser = r.Header.Get("New-Api-User")
		gotModel = r.URL.Query().Get("model_name")
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
	assert.Equal(t, "gpt-5", gotModel, "官方 /api/log/self 自己执行筛选与分页")
	assert.Equal(t, UpstreamLogScopeFiltered, res.Scope)
	assert.Equal(t, 3, res.Total)
}

// 账号令牌 + request_id 是唯一能查上游全部历史的路径（官方 ≥ v0.10.8）。
func TestQueryUpstreamLogs_AccountScopeExact(t *testing.T) {
	var gotRequestId string
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		gotRequestId = r.URL.Query().Get("request_id")
		return http.StatusOK, `{"success":true,"data":{"total":1,"items":[{"id":1,"request_id":"req_a","model_name":"gpt-5"}]}}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, "req_a", gotRequestId)
	assert.Equal(t, UpstreamLogScopeExact, res.Scope)
	require.Len(t, res.Items, 1)
}

// v0.10.8 之前的官方 /api/log/self 忽略 request_id，返回该账号最新一页日志。
// 不本地复核就会把别的请求当成查询结果展示出来。
func TestQueryUpstreamLogs_AccountScopeLegacyUpstreamIgnoresRequestId(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":{"total":42,"items":[{"id":1,"model_name":"gpt-5"},{"id":2,"model_name":"gpt-4"}]}}`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogVersionUnsupported)
}

// 上游确实没有这条日志时返回 0 条，而不是版本错误：账号令牌查的是全部历史，
// 这个「没有」是确定答案。
func TestQueryUpstreamLogs_AccountScopeExactNotFound(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":true,"data":{"total":0,"items":[]}}`
	})
	defer srv.Close()

	res, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{RequestId: "req_a"}, 1, 50)
	require.NoError(t, err)
	assert.Empty(t, res.Items)
	assert.Equal(t, UpstreamLogScopeExact, res.Scope)
}

// 账号范围与令牌范围的结果含义不同（该账号全部令牌 vs 单个令牌），不能互相降级：
// 账号接口不可用时如实报错，由控制器决定是否换另一套凭证。
func TestQueryUpstreamLogs_AccountScopeNeverFallsBackToTokenScope(t *testing.T) {
	var paths []string
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		paths = append(paths, r.URL.Path)
		return http.StatusNotFound, `not found`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "pat-token", APIUser: "7", AccountScope: true},
		UpstreamLogFilters{}, 1, 50)
	require.ErrorIs(t, err, ErrUpstreamLogEndpointMissing)
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
}

// 阶梯计价详情不是「有没有 expr_b64」这一个开关：档位、计价单位、固定单价、命中的请求
// 规则、计费 token 明细与用量事实都由 other 里的独立字段驱动。漏放行其中任何一个，
// 上游详情就会渲染出一张有表头没数据的阶梯表——比不渲染更容易被当成「上游没计费」。
func TestProjectUpstreamLogItem_KeepsTieredBillingFields(t *testing.T) {
	raw := `{"id":3,"type":2,"other":{"billing_mode":"tiered_expr","expr_b64":"e30=","matched_tier":"tier-2",
	  "billing_unit":"token","fixed_price":0.25,"image_count":2,
	  "billing_tokens":{"p":10,"c":20},"image_cache_tokens":4,
	  "usage_facts":{"units":3},"request_rules":[{"name":"long-context"}],
	  "tool_surcharges":[{"name":"lookup","count":1,"price":5}]}}`
	var r upstreamRawLog
	require.NoError(t, common.Unmarshal([]byte(raw), &r))

	other := projectUpstreamLogItem(r).Other
	for _, k := range []string{"billing_unit", "fixed_price", "image_count", "billing_tokens",
		"image_cache_tokens", "usage_facts", "request_rules", "tool_surcharges"} {
		assert.Contains(t, other, k, "other.%s drives the tiered pricing table", k)
	}
}

// reject_reason 在官方是 SetAdmin 字段，两个用户级接口都会连 admin_info 一起把它剥掉。
// 放行它只会在白名单里留一条永远取不到值的死条目。
func TestProjectUpstreamLogItem_DropsRejectReason(t *testing.T) {
	var r upstreamRawLog
	require.NoError(t, common.Unmarshal(
		[]byte(`{"id":1,"other":{"frt":1,"reject_reason":"blocked","admin_info":{"reject_reason":"blocked"}}}`), &r))

	other := projectUpstreamLogItem(r).Other
	assert.Contains(t, other, "frt")
	assert.NotContains(t, other, "reject_reason")
	assert.NotContains(t, other, "admin_info")
}

// 官方 GetUserLogs 只读固定的一组参数。多下发 username / channel 不会报错，上游会静默
// 忽略并返回未筛选的一页日志——那一页被当成筛选结果展示，就是「按渠道过滤却看到全部渠道」。
func TestBuildUpstreamLogQuery_OnlyOfficiallySupportedParams(t *testing.T) {
	q := buildUpstreamLogQuery(UpstreamLogFilters{
		Type: 2, Username: "alice", TokenName: "tk", ModelName: "gpt-5",
		StartTimestamp: 10, EndTimestamp: 20, Channel: 9, Group: "vip",
		RequestId: "req_1", UpstreamRequestId: "up_1",
	}, 2, 30)

	for _, k := range []string{"p", "page_size", "type", "token_name", "model_name",
		"start_timestamp", "end_timestamp", "group", "request_id", "upstream_request_id"} {
		assert.True(t, q.Has(k), "官方 GetUserLogs 会读取 %q", k)
	}
	for _, k := range []string{"username", "channel", "log_id"} {
		assert.False(t, q.Has(k), "官方 GetUserLogs 不读 %q，下发它只会让结果看起来已筛选", k)
	}
}

// 账号接口按自己认识的条件分页，渠道与用户名必须本地复核：否则页面拿到的是该账号最新
// 一页的全部渠道日志，却被标成「已按渠道筛选」。
func TestQueryUpstreamLogs_AccountScopeRechecksChannelAndUsernameLocally(t *testing.T) {
	var gotQuery url.Values
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		gotQuery = r.URL.Query()
		return http.StatusOK, `{"success":true,"data":{"total":3,"items":[
		  {"id":1,"request_id":"a","channel":5,"username":"alice"},
		  {"id":2,"request_id":"b","channel":7,"username":"alice"},
		  {"id":3,"request_id":"c","channel":5,"username":"bob"}]}}`
	})
	defer srv.Close()

	cred := UpstreamLogCredential{Token: "pat", APIUser: "7", AccountScope: true}
	res, err := QueryUpstreamLogs(context.Background(), srv.URL, cred, UpstreamLogFilters{Channel: 5}, 1, 50)
	require.NoError(t, err)
	assert.False(t, gotQuery.Has("channel"), "官方接口不认 channel，不得下发")
	require.Len(t, res.Items, 2, "只能留下该渠道的日志")
	assert.Equal(t, 2, res.Total, "total 必须反映复核后的结果，不能沿用上游未筛选的条数")
	for _, it := range res.Items {
		assert.Equal(t, 5, it.Channel)
	}
	assert.Equal(t, UpstreamLogScopeFiltered, res.Scope)

	byUser, err := QueryUpstreamLogs(context.Background(), srv.URL, cred, UpstreamLogFilters{Username: "bob"}, 1, 50)
	require.NoError(t, err)
	assert.False(t, gotQuery.Has("username"), "官方接口不认 username，不得下发")
	require.Len(t, byUser.Items, 1)
	assert.Equal(t, 3, byUser.Items[0].Id)
}

// /api/log/token 什么参数都不接受，所有条件都得在本地复核，页面上填的条件才不会被悄悄丢掉。
func TestFilterRecentItems_AppliesEveryFilterLocally(t *testing.T) {
	items := []UpstreamLogItem{
		{Id: 1, Channel: 5, Username: "alice", Group: "vip", ModelName: "gpt-5", CreatedAt: 100},
		{Id: 2, Channel: 7, Username: "alice", Group: "vip", ModelName: "gpt-5", CreatedAt: 200},
		{Id: 3, Channel: 5, Username: "bob", Group: "default", ModelName: "gpt-4", CreatedAt: 300},
	}
	cases := []struct {
		name    string
		filters UpstreamLogFilters
		wantIds []int
	}{
		{"channel", UpstreamLogFilters{Channel: 5}, []int{1, 3}},
		{"username", UpstreamLogFilters{Username: "bob"}, []int{3}},
		{"group", UpstreamLogFilters{Group: "vip"}, []int{1, 2}},
		{"combined", UpstreamLogFilters{Channel: 5, Group: "vip"}, []int{1}},
		{"no filter", UpstreamLogFilters{}, []int{1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int, 0, len(items))
			for _, it := range filterRecentItems(items, tc.filters) {
				got = append(got, it.Id)
			}
			assert.Equal(t, tc.wantIds, got)
		})
	}
}

// 404 是「上游没有这个能力」，与 5xx / 连不上的「上游不可用」必须分开：本站只按官方
// new-api 的能力对接，上游若是别的网关实现（sub2api 的逐请求明细只在它自己的管理端），
// 提示要让管理员去上游后台查，而不是去重试或排查渠道连通性。
func TestQueryUpstreamLogs_MissingEndpointIsNotUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		accountScope bool
		wantPath     string
	}{
		{name: "token scope", accountScope: false, wantPath: upstreamLogTokenRecentPath},
		{name: "account scope", accountScope: true, wantPath: upstreamLogAccountPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
				paths = append(paths, r.URL.Path)
				return http.StatusNotFound, `404 page not found`
			})
			defer srv.Close()

			cred := UpstreamLogCredential{Token: "k"}
			if tc.accountScope {
				cred = UpstreamLogCredential{Token: "pat", APIUser: "7", AccountScope: true}
			}
			_, err := QueryUpstreamLogs(context.Background(), srv.URL, cred, UpstreamLogFilters{}, 1, 50)

			require.ErrorIs(t, err, ErrUpstreamLogEndpointMissing)
			require.NotErrorIs(t, err, ErrUpstreamLogUnavailable)
			assert.Equal(t, []string{tc.wantPath}, paths, "a 404 must not make the query try the other endpoint")
		})
	}
}

// 边界：只有 404 表示能力缺失，其余非 200 仍是「上游不可用」。
func TestQueryUpstreamLogs_NonNotFoundStatusStaysUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
				return status, `boom`
			})
			defer srv.Close()

			_, err := QueryUpstreamLogs(context.Background(), srv.URL,
				UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)

			require.ErrorIs(t, err, ErrUpstreamLogUnavailable)
			require.NotErrorIs(t, err, ErrUpstreamLogEndpointMissing)
		})
	}
}

// 失败时必须带回上游真实的状态码与正文：只给一句「上游不可用」，管理员无法区分是网关
// 拦截、鉴权失败还是上游自己报错，只能去翻上游日志或抓包。
func TestQueryUpstreamLogs_FailureCarriesUpstreamResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		sentinel error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"success":false,"message":"无效的令牌"}`, sentinel: ErrUpstreamLogUnauthorized},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `too many requests`, sentinel: ErrUpstreamLogRateLimited},
		{name: "endpoint missing", status: http.StatusNotFound, body: `404 page not found`, sentinel: ErrUpstreamLogEndpointMissing},
		{name: "gateway error", status: http.StatusBadGateway, body: `<html><title>502 Bad Gateway</title></html>`, sentinel: ErrUpstreamLogUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
				return tc.status, tc.body
			})
			defer srv.Close()

			_, err := QueryUpstreamLogs(context.Background(), srv.URL,
				UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)

			require.ErrorIs(t, err, tc.sentinel, "the sentinel must survive the wrapping")
			var detail *UpstreamLogHTTPError
			require.ErrorAs(t, err, &detail)
			assert.Equal(t, tc.status, detail.StatusCode)
			assert.NotEmpty(t, detail.Snippet)
		})
	}
}

// 200 但解析不了时，正文正是判断「上游到底是什么东西」的唯一线索。
func TestQueryUpstreamLogs_InvalidResponseCarriesBody(t *testing.T) {
	srv := newUpstreamServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `<!doctype html><html><body>login</body></html>`
	})
	defer srv.Close()

	_, err := QueryUpstreamLogs(context.Background(), srv.URL,
		UpstreamLogCredential{Token: "k"}, UpstreamLogFilters{}, 1, 50)

	require.ErrorIs(t, err, ErrUpstreamLogInvalidResp)
	var detail *UpstreamLogHTTPError
	require.ErrorAs(t, err, &detail)
	assert.Equal(t, http.StatusOK, detail.StatusCode)
	assert.Contains(t, detail.Snippet, "login")
}

// 上游的错误回显会把我们发出去的密钥原样写回来，直接展示等于把渠道密钥印在浏览器里。
func TestSanitizeUpstreamSnippet(t *testing.T) {
	const credential = "AbCd1234EfGh5678"

	t.Run("masks the credential we sent", func(t *testing.T) {
		out := sanitizeUpstreamSnippet([]byte(`{"message":"invalid key `+credential+`"}`), credential)
		assert.NotContains(t, out, credential)
		assert.Contains(t, out, "***")
	})

	t.Run("masks credential-shaped values we did not send", func(t *testing.T) {
		out := sanitizeUpstreamSnippet([]byte(`unauthorized: sk-live-abcdef123456 / Bearer eyJhbGciOi`), "")
		assert.NotContains(t, out, "sk-live-abcdef123456")
		assert.NotContains(t, out, "eyJhbGciOi")
	})

	t.Run("collapses whitespace and control characters", func(t *testing.T) {
		out := sanitizeUpstreamSnippet([]byte("line one\r\n\tline\x00 two   three"), "")
		assert.Equal(t, "line one line two three", out)
	})

	t.Run("truncates to the snippet budget", func(t *testing.T) {
		out := sanitizeUpstreamSnippet([]byte(strings.Repeat("x", upstreamLogSnippetMaxBytes*3)), "")
		assert.Equal(t, upstreamLogSnippetMaxBytes+1, len([]rune(out)), "truncated text plus the ellipsis")
		assert.True(t, strings.HasSuffix(out, "…"))
	})

	t.Run("empty body yields empty snippet", func(t *testing.T) {
		assert.Empty(t, sanitizeUpstreamSnippet(nil, "k"))
	})
}
