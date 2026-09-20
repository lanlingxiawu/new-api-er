package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func summaryAcc(t *testing.T, dims []string, maxGroups int) *logSummaryAccumulator {
	t.Helper()
	acc, err := newLogSummaryAccumulator(dims, time.UTC, maxGroups)
	require.NoError(t, err)
	return acc
}

func TestLogSummaryAccumulator_GroupsAndMetrics(t *testing.T) {
	acc := summaryAcc(t, []string{LogSummaryDimDate, LogSummaryDimModel}, 1000)
	day1 := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC).Unix()

	rows := []*Log{
		{CreatedAt: day1, ModelName: "gpt-4o", PromptTokens: 10, CompletionTokens: 5, Quota: 100},
		{CreatedAt: day1, ModelName: "gpt-4o", PromptTokens: 20, CompletionTokens: 7, Quota: 200},
		{CreatedAt: day1, ModelName: "claude", PromptTokens: 1, CompletionTokens: 1, Quota: 10},
		{CreatedAt: day2, ModelName: "gpt-4o", PromptTokens: 3, CompletionTokens: 4, Quota: 30},
	}
	for _, l := range rows {
		require.NoError(t, acc.Add(l))
	}
	assert.Equal(t, 3, acc.Groups())

	out := acc.Rows()
	require.Len(t, out, 3)
	// 按维度值排序：2026-09-18/claude, 2026-09-18/gpt-4o, 2026-09-19/gpt-4o
	assert.Equal(t, []string{"2026-09-18", "claude", "1", "1", "1", "2", "10"}, out[0][:7])
	assert.Equal(t, "2026-09-18", out[1][0])
	assert.Equal(t, "gpt-4o", out[1][1])
	assert.Equal(t, "2", out[1][2], "调用次数")
	assert.Equal(t, "30", out[1][3], "输入 token 合计")
	assert.Equal(t, "12", out[1][4], "输出 token 合计")
	assert.Equal(t, "42", out[1][5], "总 token")
	assert.Equal(t, "300", out[1][6], "额度合计")
	assert.Equal(t, "2026-09-19", out[2][0])
}

// 天边界必须按导出时区切，而不是服务器时区——否则跨时区对账对不上。
func TestLogSummaryAccumulator_DateUsesExportTimezone(t *testing.T) {
	// UTC 的 2026-09-18 23:30 在东八区已经是 09-19。
	ts := time.Date(2026, 9, 18, 23, 30, 0, 0, time.UTC).Unix()

	utcAcc, err := newLogSummaryAccumulator([]string{LogSummaryDimDate}, time.UTC, 100)
	require.NoError(t, err)
	require.NoError(t, utcAcc.Add(&Log{CreatedAt: ts}))
	assert.Equal(t, "2026-09-18", utcAcc.Rows()[0][0])

	shanghai := time.FixedZone("UTC+8", 8*3600)
	shAcc, err := newLogSummaryAccumulator([]string{LogSummaryDimDate}, shanghai, 100)
	require.NoError(t, err)
	require.NoError(t, shAcc.Add(&Log{CreatedAt: ts}))
	assert.Equal(t, "2026-09-19", shAcc.Rows()[0][0])
}

// 月度求和远超 int32：一天就能让 prompt_tokens 越过 20 亿。
// 这类溢出不会报错，只会算出一个负数总量。
func TestLogSummaryAccumulator_NoInt32Overflow(t *testing.T) {
	acc := summaryAcc(t, []string{LogSummaryDimModel}, 10)
	const perRow = 2_000_000_000
	for range 3 {
		require.NoError(t, acc.Add(&Log{
			ModelName: "m", PromptTokens: perRow, CompletionTokens: perRow, Quota: perRow,
		}))
	}
	row := acc.Rows()[0]
	assert.Equal(t, "6000000000", row[2], "输入 token 合计必须用 int64")
	assert.Equal(t, "6000000000", row[3])
	assert.Equal(t, "12000000000", row[4], "总 token")
	assert.Equal(t, "6000000000", row[5], "额度合计")
}

// 组合数超限要带着可执行的提示失败，而不是把进程 OOM 掉。
func TestLogSummaryAccumulator_TooManyGroupsFailsInsteadOfOOM(t *testing.T) {
	acc := summaryAcc(t, []string{LogSummaryDimModel}, 3)
	for i := range 3 {
		require.NoError(t, acc.Add(&Log{ModelName: string(rune('a' + i))}))
	}
	// 已有的分组继续累加不受上限影响。
	require.NoError(t, acc.Add(&Log{ModelName: "a"}))
	// 新分组越过上限即失败。
	assert.ErrorIs(t, acc.Add(&Log{ModelName: "z"}), ErrLogSummaryTooManyGroups)
	assert.Equal(t, "too_many_groups", logExportErrorCode(ErrLogSummaryTooManyGroups))
}

func TestNewLogSummaryAccumulator_Validation(t *testing.T) {
	_, err := newLogSummaryAccumulator(nil, time.UTC, 10)
	assert.ErrorIs(t, err, ErrLogSummaryNoDimensions)
	_, err = newLogSummaryAccumulator([]string{"hour"}, time.UTC, 10)
	assert.ErrorIs(t, err, ErrLogSummaryUnknownDimension, "小时粒度刻意不支持")
}

func TestNormalizeLogSummaryDimensions(t *testing.T) {
	assert.Nil(t, NormalizeLogSummaryDimensions(nil, true))
	assert.Nil(t, NormalizeLogSummaryDimensions([]string{"bogus", " "}, true))
	assert.Equal(t,
		[]string{LogSummaryDimDate, LogSummaryDimModel},
		NormalizeLogSummaryDimensions([]string{"model_name", "date", "model_name"}, true),
		"去重并按固定顺序整理")
	assert.Equal(t,
		[]string{LogSummaryDimDate},
		NormalizeLogSummaryDimensions([]string{"date", "channel"}, false),
		"渠道维度对非管理员不可用")
}

// 聚合导出的 SQL 不取 other——这是它比明细导出快一个数量级的全部原因。
func TestSummaryScanFields_NeverSelectsOther(t *testing.T) {
	for _, dims := range [][]string{
		{LogSummaryDimDate},
		LogSummaryDimensions(),
	} {
		fields := summaryScanFields(dims)
		assert.NotContains(t, fields, "other")
		assert.Contains(t, fields, "created_at")
		assert.Contains(t, fields, "quota")
	}
	assert.Contains(t, summaryScanFields([]string{LogSummaryDimChannel}), "channel_id")
	assert.NotContains(t, summaryScanFields([]string{LogSummaryDimDate}), "channel_id",
		"没用到的维度列不该被取出来")
}

// 端到端：聚合任务产出单文件，行数等于分组数。
func TestWriteLogExport_SummaryEndToEnd(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("sum")
	base := time.Now().Unix() - 7000
	for i := range 6 {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
			l.ModelName = []string{"gpt-4o", "claude"}[i%2]
			l.PromptTokens = 10
			l.CompletionTokens = 5
			l.Quota = 100
		})
	}

	job := newExportJobFor(username, base-10, base+100, nil)
	job.Mode = LogExportModeSummary
	job.SummaryDims = []string{LogSummaryDimModel}
	require.NoError(t, runExportJob(t, job))

	require.Len(t, job.Parts, 1, "聚合结果产出单文件")
	assert.EqualValues(t, 2, job.RowCount, "两个模型 = 两行")
	assert.EqualValues(t, 6, job.ScannedRows)

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 3, "表头 + 两行")
	assert.Equal(t, []string{"Model", "Calls", "Input Tokens", "Output Tokens",
		"Total Tokens", "Quota", "Cost (USD)"}, rows[0])
	assert.Equal(t, "claude", rows[1][0])
	assert.Equal(t, "3", rows[1][1])
	assert.Equal(t, "30", rows[1][2])
	assert.Equal(t, "300", rows[1][5])
}

// 聚合与明细共用同一套筛选：异常筛选也能用来做「按模型统计估算计费的单子」。
func TestWriteLogExport_SummaryHonoursRowFilter(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("sumfilter")
	base := time.Now().Unix() - 7200
	mkExportLog(t, func(l *Log) {
		l.Username = username
		l.CreatedAt = base
		l.ModelName = "m"
		l.Quota = 100
		l.Other = `{"stream_result":{"usage_source":"estimated"}}`
	})
	mkExportLog(t, func(l *Log) {
		l.Username = username
		l.CreatedAt = base + 1
		l.ModelName = "m"
		l.Quota = 900
		l.Other = `{"stream_result":{"usage_source":"upstream"}}`
	})

	job := newExportJobFor(username, base-10, base+100, nil)
	job.Mode = LogExportModeSummary
	job.SummaryDims = []string{LogSummaryDimModel}
	job.Filters.UsageSource = []string{"estimated"}
	require.NoError(t, runExportJob(t, job))

	assert.EqualValues(t, 1, job.RowCount)
	assert.EqualValues(t, 2, job.ScannedRows, "扫描两行，只聚合命中的那一行")
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 2)
	assert.Equal(t, "1", rows[1][1], "只统计到 1 次调用")
	assert.Equal(t, "100", rows[1][5], "额度只累加命中行")
}

func TestGetSummaryMaxGroups_Defaults(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.SummaryMaxGroups = 0
	})
	assert.Equal(t, operation_setting.DefaultLogExportSummaryMaxGroups, GetLogSummaryMaxGroups())

	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.SummaryMaxGroups = 5
	})
	assert.Equal(t, 1000, GetLogSummaryMaxGroups(), "低于下限时被夹到下限")
}

// 「明细 + 汇总」：一次扫描同时产出两种分片。
//
// 最关键的断言是 ScannedRows 只等于一遍数据量——分两次导出会把同一段日志扫两遍，
// 而扫描正是整条链路上最贵的一步，这个模式存在的全部理由就是省掉那一遍。
func TestWriteLogExport_BothModeScansOnceAndEmitsTwoKinds(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("both")
	base := time.Now().Unix() - 7400
	const total = 6
	for i := range total {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
			l.ModelName = []string{"gpt-4o", "claude"}[i%2]
			l.PromptTokens = 10
			l.CompletionTokens = 5
			l.Quota = 100
		})
	}

	job := newExportJobFor(username, base-10, base+100, []string{"created_at", "model_name", "quota"})
	job.Mode = LogExportModeBoth
	job.SummaryDims = []string{LogSummaryDimModel}
	require.NoError(t, runExportJob(t, job))

	assert.EqualValues(t, total, job.RowCount, "RowCount 仍是明细行数")
	assert.EqualValues(t, total, job.ScannedRows, "只扫一遍，不是两遍")

	var detail, summary []LogExportPart
	for _, p := range job.Parts {
		if p.Kind == LogExportPartKindSummary {
			summary = append(summary, p)
		} else {
			detail = append(detail, p)
		}
	}
	require.Len(t, summary, 1, "应有且仅有一个汇总分片")
	require.NotEmpty(t, detail, "应有明细分片")
	assert.EqualValues(t, 2, summary[0].Rows, "两个模型 = 两行汇总")
	assert.Contains(t, summary[0].Path, "-summary-", "汇总分片文件名必须可区分")

	// 明细分片：表头 + 6 行。
	dRows, _ := readCSVGz(t, detail[0].Path)
	require.Len(t, dRows, total+1)
	assert.Equal(t, []string{"Time", "Model", "Quota"}, dRows[0])

	// 汇总分片：表头 + 2 行，且列集与明细不同。
	sRows, _ := readCSVGz(t, summary[0].Path)
	require.Len(t, sRows, 3)
	assert.Equal(t, []string{"Model", "Calls", "Input Tokens", "Output Tokens",
		"Total Tokens", "Quota", "Cost (USD)"}, sRows[0])
	assert.Equal(t, "claude", sRows[1][0])
	assert.Equal(t, "3", sRows[1][1])
	assert.Equal(t, "300", sRows[1][5])
}

// 纯汇总模式产出的分片同样要标成 summary，否则下载后分不清。
func TestWriteLogExport_SummaryOnlyPartIsTagged(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("sumtag")
	base := time.Now().Unix() - 7600
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = base; l.ModelName = "m"; l.Quota = 5 })

	job := newExportJobFor(username, base-10, base+100, nil)
	job.Mode = LogExportModeSummary
	job.SummaryDims = []string{LogSummaryDimModel}
	require.NoError(t, runExportJob(t, job))

	require.Len(t, job.Parts, 1)
	assert.Equal(t, LogExportPartKindSummary, job.Parts[0].Kind)
	assert.EqualValues(t, 1, job.RowCount)
}
