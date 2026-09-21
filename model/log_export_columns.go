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
	LogExportGroupDiagnostic  = "diagnostic"
	LogExportGroupAdmin       = "admin"
	LogExportGroupAudit       = "audit"
)

// LogExportMaxColumns 单次导出的列数上限，防止拼出畸形宽表拖慢写入。
// 必须容得下 builtin:full（注册表全部列），由 TestLogExportColumns_BuiltinTemplatesResolve 兜底。
const LogExportMaxColumns = 150

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
	// otherOwner 当前缓存的 other 属于哪一行，用于跨行自动失效（见 otherMap）。
	otherOwner *Log
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
	// RootOnly 仅超级管理员可见。other.root_info 里的字段（以及原样导出的
	// other_raw）含 root 专属诊断，普通管理员在接口侧看不到它们，导出侧
	// 必须同样挡住，否则导出就成了绕过角色投影的旁路。
	RootOnly bool
	// NeedOther 是否依赖 logs.other 字段。任何一列为 true 时 SQL 才 SELECT other
	// 并做 JSON 解析——other 是行宽大头，这个开关对 IO 与 CPU 都是数量级影响。
	NeedOther bool
	// NeedChannelName 是否需要查询时解析渠道名。
	NeedChannelName bool
	// Audience 该列是否可以出现在发给客户的文件里。
	//
	// 它比 AdminOnly 更严：AdminOnly 管「技术上谁能读到」，Audience 管「我们愿不愿意
	// 主动把它写进客户手里的文件」——用户在自己的日志详情页点开能看到某字段，
	// 不代表我们要把它批量导出成一份文件发过去。
	// 零值是 Internal：新增列不标注就自动落在安全的一侧。
	// AdminOnly / RootOnly 的列一律是 Internal，无需重复标注。
	Audience LogExportAudience
	// Extract 取值。返回 string，内部一律用 strconv 而非 fmt.Sprintf。
	Extract func(l *Log, ctx *rowCtx) string
}

// LogExportAudience 见 LogExportColumn.Audience。
type LogExportAudience uint8

const (
	// LogExportAudienceInternal 仅内部使用（零值）。
	LogExportAudienceInternal LogExportAudience = iota
	// LogExportAudienceCustomer 可以出现在发给客户的文件里。
	LogExportAudienceCustomer
)

// String 返回下发前端的取值。
func (a LogExportAudience) String() string {
	if a == LogExportAudienceCustomer {
		return "customer"
	}
	return "internal"
}

var (
	logExportColumns   []LogExportColumn
	logExportColumnMap map[string]*LogExportColumn
)

// ── other 取值辅助 ────────────────────────────────────────────────

// otherMap 解析并缓存本行的 other。
//
// 缓存按「属于哪一行」失效，而不是靠调用方在每行开头显式重置：行级筛选要在渲染
// **之前**读 other，若靠顺序重置，筛选解析一次、渲染再解析一次——而 other 的 JSON
// 解析占了整个渲染成本的约九成。用行指针做归属判断，先判定后渲染只解析一次，
// 且不可能因为调用顺序变化而读到上一行的数据。
func (ctx *rowCtx) otherMap(l *Log) map[string]any {
	if !ctx.otherParsed || ctx.otherOwner != l {
		ctx.otherParsed = true
		ctx.otherOwner = l
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

// streamResult 取 other.stream_result（公有）。只有流式请求才有这个字段，
// 返回 nil 表示「非流式 / 旧日志」，不代表异常。
func (ctx *rowCtx) streamResult(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	sub, _ := m["stream_result"].(map[string]any)
	return sub
}

// streamStatus 取 other.stream_status（公有）。同样仅流式请求才有。
func (ctx *rowCtx) streamStatus(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	sub, _ := m["stream_status"].(map[string]any)
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

func (ctx *rowCtx) rootInfo(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	sub, _ := m["root_info"].(map[string]any)
	return sub
}

// opParams 取 other.op.params——审计/登录日志的语言无关操作参数，
// 被操作用户（target_user_id / target_username）即存放于此。
func (ctx *rowCtx) opParams(l *Log) map[string]any {
	m := ctx.otherMap(l)
	if m == nil {
		return nil
	}
	op, _ := m["op"].(map[string]any)
	if op == nil {
		return nil
	}
	sub, _ := op["params"].(map[string]any)
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

// isUnsetUserGroupRatio 判断 other.user_group_ratio 是不是「没有专属倍率」的哨兵值。
//
// -1 来自 HandleGroupRatio 的 GroupRatioInfo 初值，文本计费路径以前无条件把它写进日志，
// 所以历史数据里大量存在（测试库单是 group_ratio=10 的就有 141 万条）。倍率合法取值
// 不小于 0，因此负数一律当作未设置。类型集与 formatAny 保持一致。
func isUnsetUserGroupRatio(v any) bool {
	switch n := v.(type) {
	case float64:
		return n < 0
	case int:
		return n < 0
	case int64:
		return n < 0
	default:
		return false
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

// opParamsCol 生成一个从 other.op.params 取值的列（一律 AdminOnly）。
func opParamsCol(key, otherKey, label string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: LogExportGroupAudit,
		AdminOnly: true, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.opParams(l), otherKey)
		},
	}
}

// rootInfoCol 生成一个从 other.root_info 取值的列（一律 RootOnly）。
func rootInfoCol(key, otherKey, label, group string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: group,
		AdminOnly: true, RootOnly: true, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.rootInfo(l), otherKey)
		},
	}
}

// streamResultCol 生成一个从 other.stream_result 取值的列。
// stream_result 由 SetPublic 写入，属主本人可见，因此不标 AdminOnly。
func streamResultCol(key, otherKey, label string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: LogExportGroupDiagnostic, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.streamResult(l), otherKey)
		},
	}
}

// streamStatusCol 生成一个从 other.stream_status 取值的列。
func streamStatusCol(key, otherKey, label string) LogExportColumn {
	return LogExportColumn{
		Key: key, Label: label, Group: LogExportGroupDiagnostic, NeedOther: true,
		Extract: func(l *Log, ctx *rowCtx) string {
			return otherValue(ctx.streamStatus(l), otherKey)
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

// logExportCustomerColumns 是可以出现在「客户对账单」里的列。
//
// 收录标准：客户要能凭这份文件把账自己复算一遍，且不含任何暴露上游供应链或
// 内部定价策略的字段。逐列裁决理由见 docs/design/usage-log-export-anomaly-and-templates.md §4.3。
//
// 明确排除且不要再加回来的：
//   - upstream_model_name / is_model_mapped / upstream_request_id —— 暴露模型映射与上游身份
//   - billing_source / billing_mode / request_rules —— 内部计费路径与规则原文
//   - ip / content —— 与对账无关；content 会携带错误信息与内部提示文本
//   - 全部 stream_* 诊断列、frt、tokens_per_sec、use_time —— 与对账无关，
//     放进去只会引出「为什么这条慢」的二次追问
//   - user_group_ratio —— 它不是另一个乘数：HandleGroupRatio 在命中专属倍率时把同一个
//     值同时写进 group_ratio 和 user_group_ratio，没命中时它是哨兵值 -1。给客户的文件里
//     放一列要么与 group_ratio 逐字重复、要么为空的列，只会引出「这两列什么关系」的追问。
//     它的实际用途是内部核账时标出「这条用的是专属价」，留在内部模板即可
//   - quota —— 本站的内部计量单位，客户既核对不了账单，又要反过来问换算关系；
//     金额一列 cost_usd 才是他要的
var logExportCustomerColumns = []string{
	// 标识
	"created_at", "request_id", "model_name", "token_name", "group",
	// 计费基数
	"prompt_tokens", "completion_tokens", "total_tokens",
	"cache_tokens", "cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h",
	"text_input", "text_output", "audio_input", "audio_output",
	"image_output", "image_cache_tokens", "billing_tokens",
	// 倍率与单价：不给这些，客户就只能核对总额、对不上就只能找客服
	"model_ratio", "completion_ratio", "group_ratio",
	"cache_ratio", "cache_creation_ratio", "cache_creation_ratio_5m", "cache_creation_ratio_1h",
	"audio_ratio", "audio_completion_ratio", "image_ratio",
	"model_price", "billing_unit", "fixed_price", "image_count",
	"tool_surcharges", "usage_facts",
	// 客户按哪一档阶梯价计费，属于他该知道的信息
	"matched_tier",
	// 结果：只给金额。额度是本站的内部计量单位，客户拿它既核对不了账单、
	// 又要反过来问「额度怎么换算成钱」，给 cost_usd 就够了。
	"cost_usd",
}

func init() {
	logExportColumns = buildLogExportColumns()
	logExportColumnMap = make(map[string]*LogExportColumn, len(logExportColumns))
	for i := range logExportColumns {
		logExportColumnMap[logExportColumns[i].Key] = &logExportColumns[i]
	}
	for _, key := range logExportCustomerColumns {
		col, ok := logExportColumnMap[key]
		if !ok {
			// 注册表里没有这个 key：清单写错了。此处 panic 在进程启动时就暴露，
			// 好过让「客户对账单」静默少一列。测试也会先一步抓到。
			panic("log export: unknown customer column " + key)
		}
		if col.AdminOnly || col.RootOnly {
			panic("log export: admin-only column marked as customer-facing: " + key)
		}
		col.Audience = LogExportAudienceCustomer
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
		otherCol("image_output", "Image Input Tokens", LogExportGroupTokens),
		otherCol("image_cache_tokens", "Image Cache Read Tokens", LogExportGroupTokens),
		otherCol("billing_tokens", "Billing Token Breakdown", LogExportGroupTokens),
		// 以下两列只在历史日志里有值：新日志按工具逐项记进 tool_surcharges。
		otherCol("web_search_call_count", "Web Search Calls (legacy)", LogExportGroupTokens),
		otherCol("file_search_call_count", "File Search Calls (legacy)", LogExportGroupTokens),

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
		// user_group_ratio 只在该用户配了专属分组倍率时才有意义，值与 group_ratio 相同
		// （HandleGroupRatio 把专属倍率同时写进两个字段）；没配时历史日志里是哨兵值 -1。
		// 这一列的作用是标出「这条用的是专属价」，所以哨兵值渲染成空，不能打印 -1。
		{Key: "user_group_ratio", Label: "User Group Ratio", Group: LogExportGroupBilling, NeedOther: true,
			Extract: func(l *Log, ctx *rowCtx) string {
				m := ctx.otherMap(l)
				if m == nil {
					return ""
				}
				v, ok := m["user_group_ratio"]
				if !ok || isUnsetUserGroupRatio(v) {
					return ""
				}
				return formatAny(v)
			}},
		otherCol("cache_ratio", "Cache Ratio", LogExportGroupBilling),
		otherCol("cache_creation_ratio", "Cache Write Ratio", LogExportGroupBilling),
		otherCol("cache_creation_ratio_5m", "Cache Write Ratio (5m)", LogExportGroupBilling),
		otherCol("cache_creation_ratio_1h", "Cache Write Ratio (1h)", LogExportGroupBilling),
		otherCol("audio_ratio", "Audio Ratio", LogExportGroupBilling),
		otherCol("audio_completion_ratio", "Audio Completion Ratio", LogExportGroupBilling),
		otherCol("image_ratio", "Image Input Ratio", LogExportGroupBilling),
		otherCol("model_price", "Model Price", LogExportGroupBilling),
		otherCol("billing_unit", "Billing Unit", LogExportGroupBilling),
		otherCol("fixed_price", "Fixed Price", LogExportGroupBilling),
		otherCol("image_count", "Billed Image Count", LogExportGroupBilling),
		otherCol("request_rules", "Request Billing Rules", LogExportGroupBilling),
		otherCol("tool_surcharges", "Tool Surcharges", LogExportGroupBilling),
		otherCol("usage_facts", "Usage Facts", LogExportGroupBilling),
		// 以下两列只在历史日志里有值：新日志按工具逐项记进 tool_surcharges。
		otherCol("web_search_price", "Web Search Price (legacy)", LogExportGroupBilling),
		otherCol("file_search_price", "File Search Price (legacy)", LogExportGroupBilling),

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

		// ── diagnostic ───────────────────────────────────────
		// 这些字段原本埋在 other 的嵌套 JSON 里：stream_status 列整块吐 JSON，
		// 在表格软件里既不能排序也不能筛选。拍平成独立列之后，
		// 「把所有按估算收费的单子按费用倒序排一遍」才成为一次点击的事。
		streamResultCol("usage_source", "usage_source", "Usage Source"),
		streamResultCol("settlement_state", "settlement_state", "Settlement State"),
		streamResultCol("stream_failed", "failed", "Stream Failed"),
		streamResultCol("client_gone", "client_gone", "Client Disconnected"),
		streamResultCol("effective_content", "effective_content", "Effective Content Delivered"),
		streamResultCol("confirmed_usage", "confirmed_usage", "Upstream Usage Confirmed"),
		streamResultCol("intended_quota", "intended_quota", "Intended Quota"),
		streamResultCol("reserved_quota", "reserved_quota", "Reserved Quota"),
		streamStatusCol("stream_status_text", "status", "Stream Result"),
		streamStatusCol("stream_end_reason", "end_reason", "Stream End Reason"),
		streamStatusCol("stream_error_count", "error_count", "Stream Error Count"),
		otherCol("stream_diagnostic_attempt", "Diagnostic Attempt", LogExportGroupDiagnostic),
		// 既有的 retry_chain 是文本，无法按次数排序或筛选；重试次数单独成列。
		{Key: "retry_count", Label: "Retry Count", Group: LogExportGroupDiagnostic,
			AdminOnly: true, NeedOther: true,
			Extract: func(l *Log, ctx *rowCtx) string {
				adminInfo := ctx.adminInfo(l)
				if adminInfo == nil {
					return ""
				}
				chain, ok := adminInfo["use_channel"].([]any)
				if !ok {
					return ""
				}
				return strconv.Itoa(len(chain))
			}},
		// anomaly_flags 与异常筛选共用 logAnomalyFlags，口径永远一致。
		{Key: "anomaly_flags", Label: "Anomaly Flags", Group: LogExportGroupDiagnostic,
			AdminOnly: true, NeedOther: true,
			Extract: func(l *Log, ctx *rowCtx) string {
				return strings.Join(logAnomalyFlags(l, ctx), ",")
			}},

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
		adminInfoCol("billing_model", "billing_model", "Billing Model", LogExportGroupAdmin),
		adminInfoCol("conversion_diagnostics", "conversion_diagnostics", "Conversion Diagnostics", LogExportGroupAdmin),
		adminInfoCol("channel_affinity", "channel_affinity", "Channel Affinity", LogExportGroupAdmin),
		adminInfoCol("task_plugin", "task_plugin", "Task Plugin", LogExportGroupAdmin),
		rootInfoCol("upstream_task_id", "upstream_task_id", "Upstream Task ID", LogExportGroupAdmin),
		rootInfoCol("task_node_name", "node_name", "Task Node", LogExportGroupAdmin),
		rootInfoCol("task_plugin_runtime", "task_plugin", "Task Plugin Runtime", LogExportGroupAdmin),
		// other_raw 原样导出整条 other，其中含 root_info，只有超级管理员能取。
		{Key: "other_raw", Label: "Raw Other JSON", Group: LogExportGroupAdmin,
			AdminOnly: true, RootOnly: true, NeedOther: true,
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
		opParamsCol("target_username", "target_username", "Target User"),
		opParamsCol("target_user_id", "target_user_id", "Target User ID"),
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
	// LogExportTemplateCustomerInvoice 唯一一个可以直接发给客户的模板。
	LogExportTemplateCustomerInvoice = "builtin:customer_invoice"
	LogExportTemplateOperations      = "builtin:operations"
	LogExportTemplateAnomaly         = "builtin:anomaly"
)

// BuiltinLogExportTemplate 内置模板定义。Name 为英文原文，前端按自身 i18n 约定 t(name)。
type BuiltinLogExportTemplate struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsDefault bool     `json:"is_default"`
	// Purpose 用途分组：reconciliation / analytics / diagnostic / audit。
	// 前端按它给模板下拉分组，避免九个模板平铺成一长条。
	Purpose string `json:"purpose"`
	// Audience 为 "customer" 表示该模板的列全部可发给客户，前端据此打徽章。
	// 这条性质由 TestLogExportTemplates_CustomerAudienceIsClosed 强制，
	// 不靠人工维护——否则某天有人往对账单模板里加了一列渠道名，没人会发现。
	Audience string `json:"audience"`
}

// 模板用途。
const (
	LogExportPurposeReconciliation = "reconciliation"
	LogExportPurposeAnalytics      = "analytics"
	LogExportPurposeDiagnostic     = "diagnostic"
	LogExportPurposeAudit          = "audit"
)

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

// billingColumns 必须让人能把账单复算回来：按 token 计费靠倍率 + 各类
// token 明细，按次固定价靠 billing_unit/fixed_price，按图片数靠 image_count，
// 任务类用量靠 usage_facts，工具调用附加费靠 tool_surcharges。
var billingColumns = []string{
	"created_at", "username", "token_name", "group", "model_name",
	"prompt_tokens", "completion_tokens", "total_tokens",
	"cache_tokens", "cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h",
	"text_input", "text_output", "audio_input", "audio_output", "image_output",
	"image_cache_tokens", "billing_tokens",
	"quota", "cost_usd", "billing_source", "billing_mode", "matched_tier",
	"billing_unit", "fixed_price", "image_count", "request_rules",
	"model_ratio", "completion_ratio", "group_ratio", "user_group_ratio",
	"cache_ratio", "cache_creation_ratio", "audio_ratio", "audio_completion_ratio",
	"image_ratio", "model_price", "tool_surcharges", "usage_facts",
}

var performanceColumns = []string{
	"created_at", "model_name", "channel_id", "channel_name", "retry_chain",
	"is_stream", "stream_status", "use_time", "frt", "tokens_per_sec",
	"prompt_tokens", "completion_tokens", "request_id", "upstream_request_id",
}

// auditColumns 覆盖 logs 表里仍在写入的带操作者信息的记录（充值、系统事件），
// 以及迁移到审计表之前留下的历史登录/管理记录。
// 新的登录、管理与安全事件写在 audit_logs，由审计日志页面及其自己的导出负责。
var auditColumns = []string{
	"created_at", "type", "username", "user_id",
	"admin_username", "admin_id", "admin_role", "auth_method",
	"target_username", "target_user_id",
	"audit_method", "audit_route", "audit_path", "audit_status", "audit_success",
	"payment_method", "callback_payment_method", "caller_ip", "server_ip", "node_name",
	"login_method", "user_agent", "request_path", "ip", "content",
}

// customerInvoiceColumns 客户对账单：唯一一个可以直接发给客户的列集。
// 内容就是 logExportCustomerColumns 本身——两者必须一致，否则「可发给客户」
// 这个徽章就名不副实。顺序在这里定，保证文件里列的排布是给人看的。
var customerInvoiceColumns = logExportCustomerColumns

// operationsColumns 运营统计：刻意**不依赖 other**，于是 SQL 不 SELECT other、
// 也不做 JSON 解析。这是所有模板里最快、文件最小的一个（实测渲染 0.23µs/行 vs
// 依赖 other 的 10.3µs/行），适合拉整月数据丢进 Excel 透视。
var operationsColumns = []string{
	"created_at", "username", "user_id", "group", "token_name", "model_name",
	"is_stream", "prompt_tokens", "completion_tokens", "total_tokens",
	"quota", "cost_usd", "use_time",
}

// anomalyColumns 异常排查：配合异常筛选使用。筛 usage_source=estimated，
// 导出后按 cost_usd 倒序，一眼看到所有被多收的单子。
var anomalyColumns = []string{
	"created_at", "request_id", "username", "model_name",
	"channel_id", "channel_name", "retry_count", "retry_chain",
	"anomaly_flags", "usage_source", "settlement_state",
	"stream_status_text", "stream_end_reason", "stream_error_count",
	"confirmed_usage", "effective_content", "client_gone",
	"prompt_tokens", "completion_tokens", "quota", "cost_usd",
	"intended_quota", "reserved_quota", "quota_saturation",
	"use_time", "frt", "content",
}

// BuiltinLogExportTemplates 返回内置模板列表（默认模板排在首位）。
func BuiltinLogExportTemplates() []BuiltinLogExportTemplate {
	full := make([]string, 0, len(logExportColumns))
	for i := range logExportColumns {
		full = append(full, logExportColumns[i].Key)
	}
	return []BuiltinLogExportTemplate{
		{ID: LogExportTemplateAsDisplayed, Name: "As Displayed", Columns: asDisplayedColumns, IsDefault: true,
			Purpose: LogExportPurposeAnalytics, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateCustomerInvoice, Name: "Customer Invoice", Columns: customerInvoiceColumns,
			Purpose: LogExportPurposeReconciliation, Audience: LogExportAudienceCustomer.String()},
		{ID: LogExportTemplateBilling, Name: "Billing Details", Columns: billingColumns,
			Purpose: LogExportPurposeReconciliation, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateOperations, Name: "Operations Summary", Columns: operationsColumns,
			Purpose: LogExportPurposeAnalytics, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateAnomaly, Name: "Anomaly Investigation", Columns: anomalyColumns,
			Purpose: LogExportPurposeDiagnostic, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplatePerformance, Name: "Performance Diagnostics", Columns: performanceColumns,
			Purpose: LogExportPurposeDiagnostic, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateLegacy, Name: "Legacy Export", Columns: legacyColumns,
			Purpose: LogExportPurposeReconciliation, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateAudit, Name: "Top-up & Legacy Audit", Columns: auditColumns,
			Purpose: LogExportPurposeAudit, Audience: LogExportAudienceInternal.String()},
		{ID: LogExportTemplateFull, Name: "All Columns", Columns: full,
			Purpose: LogExportPurposeDiagnostic, Audience: LogExportAudienceInternal.String()},
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

// ResolveLogExportColumns 校验并解析列集合，按「管理员 / 非管理员」两档判权。
// 管理员一档不含 root 专属列：需要 root 列的调用方用 ResolveLogExportColumnsForRole。
func ResolveLogExportColumns(keys []string, isAdmin bool) (*LogExportColumnSet, error) {
	role := common.RoleCommonUser
	if isAdmin {
		role = common.RoleAdminUser
	}
	return ResolveLogExportColumnsForRole(keys, role)
}

// ResolveLogExportColumnsForRole 校验并解析列集合。
// keys 为空时回落到默认模板；权限不足的列被静默剔除并记入 Dropped
// ——静默而非报错，是为了让管理员共享的模板被权限更低的用户套用时仍能正常导出。
func ResolveLogExportColumnsForRole(keys []string, viewerRole int) (*LogExportColumnSet, error) {
	isAdmin := viewerRole >= common.RoleAdminUser
	isRoot := viewerRole >= common.RoleRootUser
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
		if (col.AdminOnly && !isAdmin) || (col.RootOnly && !isRoot) {
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
	// other 的解析缓存由 rowCtx.otherMap 按行归属自动失效，这里无需重置：
	// 行级筛选可能已经为本行解析过一次，重置会让它白白再解析一遍。
	for i, col := range s.Columns {
		buf[i] = col.Extract(l, ctx)
	}
	return buf
}
