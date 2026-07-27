package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRowCtx() *rowCtx {
	return &rowCtx{
		loc:          time.UTC,
		channelNames: map[int]string{},
		translate:    func(key string) string { return key },
	}
}

// renderOne 渲染单列，便于逐列断言。
func renderOne(t *testing.T, key string, l *Log, ctx *rowCtx) string {
	t.Helper()
	set, err := ResolveLogExportColumns([]string{key}, true)
	require.NoError(t, err)
	row := set.Render(l, ctx, nil)
	require.Len(t, row, 1)
	return row[0]
}

func TestLogExportColumns_RegistryIntegrity(t *testing.T) {
	cols := LogExportColumns()
	require.NotEmpty(t, cols)

	seen := map[string]bool{}
	for _, col := range cols {
		assert.NotEmpty(t, col.Key, "column key must not be empty")
		assert.NotEmpty(t, col.Label, "column %s must have an English label", col.Key)
		assert.NotEmpty(t, col.Group, "column %s must belong to a group", col.Key)
		assert.NotNil(t, col.Extract, "column %s must have an extractor", col.Key)
		assert.False(t, seen[col.Key], "duplicate column key %s", col.Key)
		seen[col.Key] = true
	}
}

// 页面列改动后模板必须同步更新，否则「页面所见」名不副实。
func TestLogExportColumns_AsDisplayedTemplateIsStable(t *testing.T) {
	want := []string{
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
	assert.Equal(t, want, DefaultLogExportColumns())

	tpl, ok := LookupBuiltinLogExportTemplate(LogExportTemplateAsDisplayed)
	require.True(t, ok)
	assert.True(t, tpl.IsDefault, "as_displayed must be the default template")
	assert.Equal(t, want, tpl.Columns)
}

func TestLogExportColumns_BuiltinTemplatesResolve(t *testing.T) {
	for _, tpl := range BuiltinLogExportTemplates() {
		tpl := tpl
		t.Run(tpl.ID, func(t *testing.T) {
			set, err := ResolveLogExportColumns(tpl.Columns, true)
			require.NoError(t, err, "builtin template %s references an unknown column", tpl.ID)
			assert.Equal(t, len(tpl.Columns), len(set.Keys))
			assert.LessOrEqual(t, len(tpl.Columns), LogExportMaxColumns)
		})
	}
}

func TestResolveLogExportColumns_EmptyFallsBackToDefault(t *testing.T) {
	set, err := ResolveLogExportColumns(nil, true)
	require.NoError(t, err)
	assert.Equal(t, DefaultLogExportColumns(), set.Keys)
}

func TestResolveLogExportColumns_UnknownKeyRejected(t *testing.T) {
	_, err := ResolveLogExportColumns([]string{"created_at", "not_a_column"}, true)
	require.Error(t, err)
	var unknown *UnknownLogExportColumnError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "not_a_column", unknown.Key)
}

func TestResolveLogExportColumns_TooManyColumns(t *testing.T) {
	keys := make([]string, LogExportMaxColumns+1)
	for i := range keys {
		keys[i] = "created_at"
	}
	_, err := ResolveLogExportColumns(keys, true)
	assert.ErrorIs(t, err, ErrLogExportTooManyColumns)
}

func TestResolveLogExportColumns_DeduplicatesKeepingOrder(t *testing.T) {
	set, err := ResolveLogExportColumns([]string{"quota", "created_at", "quota"}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"quota", "created_at"}, set.Keys)
}

// 非管理员必须拿不到渠道/重试等内部路由信息，与列表接口剥离 admin_info 的口径一致。
func TestResolveLogExportColumns_AdminOnlyDroppedForNonAdmin(t *testing.T) {
	keys := []string{"created_at", "channel_id", "channel_name", "retry_chain", "quota"}
	set, err := ResolveLogExportColumns(keys, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"created_at", "quota"}, set.Keys)
	assert.ElementsMatch(t, []string{"channel_id", "channel_name", "retry_chain"}, set.Dropped)

	adminSet, err := ResolveLogExportColumns(keys, true)
	require.NoError(t, err)
	assert.Equal(t, keys, adminSet.Keys)
	assert.Empty(t, adminSet.Dropped)
}

func TestResolveLogExportColumns_AllAdminOnlyForNonAdminIsError(t *testing.T) {
	_, err := ResolveLogExportColumns([]string{"channel_id", "retry_chain"}, false)
	assert.ErrorIs(t, err, ErrLogExportNoColumns)
}

// 不勾选 other 依赖列时不应触发 JSON 解析，也不该把 other 列查出来。
func TestLogExportColumnSet_NeedOtherAndSelectFields(t *testing.T) {
	plain, err := ResolveLogExportColumns([]string{"created_at", "quota", "username"}, true)
	require.NoError(t, err)
	assert.False(t, plain.NeedOther)
	assert.NotContains(t, plain.SelectFields(), "other")
	assert.Contains(t, plain.SelectFields(), "id")
	assert.Contains(t, plain.SelectFields(), "created_at")
	assert.Contains(t, plain.SelectFields(), "quota")

	withOther, err := ResolveLogExportColumns([]string{"created_at", "frt"}, true)
	require.NoError(t, err)
	assert.True(t, withOther.NeedOther)
	assert.Contains(t, withOther.SelectFields(), "other")

	withChannel, err := ResolveLogExportColumns([]string{"channel_name"}, true)
	require.NoError(t, err)
	assert.True(t, withChannel.NeedChannelName)
}

func TestLogExportColumnSet_TotalTokensSelectsBothSources(t *testing.T) {
	set, err := ResolveLogExportColumns([]string{"total_tokens", "tokens_per_sec"}, true)
	require.NoError(t, err)
	fields := set.SelectFields()
	assert.Contains(t, fields, "prompt_tokens")
	assert.Contains(t, fields, "completion_tokens")
	assert.Contains(t, fields, "use_time")
}

func TestLogExportExtract_DirectColumns(t *testing.T) {
	l := &Log{
		Id: 42, UserId: 7, CreatedAt: 1700000000, Type: LogTypeConsume,
		Username: "alice", TokenName: "tok", TokenId: 9, Group: "vip",
		ModelName: "gpt-4o", Ip: "1.2.3.4", Content: "done",
		PromptTokens: 100, CompletionTokens: 50, UseTime: 5, IsStream: true,
		RequestId: "req-1", UpstreamRequestId: "up-1",
	}
	ctx := newTestRowCtx()

	assert.Equal(t, "2023-11-14 22:13:20", renderOne(t, "created_at", l, ctx))
	assert.Equal(t, "42", renderOne(t, "id", l, ctx))
	assert.Equal(t, "alice", renderOne(t, "username", l, ctx))
	assert.Equal(t, "7", renderOne(t, "user_id", l, ctx))
	assert.Equal(t, "tok", renderOne(t, "token_name", l, ctx))
	assert.Equal(t, "9", renderOne(t, "token_id", l, ctx))
	assert.Equal(t, "vip", renderOne(t, "group", l, ctx))
	assert.Equal(t, "gpt-4o", renderOne(t, "model_name", l, ctx))
	assert.Equal(t, "1.2.3.4", renderOne(t, "ip", l, ctx))
	assert.Equal(t, "done", renderOne(t, "content", l, ctx))
	assert.Equal(t, "100", renderOne(t, "prompt_tokens", l, ctx))
	assert.Equal(t, "50", renderOne(t, "completion_tokens", l, ctx))
	assert.Equal(t, "150", renderOne(t, "total_tokens", l, ctx))
	assert.Equal(t, "5", renderOne(t, "use_time", l, ctx))
	assert.Equal(t, "true", renderOne(t, "is_stream", l, ctx))
	assert.Equal(t, "10", renderOne(t, "tokens_per_sec", l, ctx))
	assert.Equal(t, "req-1", renderOne(t, "request_id", l, ctx))
	assert.Equal(t, "up-1", renderOne(t, "upstream_request_id", l, ctx))
	assert.Equal(t, LogExportTypeI18nKey(LogTypeConsume), renderOne(t, "type", l, ctx))
}

// 「类型」列每行都要翻译，必须缓存：否则千万行导出会产生千万次 i18n 调用。
func TestRowCtx_TranslationIsMemoized(t *testing.T) {
	calls := 0
	ctx := &rowCtx{translate: func(key string) string {
		calls++
		return "T:" + key
	}}

	assert.Equal(t, "T:a", ctx.t("a"))
	assert.Equal(t, "T:a", ctx.t("a"))
	assert.Equal(t, "T:a", ctx.t("a"))
	assert.Equal(t, 1, calls, "repeated keys must hit the cache")

	assert.Equal(t, "T:b", ctx.t("b"))
	assert.Equal(t, 2, calls)

	// 没有 translate 时原样返回 key，不 panic。
	assert.Equal(t, "x", (&rowCtx{}).t("x"))
}

func TestLogExportExtract_TypeCellCoversEveryLogType(t *testing.T) {
	cases := map[int]string{
		LogTypeUnknown: "log_export.type.unknown",
		LogTypeTopup:   "log_export.type.topup",
		LogTypeConsume: "log_export.type.consume",
		LogTypeManage:  "log_export.type.manage",
		LogTypeSystem:  "log_export.type.system",
		LogTypeError:   "log_export.type.error",
		LogTypeRefund:  "log_export.type.refund",
		LogTypeLogin:   "log_export.type.login",
	}
	ctx := newTestRowCtx()
	for logType, want := range cases {
		assert.Equal(t, want, renderOne(t, "type", &Log{Type: logType}, ctx))
	}
	// 未知的新类型值走 default 分支。
	assert.Equal(t, "log_export.type.unknown", renderOne(t, "type", &Log{Type: 99}, ctx))
}

func TestLogExportExtract_TokensPerSecEdgeCases(t *testing.T) {
	ctx := newTestRowCtx()
	assert.Equal(t, "", renderOne(t, "tokens_per_sec", &Log{UseTime: 0, CompletionTokens: 10}, ctx))
	assert.Equal(t, "", renderOne(t, "tokens_per_sec", &Log{UseTime: 3, CompletionTokens: 0}, ctx))
	assert.Equal(t, "3.33", renderOne(t, "tokens_per_sec", &Log{UseTime: 3, CompletionTokens: 10}, ctx))
}

func TestLogExportExtract_CostUsdBoundaries(t *testing.T) {
	ctx := newTestRowCtx()
	assert.Equal(t, "0", renderOne(t, "cost_usd", &Log{Quota: 0}, ctx))
	// 退款日志的 quota 为负，必须保留负号。
	negative := renderOne(t, "cost_usd", &Log{Quota: -500000}, ctx)
	assert.Equal(t, "-1", negative)
	assert.Equal(t, "-500000", renderOne(t, "quota", &Log{Quota: -500000}, ctx))
}

func TestLogExportExtract_OtherFields(t *testing.T) {
	l := &Log{Other: `{"frt":123.0,"cache_tokens":7,"model_ratio":0.5,"billing_source":"subscription","is_model_mapped":true,"upstream_model_name":"gpt-4o-2024"}`}
	ctx := newTestRowCtx()

	assert.Equal(t, "123", renderOne(t, "frt", l, ctx))
	assert.Equal(t, "7", renderOne(t, "cache_tokens", l, ctx))
	assert.Equal(t, "0.5", renderOne(t, "model_ratio", l, ctx))
	assert.Equal(t, "subscription", renderOne(t, "billing_source", l, ctx))
	assert.Equal(t, "true", renderOne(t, "is_model_mapped", l, ctx))
	assert.Equal(t, "gpt-4o-2024", renderOne(t, "upstream_model_name", l, ctx))
}

func TestLogExportExtract_OtherMissingOrMalformed(t *testing.T) {
	ctx := newTestRowCtx()
	// other 为空
	assert.Equal(t, "", renderOne(t, "frt", &Log{}, ctx))
	// other 非法 JSON：解析失败不应 panic，返回空串
	assert.Equal(t, "", renderOne(t, "frt", &Log{Other: "{not json"}, ctx))
	// 字段缺失
	assert.Equal(t, "", renderOne(t, "frt", &Log{Other: `{"cache_tokens":1}`}, ctx))
	// 类型不符：frt 是字符串时按原样输出，不 panic
	assert.Equal(t, "slow", renderOne(t, "frt", &Log{Other: `{"frt":"slow"}`}, ctx))
	// other 是 JSON 数组而非对象
	assert.Equal(t, "", renderOne(t, "frt", &Log{Other: `[1,2,3]`}, ctx))
}

func TestLogExportExtract_AdminInfoAndRetryChain(t *testing.T) {
	l := &Log{Other: `{"admin_info":{"use_channel":[1,2,3],"is_multi_key":true,"multi_key_index":2,"quota_saturation":{"op":"mul","kind":"overflow"}}}`}
	ctx := newTestRowCtx()

	assert.Equal(t, "1->2->3", renderOne(t, "retry_chain", l, ctx))
	assert.Equal(t, "true", renderOne(t, "is_multi_key", l, ctx))
	assert.Equal(t, "2", renderOne(t, "multi_key_index", l, ctx))
	// 复杂结构原样序列化，信息不丢。
	assert.Contains(t, renderOne(t, "quota_saturation", l, ctx), "overflow")

	// 无 admin_info / use_channel 为空时返回空串。
	assert.Equal(t, "", renderOne(t, "retry_chain", &Log{Other: `{}`}, ctx))
	assert.Equal(t, "", renderOne(t, "retry_chain", &Log{Other: `{"admin_info":{"use_channel":[]}}`}, ctx))
	assert.Equal(t, "", renderOne(t, "retry_chain", &Log{Other: `{"admin_info":{"use_channel":"x"}}`}, ctx))
}

func TestLogExportExtract_AuditInfo(t *testing.T) {
	l := &Log{Other: `{"audit_info":{"method":"POST","route":"/api/user","status":200,"success":true},"admin_info":{"admin_username":"root","auth_method":"session"}}`}
	ctx := newTestRowCtx()

	assert.Equal(t, "POST", renderOne(t, "audit_method", l, ctx))
	assert.Equal(t, "/api/user", renderOne(t, "audit_route", l, ctx))
	assert.Equal(t, "200", renderOne(t, "audit_status", l, ctx))
	assert.Equal(t, "true", renderOne(t, "audit_success", l, ctx))
	assert.Equal(t, "root", renderOne(t, "admin_username", l, ctx))
	assert.Equal(t, "session", renderOne(t, "auth_method", l, ctx))
}

func TestLogExportExtract_ChannelNameFromCacheAndRow(t *testing.T) {
	ctx := newTestRowCtx()
	ctx.channelNames[5] = "azure-east"

	assert.Equal(t, "azure-east", renderOne(t, "channel_name", &Log{ChannelId: 5}, ctx))
	// 行上已带名字时优先用行上的值。
	assert.Equal(t, "inline", renderOne(t, "channel_name", &Log{ChannelId: 5, ChannelName: "inline"}, ctx))
	// 未解析出的渠道（已删除）留空，不报错。
	assert.Equal(t, "", renderOne(t, "channel_name", &Log{ChannelId: 6}, ctx))
}

func TestLogExportExtract_OtherRawKeepsOriginalJSON(t *testing.T) {
	raw := `{"frt":1,"admin_info":{"use_channel":[1]}}`
	assert.Equal(t, raw, renderOne(t, "other_raw", &Log{Other: raw}, newTestRowCtx()))
}

func TestLogExportColumnSet_RenderReusesBufferAndResetsOtherPerRow(t *testing.T) {
	set, err := ResolveLogExportColumns([]string{"frt", "cache_tokens"}, true)
	require.NoError(t, err)
	ctx := newTestRowCtx()

	buf := make([]string, 2)
	first := set.Render(&Log{Other: `{"frt":1,"cache_tokens":2}`}, ctx, buf)
	assert.Equal(t, []string{"1", "2"}, first)

	// 第二行必须重新解析，不能复用上一行的 other。
	second := set.Render(&Log{Other: `{"frt":9,"cache_tokens":8}`}, ctx, buf)
	assert.Equal(t, []string{"9", "8"}, second)

	empty := set.Render(&Log{}, ctx, buf)
	assert.Equal(t, []string{"", ""}, empty)
}

func TestLogExportColumnSet_HeaderI18nKeysMatchColumnKeys(t *testing.T) {
	set, err := ResolveLogExportColumns([]string{"created_at", "quota"}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"log_export.col.created_at", "log_export.col.quota"}, set.HeaderI18nKeys())
}

func TestFormatAny_Values(t *testing.T) {
	assert.Equal(t, "", formatAny(nil))
	assert.Equal(t, "abc", formatAny("abc"))
	assert.Equal(t, "true", formatAny(true))
	assert.Equal(t, "5", formatAny(float64(5)))
	assert.Equal(t, "5.25", formatAny(float64(5.25)))
	assert.Equal(t, "1|2", formatAny([]any{float64(1), float64(2)}))
	assert.Equal(t, "-3", formatAny(float64(-3)))
}
