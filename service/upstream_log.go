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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 上游日志查询相关的保守实现常量。上线数据若证明需要运营可调，再通过 setting/ 单独设计热配置。
const (
	upstreamLogTotalTimeout     = 8 * time.Second
	upstreamLogResponseHeaderTO = 5 * time.Second
	upstreamLogDialTimeout      = 3 * time.Second
	upstreamLogMaxResponseBytes = 2 << 20 // 2 MiB
	upstreamLogMaxConcurrency   = 8
	// 中转密钥能访问的官方接口只有这一个：不接受任何筛选或分页参数，一次返回该令牌最近
	// MaxRecentItems（官方默认 1000）条日志，并受 CriticalRateLimit 限流。
	upstreamLogTokenRecentPath     = "/api/log/token"
	upstreamLogTokenRecentMaxItems = 1000
	// 上游账号自己的日志：凭证是上游账号访问令牌（UserAuth）。上游账号是经销商的普通客户而非管理员，
	// 管理员接口 /api/log 对它返回 403，只能用 /api/log/self（已用 nexaxis.ai 真实客户账号实测）。
	// 该接口自行执行筛选与分页，是唯一能查上游全部历史的路径。
	upstreamLogAccountPath = "/api/log/self"
)

// 查询范围标记，用于前端明确区分精确、已筛选与仅近期结果。
const (
	UpstreamLogScopeExact    = "exact"
	UpstreamLogScopeFiltered = "filtered"
	UpstreamLogScopeRecent   = "recent"
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
	ErrUpstreamLogRateLimited    = errors.New("upstream_log: upstream rate limited")
	// ErrUpstreamLogVersionUnsupported 表示上游版本记不下或查不了请求 ID：官方 v0.10.8
	// 才有 logs.request_id，更早的版本要么不返回该字段，要么忽略 request_id 筛选并返回
	// 无关日志。据此提示升级，而不是把无关日志当结果、或谎报「上游没有这条日志」。
	ErrUpstreamLogVersionUnsupported = errors.New("upstream_log: upstream version does not support request id")
	// ErrUpstreamLogEndpointMissing 表示上游根本没有 new-api 的日志接口（404）。
	// 本站只按官方 new-api 的能力对接，上游若是别的网关实现（例如 sub2api，它的逐请求
	// 明细只在自己的管理端、路径与字段都不同），这里就是终点。它与「上游不可用」是两回事：
	// 上游进程活着、只是不提供这个能力，提示必须让管理员知道去上游自己的后台查，
	// 而不是去重试或排查渠道连通性。
	ErrUpstreamLogEndpointMissing = errors.New("upstream_log: upstream has no new-api log endpoint")
)

// errUpstreamLogNotFound 表示上游没有该接口（404）。两种查询都不降级：结果含义不同，
// 只能如实报告，由控制器决定是否换另一套凭证。
var errUpstreamLogNotFound = errors.New("upstream_log: endpoint not found")

// upstreamLogSnippetMaxBytes 是允许带回给管理员的上游正文上限。够看清一条错误 JSON
// 或网关的 HTML 错误页开头，又不至于把整页 HTML 灌进提示条。
const upstreamLogSnippetMaxBytes = 400

// UpstreamLogHTTPError 在保留原有哨兵错误语义的前提下，附带上游真实的状态码与正文片段。
// 控制器据此把「上游到底回了什么」拼进给管理员的提示；errors.Is 仍按哨兵匹配，
// 所有既有分支（换凭证、不降级、映射 i18n 键）都不受影响。
type UpstreamLogHTTPError struct {
	StatusCode int
	// Snippet 已截断并脱敏，可以直接展示给管理员。
	Snippet string
	err     error
}

func (e *UpstreamLogHTTPError) Error() string {
	if e.Snippet == "" {
		return fmt.Sprintf("%v (upstream status %d)", e.err, e.StatusCode)
	}
	return fmt.Sprintf("%v (upstream status %d: %s)", e.err, e.StatusCode, e.Snippet)
}

func (e *UpstreamLogHTTPError) Unwrap() error { return e.err }

func newUpstreamLogHTTPError(sentinel error, status int, snippet string) error {
	return &UpstreamLogHTTPError{StatusCode: status, Snippet: snippet, err: sentinel}
}

// withUpstreamLogDetail 把 from 携带的上游响应信息挪到另一个哨兵上，用于 404 改判为
// 「能力缺失」这类语义转换，避免转换过程把正文丢掉。
func withUpstreamLogDetail(sentinel error, from error) error {
	var detail *UpstreamLogHTTPError
	if errors.As(from, &detail) {
		return newUpstreamLogHTTPError(sentinel, detail.StatusCode, detail.Snippet)
	}
	return sentinel
}

// readUpstreamSnippet 读取失败响应的开头并脱敏；读失败时返回空串，不影响错误本身。
func readUpstreamSnippet(body io.Reader, credToken string) string {
	raw, err := io.ReadAll(io.LimitReader(body, upstreamLogSnippetMaxBytes+1))
	if err != nil && len(raw) == 0 {
		return ""
	}
	return sanitizeUpstreamSnippet(raw, credToken)
}

// sanitizeUpstreamSnippet 把上游正文压成一行可展示的短文本：折叠空白、去控制字符、
// 抹掉凭证样式的内容、按字符数截断。
//
// 抹掉本次使用的凭证是关键一步：上游的错误回显（例如「invalid key sk-xxx」）会把我们
// 发出去的密钥原样写回来，直接展示等于把渠道密钥印在浏览器里。
func sanitizeUpstreamSnippet(raw []byte, credToken string) string {
	s := string(raw)
	if credToken != "" {
		s = strings.ReplaceAll(s, credToken, "***")
	}
	s = upstreamLogCredentialPattern.ReplaceAllString(s, "***")
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == ' ' {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > upstreamLogSnippetMaxBytes {
		out = string([]rune(out)[:upstreamLogSnippetMaxBytes]) + "…"
	}
	return out
}

// 凭证样式：sk- 前缀的密钥，以及 Authorization 头被回显的情况。
var upstreamLogCredentialPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9._-]{4,}|bearer\s+[A-Za-z0-9._-]{4,})`)

// UpstreamLogCredential 决定用哪种凭证、打哪个上游接口。
//
// 两种模式的返回范围**不同**，不能互相回退：
//   - AccountScope=false：渠道中转密钥 + /api/log/token，只能看到该令牌最近 1000 条日志；
//   - AccountScope=true ：上游账号访问令牌 + /api/log/self，看到的是该账号下所有令牌的全部日志。
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
//
// 没有 LogId：官方用户级接口返回前会用 assignDisplayLogIds 把 id 改写成页内序号 1..N，
// 拿它跟本站记录的日志 ID 比对只会命中随机一条，这个筛选条件无法正确实现。
type UpstreamLogFilters struct {
	Type              int
	Username          string
	TokenName         string
	ModelName         string
	StartTimestamp    int64
	EndTimestamp      int64
	Channel           int
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
	// Username 是上游账号自己的用户名，官方用户级接口原样返回；本地按用户名复核要用它。
	Username string `json:"username,omitempty"`
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
	Scope     string            `json:"scope"`
	Total     int               `json:"total"`
	Items     []UpstreamLogItem `json:"items"`
	ElapsedMs int64             `json:"elapsed_ms"`
}

// other 字段 allowlist：只保留无凭证风险的诊断字段。
//
// 上游详情复用本站日志详情的排版，所以这里放行的正是那套排版会读取的键（真实上游
// 的用户级日志接口确实返回这些字段）。保持白名单而非透传：未列出的键一律丢弃。
//
// 刻意不放行：
//   - admin_info / audit_info / op / login_method / user_agent：管理员与审计内部信息；
//   - po（参数覆盖记录）：参数覆盖可以改写请求头，记录内容里可能带有凭证值；
//   - stream_diagnostic_available：会让详情拿上游的请求 ID 去查本站的流式诊断，必然查错；
//   - reject_reason：官方写在 admin_info 下，两个用户级接口都会连 admin_info 一起剥掉，
//     放行它只会在白名单里留一条永远取不到值的死条目。
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
	// 阶梯计价：expr_b64 决定详情是否渲染阶梯表，档位、单价、命中规则与用量事实决定表里
	// 有没有数据。只放行前两个会得到一张空表，所以这一组必须整组放行。
	"expr_b64": true, "matched_tier": true, "billing_unit": true, "fixed_price": true,
	"image_count": true, "billing_tokens": true, "image_cache_tokens": true,
	"usage_facts": true, "request_rules": true, "tool_surcharges": true,
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
	// 违规、退款
	"violation_fee_code": true, "violation_fee_marker": true,
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

// buildUpstreamLogQuery 只下发官方用户级日志接口真正会读的参数；空值不下发。
//
// 官方 GetUserLogs 读取的全部 query 参数就是这些：p、page_size、type、start_timestamp、
// end_timestamp、token_name、model_name、group、request_id、upstream_request_id。
// username 与 channel 不在其中，下发了会被静默忽略、上游照常返回未筛选的一页日志——
// 再把那一页标成「已按条件筛选」，管理员就会把别的渠道的日志当成自己要找的记录。
// 因此这两个条件一律由 matchesLocalOnlyFilters 在本地复核。
func buildUpstreamLogQuery(f UpstreamLogFilters, page int, pageSize int) url.Values {
	q := url.Values{}
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if f.Type != 0 {
		q.Set("type", strconv.Itoa(f.Type))
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

	var result *UpstreamLogResult
	var err error
	if cred.AccountScope {
		result, err = queryUpstreamAccountLogs(ctx, baseURL, cred, f, page, pageSize)
	} else {
		result, err = queryUpstreamTokenRecentLogs(ctx, baseURL, cred, f, page, pageSize)
	}
	if err != nil {
		return nil, err
	}
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result, nil
}

// queryUpstreamAccountLogs 用上游账号访问令牌查 /api/log/self：上游自己执行筛选与分页，
// 覆盖该账号的全部历史。
func queryUpstreamAccountLogs(ctx context.Context, baseURL string, cred UpstreamLogCredential, f UpstreamLogFilters, page int, pageSize int) (*UpstreamLogResult, error) {
	queryURL, err := normalizeUpstreamLogURL(baseURL, upstreamLogAccountPath)
	if err != nil {
		return nil, err
	}
	result, err := doUpstreamLogRequest(ctx, queryURL+"?"+buildUpstreamLogQuery(f, page, pageSize).Encode(), cred, pageSize)
	if err != nil {
		if errors.Is(err, errUpstreamLogNotFound) {
			return nil, withUpstreamLogDetail(ErrUpstreamLogEndpointMissing, err)
		}
		return nil, err
	}

	requestId := strings.TrimSpace(f.RequestId)
	localFiltered := false
	if requestId != "" {
		// 本地复核上游是否真的按请求 ID 筛选过：v0.10.8 之前的官方版本直接忽略该参数，
		// 返回的是该账号最新一页日志，照单全收就会把别的请求当成查询结果展示。
		matched := make([]UpstreamLogItem, 0, len(result.Items))
		for _, it := range result.Items {
			if it.RequestId == requestId {
				matched = append(matched, it)
			}
		}
		if len(matched) == 0 && len(result.Items) > 0 {
			return nil, ErrUpstreamLogVersionUnsupported
		}
		if len(matched) != len(result.Items) {
			result.Items, localFiltered = matched, true
		}
	}
	// username / channel 官方接口不认，只能在本地复核，否则展示的是未按这两个条件筛过的一页。
	if kept := filterLocalOnly(result.Items, f); len(kept) != len(result.Items) {
		result.Items, localFiltered = kept, true
	}
	if localFiltered {
		// 上游只按自己认识的条件分页，本地复核后本页条数会变，total 只能如实反映复核后的结果。
		result.Total = len(result.Items)
	}
	if requestId != "" {
		result.Scope = UpstreamLogScopeExact
	} else {
		result.Scope = UpstreamLogScopeFiltered
	}
	return result, nil
}

// filterLocalOnly 复核官方接口不接受的筛选条件（username、channel）。
func filterLocalOnly(items []UpstreamLogItem, f UpstreamLogFilters) []UpstreamLogItem {
	if f.Username == "" && f.Channel == 0 {
		return items
	}
	out := make([]UpstreamLogItem, 0, len(items))
	for _, it := range items {
		if matchesLocalOnlyFilters(it, f) {
			out = append(out, it)
		}
	}
	return out
}

// matchesLocalOnlyFilters 判断条目是否满足官方接口不接受、只能本地复核的筛选条件。
func matchesLocalOnlyFilters(it UpstreamLogItem, f UpstreamLogFilters) bool {
	if f.Username != "" && it.Username != f.Username {
		return false
	}
	if f.Channel != 0 && it.Channel != f.Channel {
		return false
	}
	return true
}

// queryUpstreamTokenRecentLogs 用渠道中转密钥查 /api/log/token：官方该接口不接受任何
// 筛选或分页参数，一次返回该令牌最近若干条日志，因此筛选与分页都在本地完成。
func queryUpstreamTokenRecentLogs(ctx context.Context, baseURL string, cred UpstreamLogCredential, f UpstreamLogFilters, page int, pageSize int) (*UpstreamLogResult, error) {
	queryURL, err := normalizeUpstreamLogURL(baseURL, upstreamLogTokenRecentPath)
	if err != nil {
		return nil, err
	}
	// 取满整批再过滤：若按 pageSize 先截断，只会在最新几条里匹配，高流量渠道上几分钟前
	// 的请求就会被误报为「上游没有这条日志」。
	result, err := doUpstreamLogRequest(ctx, queryURL, cred, upstreamLogTokenRecentMaxItems)
	if err != nil {
		if errors.Is(err, errUpstreamLogNotFound) {
			return nil, withUpstreamLogDetail(ErrUpstreamLogEndpointMissing, err)
		}
		return nil, err
	}

	// 官方 v0.10.8 之前日志表没有 request_id 列，返回的条目一律没有该字段。此时按请求 ID
	// 追溯不可能命中，报版本不支持而不是「上游没有这条日志」。
	if strings.TrimSpace(f.RequestId) != "" && len(result.Items) > 0 {
		hasRequestId := false
		for _, it := range result.Items {
			if it.RequestId != "" {
				hasRequestId = true
				break
			}
		}
		if !hasRequestId {
			return nil, ErrUpstreamLogVersionUnsupported
		}
	}

	result.Items = filterRecentItems(result.Items, f)
	result.Total = len(result.Items)
	// 该接口不接受分页参数，只能返回一批近期数据，因此第 2 页开始必定为空，
	// 不能假装还有后续页，否则用户会在翻页时看到重复的第一页内容。
	if page > 1 {
		result.Items = []UpstreamLogItem{}
	} else if len(result.Items) > pageSize {
		result.Items = result.Items[:pageSize]
	}
	result.Scope = UpstreamLogScopeRecent
	return result, nil
}

// doUpstreamLogRequest 发起单次上游请求并解析响应。
func doUpstreamLogRequest(ctx context.Context, fullURL string, cred UpstreamLogCredential, pageSize int) (result *UpstreamLogResult, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, ErrUpstreamLogBaseURLInvalid
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Accept", "application/json")
	if cred.APIUser != "" {
		req.Header.Set("New-Api-User", cred.APIUser)
	}

	resp, err := upstreamLogClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutErr(err) {
			return nil, ErrUpstreamLogTimeout
		}
		// 连接层失败没有上游响应可展示，而 err 里带着上游地址和 dial 细节，不能外泄。
		return nil, ErrUpstreamLogUnavailable
	}
	defer DrainAndCloseResponseBody(resp)

	// 失败时把上游到底回了什么带回去：管理员否则只能看到一句「上游不可用」，无从判断是
	// 网关拦截、鉴权失败还是上游自己报错。正文在这里截断并脱敏后才允许离开服务层。
	if resp.StatusCode != http.StatusOK {
		snippet := readUpstreamSnippet(resp.Body, cred.Token)
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, newUpstreamLogHTTPError(ErrUpstreamLogUnauthorized, resp.StatusCode, snippet)
		case resp.StatusCode == http.StatusTooManyRequests:
			// 官方 /api/log/token 挂 CriticalRateLimit（默认同一 IP 20 分钟 20 次）。
			return nil, newUpstreamLogHTTPError(ErrUpstreamLogRateLimited, resp.StatusCode, snippet)
		case resp.StatusCode == http.StatusNotFound:
			// 上游没有该接口：调用方据此报能力缺失，绝不改用范围不同的另一个接口。
			return nil, newUpstreamLogHTTPError(errUpstreamLogNotFound, resp.StatusCode, snippet)
		default:
			return nil, newUpstreamLogHTTPError(ErrUpstreamLogUnavailable, resp.StatusCode, snippet)
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamLogMaxResponseBytes+1))
	if err != nil {
		return nil, ErrUpstreamLogUnavailable
	}
	if len(body) > upstreamLogMaxResponseBytes {
		return nil, ErrUpstreamLogInvalidResp
	}

	result, err = parseUpstreamLogBody(body, pageSize)
	if err != nil {
		// 200 但解析不了：正文正是判断「上游是什么东西」的唯一线索，同样带回去。
		return nil, newUpstreamLogHTTPError(err, resp.StatusCode, sanitizeUpstreamSnippet(body, cred.Token))
	}
	return result, nil
}

// parseUpstreamLogBody 解析上游标准响应；data 既可能是分页对象（/api/log/self）也可能是数组（/api/log/token）。
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
	Username          string          `json:"username"`
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
		Username:          truncateUpstreamStr(r.Username),
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

// filterRecentItems 在 /api/log/token 返回的近期数组上做本地过滤：该接口不接受任何
// 参数，所以每一个筛选条件都必须在这里复核，页面上的条件才不会被悄悄丢掉。
func filterRecentItems(items []UpstreamLogItem, f UpstreamLogFilters) []UpstreamLogItem {
	out := make([]UpstreamLogItem, 0, len(items))
	for _, it := range items {
		if !matchesLocalOnlyFilters(it, f) {
			continue
		}
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
		if f.Group != "" && it.Group != f.Group {
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
