package model

import (
	"context"
	"os"
	"testing"
	"time"

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
	// 倒序导出：首行数据是时间最大的那条。
	assert.Equal(t, username, rows[1][1])
	assert.Equal(t, "gpt-4o", rows[1][2])
	assert.Equal(t, "111", rows[1][3])

	// 分片覆盖的时间范围应与数据一致，便于用户判断下载哪一片。
	assert.Equal(t, base+int64(total)-1, job.Parts[0].EndTime)
	assert.Equal(t, base, job.Parts[0].StartTime)
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

// xlsx 超过行数上限时返回降级信号，由 runLogExport 改用 csv.gz 重跑。
func TestWriteLogExport_XlsxRowLimitTriggersDowngrade(t *testing.T) {
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
	assert.ErrorIs(t, err, errXlsxRowLimit)
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
		s.XlsxMaxRows = 2
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
	job.Format = LogExportFormatXlsx
	partPath := LogExportPartFilePath(job.JobID, 1, LogExportFormatXlsx)

	err := runExportJob(t, job)
	require.ErrorIs(t, err, errXlsxRowLimit)

	_, statErr := os.Stat(partPath)
	assert.True(t, os.IsNotExist(statErr), "half-written part must be deleted when the export fails")
}

// 闸门让出的时间不能计入任务超时：否则一开低峰模式，白天创建的任务会先干等
// 几小时再以超时失败。这里让每批休眠远超 timeout_sec，实际工作时间却很短。
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
	mkExportLog(t, func(l *Log) { l.Username = username; l.CreatedAt = ts })

	job := newExportJobFor(username, ts-5, ts+5, []string{"created_at", "username"})
	require.NoError(t, runExportJob(t, job),
		"time yielded to the resource gate must not count against the job timeout")
	assert.Equal(t, int64(1), job.RowCount)
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
