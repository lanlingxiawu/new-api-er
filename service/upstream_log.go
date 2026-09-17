package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 上游日志查询相关的保守实现常量。上线数据若证明需要运营可调，再通过 setting/ 单独设计热配置。
const (
	upstreamLogTotalTimeout      = 8 * time.Second
	upstreamLogResponseHeaderTO  = 5 * time.Second
	upstreamLogDialTimeout       = 3 * time.Second
	upstreamLogMaxResponseBytes  = 2 << 20 // 2 MiB
	upstreamLogMaxConcurrency    = 8
	upstreamLogCapabilityHeader  = "X-NewAPI-Log-Query"
	upstreamLogCapabilityValue   = "filters-v1"
	upstreamLogQueryPath         = "/api/log/token/query"
	upstreamLogRecentFallbackURL = "/api/log/token"
	// 旧版上游 /api/log/token 一次最多返回 MaxRecentItems（默认 1000）条近期日志。
	upstreamLogRecentFallbackMaxItems = 1000
	// 上游账号自己的日志：凭证是上游账号访问令牌（UserAuth）。上游账号是经销商的普通客户而非管理员，
	// 管理员接口 /api/log 对它返回 403，只能用 /api/log/self（已用 nexaxis.ai 真实客户账号实测）。
	upstreamLogAccountPath = "/api/log/self"
)

// 查询范围标记，用于前端明确区分精确、已筛选与仅近期结果。
const (
	UpstreamLogScopeExact          = "exact"
	UpstreamLogScopeFiltered       = "filtered"
	UpstreamLogScopeRecentFallback = "recent_fallback"
)

// 专用错误，控制器据此映射到用户可执行的 i18n 文案，不向浏览器暴露底层网络细节。
var (
	ErrUpstreamLogBaseURLMissing = errors.New("upstream_log: base url missing")
	ErrUpstreamLogBaseURLInvalid = errors.New("upstream_log: base url invalid")
	ErrUpstreamLogBusy           = errors.New("upstream_log: too many concurrent queries")
	ErrUpstreamLogTimeout        = errors.New("upstream_log: upstream timeout")
	ErrUpstreamLogUnauthorized   = errors.New("upstream_log: upstream rejected credential")
	ErrUpstreamLogUnavailable    = errors.New("upstream_log: upstream unavailable")
	ErrUpstreamLogInvalidResp    = errors.New("upstream_log: invalid upstream response")
)

// errUpstreamLogNotFound 表示上游没有该接口（404）。令牌作用域查询据此降级到旧接口；
// 账号范围查询不能降级，只能如实报告不可用。
var errUpstreamLogNotFound = errors.New("upstream_log: endpoint not found")

// UpstreamLogCredential 决定用哪种凭证、打哪个上游接口。
//
// 两种模式的返回范围**不同**，不能互相回退：
//   - AccountScope=false：渠道中转密钥 + /api/log/token/query，只能看到该令牌自己的日志；
//   - AccountScope=true ：上游账号访问令牌 + /api/log/self，看到的是该账号下所有令牌的日志。
//
// 把「账号范围」悄悄降级成「单令牌」会让条数与内容的含义变化而使用者无从察觉，因此禁止。
type UpstreamLogCredential struct {
	// Token 写入 Authorization: Bearer。
	Token string
	// APIUser 非空时作为 New-Api-User 头发送，与账号余额查询保持一致的调用约定。
	APIUser string
	// AccountScope 为真时查询上游账号自己的日志（/api/log/self）。
	AccountScope bool
}

// UpstreamLogFilters 复用现有日志查询语义的筛选条件；空值不下发。
type UpstreamLogFilters struct {
	Type              int
	Username          string
	TokenName         string
	ModelName         string
	StartTimestamp    int64
	EndTimestamp      int64
	Channel           int
	LogId             int
	Group             string
	RequestId         string
	UpstreamRequestId string
}

// UpstreamLogItem 是投影后的上游日志条目；只保留 UI 所需字段，
// other 做 allowlist 投影，凭证样式字段一律丢弃。
type UpstreamLogItem struct {
	Id                int    `json:"id"`
	CreatedAt         int64  `json:"created_at"`
	Type              int    `json:"type"`
	RequestId         string `json:"request_id"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty"`
	ModelName         string `json:"model_name"`
	TokenName         string `json:"token_name,omitempty"`
	// 渠道 / 分组 / IP 是上游视角的值，供上游详情复用本站日志详情排版。
	Channel          int                    `json:"channel,omitempty"`
	ChannelName      string                 `json:"channel_name,omitempty"`
	Group            string                 `json:"group,omitempty"`
	Ip               string                 `json:"ip,omitempty"`
	Quota            int                    `json:"quota"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	UseTime          int                    `json:"use_time"`
	IsStream         bool                   `json:"is_stream"`
	Content          string                 `json:"content,omitempty"`
	Other            map[string]interface{} `json:"other,omitempty"`
}

// UpstreamLogResult 是服务层返回给控制器的标准化结果。
type UpstreamLogResult struct {
	Scope         string            `json:"scope"`
	SupportsExact bool              `json:"upstream_supports_exact"`
	Total         int               `json:"total"`
	Items         []UpstreamLogItem `json:"items"`
	ElapsedMs     int64             `json:"elapsed_ms"`
}

// other 字段 allowlist：只保留无凭证风险的诊断字段。
//
// 上游详情复用本站日志详情的排版，所以这里放行的正是那套排版会读取的键（真实上游
// 的用户级日志接口确实返回这些字段）。保持白名单而非透传：未列出的键一律丢弃。
//
// 刻意不放行：
//   - admin_info / audit_info / op / login_method / user_agent：管理员与审计内部信息；
//   - po（参数覆盖记录）：参数覆盖可以改写请求头，记录内容里可能带有凭证值；
//   - stream_diagnostic_available：会让详情拿上游的请求 ID 去查本站的流式诊断，必然查错。
var upstreamLogOtherAllowlist = map[string]bool{
	// 时延与流
	"frt": true, "is_stream": true, "stream_status": true, "stream_result": true,
	// 模型与请求
	"upstream_model_name": true, "is_model_mapped": true, "reasoning_effort": true,
	"is_system_prompt_overwritten": true, "request_conversion": true, "request_path": true,
	"group": true,
	// 计费
	"billing_mode": true, "billing_source": true, "model_price": true, "model_ratio": true,
	"completion_ratio": true, "group_ratio": true, "user_group_ratio": true, "claude": true,
	"expr_b64": true, "matched_tier": true,
	// 缓存
	"cache_tokens": true, "cache_ratio": true, "cache_creation_tokens": true,
	"cache_creation_ratio": true, "cache_creation_tokens_5m": true, "cache_creation_tokens_1h": true,
	"cache_creation_ratio_5m": true, "cache_creation_ratio_1h": true,
	// 多模态
	"ws": true, "audio": true, "audio_ratio": true, "audio_completion_ratio": true,
	"audio_input": true, "audio_output": true, "text_input": true, "text_output": true,
	"image": true, "image_ratio": true, "image_output": true,
	"audio_input_seperate_price": true, "audio_input_price": true,
	// 内置工具
	"web_search": true, "web_search_call_count": true, "web_search_price": true,
	"file_search": true, "file_search_call_count": true, "file_search_price": true,
	"image_generation_call": true, "image_generation_call_price": true,
	// 违规、退款、拒绝
	"reject_reason": true, "violation_fee_code": true, "violation_fee_marker": true,
	"fee_quota": true, "task_id": true, "reason": true,
	// 订阅
	"subscription_plan_id": true, "subscription_plan_title": true, "subscription_id": true,
	"subscription_pre_consumed": true, "subscription_post_delta": true,
	"subscription_consumed": true, "subscription_remain": true, "subscription_total": true,
}

// 专用的有界信号量与 http.Client，与 relay 连接池完全隔离。
var (
	upstreamLogSem    = make(chan struct{}, upstreamLogMaxConcurrency)
	upstreamLogClient = &http.Client{
		Timeout: upstreamLogTotalTimeout,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: upstreamLogDialTimeout}).DialContext,
			ResponseHeaderTimeout: upstreamLogResponseHeaderTO,
			MaxIdleConns:          upstreamLogMaxConcurrency,
			MaxIdleConnsPerHost:   upstreamLogMaxConcurrency,
			IdleConnTimeout:       30 * time.Second,
		},
	}
)

// normalizeUpstreamLogURL 从渠道 base_url 生成上游日志查询 URL。
// 只接受 http/https，去掉末尾 /v1，禁止 userinfo 与 fragment。
func normalizeUpstreamLogURL(baseURL string, path string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", ErrUpstreamLogBaseURLMissing
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", ErrUpstreamLogBaseURLInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrUpstreamLogBaseURLInvalid
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", ErrUpstreamLogBaseURLInvalid
	}
	trimmed := strings.TrimRight(u.Path, "/")
	trimmed = strings.TrimSuffix(trimmed, "/v1")
	u.Path = trimmed + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// buildUpstreamLogQuery 把筛选条件映射为与通用日志查询一致的 query 参数；空值不下发。
func buildUpstreamLogQuery(f UpstreamLogFilters, page int, pageSize int) url.Values {
	q := url.Values{}
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if f.Type != 0 {
		q.Set("type", strconv.Itoa(f.Type))
	}
	if f.Username != "" {
		q.Set("username", f.Username)
	}
	if f.TokenName != "" {
		q.Set("token_name", f.TokenName)
	}
	if f.ModelName != "" {
		q.Set("model_name", f.ModelName)
	}
	if f.StartTimestamp != 0 {
		q.Set("start_timestamp", strconv.FormatInt(f.StartTimestamp, 10))
	}
	if f.EndTimestamp != 0 {
		q.Set("end_timestamp", strconv.FormatInt(f.EndTimestamp, 10))
	}
	if f.Channel != 0 {
		q.Set("channel", strconv.Itoa(f.Channel))
	}
	if f.LogId != 0 {
		q.Set("log_id", strconv.Itoa(f.LogId))
	}
	if f.Group != "" {
		q.Set("group", f.Group)
	}
	if f.RequestId != "" {
		q.Set("request_id", f.RequestId)
	}
	if f.UpstreamRequestId != "" {
		q.Set("upstream_request_id", f.UpstreamRequestId)
	}
	return q
}

// upstreamLogRawResponse 匹配 new-api 标准 success/message/data 结构。
type upstreamLogRawResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// QueryUpstreamLogs 向上游 new-api 实例发起只读日志查询并返回标准化结果。
// key 为该渠道选定令牌，只写入 Authorization Header，绝不出现在返回值或日志中。
func QueryUpstreamLogs(ctx context.Context, baseURL string, cred UpstreamLogCredential, f UpstreamLogFilters, page int, pageSize int) (*UpstreamLogResult, error) {
	select {
	case upstreamLogSem <- struct{}{}:
		defer func() { <-upstreamLogSem }()
	default:
		return nil, ErrUpstreamLogBusy
	}

	start := time.Now()

	path := upstreamLogQueryPath
	if cred.AccountScope {
		path = upstreamLogAccountPath
	}
	queryURL, err := normalizeUpstreamLogURL(baseURL, path)
	if err != nil {
		return nil, err
	}
	q := buildUpstreamLogQuery(f, page, pageSize)

	result, capable, err := doUpstreamLogRequest(ctx, queryURL+"?"+q.Encode(), cred, pageSize)
	if err != nil {
		if !errors.Is(err, errUpstreamLogNotFound) {
			return nil, err
		}
		// 账号范围查询没有可降级的对象：降到令牌接口会把账号结果换成单令牌结果。
		if cred.AccountScope {
			return nil, ErrUpstreamLogUnavailable
		}
		result, capable = &UpstreamLogResult{}, false
	}

	// 用户日志接口本身就执行完整筛选（含 request_id），不依赖能力头。
	if cred.AccountScope {
		if strings.TrimSpace(f.RequestId) != "" {
			result.Scope = UpstreamLogScopeExact
		} else {
			result.Scope = UpstreamLogScopeFiltered
		}
		result.SupportsExact = true
		result.ElapsedMs = time.Since(start).Milliseconds()
		return result, nil
	}

	if capable {
		// 上游原生执行完整筛选：含请求 ID 时视为精确查询。
		if strings.TrimSpace(f.RequestId) != "" {
			result.Scope = UpstreamLogScopeExact
		} else {
			result.Scope = UpstreamLogScopeFiltered
		}
		result.SupportsExact = true
		result.ElapsedMs = time.Since(start).Milliseconds()
		return result, nil
	}

	// 旧版上游无能力 Header：回退近期数组并本地过滤。
	// 该接口不接受分页参数，只能返回一页近期数据，因此第 2 页开始必定为空，
	// 不能假装还有后续页，否则用户会在翻页时看到重复的第一页内容。
	fallbackURL, err := normalizeUpstreamLogURL(baseURL, upstreamLogRecentFallbackURL)
	if err != nil {
		return nil, err
	}
	// 必须先在整批近期日志上过滤、再截到一页：若按 pageSize 先截断，只会在最新几条里匹配，
	// 高流量渠道上几分钟前的请求就会被误报为「上游没有这条日志」。
	fbResult, _, err := doUpstreamLogRequest(ctx, fallbackURL, cred, upstreamLogRecentFallbackMaxItems)
	if err != nil {
		if errors.Is(err, errUpstreamLogNotFound) {
			return nil, ErrUpstreamLogUnavailable
		}
		return nil, err
	}
	fbResult.Items = filterRecentItems(fbResult.Items, f)
	fbResult.Total = len(fbResult.Items)
	if page > 1 {
		fbResult.Items = []UpstreamLogItem{}
	} else if len(fbResult.Items) > pageSize {
		fbResult.Items = fbResult.Items[:pageSize]
	}
	fbResult.Scope = UpstreamLogScopeRecentFallback
	fbResult.SupportsExact = false
	fbResult.ElapsedMs = time.Since(start).Milliseconds()
	return fbResult, nil
}

// doUpstreamLogRequest 发起单次上游请求并解析响应；capable 表示上游声明了完整筛选能力。
func doUpstreamLogRequest(ctx context.Context, fullURL string, cred UpstreamLogCredential, pageSize int) (result *UpstreamLogResult, capable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, false, ErrUpstreamLogBaseURLInvalid
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Accept", "application/json")
	if cred.APIUser != "" {
		req.Header.Set("New-Api-User", cred.APIUser)
	}

	resp, err := upstreamLogClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutErr(err) {
			return nil, false, ErrUpstreamLogTimeout
		}
		return nil, false, ErrUpstreamLogUnavailable
	}
	defer DrainAndCloseResponseBody(resp)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, ErrUpstreamLogUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		// 旧版上游没有该接口：由调用方决定降级还是报不可用。
		return nil, false, errUpstreamLogNotFound
	case resp.StatusCode >= 500:
		return nil, false, ErrUpstreamLogUnavailable
	case resp.StatusCode != http.StatusOK:
		return nil, false, ErrUpstreamLogUnavailable
	}

	capable = strings.EqualFold(resp.Header.Get(upstreamLogCapabilityHeader), upstreamLogCapabilityValue)

	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamLogMaxResponseBytes+1))
	if err != nil {
		return nil, false, ErrUpstreamLogUnavailable
	}
	if len(body) > upstreamLogMaxResponseBytes {
		return nil, false, ErrUpstreamLogInvalidResp
	}

	parsed, err := parseUpstreamLogBody(body, pageSize)
	if err != nil {
		return nil, false, err
	}
	return parsed, capable, nil
}

// parseUpstreamLogBody 解析上游标准响应；data 既可能是分页对象也可能是数组（近期降级）。
func parseUpstreamLogBody(body []byte, pageSize int) (*UpstreamLogResult, error) {
	var raw upstreamLogRawResponse
	if err := common.Unmarshal(body, &raw); err != nil {
		return nil, ErrUpstreamLogInvalidResp
	}
	if !raw.Success {
		return nil, ErrUpstreamLogInvalidResp
	}
	if len(raw.Data) == 0 {
		return &UpstreamLogResult{Items: []UpstreamLogItem{}}, nil
	}

	dataType := common.GetJsonType(raw.Data)
	rawItems, total, err := extractUpstreamRawItems(raw.Data, dataType)
	if err != nil {
		return nil, err
	}

	items := make([]UpstreamLogItem, 0, len(rawItems))
	for i, ri := range rawItems {
		if i >= pageSize {
			break
		}
		items = append(items, projectUpstreamLogItem(ri))
	}
	if total < len(items) {
		total = len(items)
	}
	return &UpstreamLogResult{Total: total, Items: items}, nil
}

// upstreamRawLog 只解析投影所需字段，忽略上游未知/敏感字段。
type upstreamRawLog struct {
	Id                int             `json:"id"`
	CreatedAt         int64           `json:"created_at"`
	Type              int             `json:"type"`
	RequestId         string          `json:"request_id"`
	UpstreamRequestId string          `json:"upstream_request_id"`
	ModelName         string          `json:"model_name"`
	TokenName         string          `json:"token_name"`
	Channel           int             `json:"channel"`
	ChannelName       string          `json:"channel_name"`
	Group             string          `json:"group"`
	Ip                string          `json:"ip"`
	Quota             int             `json:"quota"`
	PromptTokens      int             `json:"prompt_tokens"`
	CompletionTokens  int             `json:"completion_tokens"`
	UseTime           int             `json:"use_time"`
	IsStream          bool            `json:"is_stream"`
	Content           string          `json:"content"`
	Other             json.RawMessage `json:"other"`
}

func extractUpstreamRawItems(data json.RawMessage, dataType string) ([]upstreamRawLog, int, error) {
	switch dataType {
	case "array":
		var arr []upstreamRawLog
		if err := common.Unmarshal(data, &arr); err != nil {
			return nil, 0, ErrUpstreamLogInvalidResp
		}
		return arr, len(arr), nil
	case "object":
		var page struct {
			Items []upstreamRawLog `json:"items"`
			Total int              `json:"total"`
		}
		if err := common.Unmarshal(data, &page); err != nil {
			return nil, 0, ErrUpstreamLogInvalidResp
		}
		return page.Items, page.Total, nil
	default:
		return nil, 0, ErrUpstreamLogInvalidResp
	}
}

const upstreamLogMaxStringLen = 4096

func truncateUpstreamStr(s string) string {
	if len(s) > upstreamLogMaxStringLen {
		return s[:upstreamLogMaxStringLen]
	}
	return s
}

func projectUpstreamLogItem(r upstreamRawLog) UpstreamLogItem {
	item := UpstreamLogItem{
		Id:                r.Id,
		CreatedAt:         r.CreatedAt,
		Type:              r.Type,
		RequestId:         truncateUpstreamStr(r.RequestId),
		UpstreamRequestId: truncateUpstreamStr(r.UpstreamRequestId),
		ModelName:         truncateUpstreamStr(r.ModelName),
		TokenName:         truncateUpstreamStr(r.TokenName),
		Channel:           r.Channel,
		ChannelName:       truncateUpstreamStr(r.ChannelName),
		Group:             truncateUpstreamStr(r.Group),
		Ip:                truncateUpstreamStr(r.Ip),
		Quota:             r.Quota,
		PromptTokens:      r.PromptTokens,
		CompletionTokens:  r.CompletionTokens,
		UseTime:           r.UseTime,
		IsStream:          r.IsStream,
		Content:           truncateUpstreamStr(r.Content),
	}
	if len(r.Other) > 0 {
		otherRaw := []byte(r.Other)
		// new-api 的 Log.Other 是字符串列，上游日志接口返回的是「装着 JSON 的字符串」；
		// 先剥掉这一层，否则整个 other 会被丢弃。
		var otherStr string
		if common.Unmarshal(otherRaw, &otherStr) == nil {
			otherRaw = []byte(otherStr)
		}
		var otherMap map[string]interface{}
		if err := common.Unmarshal(otherRaw, &otherMap); err == nil && len(otherMap) > 0 {
			projected := make(map[string]interface{}, len(upstreamLogOtherAllowlist))
			for k, v := range otherMap {
				if upstreamLogOtherAllowlist[k] {
					projected[k] = v
				}
			}
			if len(projected) > 0 {
				item.Other = projected
			}
		}
	}
	return item
}

// filterRecentItems 在旧版上游近期数组上做本地过滤，语义与服务端筛选一致。
func filterRecentItems(items []UpstreamLogItem, f UpstreamLogFilters) []UpstreamLogItem {
	out := make([]UpstreamLogItem, 0, len(items))
	for _, it := range items {
		if f.Type != 0 && it.Type != f.Type {
			continue
		}
		if f.ModelName != "" && !strings.Contains(it.ModelName, strings.ReplaceAll(f.ModelName, "%", "")) {
			continue
		}
		if f.TokenName != "" && it.TokenName != f.TokenName {
			continue
		}
		if f.RequestId != "" && it.RequestId != f.RequestId {
			continue
		}
		if f.UpstreamRequestId != "" && it.UpstreamRequestId != f.UpstreamRequestId {
			continue
		}
		if f.LogId != 0 && it.Id != f.LogId {
			continue
		}
		if f.StartTimestamp != 0 && it.CreatedAt < f.StartTimestamp {
			continue
		}
		if f.EndTimestamp != 0 && it.CreatedAt > f.EndTimestamp {
			continue
		}
		out = append(out, it)
	}
	return out
}

func isTimeoutErr(err error) bool {
	var t interface{ Timeout() bool }
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return fmt.Sprintf("%v", err) == context.DeadlineExceeded.Error()
}
