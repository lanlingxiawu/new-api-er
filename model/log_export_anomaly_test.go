package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherWith 拼一条 other，便于按需组合各种异常特征。
func anomalyOther(streamResult, streamStatus, adminInfo string) string {
	parts := ""
	add := func(key, raw string) {
		if raw == "" {
			return
		}
		if parts != "" {
			parts += ","
		}
		parts += `"` + key + `":` + raw
	}
	add("stream_result", streamResult)
	add("stream_status", streamStatus)
	add("admin_info", adminInfo)
	return "{" + parts + "}"
}

func TestLogAnomalyFlags(t *testing.T) {
	cases := []struct {
		name  string
		other string
		want  []string
	}{
		{
			name:  "非流式请求没有 stream_result，缺字段不等于异常",
			other: `{"model_ratio":2.5}`,
			want:  nil,
		},
		{
			name:  "完全正常的流式请求",
			other: anomalyOther(`{"usage_source":"upstream","settlement_state":"settled"}`, `{"status":"ok","end_reason":"normal"}`, ""),
			want:  nil,
		},
		{
			name:  "空 other",
			other: "",
			want:  nil,
		},
		{
			name:  "本次多收费事故的特征：上游没给用量，按估算收费",
			other: anomalyOther(`{"usage_source":"estimated","settlement_state":"settled"}`, `{"status":"error","end_reason":"upstream_incomplete"}`, ""),
			want:  []string{LogAnomalyEstimatedUsage, LogAnomalyStreamError},
		},
		{
			name:  "混合用量",
			other: anomalyOther(`{"usage_source":"mixed"}`, "", ""),
			want:  []string{LogAnomalyMixedUsage},
		},
		{
			name:  "判定为不计费（反向风险：本该收费却没收）",
			other: anomalyOther(`{"usage_source":"none"}`, "", ""),
			want:  []string{LogAnomalyNoUsage},
		},
		{
			name:  "结算未落终态",
			other: anomalyOther(`{"usage_source":"upstream","settlement_state":"partial"}`, "", ""),
			want:  []string{LogAnomalyUnsettled},
		},
		{
			name:  "结算状态缺失按旧日志处理，不判异常",
			other: anomalyOther(`{"usage_source":"upstream"}`, "", ""),
			want:  nil,
		},
		{
			name:  "流内有错误但结束状态为 ok",
			other: anomalyOther("", `{"status":"ok","error_count":2}`, ""),
			want:  []string{LogAnomalyStreamError},
		},
		{
			name:  "单渠道未重试",
			other: anomalyOther("", "", `{"use_channel":["12"]}`),
			want:  nil,
		},
		{
			name:  "发生过跨渠道重试",
			other: anomalyOther("", "", `{"use_channel":["12","31"]}`),
			want:  []string{LogAnomalyRetried},
		},
		{
			name:  "配额饱和截断，存在即异常",
			other: anomalyOther("", "", `{"quota_saturation":{"clamped":true}}`),
			want:  []string{LogAnomalyQuotaSaturated},
		},
		{
			name: "多个异常同时命中，按固定顺序输出",
			other: anomalyOther(`{"usage_source":"estimated","settlement_state":"failed"}`,
				`{"status":"error","error_count":1}`, `{"use_channel":["1","2","3"],"quota_saturation":{}}`),
			want: []string{LogAnomalyEstimatedUsage, LogAnomalyStreamError,
				LogAnomalyUnsettled, LogAnomalyRetried, LogAnomalyQuotaSaturated},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := logAnomalyFlags(&Log{Other: tc.other}, newTestRowCtx())
			assert.Equal(t, tc.want, got)
		})
	}
}

// anomaly_flags 列与异常筛选必须出自同一个判定函数，否则会出现
// 「筛出来的行标记为空」或「标记了却筛不到」。
func TestLogExportAnomaly_FilterAndFlagsAgree(t *testing.T) {
	rows := []*Log{
		{Id: 1, Other: `{"model_ratio":1}`},
		{Id: 2, Other: anomalyOther(`{"usage_source":"estimated"}`, "", "")},
		{Id: 3, Other: anomalyOther(`{"usage_source":"upstream","settlement_state":"settled"}`, `{"status":"ok"}`, "")},
		{Id: 4, Other: anomalyOther("", "", `{"use_channel":["1","2"]}`)},
		{Id: 5, Other: ""},
	}
	ctx := newTestRowCtx()

	var flagged, matched []int
	for _, l := range rows {
		if len(logAnomalyFlags(l, ctx)) > 0 {
			flagged = append(flagged, l.Id)
		}
		if logExportRowMatchesAnomaly(l, ctx, nil) {
			matched = append(matched, l.Id)
		}
	}
	assert.Equal(t, []int{2, 4}, flagged)
	assert.Equal(t, flagged, matched, "「任意异常」筛选的结果集必须与被标记的行完全一致")

	// 指定种类时只命中该种类。
	var estimated []int
	for _, l := range rows {
		if logExportRowMatchesAnomaly(l, ctx, []string{LogAnomalyEstimatedUsage}) {
			estimated = append(estimated, l.Id)
		}
	}
	assert.Equal(t, []int{2}, estimated)
}

func TestNormalizeAnomalyKinds(t *testing.T) {
	assert.Nil(t, normalizeAnomalyKinds(nil))
	assert.Nil(t, normalizeAnomalyKinds([]string{"nope", " "}), "未知取值必须被丢弃")
	assert.Equal(t,
		[]string{LogAnomalyEstimatedUsage, LogAnomalyRetried},
		normalizeAnomalyKinds([]string{"retried", "estimated_usage", "retried", "bogus"}),
		"去重并按固定顺序整理")
}

// 行级筛选：多个条件之间 AND，单个条件内部多个取值 OR。
func TestLogExportFilter_MatchesRow(t *testing.T) {
	estimatedRetried := &Log{Other: anomalyOther(
		`{"usage_source":"estimated","settlement_state":"settled"}`,
		`{"status":"error","end_reason":"upstream_incomplete"}`,
		`{"use_channel":["1","2","3"]}`)}
	plain := &Log{Other: anomalyOther(`{"usage_source":"upstream","settlement_state":"settled"}`, `{"status":"ok"}`, "")}
	ctx := newTestRowCtx()

	two := 2
	four := 4
	cases := []struct {
		name   string
		filter LogExportFilter
		row    *Log
		want   bool
	}{
		{"用量来源命中", LogExportFilter{UsageSource: []string{"estimated", "mixed"}}, estimatedRetried, true},
		{"用量来源不命中", LogExportFilter{UsageSource: []string{"estimated"}}, plain, false},
		{"结束原因命中", LogExportFilter{StreamEndReason: []string{"upstream_incomplete"}}, estimatedRetried, true},
		{"结束原因不命中", LogExportFilter{StreamEndReason: []string{"client_disconnect"}}, estimatedRetried, false},
		{"结算状态命中", LogExportFilter{SettlementState: []string{"settled"}}, plain, true},
		{"重试次数达标", LogExportFilter{MinRetryCount: &two}, estimatedRetried, true},
		{"重试次数不足", LogExportFilter{MinRetryCount: &four}, estimatedRetried, false},
		{"无重试记录时次数条件不通过", LogExportFilter{MinRetryCount: &two}, plain, false},
		{"多条件 AND：都满足", LogExportFilter{UsageSource: []string{"estimated"}, MinRetryCount: &two}, estimatedRetried, true},
		{"多条件 AND：一个不满足即淘汰", LogExportFilter{UsageSource: []string{"estimated"}, MinRetryCount: &four}, estimatedRetried, false},
		{"无条件时全部通过", LogExportFilter{}, plain, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.filter.matchesRow(tc.row, ctx))
		})
	}
}

func TestLogExportFilter_HasRowFilter(t *testing.T) {
	one := 1
	assert.False(t, LogExportFilter{}.HasRowFilter())
	assert.False(t, LogExportFilter{QuotaMin: &one}.HasRowFilter(), "数值条件下推 SQL，不算行级筛选")
	assert.True(t, LogExportFilter{AnomalyOnly: true}.HasRowFilter())
	assert.True(t, LogExportFilter{UsageSource: []string{"estimated"}}.HasRowFilter())
	assert.True(t, LogExportFilter{MinRetryCount: &one}.HasRowFilter())
}

// other 的解析缓存必须按行失效：行级筛选先读一次，渲染再读一次，
// 只能解析一次；换了一行必须重新解析。
func TestRowCtx_OtherCacheIsPerRow(t *testing.T) {
	ctx := newTestRowCtx()
	first := &Log{Other: `{"usage_source":"estimated"}`}
	second := &Log{Other: `{"usage_source":"upstream"}`}

	require.Equal(t, "estimated", otherValue(ctx.otherMap(first), "usage_source"))
	// 同一行重复取用不应改变结果，且复用同一份缓存。
	cached := ctx.otherMap(first)
	assert.Equal(t, "estimated", otherValue(cached, "usage_source"))

	assert.Equal(t, "upstream", otherValue(ctx.otherMap(second), "usage_source"),
		"换行后必须重新解析，不能读到上一行的数据")
	assert.Equal(t, "estimated", otherValue(ctx.otherMap(first), "usage_source"),
		"再切回上一行也要重新解析")
}

// 「输出为 0 却计费」是一类真实存在的可疑账单，但只有下限条件时写不出来：
// completion_tokens 需要**上限**才能表达「等于 0」。
//
// 刻意不把它做成异常标记：embedding 天然没有输出 token，按次/按张计费的请求
// 同样如此，一刀切会把大量正常账单标成异常（生产数据实测 7 天 7 条里有 2 条
// 是 text-embedding-*）。这里提供精确的数值条件，由使用者自行判断。
func TestLogExportFilter_ZeroOutputButCharged(t *testing.T) {
	requireLogDB(t)
	username := uniq("zeroout")
	base := time.Now().Unix() - 8200

	mk := func(completion, quota int) {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base
			l.CompletionTokens = completion
			l.PromptTokens = 10
			l.Quota = quota
		})
	}
	mk(0, 135) // 目标：输出 0 却扣了钱
	mk(0, 0)   // 输出 0 且未计费——不该命中
	mk(50, 90) // 有输出——不该命中

	zero, one := 0, 1
	filter := LogExportFilter{
		Username:            username,
		StartTimestamp:      base - 10,
		EndTimestamp:        base + 10,
		CompletionTokensMax: &zero,
		QuotaMin:            &one,
	}
	logs, err := scanLogExportBatch(context.Background(), filter,
		[]string{"id", "created_at", "completion_tokens", "quota"}, base-10, base+10, nil, 100)
	require.NoError(t, err)
	require.Len(t, logs, 1, "只应命中「输出 0 且计费 > 0」那一条")
	assert.Equal(t, 0, logs[0].CompletionTokens)
	assert.Equal(t, 135, logs[0].Quota)
}

// 上限条件里 0 必须当作有效取值，不能被当成「未设置」丢掉（Rule 5）。
func TestApplyLogExportFilter_ZeroIsAMeaningfulBound(t *testing.T) {
	requireLogDB(t)
	username := uniq("zerobound")
	base := time.Now().Unix() - 8400
	for _, c := range []int{0, 7} {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base
			l.CompletionTokens = c
			l.Quota = 1
		})
	}
	zero := 0
	filter := LogExportFilter{
		Username: username, StartTimestamp: base - 10, EndTimestamp: base + 10,
		CompletionTokensMax: &zero,
	}
	logs, err := scanLogExportBatch(context.Background(), filter,
		[]string{"id", "created_at", "completion_tokens"}, base-10, base+10, nil, 100)
	require.NoError(t, err)
	require.Len(t, logs, 1, "completion_tokens_max=0 必须生效而不是被忽略")
	assert.Equal(t, 0, logs[0].CompletionTokens)
}

// 「计费了」在系统里就是 quota > 0（quota 是整数额度列）。
// 这条断言的价值在于把语义钉死：不能再用「>= 1」这种需要先知道单位的魔法数字，
// 更不能让人拿界面上的美元数字去填额度阈值——两者差 QuotaPerUnit 倍。
func TestLogExportFilter_ChargedMeansQuotaAboveZero(t *testing.T) {
	requireLogDB(t)
	username := uniq("charged")
	base := time.Now().Unix() - 8600
	// 1 额度 = $0.000002，是系统能记录的最小非零费用。
	for _, q := range []int{0, 1, 50} {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base
			l.Quota = q
		})
	}

	scan := func(f LogExportFilter) []*Log {
		f.Username = username
		f.StartTimestamp, f.EndTimestamp = base-10, base+10
		logs, err := scanLogExportBatch(context.Background(), f,
			[]string{"id", "created_at", "quota"}, base-10, base+10, nil, 100)
		require.NoError(t, err)
		return logs
	}

	yes, no := true, false
	charged := scan(LogExportFilter{Charged: &yes})
	require.Len(t, charged, 2, "quota=1 这种最小额度也算计费了，不能被漏掉")
	for _, l := range charged {
		assert.Positive(t, l.Quota)
	}

	free := scan(LogExportFilter{Charged: &no})
	require.Len(t, free, 1)
	assert.Zero(t, free[0].Quota)

	// 与等价的 quota_min=1 结果一致——语义相同，只是不必让人知道单位。
	one := 1
	assert.Len(t, scan(LogExportFilter{QuotaMin: &one}), len(charged))
}
