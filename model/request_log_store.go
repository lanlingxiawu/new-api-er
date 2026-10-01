package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 请求日志正文（请求头/请求体/返回头/返回体）的磁盘存储层：根目录、归属校验与读取。
// 写入格式见 request_log_segment.go（按日期目录存放的追加分段）。
//
// 升级前的版本每条日志一个文件：<root>/<YYYY-MM-DD>/<created_at%8>/<created_at>_<request_id>.json。
// 这类条目仍可能随索引快照恢复回来，读取与清理继续兼容，直到它们被淘汰。
//
// 索引里保存的是斜杠分隔的相对路径（rel），落到文件系统时才转成平台分隔符，
// 保证 Redis 快照在不同平台之间可读。

const (
	requestLogDirPerm  = 0o755
	requestLogFilePerm = 0o644

	// 归属标记文件。清理协程会删除根目录下一切不属于本布局的内容，因此必须能证明
	// 这个目录是本功能独占的——REQUEST_LOG_DIR 一旦被指向共享目录（/var/log、数据盘
	// 根等），没有这道校验就会把无关文件一并回收。
	requestLogOwnerMarker = ".new-api-request-log"

	defaultRequestLogWriters       = 4
	maxRequestLogWriters           = 32
	defaultRequestLogQueueSize     = 1000
	defaultRequestLogSweepInterval = 300 * time.Second
)

var errRequestLogStoreUnavailable = errors.New("request log store unavailable")

var (
	requestLogRoot  string
	requestLogReady bool

	requestLogWriters       = defaultRequestLogWriters
	requestLogQueueSize     = defaultRequestLogQueueSize
	requestLogSweepInterval = defaultRequestLogSweepInterval
)

// InitRequestLogStore 解析请求日志的部署级环境变量并准备根目录。
//
// 必须由 InitResources 在 godotenv.Load(".env") 之后调用，不能改成包级变量初始化：
// Go 会在 main() 之前完成所有被导入包的变量初始化，那时 .env 还没被读进环境，
// 写在 .env 里的调参会被静默忽略（历史上 REQUEST_LOG_MAX_INFLIGHT 就是这么失效的）。
func InitRequestLogStore() {
	requestLogWriters = clampInt(common.GetEnvOrDefault("REQUEST_LOG_WRITERS", defaultRequestLogWriters), 1, maxRequestLogWriters)
	requestLogQueueSize = clampInt(common.GetEnvOrDefault("REQUEST_LOG_MAX_INFLIGHT", defaultRequestLogQueueSize), 1, 1<<20)
	requestLogSweepInterval = time.Duration(maxInt(common.GetEnvOrDefault("REQUEST_LOG_SWEEP_INTERVAL_SEC", int(defaultRequestLogSweepInterval/time.Second)), 0)) * time.Second
	closeDefaultRequestLogWriter()

	requestLogRoot = resolveRequestLogRoot()
	if err := os.MkdirAll(requestLogRoot, requestLogDirPerm); err != nil {
		requestLogReady = false
		common.SysError("request log disabled: cannot create directory " + requestLogRoot + ": " + err.Error())
		return
	}
	if err := claimRequestLogRoot(); err != nil {
		requestLogReady = false
		common.SysError("request log disabled: " + err.Error())
		return
	}
	requestLogReady = true
	common.SysLog("request log bodies stored under " + requestLogRoot)
}

// claimRequestLogRoot 确认根目录由本功能独占，并留下归属标记。
//
// 失败即关闭整个功能而不是仅关闭清理：留着写入但不回收会让磁盘无界增长，
// 两种降级都不可接受，让部署方把 REQUEST_LOG_DIR 指到专用目录才是正解。
func claimRequestLogRoot() error {
	marker := filepath.Join(requestLogRoot, requestLogOwnerMarker)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return errors.New("cannot read owner marker in " + requestLogRoot + ": " + err.Error())
	}

	entries, err := os.ReadDir(requestLogRoot)
	if err != nil {
		return errors.New("cannot read " + requestLogRoot + ": " + err.Error())
	}
	for _, entry := range entries {
		// 升级路径：老版本没有标记文件，但目录里只会有日期目录。
		if entry.IsDir() && isRequestLogDateDir(entry.Name()) {
			continue
		}
		return errors.New(requestLogRoot + " holds unrelated content (" + entry.Name() +
			"); the request log sweeper requires a dedicated directory — point REQUEST_LOG_DIR at an empty one")
	}
	if err = os.WriteFile(marker, []byte("new-api request log body store\n"), requestLogFilePerm); err != nil {
		return errors.New("cannot write owner marker in " + requestLogRoot + ": " + err.Error())
	}
	return nil
}

// resolveRequestLogRoot 默认取进程可执行文件同目录下的 relay_log。
func resolveRequestLogRoot() string {
	if dir := strings.TrimSpace(os.Getenv("REQUEST_LOG_DIR")); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		// 取不到可执行文件路径时退回工作目录，功能可用性优先于路径习惯。
		return filepath.Join(".", "relay_log")
	}
	return filepath.Join(filepath.Dir(exe), "relay_log")
}

// RequestLogStoreReady 报告正文目录是否可用。不可用时整条日志被丢弃，
// 而不是退化成"只有索引没有正文"——那样的条目点开是空的，比没有日志更误导。
func RequestLogStoreReady() bool { return requestLogReady }

// RequestLogRoot 返回正文根目录，供清理协程与测试使用。
func RequestLogRoot() string { return requestLogRoot }

// RequestLogWriterCount 返回写盘 worker 数，等于稳态峰值文件句柄数。
func RequestLogWriterCount() int { return requestLogWriters }

// RequestLogQueueSize 返回写队列深度。
func RequestLogQueueSize() int { return requestLogQueueSize }

// readRequestLogFile 读回完整日志（含大字段）：分段记录按偏移读，旧格式整文件读。
func readRequestLogFile(rel string) (*RequestLog, error) {
	if !requestLogReady {
		return nil, errRequestLogStoreUnavailable
	}
	if segRel, offset, length, ok := parseRequestLogRecordRel(rel); ok {
		return readRequestLogRecord(segRel, offset, length)
	}
	data, err := os.ReadFile(requestLogFullPath(rel))
	if err != nil {
		return nil, err
	}
	var log RequestLog
	if err = common.Unmarshal(data, &log); err != nil {
		return nil, err
	}
	return &log, nil
}

// requestLogFileExists 用于快照恢复时剔除正文已消失的条目，避免详情点开是 404。
func requestLogFileExists(rel string) bool {
	return requestLogBodyExists(rel, nil)
}

// requestLogBodyExists 判断索引条目的正文是否还在。segSizes 非 nil 时按分段缓存
// 文件大小：恢复 15 万条快照时，同一分段里的上万条记录只需 stat 一次。
// 分段比记录末尾还短（进程崩溃时写了一半）视为不存在。
func requestLogBodyExists(rel string, segSizes map[string]int64) bool {
	if !requestLogReady || rel == "" {
		return false
	}
	segRel, offset, length, ok := parseRequestLogRecordRel(rel)
	if !ok {
		info, err := os.Stat(requestLogFullPath(rel))
		return err == nil && !info.IsDir()
	}
	size, cached := segSizes[segRel]
	if !cached {
		size = -1
		if info, err := os.Stat(requestLogFullPath(segRel)); err == nil && !info.IsDir() {
			size = info.Size()
		}
		if segSizes != nil {
			segSizes[segRel] = size
		}
	}
	return requestLogRecordWithin(size, offset, length)
}

// requestLogRecordWithin 判断 [offset, offset+length) 是否完整落在 size 字节的分段内。
// 用减法比较而不是 offset+length：快照里的 rel 是外部数据，偏移接近 MaxInt64 时
// 加法会回绕成负数，把不存在的记录判成存在。
func requestLogRecordWithin(size, offset int64, length int) bool {
	return size >= 0 && offset >= 0 && length > 0 && offset <= size-int64(length)
}

func requestLogFullPath(rel string) string {
	return filepath.Join(requestLogRoot, filepath.FromSlash(rel))
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(v, lo int) int {
	if v < lo {
		return lo
	}
	return v
}
