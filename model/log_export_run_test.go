package model

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastExportSettings 把节流参数压到最小，让用例快速跑完；
// 同时保留分片/窗口逻辑本身。
func fastExportSettings(t *testing.T, mut func(s *operation_setting.LogExportSetting)) {
	t.Helper()
	s := operation_setting.GetLogExportSetting()
	prev := *s
	s.BatchSleepMs = 0
	s.MaxRowsPerSec = 10_000_000
	s.CPUSoftLimit = 99
	s.CPUHardLimit = 100
	s.OffpeakOnly = false
	s.BatchSize = 5
	s.WindowSec = 3600
	s.RowsPerFile = 1000000
	s.MaxParts = 100
	s.GzipLevel = 1
	if mut != nil {
		mut(s)
	}
	t.Cleanup(func() { *operation_setting.GetLogExportSetting() = prev })
}

func runExportJob(t *testing.T, job *LogExportJob) error {
	t.Helper()
	t.Cleanup(func() { RemoveLogExportJobFiles(job) })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return writeLogExport(ctx, job)
}

func newExportJobFor(username string, start, end int64, columns []string) *LogExportJob {
	return &LogExportJob{
		JobID:   NewLogExportJobID(),
		UserID:  1,
		Status:  LogExportStatusRunning,
		Format:  LogExportFormatCSVGz,
		Columns: columns,
		Lang:    "en",
		Options: LogExportOptions{CSVBOM: false, Header: true, Timezone: "UTC"},
		Filters: LogExportFilter{
			Username:       username,
			StartTimestamp: start,
			EndTimestamp:   end,
		},
	}
}

func TestWriteLogExport_EndToEnd(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("e2e")
	base := time.Now().Unix() - 5000
	const total = 12
	for i := 0; i < total; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
			l.Quota = 100 + i
			l.ModelName = "gpt-4o"
		})
	}

	job := newExportJobFor(username, base-10, base+int64(total)+10,
		[]string{"created_at", "username", "model_name", "quota"})
	require.NoError(t, runExportJob(t, job))

	assert.Equal(t, int64(total), job.RowCount)
	require.Len(t, job.Parts, 1)
	assert.Equal(t, int64(total), job.Parts[0].Rows)
	assert.Greater(t, job.Parts[0].Bytes, int64(0))
	assert.Greater(t, job.BytesOut, int64(0))

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, total+1, "header + data rows")
	assert.Equal(t, []string{"Time", "User", "Model", "Quota"}, rows[0])
	// 正序导出：首行数据是时间最早的那条，末行是最晚的那条。
	assert.Equal(t, username, rows[1][1])
	assert.Equal(t, "gpt-4o", rows[1][2])
	assert.Equal(t, "100", rows[1][3])
	assert.Equal(t, "111", rows[total][3])

	// 分片覆盖的时间范围应与数据一致，便于用户判断下载哪一片。
	assert.Equal(t, base+int64(total)-1, job.Parts[0].EndTime)
	assert.Equal(t, base, job.Parts[0].StartTime)
}

func TestWriteLogExport_CompleteStoredFields(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	owner := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	username := uniq("rawexport")
	base := time.Now().Unix() - 5000
	samples := []string{
		` {"stream_diagnostic_available":false,"unknown":9007199254740993,"stream_diagnostic":{"read_error":"http2: timeout awaiting response headers","body":"` + strings.Repeat("x", 40000) + `","omitted_bytes":564}} `,
		`{"claude_diagnostic":{"read_error":"unexpected EOF","body_base64":"YWJj"},"claude_stream":{"status":"error"}}`,
		`{"stream_diagnostic":invalid JSON`,
		"",
	}
	var originals []*Log
	for index, sample := range samples {
		row := mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(index)
			l.Other = sample
			l.Quota = 0
			l.IsStream = false
			l.Content = "quoted, \"line\"\nnext line"
		})
		originals = append(originals, row)
		require.NoError(t, LOG_DB.Table("logs").Where("id = ?", row.Id).Update("channel_name", "stored channel").Error)
	}
	require.NoError(t, LOG_DB.Table("logs").Where("id = ?", originals[3].Id).
		Updates(map[string]any{"other": nil, "content": nil, "quota": nil}).Error)

	job := newExportJobFor(username, base-1, base+10, []string{"request_id", "log_raw", "other_raw"})
	job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
	require.NoError(t, runExportJob(t, job))
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, len(samples)+1)
	require.Equal(t, "Raw Log JSON", rows[0][1])
	for index, original := range originals {
		expected := map[string]any{
			"id": original.Id, "user_id": original.UserId, "created_at": original.CreatedAt,
			"type": original.Type, "content": original.Content, "username": original.Username,
			"token_name": original.TokenName, "model_name": original.ModelName, "quota": 0,
			"prompt_tokens": original.PromptTokens, "completion_tokens": original.CompletionTokens,
			"use_time": original.UseTime, "is_stream": false, "channel_id": original.ChannelId,
			"channel_name": "stored channel", "token_id": original.TokenId, "group": original.Group,
			"ip": original.Ip, "request_id": original.RequestId, "upstream_request_id": original.UpstreamRequestId,
			"other": samples[index],
		}
		if index == 3 {
			expected["other"], expected["content"], expected["quota"] = nil, nil, nil
		}
		data, err := common.Marshal(expected)
		require.NoError(t, err)
		require.JSONEq(t, string(data), rows[index+1][1])
		require.NotContains(t, rows[index+1][2], "read_error", "ordinary columns must remain filtered")
	}
	var standard Log
	require.NoError(t, LOG_DB.Where("id = ?", originals[0].Id).First(&standard).Error)
	require.NotContains(t, standard.Other, "read_error", "full export must not bypass the normal read hook globally")
}

func TestWriteLogExport_CompleteFieldsRejectsInvalidAccessAndOptions(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	owner := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	ratio := "1"
	for _, tc := range []struct {
		name   string
		mutate func(*LogExportJob)
		want   error
	}{
		{"legacy role", func(j *LogExportJob) { j.CreatorRole = 0 }, ErrLogExportAccessChanged},
		{"admin", func(j *LogExportJob) { j.CreatorRole = common.RoleAdminUser }, ErrLogExportAccessChanged},
		{"self", func(j *LogExportJob) { j.SelfExport = true }, ErrLogExportAccessChanged},
		{"audit", func(j *LogExportJob) { j.DataSource = LogExportDataAudit }, ErrLogExportAccessChanged},
		{"summary", func(j *LogExportJob) { j.Mode = LogExportModeSummary }, ErrLogExportRawOptions},
		{"repricing", func(j *LogExportJob) { j.GroupRatioOverride = &ratio }, ErrLogExportRawOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := newExportJobFor(owner.Username, 1, 2, []string{LogExportRawColumn})
			job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
			tc.mutate(job)
			require.ErrorIs(t, runExportJob(t, job), tc.want)
			require.Empty(t, job.Parts)
		})
	}
	for _, change := range []map[string]any{{"role": common.RoleAdminUser}, {"role": common.RoleRootUser, "status": common.UserStatusDisabled}} {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", owner.Id).Updates(change).Error)
		job := newExportJobFor(owner.Username, 1, 2, []string{LogExportRawColumn})
		job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
		require.ErrorIs(t, runExportJob(t, job), ErrLogExportAccessChanged)
	}
}

func TestWriteLogExport_CompleteFieldsUsesPublicFiltersAndSummary(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	owner := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	base := time.Now().Unix() - 5000
	for _, status := range []string{"error", "success"} {
		mkExportLog(t, func(l *Log) {
			l.Username, l.CreatedAt = owner.Username, base
			l.Other = `{"stream_status":{"status":"` + status + `"},"claude_stream":{"status":"` + status + `"},"claude_diagnostic":{"read_error":"private"}}`
		})
	}
	job := newExportJobFor(owner.Username, base-1, base+1, []string{LogExportRawColumn, "stream_status_text"})
	job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
	job.Mode, job.SummaryDims = LogExportModeBoth, []string{"date"}
	job.Filters.AnomalyKinds = []string{"stream_error"}
	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, 1, job.RowCount)
	require.Len(t, job.Parts, 2)
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 2)
	require.Contains(t, rows[1][0], "claude_diagnostic")
	require.Contains(t, rows[1][0], "claude_stream")
	require.Equal(t, "error", rows[1][1])
	require.Equal(t, "summary", job.Parts[1].Kind)
}

func TestLogExportScanner_CompleteFieldsStopsAfterRevocation(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) { s.BatchSize = 1 })
	owner := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	base := time.Now().Unix() - 5000
	for range 2 {
		mkExportLog(t, func(l *Log) { l.Username, l.CreatedAt = owner.Username, base })
	}
	job := newExportJobFor(owner.Username, base-1, base+1, []string{LogExportRawColumn})
	job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
	scanner := newLogExportScanner(job, logExportRawFields(), false, newTestRowCtx())
	batches := 0
	err := scanner.runUsageBatches(context.Background(), func(batch LogExportBatch) error {
		batches++
		require.Len(t, batch.Logs, 1)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", owner.Id).Update("role", common.RoleAdminUser).Error)
		scanner.lastValidated = time.Time{}
		return nil
	})
	require.ErrorIs(t, err, ErrLogExportAccessChanged)
	require.Equal(t, 1, batches)
}

func TestWriteLogExport_CompleteFieldsAcrossBatchesAndParts(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSize = 3000
		s.RowsPerFile = 50
	})
	owner := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	base := time.Now().Unix() - 5000
	other := `{"stream_diagnostic":{"body":"` + strings.Repeat("x", 40000) + `"}}`
	wantIDs := make([]string, 0, 105)
	for range 105 {
		row := mkExportLog(t, func(l *Log) {
			l.Username, l.CreatedAt, l.Other = owner.Username, base, other
		})
		wantIDs = append(wantIDs, strconv.Itoa(row.Id))
	}
	job := newExportJobFor(owner.Username, base, base, []string{"id", LogExportRawColumn})
	job.UserID, job.CreatorRole = owner.Id, common.RoleRootUser
	scanner := newLogExportScanner(job, logExportRawFields(), false, newTestRowCtx())
	var batches []int
	require.NoError(t, scanner.runUsageBatches(context.Background(), func(batch LogExportBatch) error {
		batches = append(batches, len(batch.rawLogs))
		return nil
	}))
	require.Equal(t, []int{100, 5}, batches, "the raw batch cap must not mark the first batch as complete")
	job.ScannedRows = 0
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	require.NoError(t, runExportJob(t, job))
	runtime.ReadMemStats(&after)
	t.Logf("105 rows with 40,000-byte bodies: elapsed=%s, total_alloc=%d bytes", time.Since(started), after.TotalAlloc-before.TotalAlloc)
	require.EqualValues(t, 105, job.RowCount)
	require.Len(t, job.Parts, 3)
	var gotIDs []string
	for _, part := range job.Parts {
		rows, _ := readCSVGz(t, part.Path)
		for _, row := range rows[1:] {
			gotIDs = append(gotIDs, row[0])
			var raw struct {
				Other string `json:"other"`
			}
			require.NoError(t, common.UnmarshalJsonStr(row[1], &raw))
			require.Equal(t, other, raw.Other)
		}
	}
	require.Equal(t, wantIDs, gotIDs, "identical timestamps must preserve every row exactly once across batches and files")
}

func TestWriteLogExport_RotatesParts(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.RowsPerFile = 10
		s.BatchSize = 4
	})

	username := uniq("rot")
	base := time.Now().Unix() - 6000
	const total = 25
	for i := 0; i < total; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	job := newExportJobFor(username, base-10, base+int64(total)+10, []string{"created_at", "username"})
	require.NoError(t, runExportJob(t, job))

	require.Len(t, job.Parts, 3, "25 rows at 10 rows/part must produce 3 parts")
	assert.Equal(t, int64(10), job.Parts[0].Rows)
	assert.Equal(t, int64(10), job.Parts[1].Rows)
	assert.Equal(t, int64(5), job.Parts[2].Rows)
	assert.Equal(t, int64(total), job.RowCount)

	// 分片内容合起来必须正好是全部数据，且不重复。
	seen := map[string]bool{}
	dataRows := 0
	for _, part := range job.Parts {
		rows, _ := readCSVGz(t, part.Path)
		require.Greater(t, len(rows), 1)
		for _, row := range rows[1:] {
			assert.False(t, seen[row[0]], "duplicate row across parts: %v", row)
			seen[row[0]] = true
			dataRows++
		}
	}
	assert.Equal(t, total, dataRows)
}

func TestWriteLogExport_MultipleWindows(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	// 窗口设得比数据跨度小，强制走多轮窗口。
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.WindowSec = 30
		s.BatchSize = 3
	})

	username := uniq("win")
	base := time.Now().Unix() - 8000
	const total = 10
	for i := 0; i < total; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i*20) // 跨越 ~7 个 30s 窗口
		})
	}

	job := newExportJobFor(username, base-5, base+int64(total*20)+5, []string{"created_at", "username"})
	require.NoError(t, runExportJob(t, job))
	assert.Equal(t, int64(total), job.RowCount, "window slicing must not drop rows")
}

// 窗口是闭区间，相邻窗口若都包含边界那一秒，该秒的行会被导出两次
// （每个窗口的 keyset 游标是独立重置的，重复不会被自然过滤掉）。
// 这里把数据精确摆在窗口边界上验证不重不漏。
func TestWriteLogExport_NoDuplicatesAtWindowBoundary(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	// 必须用 >= MinLogExportWindowSec 的窗口，否则 getter 会把它抬到下限，
	// 下面算出来的边界就不是真正的窗口边界，用例也就白测了。
	const windowSec = operation_setting.MinLogExportWindowSec
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.WindowSec = windowSec
		s.BatchSize = 50
	})

	username := uniq("bound")
	base := time.Now().Unix() - 9600
	exportEnd := base + 300
	// 第一个窗口是 [exportEnd-windowSec+1, exportEnd]，边界落在 exportEnd-windowSec+1。
	boundary := exportEnd - windowSec + 1
	stamps := []int64{
		exportEnd,     // 窗口 1 首行
		boundary,      // 窗口 1 末行（旧实现里会被窗口 2 再扫一次）
		boundary - 1,  // 窗口 2 首行
		boundary - 5,  // 窗口 2 中间
		boundary - 61, // 窗口 3
	}
	for _, ts := range stamps {
		ts := ts
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = ts
		})
	}

	job := newExportJobFor(username, base, exportEnd, []string{"created_at", "id"})
	require.NoError(t, runExportJob(t, job))

	assert.Equal(t, int64(len(stamps)), job.RowCount, "boundary rows must be exported exactly once")

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, len(stamps)+1)
	seen := map[string]bool{}
	for _, row := range rows[1:] {
		id := row[1]
		assert.False(t, seen[id], "row %s exported more than once", id)
		seen[id] = true
	}
}

func TestWriteLogExport_EmptyResultProducesHeaderOnlyPart(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	job := newExportJobFor(uniq("empty"), time.Now().Unix()-100, time.Now().Unix(),
		[]string{"created_at", "username"})
	require.NoError(t, runExportJob(t, job))

	assert.Zero(t, job.RowCount)
	require.Len(t, job.Parts, 1)
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 1, "only the header row")
}

// 取消标志必须在下一批被检测到，任务立即停止。
func TestWriteLogExport_StopsWhenCanceled(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("cancel")
	base := time.Now().Unix() - 9000
	for i := 0; i < 5; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	job := newExportJobFor(username, base-10, base+100, []string{"created_at"})
	CancelLogExportJob(job.JobID)
	t.Cleanup(func() { _ = removeCancelFlagForTest(job.JobID) })

	err := runExportJob(t, job)
	assert.ErrorIs(t, err, context.Canceled)
}

// 旧 XLSX 请求直接生成 CSV.GZ，不受历史工作表行数上限约束。
func TestWriteLogExport_LegacyXlsxUsesCSVWithoutRowLimit(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.XlsxMaxRows = 3
		s.BatchSize = 10
	})

	username := uniq("xlsx")
	base := time.Now().Unix() - 9500
	for i := 0; i < 6; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	job := newExportJobFor(username, base-10, base+100, []string{"created_at", "username"})
	job.Format = LogExportFormatXlsx
	err := runExportJob(t, job)
	assert.NoError(t, err)
	assert.Equal(t, LogExportFormatCSVGz, job.Format)
}

func TestWriteLogExport_TooManyPartsFails(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.RowsPerFile = 2
		s.MaxParts = 2
		s.BatchSize = 10
	})

	username := uniq("parts")
	base := time.Now().Unix() - 9800
	for i := 0; i < 10; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	job := newExportJobFor(username, base-10, base+100, []string{"created_at"})
	err := runExportJob(t, job)
	assert.ErrorIs(t, err, ErrLogExportTooManyParts)
}

// 时区选项影响时间列的渲染。
func TestWriteLogExport_TimezoneOption(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("tz")
	ts := time.Date(2026, 7, 27, 0, 30, 0, 0, time.UTC).Unix()
	mkExportLog(t, func(l *Log) {
		l.Username = username
		l.CreatedAt = ts
	})

	job := newExportJobFor(username, ts-10, ts+10, []string{"created_at"})
	job.Options.Timezone = "Asia/Shanghai"
	require.NoError(t, runExportJob(t, job))

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 2)
	assert.Equal(t, "2026-07-27 08:30:00", rows[1][0], "timestamps must render in the requested timezone")
}

// 表头按任务语言渲染。
func TestWriteLogExport_HeaderUsesJobLanguage(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("lang")
	ts := time.Now().Unix() - 4000
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = ts })

	job := newExportJobFor(username, ts-10, ts+10, []string{"created_at", "quota"})
	job.Lang = "zh-CN"
	require.NoError(t, runExportJob(t, job))

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.GreaterOrEqual(t, len(rows), 1)
	assert.Equal(t, []string{"时间", "配额"}, rows[0])
}

func TestWriteLogExport_HeaderCanBeDisabled(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)

	username := uniq("nohdr")
	ts := time.Now().Unix() - 4200
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = ts })

	job := newExportJobFor(username, ts-10, ts+10, []string{"created_at"})
	job.Options.Header = false
	require.NoError(t, runExportJob(t, job))

	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 1, "no header row when disabled")
}

// 失败时未收尾的分片没进 job.Parts，RemoveLogExportJobFiles 清不到它，
// 必须由 writeLogExport 自己删掉，否则半成品会占着磁盘直到任务过期。
func TestWriteLogExport_RemovesUnfinishedPartOnFailure(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSize = 10
	})

	username := uniq("leak")
	base := time.Now().Unix() - 9900
	for i := 0; i < 5; i++ {
		i := i
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	job := newExportJobFor(username, base-10, base+100, []string{"created_at"})
	partPath := LogExportPartFilePath(job.JobID, 1, LogExportFormatCSVGz)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Cleanup(func() { RemoveLogExportJobFiles(job) })
	err := writeLogExport(ctx, job)
	require.ErrorIs(t, err, context.Canceled)

	_, statErr := os.Stat(partPath)
	assert.True(t, os.IsNotExist(statErr), "half-written part must be deleted when the export fails")
}

// 扫描器内部仍按工作时间计算预算，后台任务另有包含等待时间的总时限。
// 此用例只验证扫描器预算；总时限由 TestExportTotalTimeoutIncludesResourceWait 覆盖。
func TestWriteLogExport_ThrottledTimeDoesNotConsumeTimeout(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.TimeoutSec = 1
		s.BatchSleepMs = 1200 // 单批休眠就已超过整个 timeout
		s.BatchSize = 10
	})

	username := uniq("budget")
	ts := time.Now().Unix() - 9700
	// 必须喂满一整批：休眠按**实际读到的行数**计费，只放一行的话这一批只付
	// 十分之一的 batch_sleep_ms，攒不出超过 timeout_sec 的让出时间，
	// 用例就证明不了它本来要证明的事。
	const rows = 10
	for i := range rows {
		mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = ts - int64(i) })
	}

	job := newExportJobFor(username, ts-rows, ts+5, []string{"created_at", "username"})
	require.NoError(t, runExportJob(t, job),
		"time yielded to the resource gate must not count against the job timeout")
	assert.EqualValues(t, rows, job.RowCount)
	assert.Greater(t, job.ThrottledMs, int64(1000))
}

func TestLogExportErrorCode_MapsStableCodes(t *testing.T) {
	assert.Equal(t, "disk_full", logExportErrorCode(ErrLogExportDiskFull))
	assert.Equal(t, "too_many_parts", logExportErrorCode(ErrLogExportTooManyParts))
	assert.Equal(t, "timeout", logExportErrorCode(context.DeadlineExceeded))
	assert.Equal(t, "internal", logExportErrorCode(assertAnError{}))
}

type assertAnError struct{}

func (assertAnError) Error() string { return "boom" }

// ── 扫描窗口自适应 ────────────────────────────────────────────────

func TestNextLogExportWindowSec(t *testing.T) {
	const cfg = 3600
	cases := []struct {
		name                     string
		current                  int64
		rows, batches, batchSize int
		want                     int64
	}{
		{"读不满一批就翻倍", 3600, 10, 1, 3000, 7200},
		{"空窗口也翻倍", 3600, 0, 1, 3000, 7200},
		{"恰好读满一批不再翻倍", 3600, 3000, 1, 3000, 3600},
		{"翻倍封顶在配置层硬上限", 86400, 0, 1, 3000, operation_setting.MaxLogExportWindowSec},
		{"接近上限时不越界", 50000, 0, 1, 3000, operation_setting.MaxLogExportWindowSec},
		{"批次用满就减半", 14400, 99999, logExportWindowShrinkBatches, 3000, 7200},
		{"减半不低于配置值", 3600, 99999, logExportWindowShrinkBatches, 3000, cfg},
		{"密度适中则保持不变", 7200, 9000, 3, 3000, 7200},
		{"批大小为 0 时不调整", 7200, 0, 1, 0, 7200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextLogExportWindowSec(tc.current, cfg, tc.rows, tc.batches, tc.batchSize, 50*time.Millisecond, 10*time.Second)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNextLogExportWindowSecUsesQueryCost(t *testing.T) {
	for _, tc := range []struct {
		name          string
		current       int64
		cost, timeout time.Duration
		want          int64
	}{
		{"fast sparse window grows", 3600, time.Second, 10 * time.Second, 7200},
		{"few matches do not imply cheap reads", 3600, 2 * time.Second, 10 * time.Second, 3600},
		{"slow window shrinks below configuration", 3600, 5 * time.Second, 10 * time.Second, 1800},
		{"timeout-relative growth threshold", 3600, 300 * time.Millisecond, time.Second, 3600},
		{"short timeout slow threshold", 3600, 500 * time.Millisecond, time.Second, 1800},
		{"hard minimum is preserved", 60, 9 * time.Second, 10 * time.Second, 60},
		{"long timeout still bounds growth cost", 3600, 2 * time.Second, 30 * time.Second, 3600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, nextLogExportWindowSec(tc.current, 3600, 0, 1, 3000, tc.cost, tc.timeout))
		})
	}
}

// 窗口宽度自适应扩张之后，边界拼接必须依然不重不漏。
// 这是整套窗口逻辑里最容易出错、后果最隐蔽的一条：重复行会真的写进文件。
func TestWriteLogExport_AdaptiveWindowKeepsRowsExactlyOnce(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	// 窗口起步取下限，批大小取 2：数据稀疏，窗口会一路翻倍，
	// 途中多次跨越「扩张后的新边界」。
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.WindowSec = operation_setting.MinLogExportWindowSec
		s.BatchSize = 2
	})

	username := uniq("adaptwin")
	exportEnd := time.Now().Unix() - 100
	exportStart := exportEnd - 4000

	// 把行摆在多个窗口边界附近：每翻一次倍，边界位置都会变，
	// 固定间隔的数据足以覆盖扩张过程中出现的各个边界。
	want := map[int64]bool{}
	for offset := int64(0); offset <= 3900; offset += 57 {
		ts := exportEnd - offset
		want[ts] = true
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = ts
		})
	}

	job := newExportJobFor(username, exportStart, exportEnd, []string{"created_at", "id"})
	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, len(want), job.RowCount, "自适应扩张不得丢行或重复行")

	seen := map[string]bool{}
	for _, part := range job.Parts {
		rows, _ := readCSVGz(t, part.Path)
		for _, row := range rows[1:] {
			id := row[1]
			assert.False(t, seen[id], "日志 %s 被导出了不止一次", id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, len(want))
}

// 宽时间范围 + 稀疏数据是导出最常见也最容易被拖慢的用法。
//
// 闸门曾经在查询**之前**按「请求的批大小」收费，于是每个窗口的最后一批和每个空
// 窗口都要按满批付钱：7 天 = 168 个 1 小时窗口 × 150ms = 25,200ms 的固定税，
// 而真实工作只有几毫秒。这里用生产默认参数锁住这条回归。
//
// 断言的是闸门累计计费（由行数算出的确定值），不是墙上时间，因此不受机器负载影响。
func TestWriteLogExport_SparseWideRangeDoesNotPayForUnreadRows(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.MaxRowsPerSec = 20000 // 生产默认
		s.WindowSec = 3600      // 生产默认
		s.BatchSize = 3000      // 生产默认
		s.CPUSoftLimit = 99
		s.CPUHardLimit = 100
		// batch_sleep_ms 取 0 有两个目的：① 让 ThrottledMs 只反映令牌桶按行计费，
		// 断言才是在测这条修复；② penalty 的上限是 8×base，置 0 等于关掉它——
		// penalty 由实测查询耗时反馈得出，在跑全量套件的共享数据库上完全不可控，
		// 留着它这条断言必然飘。
		// 关掉之后对照关系依然成立：旧实现在 batch_sleep_ms=0 下，令牌桶仍按
		// 3000 行收费，168 个窗口照样是 168 × 150ms = 25,200ms。
		s.BatchSleepMs = 0
	})

	username := uniq("sparse")
	exportEnd := time.Now().Unix() - 100
	const days = 7
	exportStart := exportEnd - days*86400

	rows := 0
	for offset := int64(0); offset < days*86400; offset += 6 * 3600 {
		mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = exportEnd - offset })
		rows++
	}

	job := newExportJobFor(username, exportStart, exportEnd, DefaultLogExportColumns())
	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, rows, job.RowCount)

	// 旧实现在这里是 25,200ms。给两个数量级的余量：只要没退回「按请求量收费」，
	// 实际值是几十毫秒。
	assert.Less(t, job.ThrottledMs, int64(500),
		"稀疏宽范围导出不得为没读到的行付费")
}
