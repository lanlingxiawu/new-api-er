package operation_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 每个 getter 都必须对非法值（0/负数）兜底到默认值，否则一次误配置就会让导出
// 死循环或彻底失速。
func TestLogExportSetting_IntGetterFallbacks(t *testing.T) {
	cases := []struct {
		name    string
		def     int
		get     func(s *LogExportSetting) int
		set     func(s *LogExportSetting, v int)
		zeroOK  bool // 0 是有意义的取值，不该被兜底
		negOnly bool
	}{
		{name: "UserCooldownSec", def: DefaultLogExportUserCooldownSec,
			get: func(s *LogExportSetting) int { return s.GetUserCooldownSec() },
			set: func(s *LogExportSetting, v int) { s.UserCooldownSec = v }},
		{name: "MaxActiveJobsPerUser", def: DefaultLogExportMaxActiveJobsPerUser,
			get: func(s *LogExportSetting) int { return s.GetMaxActiveJobsPerUser() },
			set: func(s *LogExportSetting, v int) { s.MaxActiveJobsPerUser = v }},
		{name: "TimeoutSec", def: DefaultLogExportTimeoutSec,
			get: func(s *LogExportSetting) int { return s.GetTimeoutSec() },
			set: func(s *LogExportSetting, v int) { s.TimeoutSec = v }},
		{name: "MaxTemplatesPerUser", def: DefaultLogExportMaxTemplatesPerUser,
			get: func(s *LogExportSetting) int { return s.GetMaxTemplatesPerUser() },
			set: func(s *LogExportSetting, v int) { s.MaxTemplatesPerUser = v }},
		{name: "BatchSize", def: DefaultLogExportBatchSize,
			get: func(s *LogExportSetting) int { return s.GetBatchSize() },
			set: func(s *LogExportSetting, v int) { s.BatchSize = v }},
		{name: "BatchQueryTimeoutSec", def: DefaultLogExportBatchQueryTimeoutSec,
			get: func(s *LogExportSetting) int { return s.GetBatchQueryTimeoutSec() },
			set: func(s *LogExportSetting, v int) { s.BatchQueryTimeoutSec = v }},
		{name: "MaxRowsPerSec", def: DefaultLogExportMaxRowsPerSec,
			get: func(s *LogExportSetting) int { return s.GetMaxRowsPerSec() },
			set: func(s *LogExportSetting, v int) { s.MaxRowsPerSec = v }},
		{name: "CPUCheckIntervalMs", def: DefaultLogExportCPUCheckIntervalMs,
			get: func(s *LogExportSetting) int { return s.GetCPUCheckIntervalMs() },
			set: func(s *LogExportSetting, v int) { s.CPUCheckIntervalMs = v }},
		{name: "GzipLevel", def: DefaultLogExportGzipLevel,
			get: func(s *LogExportSetting) int { return s.GetGzipLevel() },
			set: func(s *LogExportSetting, v int) { s.GzipLevel = v }},
		{name: "RowsPerFile", def: DefaultLogExportRowsPerFile,
			get: func(s *LogExportSetting) int { return s.GetRowsPerFile() },
			set: func(s *LogExportSetting, v int) { s.RowsPerFile = v }},
		{name: "MaxParts", def: DefaultLogExportMaxParts,
			get: func(s *LogExportSetting) int { return s.GetMaxParts() },
			set: func(s *LogExportSetting, v int) { s.MaxParts = v }},
		{name: "XlsxMaxRows", def: DefaultLogExportXlsxMaxRows,
			get: func(s *LogExportSetting) int { return s.GetXlsxMaxRows() },
			set: func(s *LogExportSetting, v int) { s.XlsxMaxRows = v }},
		{name: "MinFreeDiskMB", def: DefaultLogExportMinFreeDiskMB,
			get: func(s *LogExportSetting) int { return s.GetMinFreeDiskMB() },
			set: func(s *LogExportSetting, v int) { s.MinFreeDiskMB = v }},
		{name: "MaxConcurrentDownloadsPerUser", def: DefaultLogExportMaxConcurrentDownloadsPerUser,
			get: func(s *LogExportSetting) int { return s.GetMaxConcurrentDownloadsPerUser() },
			set: func(s *LogExportSetting, v int) { s.MaxConcurrentDownloadsPerUser = v }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := &LogExportSetting{}
			tc.set(s, 0)
			assert.Equal(t, tc.def, tc.get(s), "zero must fall back to the default")
			tc.set(s, -5)
			assert.Equal(t, tc.def, tc.get(s), "negative must fall back to the default")
			tc.set(s, 7)
			assert.Equal(t, 7, tc.get(s), "valid value must be honoured")
		})
	}
}

// max_concurrent_jobs = 0 是「停止接受新任务」的运营手段，不能被兜底成默认值。
func TestLogExportSetting_MaxConcurrentJobsZeroIsMeaningful(t *testing.T) {
	s := &LogExportSetting{MaxConcurrentJobs: 0}
	assert.Equal(t, 0, s.GetMaxConcurrentJobs())

	s.MaxConcurrentJobs = -1
	assert.Equal(t, DefaultLogExportMaxConcurrentJobs, s.GetMaxConcurrentJobs())

	s.MaxConcurrentJobs = 3
	assert.Equal(t, 3, s.GetMaxConcurrentJobs())
}

// cpu_hard_limit = 0 是「一键刹车」，同样不能被兜底。
func TestLogExportSetting_CPUHardLimitZeroIsMeaningful(t *testing.T) {
	s := &LogExportSetting{CPUHardLimit: 0}
	assert.Equal(t, float64(0), s.GetCPUHardLimit())

	s.CPUHardLimit = -1
	assert.Equal(t, float64(DefaultLogExportCPUHardLimit), s.GetCPUHardLimit())

	s.CPUHardLimit = 120
	assert.Equal(t, float64(DefaultLogExportCPUHardLimit), s.GetCPUHardLimit(), "out-of-range must fall back")

	s.CPUHardLimit = 90
	assert.Equal(t, float64(90), s.GetCPUHardLimit())
}

func TestLogExportSetting_CPUSoftLimitBounds(t *testing.T) {
	s := &LogExportSetting{}
	assert.Equal(t, float64(DefaultLogExportCPUSoftLimit), s.GetCPUSoftLimit())
	s.CPUSoftLimit = 101
	assert.Equal(t, float64(DefaultLogExportCPUSoftLimit), s.GetCPUSoftLimit())
	s.CPUSoftLimit = 55
	assert.Equal(t, float64(55), s.GetCPUSoftLimit())
}

func TestLogExportSetting_GzipLevelOutOfRange(t *testing.T) {
	s := &LogExportSetting{GzipLevel: 10}
	assert.Equal(t, DefaultLogExportGzipLevel, s.GetGzipLevel())
	s.GzipLevel = 9
	assert.Equal(t, 9, s.GetGzipLevel())
}

func TestLogExportSetting_DurationGetters(t *testing.T) {
	s := &LogExportSetting{}
	assert.Equal(t, time.Duration(DefaultLogExportJobTTLHours)*time.Hour, s.GetJobTTL())
	assert.Equal(t, time.Duration(DefaultLogExportDownloadTokenTTLSec)*time.Second, s.GetDownloadTokenTTL())
	assert.Equal(t, time.Duration(DefaultLogExportDownloadSessionTTLSec)*time.Second, s.GetDownloadSessionTTL())

	s.JobTTLHours = 2
	s.DownloadTokenTTLSec = 30
	s.DownloadSessionTTLSec = 600
	assert.Equal(t, 2*time.Hour, s.GetJobTTL())
	assert.Equal(t, 30*time.Second, s.GetDownloadTokenTTL())
	assert.Equal(t, 600*time.Second, s.GetDownloadSessionTTL())
}

func TestLogExportSetting_RangeAndWindowGetters(t *testing.T) {
	s := &LogExportSetting{}
	assert.Equal(t, int64(DefaultLogExportAdminMaxRangeSec), s.GetAdminMaxRangeSec())
	assert.Equal(t, int64(DefaultLogExportWindowSec), s.GetWindowSec())

	s.AdminMaxRangeSec = 86400
	s.WindowSec = 600
	assert.Equal(t, int64(86400), s.GetAdminMaxRangeSec())
	assert.Equal(t, int64(600), s.GetWindowSec())
}

// 这些旋钮暴露在管理后台设置页，填个极大值不该能绕过保护阀：
// 批大小决定单批取回多少行，行速率决定令牌桶放行速度，分片/xlsx 行数决定单文件写入量。
func TestLogExportSetting_GettersClampUpperBounds(t *testing.T) {
	const huge = 1 << 30
	cases := []struct {
		name string
		want int
		get  func(s *LogExportSetting) int
		set  func(s *LogExportSetting, v int)
	}{
		{"BatchSize", MaxLogExportBatchSize,
			func(s *LogExportSetting) int { return s.GetBatchSize() },
			func(s *LogExportSetting, v int) { s.BatchSize = v }},
		{"MaxRowsPerSec", MaxLogExportMaxRowsPerSec,
			func(s *LogExportSetting) int { return s.GetMaxRowsPerSec() },
			func(s *LogExportSetting, v int) { s.MaxRowsPerSec = v }},
		{"RowsPerFile", MaxLogExportRowsPerFile,
			func(s *LogExportSetting) int { return s.GetRowsPerFile() },
			func(s *LogExportSetting, v int) { s.RowsPerFile = v }},
		{"MaxParts", MaxLogExportMaxParts,
			func(s *LogExportSetting) int { return s.GetMaxParts() },
			func(s *LogExportSetting, v int) { s.MaxParts = v }},
		{"XlsxMaxRows", MaxLogExportXlsxMaxRows,
			func(s *LogExportSetting) int { return s.GetXlsxMaxRows() },
			func(s *LogExportSetting, v int) { s.XlsxMaxRows = v }},
		{"BatchSleepMs", MaxLogExportBatchSleepMs,
			func(s *LogExportSetting) int { return s.GetBatchSleepMs() },
			func(s *LogExportSetting, v int) { s.BatchSleepMs = v }},
		{"BatchQueryTimeoutSec", MaxLogExportBatchQueryTimeoutSec,
			func(s *LogExportSetting) int { return s.GetBatchQueryTimeoutSec() },
			func(s *LogExportSetting, v int) { s.BatchQueryTimeoutSec = v }},
		{"CPUCheckIntervalMs", MaxLogExportCPUCheckIntervalMs,
			func(s *LogExportSetting) int { return s.GetCPUCheckIntervalMs() },
			func(s *LogExportSetting, v int) { s.CPUCheckIntervalMs = v }},
		{"MinFreeDiskMB", MaxLogExportMinFreeDiskMB,
			func(s *LogExportSetting) int { return s.GetMinFreeDiskMB() },
			func(s *LogExportSetting, v int) { s.MinFreeDiskMB = v }},
		{"TimeoutSec", MaxLogExportTimeoutSec,
			func(s *LogExportSetting) int { return s.GetTimeoutSec() },
			func(s *LogExportSetting, v int) { s.TimeoutSec = v }},
		{"UserCooldownSec", MaxLogExportUserCooldownSec,
			func(s *LogExportSetting) int { return s.GetUserCooldownSec() },
			func(s *LogExportSetting, v int) { s.UserCooldownSec = v }},
		{"MaxConcurrentJobs", MaxLogExportConcurrentJobs,
			func(s *LogExportSetting) int { return s.GetMaxConcurrentJobs() },
			func(s *LogExportSetting, v int) { s.MaxConcurrentJobs = v }},
		{"MaxActiveJobsPerUser", MaxLogExportActiveJobsPerUser,
			func(s *LogExportSetting) int { return s.GetMaxActiveJobsPerUser() },
			func(s *LogExportSetting, v int) { s.MaxActiveJobsPerUser = v }},
		{"MaxTemplatesPerUser", MaxLogExportTemplatesPerUser,
			func(s *LogExportSetting) int { return s.GetMaxTemplatesPerUser() },
			func(s *LogExportSetting, v int) { s.MaxTemplatesPerUser = v }},
		{"MaxConcurrentDownloadsPerUser", MaxLogExportConcurrentDownloadsPerUser,
			func(s *LogExportSetting) int { return s.GetMaxConcurrentDownloadsPerUser() },
			func(s *LogExportSetting, v int) { s.MaxConcurrentDownloadsPerUser = v }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := &LogExportSetting{}
			tc.set(s, huge)
			assert.Equal(t, tc.want, tc.get(s), "an absurd value must be clamped to the cap")
			tc.set(s, tc.want)
			assert.Equal(t, tc.want, tc.get(s), "the cap itself must be accepted")
		})
	}
}

// xlsx 行数上限不能配得超过 Excel 单表的硬上限，否则写到一半必然失败。
func TestLogExportSetting_XlsxCapStaysBelowExcelHardLimit(t *testing.T) {
	const excelSheetRowLimit = 1048576
	assert.Less(t, MaxLogExportXlsxMaxRows, excelSheetRowLimit)
}

func TestLogExportSetting_DurationGettersClamp(t *testing.T) {
	s := &LogExportSetting{JobTTLHours: 1 << 20}
	assert.Equal(t, time.Duration(MaxLogExportJobTTLHours)*time.Hour, s.GetJobTTL())

	s = &LogExportSetting{DownloadTokenTTLSec: 1 << 20}
	assert.Equal(t,
		time.Duration(MaxLogExportDownloadTokenTTLSec)*time.Second,
		s.GetDownloadTokenTTL())

	s = &LogExportSetting{DownloadSessionTTLSec: 1 << 20}
	assert.Equal(t,
		time.Duration(MaxLogExportDownloadSessionTTLSec)*time.Second,
		s.GetDownloadSessionTTL())
}

func TestLogExportSetting_RangeAndWindowClamp(t *testing.T) {
	s := &LogExportSetting{AdminMaxRangeSec: 1 << 30}
	assert.Equal(t, int64(MaxLogExportAdminRangeSec), s.GetAdminMaxRangeSec())

	s = &LogExportSetting{WindowSec: 1 << 30}
	assert.Equal(t, int64(MaxLogExportWindowSec), s.GetWindowSec())
}

// 窗口过小会把一次跨月导出拆成上百万个几乎全空的查询，必须有下限兜底。
func TestLogExportSetting_WindowSecHasFloor(t *testing.T) {
	s := &LogExportSetting{WindowSec: 1}
	assert.Equal(t, int64(MinLogExportWindowSec), s.GetWindowSec())

	s.WindowSec = MinLogExportWindowSec
	assert.Equal(t, int64(MinLogExportWindowSec), s.GetWindowSec())

	s.WindowSec = MinLogExportWindowSec + 1
	assert.Equal(t, int64(MinLogExportWindowSec+1), s.GetWindowSec())
}

// batch_sleep_ms = 0 表示不额外休眠，是合法配置；负数才兜底。
func TestLogExportSetting_BatchSleepZeroAllowed(t *testing.T) {
	s := &LogExportSetting{BatchSleepMs: 0}
	assert.Equal(t, 0, s.GetBatchSleepMs())
	s.BatchSleepMs = -1
	assert.Equal(t, DefaultLogExportBatchSleepMs, s.GetBatchSleepMs())
}

func TestLogExportSetting_OffpeakWindowParsing(t *testing.T) {
	s := &LogExportSetting{OffpeakWindow: "02:00-06:00"}
	start, end := s.GetOffpeakWindow()
	assert.Equal(t, 120, start)
	assert.Equal(t, 360, end)

	// 非法配置回落默认窗口，而不是把导出永久卡住。
	for _, bad := range []string{"", "bogus", "25:00-26:00", "02:00", "02:00-06:61", "aa:bb-cc:dd"} {
		s.OffpeakWindow = bad
		start, end = s.GetOffpeakWindow()
		assert.Equal(t, 120, start, "input %q", bad)
		assert.Equal(t, 360, end, "input %q", bad)
	}
}

func TestLogExportSetting_InOffpeakWindow(t *testing.T) {
	s := &LogExportSetting{OffpeakWindow: "02:00-06:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 7, 27, h, m, 0, 0, time.Local) }

	assert.False(t, s.InOffpeakWindow(at(1, 59)))
	assert.True(t, s.InOffpeakWindow(at(2, 0)), "window start is inclusive")
	assert.True(t, s.InOffpeakWindow(at(4, 30)))
	assert.False(t, s.InOffpeakWindow(at(6, 0)), "window end is exclusive")
	assert.False(t, s.InOffpeakWindow(at(12, 0)))

	// 跨零点窗口
	s.OffpeakWindow = "22:00-04:00"
	assert.True(t, s.InOffpeakWindow(at(23, 0)))
	assert.True(t, s.InOffpeakWindow(at(0, 30)))
	assert.True(t, s.InOffpeakWindow(at(3, 59)))
	assert.False(t, s.InOffpeakWindow(at(4, 0)))
	assert.False(t, s.InOffpeakWindow(at(12, 0)))

	// 起止相同视为全天允许
	s.OffpeakWindow = "03:00-03:00"
	assert.True(t, s.InOffpeakWindow(at(12, 0)))
}

// 热更新：改完配置后 getter 必须立刻返回新值（调用方在使用点现取）。
func TestGetLogExportSetting_ReflectsHotUpdates(t *testing.T) {
	s := GetLogExportSetting()
	prev := *s
	t.Cleanup(func() { *GetLogExportSetting() = prev })

	s.BatchSleepMs = 250
	s.MaxRowsPerSec = 999
	assert.Equal(t, 250, GetLogExportSetting().GetBatchSleepMs())
	assert.Equal(t, 999, GetLogExportSetting().GetMaxRowsPerSec())
}

func TestLogExportSetting_DefaultsAreSane(t *testing.T) {
	s := GetLogExportSetting()
	assert.True(t, s.Enabled)
	assert.Equal(t, 1, s.MaxConcurrentJobs, "default must keep export to a single core's worth of work")
	assert.Equal(t, 1, s.GzipLevel, "gzip level 1 keeps CPU cost low")
	assert.False(t, s.OffpeakOnly, "offpeak mode is opt-in")
	assert.Less(t, float64(s.CPUSoftLimit), float64(s.CPUHardLimit))
}
