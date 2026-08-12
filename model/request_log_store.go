package model

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 请求日志正文（请求头/请求体/返回头/返回体）的磁盘存储层。
//
// 布局：<root>/<YYYY-MM-DD>/<created_at%8>/<created_at>_<request_id>.json
// 日期层让长期停机后的清理可以整目录删除，无需逐文件 stat；秒级时间戳对 8 取余
// 逐秒轮转，天然把同一天的文件均摊到 8 个目录。
//
// 索引里保存的是斜杠分隔的相对路径（rel），落到文件系统时才转成平台分隔符，
// 保证 Redis 快照在不同平台之间可读。

const (
	requestLogDirPerm  = 0o755
	requestLogFilePerm = 0o644

	// 单个 request id 净化后允许的最大长度。
	requestLogIdMaxLen = 128

	// 已创建目录的记忆上限。每天最多 8 个 key，超过上限说明已跨越多天，整体重建即可。
	requestLogDirCacheMax = 64

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

	requestLogDirMu    sync.RWMutex
	requestLogDirCache = make(map[string]struct{}, requestLogDirCacheMax)

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
	requestLogDirMu.Lock()
	requestLogDirCache = make(map[string]struct{}, requestLogDirCacheMax)
	requestLogDirMu.Unlock()

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

// requestLogRelPath 拼出相对根目录的斜杠路径。
func requestLogRelPath(createdAt int64, requestId string, id int64) string {
	bucket := createdAt % 8
	if bucket < 0 {
		bucket += 8
	}
	date := time.Unix(createdAt, 0).Format("2006-01-02")
	name := strconv.FormatInt(createdAt, 10) + "_" + sanitizeRequestId(requestId, id) + ".json"
	return date + "/" + strconv.FormatInt(bucket, 10) + "/" + name
}

// sanitizeRequestId 把 request id 收敛成安全的文件名片段。
//
// request id 目前由 common.NewRequestId() 生成、全为字母数字，但它同时会被写进
// 响应头并可能被上游/中间设备影响，因此不能靠"生成规则安全"这一约定来免除净化：
// 一个含 ../ 的 id 会让正文写到目录外。
func sanitizeRequestId(requestId string, id int64) string {
	b := make([]byte, 0, len(requestId))
	for i := 0; i < len(requestId); i++ {
		c := requestId[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	s := string(b)
	if len(s) > requestLogIdMaxLen {
		s = s[:requestLogIdMaxLen]
	}
	// 纯点号（""、"."、".."）在净化后依然是路径元素，必须换掉。
	if strings.Trim(s, ".") == "" {
		return "noreqid-" + strconv.FormatInt(id, 10)
	}
	return s
}

// writeRequestLogFile 把完整日志写成一个 JSON 文件。
// 不调用 fsync：请求日志是尽力而为的可观测性数据，不承担崩溃一致性。
func writeRequestLogFile(rel string, log *RequestLog) error {
	if !requestLogReady {
		return errRequestLogStoreUnavailable
	}
	data, err := common.Marshal(log)
	if err != nil {
		return err
	}
	full := requestLogFullPath(rel)
	relDir, fullDir := path.Dir(rel), filepath.Dir(full)
	if err = ensureRequestLogDir(relDir, fullDir); err != nil {
		return err
	}
	err = os.WriteFile(full, data, requestLogFilePerm)
	if err != nil && os.IsNotExist(err) {
		// 目录被清理协程回收了（跨天时可能发生）。作废记忆、重建目录后重试一次，
		// 否则该 <date>/<bucket> 的后续写入会一直失败。
		invalidateRequestLogDir(relDir)
		if err = ensureRequestLogDir(relDir, fullDir); err != nil {
			return err
		}
		err = os.WriteFile(full, data, requestLogFilePerm)
	}
	return err
}

// readRequestLogFile 读回完整日志（含大字段）。
func readRequestLogFile(rel string) (*RequestLog, error) {
	if !requestLogReady {
		return nil, errRequestLogStoreUnavailable
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
	if !requestLogReady || rel == "" {
		return false
	}
	info, err := os.Stat(requestLogFullPath(rel))
	return err == nil && !info.IsDir()
}

func requestLogFullPath(rel string) string {
	return filepath.Join(requestLogRoot, filepath.FromSlash(rel))
}

// ensureRequestLogDir 对同一个 <date>/<bucket> 只执行一次 MkdirAll，
// 避免每条日志一次目录创建 syscall。
func ensureRequestLogDir(relDir string, fullDir string) error {
	requestLogDirMu.RLock()
	_, cached := requestLogDirCache[relDir]
	requestLogDirMu.RUnlock()
	if cached {
		return nil
	}
	if err := os.MkdirAll(fullDir, requestLogDirPerm); err != nil {
		return err
	}
	requestLogDirMu.Lock()
	if len(requestLogDirCache) >= requestLogDirCacheMax {
		requestLogDirCache = make(map[string]struct{}, requestLogDirCacheMax)
	}
	requestLogDirCache[relDir] = struct{}{}
	requestLogDirMu.Unlock()
	return nil
}

func invalidateRequestLogDir(relDir string) {
	requestLogDirMu.Lock()
	delete(requestLogDirCache, relDir)
	requestLogDirMu.Unlock()
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
