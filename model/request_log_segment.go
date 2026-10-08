package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 请求日志正文的分段追加存储。
//
// 布局：<root>/<YYYY-MM-DD>/seg-<HHMMSS>-<进程标记>-<序号>.jsonl
// 每个写盘 worker 独占一个"活动分段"，只追加不改写；一条日志一行 JSON（JSON 会转义
// 正文里的换行），索引记 "<分段相对路径>#<偏移>#<长度>"。
//
// 不用"每条一个文件"：那样每条日志都要付一次 create + close，被淘汰后再付一次
// unlink。这些文件系统元数据操作与正文大小无关，压测时是网关 CPU 的最大单项
// （Linux 上写盘开销的 64% 是 open，Windows 上写盘占到总 CPU 的三成）；生产保留
// 约 15 万条时，磁盘上还要常驻 15 万个小文件，清理每轮都得全部列一遍。

const (
	// 活动分段满足任一条件即轮转：跨日期、写满、打开太久。时长上限让清理的回收
	// 粒度在低流量时也保持在分钟级。
	requestLogSegmentMaxBytes = 32 << 20
	requestLogSegmentMaxAge   = 60 * time.Second

	// 单次 write 的缓冲上限。一批日志超过它就分多次写，避免个别超大批次撑出大缓冲。
	requestLogFlushBytes = 1 << 20

	// RequestLogMaxBatch 是写盘 worker 一次合并处理的最大条数。
	RequestLogMaxBatch = 64

	requestLogSegmentPrefix = "seg-"
	requestLogSegmentSuffix = ".jsonl"
)

// 分段登记表：活动分段集合 + 本进程分段序号。
//
// 清理协程先在锁内复制活动集合并读取序号，再取索引快照。本进程序号不大于快照值、
// 又不在活动集合里的分段，必然在快照前就已关闭；分段关闭前它的所有条目都已进索引，
// 所以索引快照对它是完整的，"无引用即删"不会误删。序号更大的分段是快照之后才
// 登记的，一律保留到下一轮。
var (
	requestLogSegMu     sync.Mutex
	requestLogSegSeq    int64
	requestLogSegActive = make(map[string]struct{})
	// 区分本进程与历史进程留下的分段：历史分段早已关闭，只按索引引用判定。
	requestLogProcTag = strconv.FormatInt(time.Now().UnixNano(), 36)
)

func registerRequestLogSegment(now time.Time) (rel string, name string) {
	requestLogSegMu.Lock()
	defer requestLogSegMu.Unlock()
	requestLogSegSeq++
	name = requestLogSegmentPrefix + now.Format("150405") + "-" + requestLogProcTag + "-" +
		strconv.FormatInt(requestLogSegSeq, 10) + requestLogSegmentSuffix
	rel = now.Format("2006-01-02") + "/" + name
	requestLogSegActive[rel] = struct{}{}
	return rel, name
}

func unregisterRequestLogSegment(rel string) {
	requestLogSegMu.Lock()
	delete(requestLogSegActive, rel)
	requestLogSegMu.Unlock()
}

// requestLogSegmentSnapshot 返回活动分段的副本与当前序号。
func requestLogSegmentSnapshot() (map[string]struct{}, int64) {
	requestLogSegMu.Lock()
	defer requestLogSegMu.Unlock()
	active := make(map[string]struct{}, len(requestLogSegActive))
	for rel := range requestLogSegActive {
		active[rel] = struct{}{}
	}
	return active, requestLogSegSeq
}

func isRequestLogSegmentName(name string) bool {
	return strings.HasPrefix(name, requestLogSegmentPrefix) && strings.HasSuffix(name, requestLogSegmentSuffix)
}

// parseRequestLogSegmentName 解析出进程标记与序号；格式不符返回 ok=false。
func parseRequestLogSegmentName(name string) (procTag string, seq int64, ok bool) {
	if !isRequestLogSegmentName(name) {
		return "", 0, false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, requestLogSegmentPrefix), requestLogSegmentSuffix), "-")
	if len(parts) != 3 || len(parts[0]) != 6 || parts[1] == "" {
		return "", 0, false
	}
	seq, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || seq <= 0 {
		return "", 0, false
	}
	return parts[1], seq, true
}

// keepRequestLogSegment 判定清理时一个分段是否保留：被索引引用，或是本进程在快照
// 之后才登记的分段。格式不符的 seg-* 文件不是本功能写的，按孤儿处理。
func keepRequestLogSegment(rel, name string, referenced map[string]struct{}, snapSeq int64) bool {
	if _, ok := referenced[rel]; ok {
		return true
	}
	procTag, seq, ok := parseRequestLogSegmentName(name)
	return ok && procTag == requestLogProcTag && seq > snapSeq
}

// requestLogRecordRel 编码一条记录在分段中的位置。
func requestLogRecordRel(segRel string, offset int64, length int) string {
	return segRel + "#" + strconv.FormatInt(offset, 10) + "#" + strconv.Itoa(length)
}

// parseRequestLogRecordRel 解析分段记录位置；旧的整文件 rel（不含 #）返回 ok=false。
func parseRequestLogRecordRel(rel string) (segRel string, offset int64, length int, ok bool) {
	i := strings.IndexByte(rel, '#')
	if i <= 0 {
		return "", 0, 0, false
	}
	rest := rel[i+1:]
	j := strings.IndexByte(rest, '#')
	if j < 0 {
		return "", 0, 0, false
	}
	offset, err := strconv.ParseInt(rest[:j], 10, 64)
	if err != nil || offset < 0 {
		return "", 0, 0, false
	}
	n, err := strconv.Atoi(rest[j+1:])
	if err != nil || n <= 0 {
		return "", 0, 0, false
	}
	return rel[:i], offset, n, true
}

// RequestLogWriter 持有一个活动分段，把一批日志追加进去并登记索引。
// 不可并发使用：每个写盘 worker 各持一个。
type RequestLogWriter struct {
	file   *os.File
	root   string
	date   string
	rel    string
	size   int64
	opened time.Time
	buf    []byte
}

func NewRequestLogWriter() *RequestLogWriter { return &RequestLogWriter{} }

// Record 写入一批日志：先落盘、成功后才进索引，保证索引 ⊆ 磁盘。
// 写失败时这一段（≤ requestLogFlushBytes）整体丢弃并换新分段。
func (w *RequestLogWriter) Record(logs []*RequestLog) {
	if !RequestLogStoreReady() {
		return
	}
	entries := make([]requestLogIndexEntry, 0, len(logs))
	for i := 0; i < len(logs); {
		// 先跳过空条目，避免为空批次创建分段。
		for i < len(logs) && logs[i] == nil {
			i++
		}
		if i >= len(logs) {
			break
		}
		if err := w.prepare(time.Now()); err != nil {
			reportRequestLogWriteFailure(err)
			return
		}
		w.buf = w.buf[:0]
		entries = entries[:0]
		for ; i < len(logs) && len(w.buf) < requestLogFlushBytes; i++ {
			log := logs[i]
			if log == nil {
				continue
			}
			if log.CreatedAt == 0 {
				log.CreatedAt = common.GetTimestamp()
			}
			log.Id = int(atomic.AddInt64(&reqLogSeq, 1))
			data, err := common.Marshal(log)
			if err != nil {
				reportRequestLogWriteFailure(err)
				continue
			}
			offset := w.size + int64(len(w.buf))
			w.buf = append(w.buf, data...)
			w.buf = append(w.buf, '\n')
			entries = append(entries, requestLogIndexEntry{
				meta: cloneRequestLogMeta(log),
				rel:  requestLogRecordRel(w.rel, offset, len(data)),
			})
		}
		if len(entries) == 0 {
			continue
		}
		n, err := w.file.Write(w.buf)
		w.size += int64(n)
		if err != nil {
			reportRequestLogWriteFailure(err)
			w.Close()
			continue
		}
		appendRequestLogIndexBatch(entries)
	}
	// 个别超大批次过后不长期占着大缓冲。
	if cap(w.buf) > 4*requestLogFlushBytes {
		w.buf = nil
	}
}

// prepare 按需轮转并保证有一个可写的活动分段。
func (w *RequestLogWriter) prepare(now time.Time) error {
	if w.file != nil && (w.root != requestLogRoot ||
		w.date != now.Format("2006-01-02") ||
		w.size >= requestLogSegmentMaxBytes ||
		now.Sub(w.opened) >= requestLogSegmentMaxAge) {
		w.Close()
	}
	if w.file != nil {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		rel, _ := registerRequestLogSegment(now)
		full := requestLogFullPath(rel)
		// 每次都 MkdirAll：日期目录可能刚被清理协程回收（跨天时）。分段每分钟才开一次，
		// 这点开销可以忽略。
		if err := os.MkdirAll(filepath.Dir(full), requestLogDirPerm); err != nil {
			unregisterRequestLogSegment(rel)
			return err
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, requestLogFilePerm)
		if err != nil {
			unregisterRequestLogSegment(rel)
			lastErr = err
			if os.IsExist(err) || os.IsNotExist(err) {
				continue
			}
			return err
		}
		w.file, w.root, w.date, w.rel, w.size, w.opened = f, requestLogRoot, now.Format("2006-01-02"), rel, 0, now
		return nil
	}
	return fmt.Errorf("cannot open a request log segment: %w", lastErr)
}

// Close 关闭活动分段并解除保护，此后它由清理协程按索引引用判定。
func (w *RequestLogWriter) Close() {
	if w.file == nil {
		return
	}
	if err := w.file.Close(); err != nil {
		reportRequestLogWriteFailure(err)
	}
	unregisterRequestLogSegment(w.rel)
	w.file = nil
}

// readRequestLogRecord 按位置读回一条分段记录。
func readRequestLogRecord(segRel string, offset int64, length int) (*RequestLog, error) {
	f, err := os.Open(requestLogFullPath(segRel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// 先按分段实际大小校验位置再分配：length 来自索引/快照，越界的 length 不能换来
	// 一次同等大小的分配。
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !requestLogRecordWithin(info.Size(), offset, length) {
		return nil, fmt.Errorf("request log record %s#%d#%d out of segment bounds (size %d)", segRel, offset, length, info.Size())
	}
	data := make([]byte, length)
	if _, err = f.ReadAt(data, offset); err != nil {
		return nil, err
	}
	var log RequestLog
	if err = common.Unmarshal(data, &log); err != nil {
		return nil, err
	}
	return &log, nil
}
