package controller

import "github.com/QuantumNous/new-api/model"

func channelHasSensitiveChanges(channel *PatchChannel, origin *model.Channel, requestData map[string]any) bool {
	if _, ok := requestData["type"]; ok && channel.Type != origin.Type {
		return true
	}
	if _, ok := requestData["key"]; ok && channel.Key != "" && channel.Key != origin.Key {
		return true
	}
	if _, ok := requestData["base_url"]; ok && !equalStringPtr(channel.BaseURL, origin.BaseURL) {
		return true
	}
	if _, ok := requestData["openai_organization"]; ok && !equalStringPtr(channel.OpenAIOrganization, origin.OpenAIOrganization) {
		return true
	}
	if _, ok := requestData["header_override"]; ok && !equalStringPtr(channel.HeaderOverride, origin.HeaderOverride) {
		return true
	}
	if _, ok := requestData["param_override"]; ok && !equalStringPtr(channel.ParamOverride, origin.ParamOverride) {
		return true
	}
	if _, ok := requestData["setting"]; ok && !equalStringPtr(channel.Setting, origin.Setting) {
		return true
	}
	// 每日金额上限属于金额管控，与放在 setting JSON 时的权限门槛保持一致：
	// 修改需要 ChannelSensitiveWrite。
	if _, ok := requestData["daily_quota_limit"]; ok && channel.DailyQuotaLimit != origin.DailyQuotaLimit {
		return true
	}
	if _, ok := requestData["daily_limit_auto_recover"]; ok && !equalIntPtr(channel.DailyLimitAutoRecover, origin.DailyLimitAutoRecover) {
		return true
	}
	if _, ok := requestData["daily_limit_recover_minutes"]; ok && channel.DailyLimitRecoverMinutes != origin.DailyLimitRecoverMinutes {
		return true
	}
	if _, ok := requestData["other"]; ok && channel.Other != origin.Other {
		return true
	}
	if _, ok := requestData["settings"]; ok && channel.OtherSettings != origin.OtherSettings {
		return true
	}
	if _, ok := requestData["key_mode"]; ok && channel.KeyMode != nil {
		return true
	}
	// Fail closed: any field present in the request that is neither a known
	// sensitive field (gated above) nor an explicitly classified non-sensitive
	// field must be treated as sensitive. This keeps a newly added channel field
	// from silently becoming editable by ChannelWrite-only admins until it is
	// consciously classified in channelNonSensitiveFields.
	for field := range requestData {
		if _, ok := channelSensitiveFields[field]; ok {
			continue
		}
		if _, ok := channelNonSensitiveFields[field]; ok {
			continue
		}
		if _, ok := channelOperationalFields[field]; ok {
			continue
		}
		if _, ok := channelReadOnlyFields[field]; ok {
			continue
		}
		return true
	}
	return false
}

// channelSensitiveFields lists the channel fields whose modification requires
// ChannelSensitiveWrite. They are each checked individually in
// channelHasSensitiveChanges with a precise old-vs-new comparison; this set is
// used to exclude them from the fail-closed scan for unknown fields.
var channelSensitiveFields = map[string]struct{}{
	"type":                {},
	"key":                 {},
	"base_url":            {},
	"openai_organization": {},
	"header_override":     {},
	"param_override":      {},
	"setting":             {},
	"other":               {},
	"settings":            {},
	"key_mode":            {},

	"daily_quota_limit":           {},
	"daily_limit_auto_recover":    {},
	"daily_limit_recover_minutes": {},
}

// channelOperationalFields lists fields managed by operation endpoints instead
// of the general channel edit endpoint.
var channelOperationalFields = map[string]struct{}{
	"status": {},
}

// channelReadOnlyFields lists server-managed/accounting fields that the general
// channel edit endpoint must ignore even if a client sends them.
var channelReadOnlyFields = map[string]struct{}{
	"created_time":               {},
	"test_time":                  {},
	"response_time":              {},
	"balance":                    {},
	"balance_updated_time":       {},
	"used_quota":                 {},
	"account_balance":            {},
	"account_balance_configured": {},

	// 服务端管理的禁用来源标记与展示字段：客户端传入一律清零/忽略，
	// 防止伪造「因每日上限被禁用」的状态骗过次日自动恢复。
	"daily_limit_disabled_at":   {},
	"daily_limit_disabled_date": {},
	"daily_usage":               {},
	// 限时恢复的轮次由服务端维护（恢复、手动启用、改恢复间隔时写入），客户端不能伪造。
	"daily_limit_period_start": {},
	// 统计口径已停用、统一按上游消耗统计；旧客户端仍可能回传，按只读忽略，不当作敏感修改。
	"daily_limit_basis": {},
}

func clearChannelReadOnlyFields(channel *PatchChannel, requestData map[string]any) {
	if _, ok := requestData["created_time"]; ok {
		channel.CreatedTime = 0
	}
	if _, ok := requestData["test_time"]; ok {
		channel.TestTime = 0
	}
	if _, ok := requestData["response_time"]; ok {
		channel.ResponseTime = 0
	}
	if _, ok := requestData["balance"]; ok {
		channel.Balance = 0
	}
	if _, ok := requestData["balance_updated_time"]; ok {
		channel.BalanceUpdatedTime = 0
	}
	if _, ok := requestData["used_quota"]; ok {
		channel.UsedQuota = 0
	}
	if _, ok := requestData["daily_limit_disabled_at"]; ok {
		channel.DailyLimitDisabledAt = 0
	}
	if _, ok := requestData["daily_limit_disabled_date"]; ok {
		channel.DailyLimitDisabledDate = 0
	}
	if _, ok := requestData["daily_usage"]; ok {
		channel.DailyUsage = nil
	}
	if _, ok := requestData["daily_limit_period_start"]; ok {
		channel.DailyLimitPeriodStart = 0
	}
}

// equalIntPtr 比较两个 *int 是否相等（均为 nil 视为相等）。
func equalIntPtr(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// channelNonSensitiveFields lists routing / server-managed channel
// fields a ChannelWrite admin may edit without ChannelSensitiveWrite. When a new
// field is added to model.Channel it must be added to either this set or
// channelSensitiveFields or channelOperationalFields; otherwise it falls through
// to the fail-closed branch and is treated as sensitive. The
// TestChannelFieldsAreClassified guard test enforces this.
var channelNonSensitiveFields = map[string]struct{}{
	"id":                  {},
	"test_model":          {},
	"name":                {},
	"weight":              {},
	"models":              {},
	"group":               {},
	"model_mapping":       {},
	"status_code_mapping": {},
	"priority":            {},
	"auto_ban":            {},
	"other_info":          {},
	"tag":                 {},
	"remark":              {},
	"channel_info":        {},
	"multi_key_mode":      {},
	"cost_ratio":          {},
}
