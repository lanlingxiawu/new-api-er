package model

import (
	"bufio"
	"os"
	"path/filepath"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const businessStatsDeadLetterFile = "business_stats_dead_letter"

// currentDateFunc 返回当前日期字符串，用于生成带日期的日志文件名。
// 可在测试中替换以模拟跨日场景。
var currentDateFunc = func() string {
	return time.Now().Format("2006-01-02")
}

func currentDate() string {
	return currentDateFunc()
}

// fallbackFilename 将 basename 和日期拼成最终文件名，例如：
// "business_stats_fallback" + "2026-06-20" → "business_stats_fallback.2026-06-20.log"
func fallbackFilename(basename, date string) string {
	return basename + "." + date + ".log"
}

// fallbackWriteEntry 携带预序列化的 JSON 行和入队时捕获的日期。
// 日期在调用方（入队时）确定，goroutine 无需再读全局时间，避免入队到处理
// 之间发生跨日时写入错误文件。
type fallbackWriteEntry struct {
	basename string // e.g. "business_stats_fallback"
	line     []byte
	date     string // "2006-01-02"，入队时捕获
}

// fallbackQueue 将调用方与文件 I/O 解耦。后台单一 goroutine（init 启动）是唯一消费者。
// 队列满时 writeBusinessStatsJSONLine 返回 false，触发熔断器 hard-disable。
var fallbackQueue = make(chan fallbackWriteEntry, 10000)

// fallbackCloseAll 向 goroutine 发送关闭请求，goroutine 将 flush 并关闭所有 fd 后
// 关闭 done channel 通知调用方。Windows 上 t.TempDir() 清理前必须先释放打开的 fd。
var fallbackCloseAll = make(chan chan struct{}, 1)

func init() {
	go runFallbackWriteLoop()
}

// fallbackFileState 持有单个日期日志文件的持久 fd 和带缓冲写入器。
type fallbackFileState struct {
	f    *os.File
	bw   *bufio.Writer
	date string // 文件对应的日期，用于 evictStale 判断，避免依赖 map key 格式
}

func (s *fallbackFileState) close() {
	if s.bw != nil {
		s.bw.Flush()
	}
	if s.f != nil {
		s.f.Close()
	}
}

func openFallbackFile(basename, date string) *fallbackFileState {
	logDir := "."
	if common.LogDir != nil && *common.LogDir != "" {
		logDir = *common.LogDir
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		common.SysError("fallback_writer: mkdir failed: " + err.Error())
		return nil
	}
	path := filepath.Join(logDir, fallbackFilename(basename, date))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		common.SysError("fallback_writer: open failed: " + err.Error())
		return nil
	}
	return &fallbackFileState{f: f, bw: bufio.NewWriterSize(f, 32*1024), date: date}
}

// runFallbackWriteLoop 是单一写入 goroutine。
//
// writers 按 "basename.date" 为 key 缓存已打开的文件 fd，不同日期的条目
// 天然写入各自的文件，无需检测跨日 —— 因为日期已在入队时固化到 entry.date。
// 批量排空队列后统一 flush，空闲时按 ticker 定期 flush 防止数据滞留缓冲。
func runFallbackWriteLoop() {
	const idleFlushInterval = 2 * time.Second

	// key = basename + "." + date，例如 "business_stats_fallback.2026-06-20"
	writers := make(map[string]*fallbackFileState)
	ticker := time.NewTicker(idleFlushInterval)
	defer ticker.Stop()

	getWriter := func(basename, date string) *fallbackFileState {
		key := basename + "." + date
		if fw, ok := writers[key]; ok {
			return fw
		}
		fw := openFallbackFile(basename, date)
		if fw != nil {
			writers[key] = fw
		}
		return fw
	}

	write := func(entry fallbackWriteEntry) {
		fw := getWriter(entry.basename, entry.date)
		if fw == nil {
			return
		}
		if _, err := fw.bw.Write(entry.line); err != nil {
			common.SysError("fallback_writer: write error, reopening: " + err.Error())
			fw.close()
			key := entry.basename + "." + entry.date
			delete(writers, key)
			// 重试一次，用新 fd
			fw = openFallbackFile(entry.basename, entry.date)
			if fw == nil {
				return
			}
			writers[key] = fw
			fw.bw.Write(entry.line) //nolint:errcheck
		}
	}

	flushAll := func() {
		for name, fw := range writers {
			if err := fw.bw.Flush(); err != nil {
				common.SysError("fallback_writer: flush error: " + err.Error())
				fw.close()
				delete(writers, name)
			}
		}
	}

	drain := func() {
		for {
			select {
			case e := <-fallbackQueue:
				write(e)
			default:
				return
			}
		}
	}

	closeAll := func() {
		// 先排空队列，再关闭 fd，防止积压条目丢失
		drain()
		flushAll()
		for name, fw := range writers {
			fw.close()
			delete(writers, name)
		}
	}

	// evictStale 关闭早于今天的日志文件的 fd，防止长期运行时 fd 泄漏。
	// 日期直接从 fallbackFileState.date 读取，不依赖 map key 格式。
	evictStale := func() {
		today := currentDate()
		for key, fw := range writers {
			if fw.date < today {
				fw.close()
				delete(writers, key)
			}
		}
	}

	for {
		select {
		case entry := <-fallbackQueue:
			write(entry)
			// 排空当前所有积压条目，批量写后再 flush，减少 syscall 次数
			drain()
			flushAll()

		case <-ticker.C:
			flushAll()
			evictStale()

		case done := <-fallbackCloseAll:
			closeAll()
			close(done)
		}
	}
}

// writeBusinessStatsJSONLine 序列化 record，捕获当前日期，并将 JSON 行投入异步写队列。
// 仅在序列化失败或队列满时返回 false；队列满时触发熔断器 hard-disable。
func writeBusinessStatsJSONLine(basename, logPrefix string, record map[string]any) bool {
	data, err := common.Marshal(record)
	if err != nil {
		common.SysError(logPrefix + ": marshal failed: " + err.Error())
		return false
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'

	date := currentDate() // 入队时固化日期，goroutine 处理时不再依赖全局时间

	select {
	case fallbackQueue <- fallbackWriteEntry{basename: basename, line: line, date: date}:
		return true
	default:
		common.SysError(logPrefix + ": fallback queue full, entry dropped")
		return false
	}
}

func writeBusinessStatsDeadLetter(kind, reason string, attempts int, payload any) {
	record := map[string]any{
		"time":     time.Now().Unix(),
		"kind":     kind,
		"reason":   reason,
		"attempts": attempts,
		"payload":  payload,
	}
	writeBusinessStatsJSONLine(businessStatsDeadLetterFile, "writeBusinessStatsDeadLetter", record)
}

func writeBusinessStatsFallback(kind, reason string, payload any) bool {
	record := map[string]any{
		"time":    time.Now().Unix(),
		"kind":    kind,
		"reason":  reason,
		"payload": payload,
	}
	return writeBusinessStatsJSONLine(businessStatsFallbackFile, "writeBusinessStatsFallback", record)
}
