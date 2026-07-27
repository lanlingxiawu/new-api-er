package operation_setting

import (
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// LogExportSetting 使用日志导出（管理员后台任务）的可调参数。
//
// 所有参数均支持热更新：调用方必须在使用点现调 getter，不得在任务启动时把配置
// 快照进结构体，否则运行中的任务无法响应配置变更。哪些参数对运行中的任务立即
// 生效、哪些仅对新分片/新任务生效，见 docs/design/usage-log-export.md §8.1。
type LogExportSetting struct {
	// ── 开关与配额 ──────────────────────────────────────────
	// Enabled 功能总开关，关闭后拒绝新建导出任务（不中断运行中的任务）。
	Enabled bool `json:"enabled"`
	// UserCooldownSec 单用户两次创建任务的最短间隔（秒）。
	UserCooldownSec int `json:"user_cooldown_sec"`
	// MaxConcurrentJobs 全局同时运行的导出任务数上限。
	MaxConcurrentJobs int `json:"max_concurrent_jobs"`
	// MaxActiveJobsPerUser 单用户未过期任务数上限。
	MaxActiveJobsPerUser int `json:"max_active_jobs_per_user"`
	// AdminMaxRangeSec 单次导出允许的最大时间跨度（秒）。
	AdminMaxRangeSec int `json:"admin_max_range_sec"`
	// TimeoutSec 单任务超时（秒）。ctx 在任务启动时创建，改动不影响运行中的任务。
	TimeoutSec int `json:"timeout_sec"`
	// JobTTLHours 任务状态与分片文件的保留时长（小时）。
	JobTTLHours int `json:"job_ttl_hours"`
	// MaxTemplatesPerUser 单用户自定义模板数上限。
	MaxTemplatesPerUser int `json:"max_templates_per_user"`

	// ── 资源治理 ────────────────────────────────────────────
	// BatchSize 每批从 LOG_DB 读取的行数。
	BatchSize int `json:"batch_size"`
	// BatchSleepMs 每批之间的基础休眠毫秒数（节流保护 relay 写入）。
	BatchSleepMs int `json:"batch_sleep_ms"`
	// BatchQueryTimeoutSec 单批查询的超时（秒），防止慢查询长期占住连接。
	BatchQueryTimeoutSec int `json:"batch_query_timeout_sec"`
	// WindowSec 扫描时间窗口大小（秒），把单次索引区间限死。
	WindowSec int `json:"window_sec"`
	// MaxRowsPerSec 令牌桶行速率上限。
	MaxRowsPerSec int `json:"max_rows_per_sec"`
	// CPUSoftLimit CPU 使用率软限（百分比），超过则线性加大休眠。
	CPUSoftLimit int `json:"cpu_soft_limit"`
	// CPUHardLimit CPU 使用率硬限（百分比），超过则暂停导出。设为 0 可一键刹车。
	CPUHardLimit int `json:"cpu_hard_limit"`
	// CPUCheckIntervalMs CPU 水位检查间隔（毫秒）。
	CPUCheckIntervalMs int `json:"cpu_check_interval_ms"`
	// GzipLevel gzip 压缩等级（1~9），1 最省 CPU。仅对新建分片生效。
	GzipLevel int `json:"gzip_level"`
	// RowsPerFile 单个分片文件最大行数。仅对新建分片生效。
	RowsPerFile int `json:"rows_per_file"`
	// MaxParts 分片数上限，超出则任务失败。
	MaxParts int `json:"max_parts"`
	// XlsxMaxRows 允许使用 xlsx 的行数上限，超出自动降级为 csv.gz。
	XlsxMaxRows int `json:"xlsx_max_rows"`
	// MinFreeDiskMB 磁盘可用空间下限（MB），低于此值拒绝创建/中止任务。
	MinFreeDiskMB int `json:"min_free_disk_mb"`
	// OffpeakOnly 仅在低峰时段运行导出。
	OffpeakOnly bool `json:"offpeak_only"`
	// OffpeakWindow 低峰时段，格式 "HH:MM-HH:MM"，服务器本地时区，支持跨零点。
	OffpeakWindow string `json:"offpeak_window"`

	// ── 下载 ────────────────────────────────────────────────
	// DownloadTokenTTLSec 下载令牌有效期（秒）。仅对新签发的令牌生效。
	DownloadTokenTTLSec int `json:"download_token_ttl_sec"`
	// DownloadSessionTTLSec 令牌首次使用后的续传窗口（秒）。
	DownloadSessionTTLSec int `json:"download_session_ttl_sec"`
	// MaxConcurrentDownloadsPerUser 单用户并发下载数上限。
	MaxConcurrentDownloadsPerUser int `json:"max_concurrent_downloads_per_user"`
}

const (
	DefaultLogExportUserCooldownSec      = 300
	DefaultLogExportMaxConcurrentJobs    = 1
	DefaultLogExportMaxActiveJobsPerUser = 3
	DefaultLogExportAdminMaxRangeSec     = 2678400 // 31 天
	DefaultLogExportTimeoutSec           = 7200
	DefaultLogExportJobTTLHours          = 24
	DefaultLogExportMaxTemplatesPerUser  = 50

	DefaultLogExportBatchSize            = 3000
	DefaultLogExportBatchSleepMs         = 100
	DefaultLogExportBatchQueryTimeoutSec = 10
	DefaultLogExportWindowSec            = 3600
	// MinLogExportWindowSec 扫描窗口下限，防止误配置把导出拆成海量空查询。
	MinLogExportWindowSec              = 60
	DefaultLogExportMaxRowsPerSec      = 20000
	DefaultLogExportCPUSoftLimit       = 70
	DefaultLogExportCPUHardLimit       = 85
	DefaultLogExportCPUCheckIntervalMs = 1000
	DefaultLogExportGzipLevel          = 1
	DefaultLogExportRowsPerFile        = 1000000
	DefaultLogExportMaxParts           = 100
	DefaultLogExportXlsxMaxRows        = 200000
	DefaultLogExportMinFreeDiskMB      = 2048
	DefaultLogExportOffpeakWindow      = "02:00-06:00"

	DefaultLogExportDownloadTokenTTLSec           = 60
	DefaultLogExportDownloadSessionTTLSec         = 1800
	DefaultLogExportMaxConcurrentDownloadsPerUser = 2
)

// 上限。这些旋钮已经暴露在管理后台设置页，手滑填个极大值不该能绕过保护阀：
// 批大小直接决定单次查询取回多少行（内存与 DB 压力），行速率决定令牌桶放行的
// 速度，分片行数与 xlsx 行数决定单文件的写入量。getter 层统一 clamp，前端表单
// 的 max 与这里保持一致。
const (
	// MaxLogExportBatchSize 单批最多取多少行。再大就是单次查询把内存和 DB 一起顶穿。
	MaxLogExportBatchSize = 50000
	// MaxLogExportBatchSleepMs 批间休眠上限（1 分钟），再大等同于挂起。
	MaxLogExportBatchSleepMs = 60000
	// MaxLogExportBatchQueryTimeoutSec 单批查询超时上限（5 分钟）。
	MaxLogExportBatchQueryTimeoutSec = 300
	// MaxLogExportWindowSec 扫描窗口上限（1 天），避免退化成一次跨月大范围扫描。
	MaxLogExportWindowSec = 86400
	// MaxLogExportMaxRowsPerSec 行速率上限，超过这个量级已经谈不上「限速」了。
	MaxLogExportMaxRowsPerSec = 500000
	// MaxLogExportCPUCheckIntervalMs CPU 水位检查间隔上限（1 分钟）。
	MaxLogExportCPUCheckIntervalMs = 60000
	// MaxLogExportRowsPerFile 单分片行数上限。
	MaxLogExportRowsPerFile = 5000000
	// MaxLogExportMaxParts 分片数上限。
	MaxLogExportMaxParts = 1000
	// MaxLogExportXlsxMaxRows xlsx 行数上限。Excel 单表硬上限是 1048576（含表头），
	// 不能配得比它还大，否则写到一半必然失败。
	MaxLogExportXlsxMaxRows = 1000000
	// MaxLogExportMinFreeDiskMB 磁盘余量下限的上限（1 TB），再大等于永远拒绝导出。
	MaxLogExportMinFreeDiskMB = 1024 * 1024
	// MaxLogExportTimeoutSec 单任务超时上限（24 小时）。
	MaxLogExportTimeoutSec = 86400
	// MaxLogExportJobTTLHours 任务保留时长上限（30 天）。
	MaxLogExportJobTTLHours = 24 * 30
	// MaxLogExportUserCooldownSec 冷却上限（1 天）。
	MaxLogExportUserCooldownSec = 86400
	// MaxLogExportConcurrentJobs 并发任务数上限——导出是 CPU 密集的后台作业。
	MaxLogExportConcurrentJobs = 16
	// MaxLogExportActiveJobsPerUser 单用户在途任务上限。
	MaxLogExportActiveJobsPerUser = 20
	// MaxLogExportTemplatesPerUser 单用户模板数上限。
	MaxLogExportTemplatesPerUser = 500
	// MaxLogExportAdminRangeSec 单次导出时间跨度上限（1 年）。
	MaxLogExportAdminRangeSec = 366 * 86400
	// MaxLogExportDownloadTokenTTLSec 下载令牌有效期上限（1 小时）。
	MaxLogExportDownloadTokenTTLSec = 3600
	// MaxLogExportDownloadSessionTTLSec 续传窗口上限（12 小时）。
	MaxLogExportDownloadSessionTTLSec = 12 * 3600
	// MaxLogExportConcurrentDownloadsPerUser 单用户并发下载上限。
	MaxLogExportConcurrentDownloadsPerUser = 16
)

// clampInt 把配置值夹在 [min, max] 内；非法值（<=0）由调用方先行回退默认值。
func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

var logExportSetting = LogExportSetting{
	Enabled:              true,
	UserCooldownSec:      DefaultLogExportUserCooldownSec,
	MaxConcurrentJobs:    DefaultLogExportMaxConcurrentJobs,
	MaxActiveJobsPerUser: DefaultLogExportMaxActiveJobsPerUser,
	AdminMaxRangeSec:     DefaultLogExportAdminMaxRangeSec,
	TimeoutSec:           DefaultLogExportTimeoutSec,
	JobTTLHours:          DefaultLogExportJobTTLHours,
	MaxTemplatesPerUser:  DefaultLogExportMaxTemplatesPerUser,

	BatchSize:            DefaultLogExportBatchSize,
	BatchSleepMs:         DefaultLogExportBatchSleepMs,
	BatchQueryTimeoutSec: DefaultLogExportBatchQueryTimeoutSec,
	WindowSec:            DefaultLogExportWindowSec,
	MaxRowsPerSec:        DefaultLogExportMaxRowsPerSec,
	CPUSoftLimit:         DefaultLogExportCPUSoftLimit,
	CPUHardLimit:         DefaultLogExportCPUHardLimit,
	CPUCheckIntervalMs:   DefaultLogExportCPUCheckIntervalMs,
	GzipLevel:            DefaultLogExportGzipLevel,
	RowsPerFile:          DefaultLogExportRowsPerFile,
	MaxParts:             DefaultLogExportMaxParts,
	XlsxMaxRows:          DefaultLogExportXlsxMaxRows,
	MinFreeDiskMB:        DefaultLogExportMinFreeDiskMB,
	OffpeakOnly:          false,
	OffpeakWindow:        DefaultLogExportOffpeakWindow,

	DownloadTokenTTLSec:           DefaultLogExportDownloadTokenTTLSec,
	DownloadSessionTTLSec:         DefaultLogExportDownloadSessionTTLSec,
	MaxConcurrentDownloadsPerUser: DefaultLogExportMaxConcurrentDownloadsPerUser,
}

func (s *LogExportSetting) GetUserCooldownSec() int {
	if s.UserCooldownSec <= 0 {
		return DefaultLogExportUserCooldownSec
	}
	return clampInt(s.UserCooldownSec, 0, MaxLogExportUserCooldownSec)
}

// GetMaxConcurrentJobs 允许为 0：运营可用它停止接受新任务（不中断运行中的任务）。
func (s *LogExportSetting) GetMaxConcurrentJobs() int {
	if s.MaxConcurrentJobs < 0 {
		return DefaultLogExportMaxConcurrentJobs
	}
	// 0 有意义（停止接受新任务），所以下限是 0 而不是 1。
	return clampInt(s.MaxConcurrentJobs, 0, MaxLogExportConcurrentJobs)
}

func (s *LogExportSetting) GetMaxActiveJobsPerUser() int {
	if s.MaxActiveJobsPerUser <= 0 {
		return DefaultLogExportMaxActiveJobsPerUser
	}
	return clampInt(s.MaxActiveJobsPerUser, 1, MaxLogExportActiveJobsPerUser)
}

func (s *LogExportSetting) GetAdminMaxRangeSec() int64 {
	if s.AdminMaxRangeSec <= 0 {
		return DefaultLogExportAdminMaxRangeSec
	}
	return int64(clampInt(s.AdminMaxRangeSec, 1, MaxLogExportAdminRangeSec))
}

func (s *LogExportSetting) GetTimeoutSec() int {
	if s.TimeoutSec <= 0 {
		return DefaultLogExportTimeoutSec
	}
	return clampInt(s.TimeoutSec, 1, MaxLogExportTimeoutSec)
}

func (s *LogExportSetting) GetJobTTL() time.Duration {
	hours := s.JobTTLHours
	if hours <= 0 {
		hours = DefaultLogExportJobTTLHours
	}
	return time.Duration(clampInt(hours, 1, MaxLogExportJobTTLHours)) * time.Hour
}

func (s *LogExportSetting) GetMaxTemplatesPerUser() int {
	if s.MaxTemplatesPerUser <= 0 {
		return DefaultLogExportMaxTemplatesPerUser
	}
	return clampInt(s.MaxTemplatesPerUser, 1, MaxLogExportTemplatesPerUser)
}

func (s *LogExportSetting) GetBatchSize() int {
	if s.BatchSize <= 0 {
		return DefaultLogExportBatchSize
	}
	return clampInt(s.BatchSize, 1, MaxLogExportBatchSize)
}

func (s *LogExportSetting) GetBatchSleepMs() int {
	if s.BatchSleepMs < 0 {
		return DefaultLogExportBatchSleepMs
	}
	// 0 有意义（不额外休眠），下限取 0。
	return clampInt(s.BatchSleepMs, 0, MaxLogExportBatchSleepMs)
}

func (s *LogExportSetting) GetBatchQueryTimeoutSec() int {
	if s.BatchQueryTimeoutSec <= 0 {
		return DefaultLogExportBatchQueryTimeoutSec
	}
	return clampInt(s.BatchQueryTimeoutSec, 1, MaxLogExportBatchQueryTimeoutSec)
}

// GetWindowSec 返回扫描窗口大小，并施加一个下限。
// 窗口过小会把一次跨月导出拆成上百万个几乎全空的查询——每个窗口都要走一遍
// 索引定位，是纯粹的 DB 负担。60 秒足够细，也足够避免这种误配置。
func (s *LogExportSetting) GetWindowSec() int64 {
	if s.WindowSec <= 0 {
		return DefaultLogExportWindowSec
	}
	return int64(clampInt(s.WindowSec, MinLogExportWindowSec, MaxLogExportWindowSec))
}

func (s *LogExportSetting) GetMaxRowsPerSec() int {
	if s.MaxRowsPerSec <= 0 {
		return DefaultLogExportMaxRowsPerSec
	}
	return clampInt(s.MaxRowsPerSec, 1, MaxLogExportMaxRowsPerSec)
}

func (s *LogExportSetting) GetCPUSoftLimit() float64 {
	if s.CPUSoftLimit <= 0 || s.CPUSoftLimit > 100 {
		return DefaultLogExportCPUSoftLimit
	}
	return float64(s.CPUSoftLimit)
}

// GetCPUHardLimit 允许为 0：把硬限设为 0 是「一键刹车」，所有运行中的任务会在
// 下一次闸门检查时暂停（保持 running，不丢进度）。
func (s *LogExportSetting) GetCPUHardLimit() float64 {
	if s.CPUHardLimit < 0 || s.CPUHardLimit > 100 {
		return DefaultLogExportCPUHardLimit
	}
	return float64(s.CPUHardLimit)
}

func (s *LogExportSetting) GetCPUCheckIntervalMs() int {
	if s.CPUCheckIntervalMs <= 0 {
		return DefaultLogExportCPUCheckIntervalMs
	}
	return clampInt(s.CPUCheckIntervalMs, 1, MaxLogExportCPUCheckIntervalMs)
}

func (s *LogExportSetting) GetGzipLevel() int {
	if s.GzipLevel < 1 || s.GzipLevel > 9 {
		return DefaultLogExportGzipLevel
	}
	return s.GzipLevel
}

func (s *LogExportSetting) GetRowsPerFile() int {
	if s.RowsPerFile <= 0 {
		return DefaultLogExportRowsPerFile
	}
	return clampInt(s.RowsPerFile, 1, MaxLogExportRowsPerFile)
}

func (s *LogExportSetting) GetMaxParts() int {
	if s.MaxParts <= 0 {
		return DefaultLogExportMaxParts
	}
	return clampInt(s.MaxParts, 1, MaxLogExportMaxParts)
}

func (s *LogExportSetting) GetXlsxMaxRows() int {
	if s.XlsxMaxRows <= 0 {
		return DefaultLogExportXlsxMaxRows
	}
	return clampInt(s.XlsxMaxRows, 1, MaxLogExportXlsxMaxRows)
}

func (s *LogExportSetting) GetMinFreeDiskMB() int {
	if s.MinFreeDiskMB <= 0 {
		return DefaultLogExportMinFreeDiskMB
	}
	return clampInt(s.MinFreeDiskMB, 1, MaxLogExportMinFreeDiskMB)
}

func (s *LogExportSetting) GetDownloadTokenTTL() time.Duration {
	sec := s.DownloadTokenTTLSec
	if sec <= 0 {
		sec = DefaultLogExportDownloadTokenTTLSec
	}
	return time.Duration(clampInt(sec, 1, MaxLogExportDownloadTokenTTLSec)) * time.Second
}

func (s *LogExportSetting) GetDownloadSessionTTL() time.Duration {
	sec := s.DownloadSessionTTLSec
	if sec <= 0 {
		sec = DefaultLogExportDownloadSessionTTLSec
	}
	return time.Duration(clampInt(sec, 1, MaxLogExportDownloadSessionTTLSec)) * time.Second
}

func (s *LogExportSetting) GetMaxConcurrentDownloadsPerUser() int {
	if s.MaxConcurrentDownloadsPerUser <= 0 {
		return DefaultLogExportMaxConcurrentDownloadsPerUser
	}
	return clampInt(s.MaxConcurrentDownloadsPerUser, 1, MaxLogExportConcurrentDownloadsPerUser)
}

// GetOffpeakWindow 解析低峰时段，返回起止的「当日分钟数」。
// 解析失败时回落到默认窗口，避免一条写错的配置把导出永久卡住。
func (s *LogExportSetting) GetOffpeakWindow() (startMin, endMin int) {
	startMin, endMin, ok := parseOffpeakWindow(s.OffpeakWindow)
	if !ok {
		startMin, endMin, _ = parseOffpeakWindow(DefaultLogExportOffpeakWindow)
	}
	return startMin, endMin
}

// InOffpeakWindow 判断给定时刻是否落在低峰时段内，支持跨零点窗口（如 22:00-04:00）。
// 起止相同视为全天允许。
func (s *LogExportSetting) InOffpeakWindow(t time.Time) bool {
	startMin, endMin := s.GetOffpeakWindow()
	if startMin == endMin {
		return true
	}
	cur := t.Hour()*60 + t.Minute()
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	// 跨零点：例如 22:00-04:00
	return cur >= startMin || cur < endMin
}

func parseOffpeakWindow(window string) (startMin, endMin int, ok bool) {
	parts := strings.Split(strings.TrimSpace(window), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	startMin, ok = parseClockMinutes(parts[0])
	if !ok {
		return 0, 0, false
	}
	endMin, ok = parseClockMinutes(parts[1])
	if !ok {
		return 0, 0, false
	}
	return startMin, endMin, true
}

func parseClockMinutes(value string) (int, bool) {
	fields := strings.Split(strings.TrimSpace(value), ":")
	if len(fields) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func init() {
	config.GlobalConfig.Register("log_export_setting", &logExportSetting)
}

// GetLogExportSetting 返回配置。调用方必须在使用点现调，不得跨请求/跨批次缓存指针，
// 否则热更新失效（见 docs/design/usage-log-export.md §8.1）。
func GetLogExportSetting() *LogExportSetting { return &logExportSetting }
