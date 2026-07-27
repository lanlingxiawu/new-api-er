package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkExportLog 插入一条日志到 LOG_DB 并注册清理。
func mkExportLog(t *testing.T, mut func(l *Log)) *Log {
	t.Helper()
	requireLogDB(t)
	l := &Log{
		UserId:            nextTestID(),
		CreatedAt:         time.Now().Unix(),
		Type:              LogTypeConsume,
		Username:          uniq("exp"),
		TokenName:         uniq("tok"),
		ModelName:         "gpt-4o",
		Group:             "default",
		Quota:             100,
		PromptTokens:      10,
		CompletionTokens:  5,
		UseTime:           1,
		RequestId:         uniq("rid"),
		UpstreamRequestId: uniq("urid"),
	}
	if mut != nil {
		mut(l)
	}
	require.NoError(t, LOG_DB.Create(l).Error)
	id := l.Id
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Unscoped().Delete(&Log{}, id)
		}
	})
	return l
}

// scanAllForExport 用 keyset 游标翻完给定窗口的全部数据，返回顺序结果。
func scanAllForExport(t *testing.T, filter LogExportFilter, fields []string, batchSize int) []*Log {
	t.Helper()
	var all []*Log
	var cursor *logExportCursor
	for i := 0; i < 1000; i++ {
		batch, err := scanLogExportBatch(context.Background(), filter, fields,
			filter.StartTimestamp, filter.EndTimestamp, cursor, batchSize)
		require.NoError(t, err)
		all = append(all, batch...)
		if len(batch) < batchSize {
			break
		}
		cursor = nextLogExportCursor(batch)
	}
	return all
}

func exportTestFields() []string {
	set, err := ResolveLogExportColumns([]string{"created_at", "id", "username", "quota", "model_name", "type"}, true)
	if err != nil {
		panic(err)
	}
	return set.SelectFields()
}

// keyset 游标必须能跨越「同一秒内多行」的边界，既不漏也不重——这是最容易写错的地方。
func TestScanLogExportBatch_KeysetCrossesSameSecondBoundary(t *testing.T) {
	requireLogDB(t)
	username := uniq("same_sec")
	ts := time.Now().Unix() - 3600

	const total = 25
	for i := 0; i < total; i++ {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = ts // 全部落在同一秒
		})
	}

	filter := LogExportFilter{
		Username:       username,
		StartTimestamp: ts - 10,
		EndTimestamp:   ts + 10,
	}
	// 批大小刻意小于同秒行数，强制游标在同一秒内翻页。
	got := scanAllForExport(t, filter, exportTestFields(), 4)

	require.Len(t, got, total, "keyset paging must not drop or duplicate rows within one second")
	seen := map[int]bool{}
	for _, l := range got {
		assert.False(t, seen[l.Id], "duplicate row id %d", l.Id)
		seen[l.Id] = true
	}
}

func TestScanLogExportBatch_DescendingOrderAndNoOverlap(t *testing.T) {
	requireLogDB(t)
	username := uniq("desc")
	base := time.Now().Unix() - 7200
	for i := 0; i < 10; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}
	filter := LogExportFilter{
		Username:       username,
		StartTimestamp: base - 5,
		EndTimestamp:   base + 100,
	}
	got := scanAllForExport(t, filter, exportTestFields(), 3)
	require.Len(t, got, 10)
	for i := 1; i < len(got); i++ {
		assert.GreaterOrEqual(t, got[i-1].CreatedAt, got[i].CreatedAt, "results must be time-descending")
	}
}

// 按时间窗口切分扫描后，各窗口拼起来必须与一次性扫描完全一致（无缝、无重叠）。
func TestScanLogExportBatch_WindowSlicingIsSeamless(t *testing.T) {
	requireLogDB(t)
	username := uniq("window")
	base := time.Now().Unix() - 10000
	const total = 12
	for i := 0; i < total; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i*10) // 跨越多个 30s 窗口
		})
	}
	filter := LogExportFilter{
		Username:       username,
		StartTimestamp: base - 1,
		EndTimestamp:   base + int64(total*10) + 1,
	}
	fields := exportTestFields()

	// 一次性扫描
	oneShot := scanAllForExport(t, filter, fields, 100)
	require.Len(t, oneShot, total)

	// 按 30 秒窗口倒序切分扫描
	var windowed []*Log
	windowEnd := filter.EndTimestamp
	for windowEnd > filter.StartTimestamp {
		windowStart := windowEnd - 30
		if windowStart < filter.StartTimestamp {
			windowStart = filter.StartTimestamp
		}
		var cursor *logExportCursor
		for {
			batch, err := scanLogExportBatch(context.Background(), filter, fields, windowStart, windowEnd, cursor, 5)
			require.NoError(t, err)
			windowed = append(windowed, batch...)
			if len(batch) < 5 {
				break
			}
			cursor = nextLogExportCursor(batch)
		}
		windowEnd = windowStart
	}

	require.Len(t, windowed, total, "window slicing must not drop rows at window edges")
	oneIDs := make([]int, len(oneShot))
	for i, l := range oneShot {
		oneIDs[i] = l.Id
	}
	winIDs := make([]int, len(windowed))
	for i, l := range windowed {
		winIDs[i] = l.Id
	}
	assert.ElementsMatch(t, oneIDs, winIDs)
}

func TestScanLogExportBatch_Filters(t *testing.T) {
	requireLogDB(t)
	marker := uniq("filt")
	base := time.Now().Unix() - 20000

	target := mkExportLog(t, func(l *Log) {
		l.Username = marker
		l.CreatedAt = base
		l.ModelName = "claude-sonnet-5"
		l.TokenName = marker + "-tok"
		l.Group = marker + "-grp"
		l.ChannelId = 4242
		l.Type = LogTypeConsume
	})
	// 干扰行：同一时间窗内，但每个维度都与 target 不同。
	mkExportLog(t, func(l *Log) {
		l.Username = marker
		l.CreatedAt = base
		l.ModelName = "gpt-4o"
		l.TokenName = marker + "-other"
		l.Group = marker + "-other"
		l.ChannelId = 5252
		l.Type = LogTypeTopup
	})

	fields := exportTestFields()
	window := LogExportFilter{Username: marker, StartTimestamp: base - 5, EndTimestamp: base + 5}

	cases := []struct {
		name  string
		mutFn func(f *LogExportFilter)
	}{
		{"model", func(f *LogExportFilter) { f.ModelName = "claude-sonnet-5" }},
		{"model_wildcard", func(f *LogExportFilter) { f.ModelName = "claude%" }},
		{"token", func(f *LogExportFilter) { f.TokenName = marker + "-tok" }},
		{"group", func(f *LogExportFilter) { f.Group = marker + "-grp" }},
		{"channel", func(f *LogExportFilter) { f.ChannelId = 4242 }},
		{"type", func(f *LogExportFilter) { f.LogType = LogTypeConsume }},
		{"user_id", func(f *LogExportFilter) { f.UserId = target.UserId }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			filter := window
			tc.mutFn(&filter)
			got := scanAllForExport(t, filter, fields, 50)
			require.Len(t, got, 1, "filter %s should match exactly the target row", tc.name)
			assert.Equal(t, target.Id, got[0].Id)
		})
	}
}

func TestScanLogExportBatch_EmptyAndSingleResult(t *testing.T) {
	requireLogDB(t)
	fields := exportTestFields()

	empty := scanAllForExport(t, LogExportFilter{
		Username:       uniq("nobody"),
		StartTimestamp: time.Now().Unix() - 100,
		EndTimestamp:   time.Now().Unix(),
	}, fields, 10)
	assert.Empty(t, empty)

	username := uniq("single")
	ts := time.Now().Unix() - 500
	mkExportLog(t, func(l *Log) {
		l.Username = username
		l.CreatedAt = ts
	})
	one := scanAllForExport(t, LogExportFilter{
		Username: username, StartTimestamp: ts - 5, EndTimestamp: ts + 5,
	}, fields, 10)
	assert.Len(t, one, 1)
}

// 时间范围之外的数据不能被带出来。
func TestScanLogExportBatch_RespectsTimeRange(t *testing.T) {
	requireLogDB(t)
	username := uniq("range")
	base := time.Now().Unix() - 30000
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = base })
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = base + 1000 })

	got := scanAllForExport(t, LogExportFilter{
		Username: username, StartTimestamp: base - 1, EndTimestamp: base + 1,
	}, exportTestFields(), 10)
	require.Len(t, got, 1)
	assert.Equal(t, base, got[0].CreatedAt)
}

// 未勾选 other 依赖列时，SELECT 不应包含 other 列。
func TestLogExportSelectClause_OmitsOtherWhenNotNeeded(t *testing.T) {
	plain, err := ResolveLogExportColumns([]string{"created_at", "quota"}, true)
	require.NoError(t, err)
	clause := logExportSelectClause(plain.SelectFields())
	assert.NotContains(t, clause, "logs.other")
	assert.Contains(t, clause, "logs.created_at")
	assert.Contains(t, clause, "logs.id")

	withOther, err := ResolveLogExportColumns([]string{"created_at", "cache_tokens"}, true)
	require.NoError(t, err)
	assert.Contains(t, logExportSelectClause(withOther.SelectFields()), "logs.other")
}

// ClickHouse 的排序键是 (created_at, request_id)，游标也用它。漏选 request_id 会让
// 同一秒内的剩余行被静默跳过，因此扫描层必须无条件补上这个字段。
func TestEnsureCursorFields_ClickHouseAlwaysSelectsRequestId(t *testing.T) {
	prev := common.LogDatabaseType()
	t.Cleanup(func() { common.SetLogDatabaseType(prev) })

	common.SetLogDatabaseType(common.DatabaseTypeMySQL)
	assert.Equal(t, []string{"id", "created_at"}, ensureCursorFields([]string{"id", "created_at"}),
		"SQL databases page by id, which is always selected — no extra column needed")

	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	got := ensureCursorFields([]string{"id", "created_at"})
	assert.Contains(t, got, "request_id")
	assert.Contains(t, got, "created_at")

	// 已经选了就不重复追加。
	already := []string{"id", "created_at", "request_id"}
	assert.Equal(t, already, ensureCursorFields(already))
}

func TestFillLogExportChannelNames_UsesCacheAndNegativeCache(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	cache := map[int]string{}
	logs := []*Log{
		{ChannelId: ch.Id},
		{ChannelId: ch.Id},
		{ChannelId: 0}, // 无渠道的日志跳过
	}
	fillLogExportChannelNames(context.Background(), logs, cache)
	assert.Equal(t, ch.Name, logs[0].ChannelName)
	assert.Equal(t, ch.Name, logs[1].ChannelName)
	assert.Equal(t, "", logs[2].ChannelName)

	// 已删除的渠道写入负缓存，后续不再重复查询。
	missingID := nextTestID()
	more := []*Log{{ChannelId: missingID}}
	fillLogExportChannelNames(context.Background(), more, cache)
	name, ok := cache[missingID]
	assert.True(t, ok, "unresolved channel must be negatively cached")
	assert.Equal(t, "", name)
}
