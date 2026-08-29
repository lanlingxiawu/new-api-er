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
	Id                int                    `json:"id"`
	CreatedAt         int64                  `json:"created_at"`
	Type              int                    `json:"type"`
	RequestId         string                 `json:"request_id"`
	UpstreamRequestId string                 `json:"upstream_request_id,omitempty"`
	ModelName         string                 `json:"model_name"`
	TokenName         string                 `json:"token_name,omitempty"`
	Quota             int                    `json:"quota"`
	PromptTokens      int                    `json:"prompt_tokens"`
	CompletionTokens  int                    `json:"completion_tokens"`
	UseTime           int                    `json:"use_time"`
	IsStream          bool                   `json:"is_stream"`
	Content           string                 `json:"content,omitempty"`
	Other             map[string]interface{} `json:"other,omitempty"`
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
var upstreamLogOtherAllowlist = map[string]bool{
	"frt":                 true,
	"is_stream":           true,
	"upstream_model_name": true,
	"model_ratio":         true,
	"completion_ratio":    true,
	"group_ratio":         true,
	"cache_tokens":        true,
	"stream_status":       true,
	"reasoning_effort":    true,
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
func QueryUpstreamLogs(ctx context.Context, baseURL string, key string, f UpstreamLogFilters, page int, pageSize int) (*UpstreamLogResult, error) {
	select {
	case upstreamLogSem <- struct{}{}:
		defer func() { <-upstreamLogSem }()
	default:
		return nil, ErrUpstreamLogBusy
	}

	start := time.Now()

	queryURL, err := normalizeUpstreamLogURL(baseURL, upstreamLogQueryPath)
	if err != nil {
		return nil, err
	}
	q := buildUpstreamLogQuery(f, page, pageSize)

	result, capable, err := doUpstreamLogRequest(ctx, queryURL+"?"+q.Encode(), key, pageSize)
	if err != nil {
		return nil, err
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
	fbResult, _, err := doUpstreamLogRequest(ctx, fallbackURL, key, pageSize)
	if err != nil {
		return nil, err
	}
	fbResult.Items = filterRecentItems(fbResult.Items, f)
	fbResult.Total = len(fbResult.Items)
	if page > 1 {
		fbResult.Items = []UpstreamLogItem{}
	}
	fbResult.Scope = UpstreamLogScopeRecentFallback
	fbResult.SupportsExact = false
	fbResult.ElapsedMs = time.Since(start).Milliseconds()
	return fbResult, nil
}

// doUpstreamLogRequest 发起单次上游请求并解析响应；capable 表示上游声明了完整筛选能力。
func doUpstreamLogRequest(ctx context.Context, fullURL string, key string, pageSize int) (result *UpstreamLogResult, capable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, false, ErrUpstreamLogBaseURLInvalid
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

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
		// 旧版上游没有该接口：返回不支持能力，交由调用方降级。
		return &UpstreamLogResult{}, false, nil
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
		Quota:             r.Quota,
		PromptTokens:      r.PromptTokens,
		CompletionTokens:  r.CompletionTokens,
		UseTime:           r.UseTime,
		IsStream:          r.IsStream,
		Content:           truncateUpstreamStr(r.Content),
	}
	if len(r.Other) > 0 {
		var otherMap map[string]interface{}
		if err := common.Unmarshal(r.Other, &otherMap); err == nil && len(otherMap) > 0 {
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
