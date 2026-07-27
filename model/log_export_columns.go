package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 列分组，前端按此折叠展示。
const (
	LogExportGroupBasic       = "basic"
	LogExportGroupTokens      = "tokens"
	LogExportGroupBilling     = "billing"
	LogExportGroupPerformance = "performance"
	LogExportGroupAdmin       = "admin"
	LogExportGroupAudit       = "audit"
)

// LogExportMaxColumns 单次导出的列数上限，防止拼出畸形宽表拖慢写入。
// 必须容得下 builtin:full（注册表全部列），由 TestLogExportColumns_BuiltinTemplatesResolve 兜底。
const LogExportMaxColumns = 100

// LogExportColumnI18nKey 返回列表头的后端 i18n key。
// 与列 Key 一一对应推导，杜绝注册表与 locale 文件脱节。
func LogExportColumnI18nKey(columnKey string) string {
	return "log_export.col." + columnKey
}

// LogExportTypeI18nKey 返回日志类型单元格的后端 i18n key。
func LogExportTypeI18nKey(logType int) string {
	switch logType {
	case LogTypeTopup:
		return "log_export.type.topup"
	case LogTypeConsume:
		return "log_export.type.consume"
	case LogTypeManage:
		return "log_export.type.manage"
	case LogTypeSystem:
		return "log_export.type.system"
	case LogTypeError:
		return "log_export.type.error"
	case LogTypeRefund:
		return "log_export.type.refund"
	case LogTypeLogin:
		return "log_export.type.login"
	default:
		return "log_export.type.unknown"
	}
}

// rowCtx 承载单行导出所需的派生数据。other 每行只解析一次，所有依赖列共享结果，
// 避免按列重复 Unmarshal。
type rowCtx struct {
	other       map[string]any
	otherParsed bool
	// channelNames 跨整次导出复用的渠道名缓存（含负缓存），由扫描层填充。
	channelNames map[int]string
	// loc 导出使用的时区。
	loc *time.Location
	// translate 把 i18n key 渲染成导出语言的文本；为 nil 时原样返回 key。
	translate func(key string) string
	// translated 缓存翻译结果。取值列（如「类型」）每行都要翻译，千万行就是千万次
	// go-i18n 本地化调用；实际用到的 key 只有个位数，缓存后每次退化成一次 map 查找。
	// 单个导出任务只有一个 goroutine 在写，无需加锁。
	translated map[string]string
}

func (ctx *rowCtx) t(key string) string {
	if ctx.translate == nil {
		return key
	}
	if v, ok := ctx.translated[key]; ok {
		return v
	}
	v := ctx.translate(key)
	if ctx.translated == nil {
		ctx.translated = make(map[string]string, 8)
	}
	ctx.translated[key] = v
	return v
}

// LogExportColumn 描述一个可导出的列。
type LogExportColumn struct {
	// Key 稳定标识，模板中存储的就是它，永不改名。
	Key string
	// Label 英文标签。前端 i18n 以英文原文为 key，直接 t(label) 即可；
	// 后端 CSV 表头走 LogExportColumnI18nKey(Key) 的 YAML 文案。
	Label string
	// Group 所属分组。
	Group string
	// AdminOnly 仅管理员可见。新的后台导出链路本身即管理员专属，该标记供
	// 旧的自助同步导出路径复用，守住「self 导出隐藏渠道/重试列」的不变量。
	AdminOnly bool
	// NeedOther 是否依赖 logs.other 字段。任何一列为 true 时 SQL 才 SELECT other
	// 并做 JSON 解析——other 是行宽大头，这个开关对 IO 与 CPU 都是数量级影响。
	NeedOther bool
	// NeedChannelName 是否需要查询时解析渠道名。
	NeedChannelName bool
	// Extract 取值。返回 string，内部一律用 strconv 而非 fmt.Sprintf。
	Extract func(l *Log, ctx *rowCtx) string
}

var (
	logExportColumns   []LogExportColumn
	logExportColumnMap map[string]*LogExportColumn
)

// ── other 取值辅助 ────────────────────────────────────────────────

func (ctx *rowCtx) otherMap(l *Log) map[string]any {
	if !ctx.otherParsed {
		ctx.otherParsed = true
		ctx.other = nil
		if l.Other != "" {
			if m, err := common.StrToMap(l.Other); err == nil {
				ctx.other = m
			}
		}
	}
	return ctx.other
}

func (ctx *rowCtx) adminInfo(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	sub, _ := m["admin_info"].(map[string]any)
	return sub
}

func (ctx *rowCtx) auditInfo(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	sub, _ := m["audit_info"].(map[string]any)
	return sub
}

// formatAny 把 other 中的任意 JSON 值渲染成单元格文本。
// 数字统一走 strconv：整数不带小数点，浮点去掉尾随零。
func formatAny(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		if val == math.Trunc(val) && math.Abs(val) < 1e15 {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			parts = append(parts, formatAny(item))
		}
		return strings.Join(parts, "|")
	default:
		// map 等复杂结构原样序列化，保证信息不丢。
		if data, err := common.Marshal(val); err == nil {
			return string(data)
		}
		return fmt.Sprint(val)
	}
}

func otherValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	return formatAny(v)
}

// otherCol 生成一个从 other 顶层取值的列。
func otherCol(key, label, group string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: group, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.otherMap(l), key)
		},
	}
}

// adminInfoCol 生成一个从 other.admin_info 取值的列（一律 AdminOnly）。
func adminInfoCol(key, otherKey, label, group string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: group,
		AdminOnly: true, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.adminInfo(l), otherKey)
		},
	}
}

// auditInfoCol 生成一个从 other.audit_info 取值的列。
func auditInfoCol(key, otherKey, label string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: LogExportGroupAudit,
		AdminOnly: true, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.auditInfo(l), otherKey)
		},
	}
}

func init() {
	logExportColumns = buildLogExportColumns()
	logExportColumnMap = make(map[string]*LogExportColumn, len(logExportColumns))
	for i := range logExportColumns {
		logExportColumnMap[logExportColumns[i].Key] = &logExportColumns[i]
	}
}

func buildLogExportColumns() []LogExportColumn {
	return []LogExportColumn{
		// ── basic ────────────────────────────────────────────
		{Key: "created_at", Label: "Time", Group: LogExportGroupBasic,
			Extract: func(l *Log, ctx *rowCtx) string {
				return time.Unix(l.CreatedAt, 0).In(ctx.loc).Format("2006-01-02 15:04:05")
			}},
		{Key: "id", Label: "Log ID", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.Id) }},
		{Key: "type", Label: "Type", Group: LogExportGroupBasic,
			Extract: func(l *Log, ctx *rowCtx) string { return ctx.t(LogExportTypeI18nKey(l.Type)) }},
		{Key: "username", Label: "User", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.Username }},
		{Key: "user_id", Label: "User ID", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.UserId) }},
		{Key: "token_name", Label: "Token", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.TokenName }},
		{Key: "token_id", Label: "Token ID", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.TokenId) }},
		{Key: "group", Label: "Group", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.Group }},
		{Key: "model_name", Label: "Model", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.ModelName }},
		otherCol("upstream_model_name", "Upstream Model", LogExportGroupBasic),
		otherCol("is_model_mapped", "Model Redirected", LogExportGroupBasic),
		{Key: "ip", Label: "IP", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.Ip }},
		{Key: "request_id", Label: "Request ID", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.RequestId }},
		{Key: "upstream_request_id", Label: "Upstream Request ID", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.UpstreamRequestId }},
		{Key: "content", Label: "Details", Group: LogExportGroupBasic,
			Extract: func(l *Log, _ *rowCtx) string { return l.Content }},

		// ── tokens ───────────────────────────────────────────
		{Key: "prompt_tokens", Label: "Input Tokens", Group: LogExportGroupTokens,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.PromptTokens) }},
		{Key: "completion_tokens", Label: "Output Tokens", Group: LogExportGroupTokens,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.CompletionTokens) }},
		{Key: "total_tokens", Label: "Total Tokens", Group: LogExportGroupTokens,
			Extract: func(l *Log, _ *rowCtx) string {
				return strconv.Itoa(l.PromptTokens + l.CompletionTokens)
			}},
		otherCol("cache_tokens", "Cache Read Tokens", LogExportGroupTokens),
		otherCol("cache_creation_tokens", "Cache Write Tokens", LogExportGroupTokens),
		otherCol("cache_creation_tokens_5m", "Cache Write Tokens (5m)", LogExportGroupTokens),
		otherCol("cache_creation_tokens_1h", "Cache Write Tokens (1h)", LogExportGroupTokens),
		otherCol("text_input", "Text Input Tokens", LogExportGroupTokens),
		otherCol("text_output", "Text Output Tokens", LogExportGroupTokens),
		otherCol("audio_input", "Audio Input Tokens", LogExportGroupTokens),
		otherCol("audio_output", "Audio Output Tokens", LogExportGroupTokens),
		otherCol("image_output", "Image Output Tokens", LogExportGroupTokens),
		otherCol("web_search_call_count", "Web Search Calls", LogExportGroupTokens),
		otherCol("file_search_call_count", "File Search Calls", LogExportGroupTokens),

		// ── billing ──────────────────────────────────────────
		{Key: "quota", Label: "Quota", Group: LogExportGroupBilling,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.Quota) }},
		{Key: "cost_usd", Label: "Cost (USD)", Group: LogExportGroupBilling,
			Extract: func(l *Log, _ *rowCtx) string {
				// 对齐前端 renderQuota(quota, 6)。
				usd := math.Round(common.QuotaToUSD(int64(l.Quota))*1e6) / 1e6
				return strconv.FormatFloat(usd, 'f', -1, 64)
			}},
		otherCol("billing_source", "Billing Source", LogExportGroupBilling),
		otherCol("billing_mode", "Billing Mode", LogExportGroupBilling),
		otherCol("matched_tier", "Matched Tier", LogExportGroupBilling),
		otherCol("model_ratio", "Model Ratio", LogExportGroupBilling),
		otherCol("completion_ratio", "Completion Ratio", LogExportGroupBilling),
		otherCol("group_ratio", "Group Ratio", LogExportGroupBilling),
		otherCol("user_group_ratio", "User Group Ratio", LogExportGroupBilling),
		otherCol("cache_ratio", "Cache Ratio", LogExportGroupBilling),
		otherCol("cache_creation_ratio", "Cache Write Ratio", LogExportGroupBilling),
		otherCol("cache_creation_ratio_5m", "Cache Write Ratio (5m)", LogExportGroupBilling),
		otherCol("cache_creation_ratio_1h", "Cache Write Ratio (1h)", LogExportGroupBilling),
		otherCol("audio_ratio", "Audio Ratio", LogExportGroupBilling),
		otherCol("audio_completion_ratio", "Audio Completion Ratio", LogExportGroupBilling),
		otherCol("image_ratio", "Image Ratio", LogExportGroupBilling),
		otherCol("model_price", "Model Price", LogExportGroupBilling),
		otherCol("web_search_price", "Web Search Price", LogExportGroupBilling),
		otherCol("file_search_price", "File Search Price", LogExportGroupBilling),

		// ── performance ──────────────────────────────────────
		{Key: "use_time", Label: "Duration (s)", Group: LogExportGroupPerformance,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.UseTime) }},
		otherCol("frt", "First Token Latency (ms)", LogExportGroupPerformance),
		{Key: "tokens_per_sec", Label: "Tokens / s", Group: LogExportGroupPerformance,
			Extract: func(l *Log, _ *rowCtx) string {
				if l.UseTime <= 0 || l.CompletionTokens <= 0 {
					return ""
				}
				tps := float64(l.CompletionTokens) / float64(l.UseTime)
				return strconv.FormatFloat(math.Round(tps*100)/100, 'f', -1, 64)
			}},
		{Key: "is_stream", Label: "Stream", Group: LogExportGroupPerformance,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.FormatBool(l.IsStream) }},
		otherCol("stream_status", "Stream Status", LogExportGroupPerformance),
		otherCol("reasoning_effort", "Reasoning Effort", LogExportGroupPerformance),

		// 登录日志字段：属于日志属主本人可见，不标 AdminOnly。
		otherCol("login_method", "Login Method", LogExportGroupAudit),
		otherCol("user_agent", "User Agent", LogExportGroupAudit),
		otherCol("request_path", "Request Path", LogExportGroupAudit),

		// ── admin ────────────────────────────────────────────
		{Key: "channel_id", Label: "Channel ID", Group: LogExportGroupAdmin, AdminOnly: true,
			Extract: func(l *Log, _ *rowCtx) string { return strconv.Itoa(l.ChannelId) }},
		{Key: "channel_name", Label: "Channel Name", Group: LogExportGroupAdmin,
			AdminOnly: true, NeedChannelName: true,
			Extract: func(l *Log, ctx *rowCtx) string {
				if l.ChannelName != "" {
					return l.ChannelName
				}
				if ctx.channelNames == nil {
					return ""
				}
				return ctx.channelNames[l.ChannelId]
			}},
		{Key: "retry_chain", Label: "Retry Chain", Group: LogExportGroupAdmin,
			AdminOnly: true, NeedOther: true,
			Extract: func(l *Log, ctx *rowCtx) string {
				adminInfo := ctx.adminInfo(l)
				if adminInfo == nil {
					return ""
				}
				chain, ok := adminInfo["use_channel"].([]any)
				if !ok || len(chain) == 0 {
					return ""
				}
				parts := make([]string, 0, len(chain))
				for _, v := range chain {
					parts = append(parts, formatAny(v))
				}
				return strings.Join(parts, "->")
			}},
		adminInfoCol("is_multi_key", "is_multi_key", "Multi Key", LogExportGroupAdmin),
		adminInfoCol("multi_key_index", "multi_key_index", "Key Index", LogExportGroupAdmin),
		adminInfoCol("usage_billing_path", "usage_billing_path", "Usage Billing Path", LogExportGroupAdmin),
		adminInfoCol("local_count_tokens", "local_count_tokens", "Local Token Counting", LogExportGroupAdmin),
		adminInfoCol("quota_saturation", "quota_saturation", "Quota Saturation", LogExportGroupAdmin),
		{Key: "other_raw", Label: "Raw Other JSON", Group: LogExportGroupAdmin,
			AdminOnly: true, NeedOther: true,
			Extract: func(l *Log, _ *rowCtx) string { return l.Other }},

		// ── audit ────────────────────────────────────────────
		adminInfoCol("admin_username", "admin_username", "Operator", LogExportGroupAudit),
		adminInfoCol("admin_id", "admin_id", "Operator ID", LogExportGroupAudit),
		adminInfoCol("admin_role", "admin_role", "Operator Role", LogExportGroupAudit),
		adminInfoCol("auth_method", "auth_method", "Auth Method", LogExportGroupAudit),
		adminInfoCol("payment_method", "payment_method", "Payment Method", LogExportGroupAudit),
		adminInfoCol("callback_payment_method", "callback_payment_method", "Callback Payment Method", LogExportGroupAudit),
		adminInfoCol("caller_ip", "caller_ip", "Caller IP", LogExportGroupAudit),
		adminInfoCol("server_ip", "server_ip", "Server IP", LogExportGroupAudit),
		adminInfoCol("node_name", "node_name", "Node Name", LogExportGroupAudit),
		adminInfoCol("version", "version", "Version", LogExportGroupAudit),
		auditInfoCol("audit_method", "method", "HTTP Method"),
		auditInfoCol("audit_route", "route", "Route"),
		auditInfoCol("audit_path", "path", "Path"),
		auditInfoCol("audit_status", "status", "HTTP Status"),
		auditInfoCol("audit_success", "success", "Succeeded"),
	}
}

// LogExportColumns 返回全部列定义（只读）。
func LogExportColumns() []LogExportColumn { return logExportColumns }

// LookupLogExportColumn 按 key 查列。
func LookupLogExportColumn(key string) (*LogExportColumn, bool) {
	col, ok := logExportColumnMap[key]
	return col, ok
}

// ── 内置模板 ──────────────────────────────────────────────────────

const (
	// LogExportTemplateAsDisplayed 默认模板：与使用日志页面展示的数据一致。
	LogExportTemplateAsDisplayed = "builtin:as_displayed"
	LogExportTemplateLegacy      = "builtin:legacy"
	LogExportTemplateBilling     = "builtin:billing"
	LogExportTemplatePerformance = "builtin:performance"
	LogExportTemplateAudit       = "builtin:audit"
	LogExportTemplateFull        = "builtin:full"
)

// BuiltinLogExportTemplate 内置模板定义。Name 为英文原文，前端按自身 i18n 约定 t(name)。
type BuiltinLogExportTemplate struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsDefault bool     `json:"is_default"`
}

// asDisplayedColumns 对齐 web 前端使用日志表格（管理员视角）的列。
// 页面上一个单元格常承载多个数据点（例如「渠道」格里同时有渠道 ID、名称、
// 多 key 序号和重试链），导出时展开成独立列——表格能靠悬浮和徽章表达的信息，
// 表格文件必须落到列上。
var asDisplayedColumns = []string{
	"created_at", "type",
	"channel_id", "channel_name", "multi_key_index", "retry_chain",
	"username",
	"token_name",
	"model_name", "upstream_model_name",
	"is_stream", "stream_status", "tokens_per_sec",
	"prompt_tokens", "completion_tokens", "cache_tokens", "cache_creation_tokens",
	"quota", "cost_usd", "billing_source",
	"use_time", "frt",
	"content",
}

// legacyColumns 与重构前的 14 列 xlsx 导出保持一致，保证老用户拿到的文件结构不变。
var legacyColumns = []string{
	"created_at", "channel_id", "username", "token_name", "group", "type",
	"model_name", "use_time", "prompt_tokens", "completion_tokens", "cost_usd",
	"ip", "retry_chain", "content",
}

var billingColumns = []string{
	"created_at", "username", "token_name", "group", "model_name",
	"prompt_tokens", "completion_tokens", "total_tokens",
	"cache_tokens", "cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h",
	"text_input", "text_output", "audio_input", "audio_output", "image_output",
	"quota", "cost_usd", "billing_source", "billing_mode", "matched_tier",
	"model_ratio", "completion_ratio", "group_ratio", "user_group_ratio",
	"cache_ratio", "cache_creation_ratio", "audio_ratio", "audio_completion_ratio",
	"image_ratio", "model_price", "web_search_price", "file_search_price",
}

var performanceColumns = []string{
	"created_at", "model_name", "channel_id", "channel_name", "retry_chain",
	"is_stream", "stream_status", "use_time", "frt", "tokens_per_sec",
	"prompt_tokens", "completion_tokens", "request_id", "upstream_request_id",
}

var auditColumns = []string{
	"created_at", "type", "username", "user_id",
	"admin_username", "admin_id", "admin_role", "auth_method",
	"audit_method", "audit_route", "audit_path", "audit_status", "audit_success",
	"payment_method", "callback_payment_method", "caller_ip", "server_ip", "node_name",
	"login_method", "user_agent", "request_path", "ip", "content",
}

// BuiltinLogExportTemplates 返回内置模板列表（默认模板排在首位）。
func BuiltinLogExportTemplates() []BuiltinLogExportTemplate {
	full := make([]string, 0, len(logExportColumns))
	for i := range logExportColumns {
		full = append(full, logExportColumns[i].Key)
	}
	return []BuiltinLogExportTemplate{
		{ID: LogExportTemplateAsDisplayed, Name: "As Displayed", Columns: asDisplayedColumns, IsDefault: true},
		{ID: LogExportTemplateLegacy, Name: "Legacy Export", Columns: legacyColumns},
		{ID: LogExportTemplateBilling, Name: "Billing Details", Columns: billingColumns},
		{ID: LogExportTemplatePerformance, Name: "Performance Diagnostics", Columns: performanceColumns},
		{ID: LogExportTemplateAudit, Name: "Audit", Columns: auditColumns},
		{ID: LogExportTemplateFull, Name: "All Columns", Columns: full},
	}
}

// LookupBuiltinLogExportTemplate 按 ID 查内置模板。
func LookupBuiltinLogExportTemplate(id string) (BuiltinLogExportTemplate, bool) {
	for _, tpl := range BuiltinLogExportTemplates() {
		if tpl.ID == id {
			return tpl, true
		}
	}
	return BuiltinLogExportTemplate{}, false
}

// DefaultLogExportColumns 返回默认模板（页面所见）的列。
func DefaultLogExportColumns() []string {
	out := make([]string, len(asDisplayedColumns))
	copy(out, asDisplayedColumns)
	return out
}

// ── 列集合解析 ────────────────────────────────────────────────────

var (
	ErrLogExportNoColumns      = errors.New("no columns selected")
	ErrLogExportTooManyColumns = errors.New("too many columns selected")
)

// UnknownLogExportColumnError 未知列错误，携带具体 key 便于定位坏模板。
type UnknownLogExportColumnError struct{ Key string }

func (e *UnknownLogExportColumnError) Error() string {
	return "unknown export column: " + e.Key
}

// LogExportColumnSet 是一次导出解析后的列集合。
type LogExportColumnSet struct {
	Keys    []string
	Columns []*LogExportColumn
	// Dropped 因权限被剔除的列（仅自助导出路径会非空）。
	Dropped []string
	// NeedOther 是否需要 SELECT logs.other 并解析 JSON。
	NeedOther bool
	// NeedChannelName 是否需要查询时解析渠道名。
	NeedChannelName bool
}

// ResolveLogExportColumns 校验并解析列集合。
// keys 为空时回落到默认模板；isAdmin=false 时静默剔除 AdminOnly 列并记入 Dropped
// ——静默而非报错，是为了让管理员共享的模板被普通用户套用时仍能正常导出。
func ResolveLogExportColumns(keys []string, isAdmin bool) (*LogExportColumnSet, error) {
	if len(keys) == 0 {
		keys = DefaultLogExportColumns()
	}
	if len(keys) > LogExportMaxColumns {
		return nil, ErrLogExportTooManyColumns
	}
	set := &LogExportColumnSet{
		Keys:    make([]string, 0, len(keys)),
		Columns: make([]*LogExportColumn, 0, len(keys)),
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		col, ok := LookupLogExportColumn(key)
		if !ok {
			return nil, &UnknownLogExportColumnError{Key: key}
		}
		if seen[key] {
			continue // 去重，保留首次出现的顺序
		}
		seen[key] = true
		if col.AdminOnly && !isAdmin {
			set.Dropped = append(set.Dropped, key)
			continue
		}
		set.Keys = append(set.Keys, key)
		set.Columns = append(set.Columns, col)
		if col.NeedOther {
			set.NeedOther = true
		}
		if col.NeedChannelName {
			set.NeedChannelName = true
		}
	}
	if len(set.Columns) == 0 {
		return nil, ErrLogExportNoColumns
	}
	return set, nil
}

// HeaderI18nKeys 返回各列表头的后端 i18n key。
func (s *LogExportColumnSet) HeaderI18nKeys() []string {
	out := make([]string, len(s.Columns))
	for i, col := range s.Columns {
		out[i] = LogExportColumnI18nKey(col.Key)
	}
	return out
}

// SelectFields 返回本次导出实际需要的数据库列，避免 SELECT *。
// 不含 other 依赖列时不取 other——该列通常是行宽的大头。
func (s *LogExportColumnSet) SelectFields() []string {
	// id 与 created_at 是 keyset 游标必需，始终选取。
	fields := map[string]bool{"id": true, "created_at": true}
	for _, col := range s.Columns {
		switch col.Key {
		case "type":
			fields["type"] = true
		case "username":
			fields["username"] = true
		case "user_id":
			fields["user_id"] = true
		case "token_name":
			fields["token_name"] = true
		case "token_id":
			fields["token_id"] = true
		case "group":
			fields[logGroupCol] = true
		case "model_name":
			fields["model_name"] = true
		case "ip":
			fields["ip"] = true
		case "request_id":
			fields["request_id"] = true
		case "upstream_request_id":
			fields["upstream_request_id"] = true
		case "content":
			fields["content"] = true
		case "prompt_tokens":
			fields["prompt_tokens"] = true
		case "completion_tokens":
			fields["completion_tokens"] = true
		case "total_tokens":
			fields["prompt_tokens"] = true
			fields["completion_tokens"] = true
		case "quota", "cost_usd":
			fields["quota"] = true
		case "use_time":
			fields["use_time"] = true
		case "tokens_per_sec":
			fields["use_time"] = true
			fields["completion_tokens"] = true
		case "is_stream":
			fields["is_stream"] = true
		case "channel_id", "channel_name":
			fields["channel_id"] = true
		}
		if col.NeedOther {
			fields["other"] = true
		}
	}
	out := make([]string, 0, len(fields))
	for f := range fields {
		out = append(out, f)
	}
	sort.Strings(out) // 稳定顺序，便于测试断言
	return out
}

// Render 把一行日志投影成单元格文本，复用调用方传入的切片以减少分配。
func (s *LogExportColumnSet) Render(l *Log, ctx *rowCtx, buf []string) []string {
	if cap(buf) < len(s.Columns) {
		buf = make([]string, len(s.Columns))
	}
	buf = buf[:len(s.Columns)]
	// 每行重置解析缓存：other 在本行内只解析一次，跨行不复用。
	ctx.otherParsed = false
	ctx.other = nil
	for i, col := range s.Columns {
		buf[i] = col.Extract(l, ctx)
	}
	return buf
}
