package model

import (
	"slices"
	"strings"
)

// 异常标记。取值顺序固定（见 logExportAnomalyOrder），导出后按 anomaly_flags 列
// 排序即可把同类问题聚到一起。
const (
	// LogAnomalyEstimatedUsage 上游没有下发用量，本站按本地估算收费。
	// 这是「多收费」类事故的首要特征。
	LogAnomalyEstimatedUsage = "estimated_usage"
	// LogAnomalyMixedUsage 部分上游用量 + 部分本地估算。
	LogAnomalyMixedUsage = "mixed_usage"
	// LogAnomalyNoUsage 判定为不计费。需要关注的是反向风险：本该收费却没收。
	LogAnomalyNoUsage = "no_usage"
	// LogAnomalyStreamError 流异常结束或流内出现过错误。
	LogAnomalyStreamError = "stream_error"
	// LogAnomalyUnsettled 结算未落到 settled 终态。
	LogAnomalyUnsettled = "settlement_unsettled"
	// LogAnomalyRetried 发生过跨渠道重试。
	LogAnomalyRetried = "retried"
	// LogAnomalyQuotaSaturated 配额计算发生饱和截断。它存在本身就是异常。
	LogAnomalyQuotaSaturated = "quota_saturated"
)

// logExportAnomalyOrder 决定 anomaly_flags 列里各标记的输出顺序。
// 固定顺序才能让「同样的一组异常」在文件里拼出同样的字符串，进而可被排序分组。
var logExportAnomalyOrder = []string{
	LogAnomalyEstimatedUsage,
	LogAnomalyMixedUsage,
	LogAnomalyNoUsage,
	LogAnomalyStreamError,
	LogAnomalyUnsettled,
	LogAnomalyRetried,
	LogAnomalyQuotaSaturated,
}

// LogExportAnomalyKinds 返回全部异常标记（供接口下发给前端做筛选项）。
func LogExportAnomalyKinds() []string {
	out := make([]string, len(logExportAnomalyOrder))
	copy(out, logExportAnomalyOrder)
	return out
}

// logAnomalyFlags 判定一行日志命中了哪些异常。
//
// **这是异常判定的唯一入口**：筛选（AnomalyPreset）与渲染（anomaly_flags 列）
// 都必须走它。两边各写一份判断迟早会漂移，结果是「筛出来的行标记为空」或
// 「标记了却筛不到」——排查工具自己出这种问题，比没有这个工具更糟。
//
// 返回 nil 表示该行没有任何异常特征。
func logAnomalyFlags(l *Log, ctx *rowCtx) []string {
	if l == nil || ctx == nil {
		return nil
	}
	hit := make(map[string]bool, len(logExportAnomalyOrder))

	// 非流式请求没有 stream_result，此时「字段缺失」不等于「异常」——
	// 绝不能把空当成命中，否则所有非流式日志都会被标成异常。
	if sr := ctx.streamResult(l); sr != nil {
		switch usageSource, _ := sr["usage_source"].(string); usageSource {
		case "estimated":
			hit[LogAnomalyEstimatedUsage] = true
		case "mixed":
			hit[LogAnomalyMixedUsage] = true
		case "none":
			hit[LogAnomalyNoUsage] = true
		}
		// 结算状态为空按「旧日志，未记录」处理，不判异常。
		if state, ok := sr["settlement_state"].(string); ok && state != "" && state != "settled" {
			hit[LogAnomalyUnsettled] = true
		}
	}

	if ss := ctx.streamStatus(l); ss != nil {
		if status, _ := ss["status"].(string); status == "error" {
			hit[LogAnomalyStreamError] = true
		}
		if count, ok := ss["error_count"].(float64); ok && count > 0 {
			hit[LogAnomalyStreamError] = true
		}
	}

	if admin := ctx.adminInfo(l); admin != nil {
		if chain, ok := admin["use_channel"].([]any); ok && len(chain) > 1 {
			hit[LogAnomalyRetried] = true
		}
		// quota_saturation 只在配额计算被钳住时才写入，存在即异常。
		if _, ok := admin["quota_saturation"]; ok {
			hit[LogAnomalyQuotaSaturated] = true
		}
	}

	if len(hit) == 0 {
		return nil
	}
	out := make([]string, 0, len(hit))
	for _, kind := range logExportAnomalyOrder {
		if hit[kind] {
			out = append(out, kind)
		}
	}
	return out
}

// logExportRowMatchesAnomaly 判断一行是否命中给定的异常筛选条件。
// kinds 为空表示「任意异常」（一键可疑筛选）。
func logExportRowMatchesAnomaly(l *Log, ctx *rowCtx, kinds []string) bool {
	flags := logAnomalyFlags(l, ctx)
	if len(flags) == 0 {
		return false
	}
	if len(kinds) == 0 {
		return true
	}
	for _, want := range kinds {
		if slices.Contains(flags, want) {
			return true
		}
	}
	return false
}

// normalizeAnomalyKinds 去重并按固定顺序整理筛选项，丢弃未知取值。
func normalizeAnomalyKinds(kinds []string) []string {
	if len(kinds) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		if k != "" && slices.Contains(logExportAnomalyOrder, k) {
			seen[k] = true
		}
	}
	if len(seen) == 0 {
		// 全是未知取值时返回 nil 而不是空切片：调用方一律用「nil 表示没有该条件」
		// 判断，空切片会让这两种情况在阅读时产生歧义。
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, kind := range logExportAnomalyOrder {
		if seen[kind] {
			out = append(out, kind)
		}
	}
	return out
}
