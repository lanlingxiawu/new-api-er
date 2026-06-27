package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// LedgerDetailSetting 台账明细功能的可调参数，含导出和列表两组。
type LedgerDetailSetting struct {
	// ── 导出参数 ──────────────────────────────────────────
	// ExportUserCooldownSec 单用户两次导出之间的最短间隔（秒）。
	ExportUserCooldownSec int `json:"export_user_cooldown_sec"`
	// ExportBatchSize 每次从数据库读取的批次行数。
	ExportBatchSize int `json:"export_batch_size"`
	// ExportBatchSleepMs 每批次处理后的休眠毫秒数（限速，减轻 DB 压力）。
	ExportBatchSleepMs int `json:"export_batch_sleep_ms"`
	// ExportRowsPerFile 每个分片文件最多写入行数，超出后自动开新文件。
	ExportRowsPerFile int `json:"export_rows_per_file"`
	// ExportMaxRangeSec 导出时允许的最大时间跨度（秒）。
	ExportMaxRangeSec int `json:"export_max_range_sec"`
	// ExportTimeoutSec 单次导出 goroutine 的超时时间（秒）。
	ExportTimeoutSec int `json:"export_timeout_sec"`

	// ── 列表查询参数 ───────────────────────────────────────
	// ListMaxRangeSec 列表查询允许的最大时间跨度（秒）。
	ListMaxRangeSec int `json:"list_max_range_sec"`
	// ListDefaultRangeSec 未传时间范围时的默认查询跨度（秒）。
	ListDefaultRangeSec int `json:"list_default_range_sec"`
	// ListDefaultLimit 列表接口默认每页行数。
	ListDefaultLimit int `json:"list_default_limit"`
	// ListMaxLimit 列表接口每页行数上限。
	ListMaxLimit int `json:"list_max_limit"`
	// ListScanBatchSize 每次从 DB 扫描的批次行数（内存 tag 过滤场景）。
	ListScanBatchSize int `json:"list_scan_batch_size"`
	// ListScanRowsPerReq 单次请求最多扫描行数，超出后返回截断结果。
	ListScanRowsPerReq int `json:"list_scan_rows_per_req"`
}

const (
	DefaultLedgerDetailExportUserCooldownSec = 300
	DefaultLedgerDetailExportBatchSize       = 3000
	DefaultLedgerDetailExportBatchSleepMs    = 100
	DefaultLedgerDetailExportRowsPerFile     = 1000000
	DefaultLedgerDetailExportMaxRangeSec     = 86400
	DefaultLedgerDetailExportTimeoutSec      = 7200

	DefaultLedgerDetailListMaxRangeSec     = 86400
	DefaultLedgerDetailListDefaultRangeSec = 86400
	DefaultLedgerDetailListDefaultLimit    = 100
	DefaultLedgerDetailListMaxLimit        = 200
	DefaultLedgerDetailListScanBatchSize   = 2000
	DefaultLedgerDetailListScanRowsPerReq  = 20000
)

var ledgerDetailSetting = LedgerDetailSetting{
	ExportUserCooldownSec: DefaultLedgerDetailExportUserCooldownSec,
	ExportBatchSize:       DefaultLedgerDetailExportBatchSize,
	ExportBatchSleepMs:    DefaultLedgerDetailExportBatchSleepMs,
	ExportRowsPerFile:     DefaultLedgerDetailExportRowsPerFile,
	ExportMaxRangeSec:     DefaultLedgerDetailExportMaxRangeSec,
	ExportTimeoutSec:      DefaultLedgerDetailExportTimeoutSec,

	ListMaxRangeSec:     DefaultLedgerDetailListMaxRangeSec,
	ListDefaultRangeSec: DefaultLedgerDetailListDefaultRangeSec,
	ListDefaultLimit:    DefaultLedgerDetailListDefaultLimit,
	ListMaxLimit:        DefaultLedgerDetailListMaxLimit,
	ListScanBatchSize:   DefaultLedgerDetailListScanBatchSize,
	ListScanRowsPerReq:  DefaultLedgerDetailListScanRowsPerReq,
}

func (s *LedgerDetailSetting) GetExportUserCooldownSec() int {
	if s.ExportUserCooldownSec <= 0 {
		return DefaultLedgerDetailExportUserCooldownSec
	}
	return s.ExportUserCooldownSec
}
func (s *LedgerDetailSetting) GetExportBatchSize() int {
	if s.ExportBatchSize <= 0 {
		return DefaultLedgerDetailExportBatchSize
	}
	return s.ExportBatchSize
}
func (s *LedgerDetailSetting) GetExportBatchSleepMs() int {
	if s.ExportBatchSleepMs <= 0 {
		return DefaultLedgerDetailExportBatchSleepMs
	}
	return s.ExportBatchSleepMs
}
func (s *LedgerDetailSetting) GetExportRowsPerFile() int {
	if s.ExportRowsPerFile <= 0 {
		return DefaultLedgerDetailExportRowsPerFile
	}
	return s.ExportRowsPerFile
}
func (s *LedgerDetailSetting) GetExportMaxRangeSec() int64 {
	if s.ExportMaxRangeSec <= 0 {
		return DefaultLedgerDetailExportMaxRangeSec
	}
	return int64(s.ExportMaxRangeSec)
}
func (s *LedgerDetailSetting) GetExportTimeoutSec() int {
	if s.ExportTimeoutSec <= 0 {
		return DefaultLedgerDetailExportTimeoutSec
	}
	return s.ExportTimeoutSec
}
func (s *LedgerDetailSetting) GetListMaxRangeSec() int64 {
	if s.ListMaxRangeSec <= 0 {
		return DefaultLedgerDetailListMaxRangeSec
	}
	return int64(s.ListMaxRangeSec)
}
func (s *LedgerDetailSetting) GetListDefaultRangeSec() int64 {
	if s.ListDefaultRangeSec <= 0 {
		return DefaultLedgerDetailListDefaultRangeSec
	}
	return int64(s.ListDefaultRangeSec)
}
func (s *LedgerDetailSetting) GetListDefaultLimit() int {
	if s.ListDefaultLimit <= 0 {
		return DefaultLedgerDetailListDefaultLimit
	}
	return s.ListDefaultLimit
}
func (s *LedgerDetailSetting) GetListMaxLimit() int {
	if s.ListMaxLimit <= 0 {
		return DefaultLedgerDetailListMaxLimit
	}
	return s.ListMaxLimit
}
func (s *LedgerDetailSetting) GetListScanBatchSize() int {
	if s.ListScanBatchSize <= 0 {
		return DefaultLedgerDetailListScanBatchSize
	}
	return s.ListScanBatchSize
}
func (s *LedgerDetailSetting) GetListScanRowsPerReq() int {
	if s.ListScanRowsPerReq <= 0 {
		return DefaultLedgerDetailListScanRowsPerReq
	}
	return s.ListScanRowsPerReq
}

func init() {
	config.GlobalConfig.Register("ledger_detail_setting", &ledgerDetailSetting)
}

func GetLedgerDetailSetting() *LedgerDetailSetting { return &ledgerDetailSetting }
