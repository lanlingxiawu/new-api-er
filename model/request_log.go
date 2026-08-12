package model

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

// RequestLog 记录中转(relay)请求的下游请求体/请求头 以及 返回给下游的返回头/返回体。
// 仅供超级管理员排查使用，写入受 common.RequestLogEnabled / RequestLogUsername 控制。
//
// 存储分两层：列表所需的元字段常驻进程内存（本文件的索引），完整正文写本机磁盘
// （request_log_store.go）。不写数据库，也不再写 Redis —— Redis 只在进程首尾各用
// 一次做索引快照（request_log_snapshot.go）。
type RequestLog struct {
	Id         int    `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	UserId     int    `json:"user_id"`
	Username   string `json:"username"`
	TokenName  string `json:"token_name"`
	ModelName  string `json:"model_name"`
	ChannelId  int    `json:"channel_id"`
	Method     string `json:"method"`
	Url        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Ip         string `json:"ip"`
	RequestId  string `json:"request_id"`
	// UseTimeMs 是中间件测得的端到端耗时（毫秒）。单位与消费日志 logs.use_time（秒）
	// 不同，因此字段名带 _ms 后缀，避免两边混用。
	UseTimeMs        int64 `json:"use_time_ms"`
	IsStream         bool  `json:"is_stream"`
	RequestBodySize  int64 `json:"request_body_size"`
	ResponseBodySize int64 `json:"response_body_size"`
	// 大字段仅在详情接口返回
	RequestHeaders  string `json:"request_headers,omitempty"`
	RequestBody     string `json:"request_body,omitempty"`
	ResponseHeaders string `json:"response_headers,omitempty"`
	ResponseBody    string `json:"response_body,omitempty"`
}

// 清理阈值的兜底默认值，当配置错误时退化使用，避免清理被静默跳过导致无限增长。
const (
	defaultRequestLogMinCount = 1000
	defaultRequestLogMaxCount = 5000
)

// effectiveRequestLogLimits 归一化清理阈值，保证 0 <= minCount < maxCount 恒成立。
// 即使配置出现 maxCount<=0 或 minCount>=maxCount 等错配，也始终返回可用于清理的安全值，
// 从而杜绝"配置错误 -> 永不清理 -> 无限增长"。
func effectiveRequestLogLimits() (maxCount int, minCount int) {
	maxCount = common.RequestLogMaxCount
	if maxCount <= 0 {
		maxCount = defaultRequestLogMaxCount
	}
	minCount = common.RequestLogMinCount
	if minCount < 0 {
		minCount = 0
	}
	if minCount >= maxCount {
		// 错配时退化为保留一半，至少保证清理动作仍会发生
		minCount = maxCount / 2
	}
	return maxCount, minCount
}

var errRequestLogNotFound = errors.New("request log not found")

// requestLogIndexEntry 是内存索引的一条：去掉大字段的元数据 + 正文文件的相对路径。
//
// 存 rel 而不是每次由 created_at/request_id 重新推导，是为了让"索引 ↔ 文件"的对应
// 关系只有一处真值；净化规则或目录分层将来调整也不会让老条目失联。
type requestLogIndexEntry struct {
	meta *RequestLog
	rel  string
}

var (
	reqLogMu    sync.Mutex
	reqLogItems []requestLogIndexEntry // 尾部为最新
	reqLogSeq   int64

	requestLogWriteFailures atomic.Int64
)

// cloneRequestLogMeta 返回去除大字段的浅拷贝，用于列表展示。
func cloneRequestLogMeta(log *RequestLog) *RequestLog {
	m := *log
	m.RequestHeaders = ""
	m.RequestBody = ""
	m.ResponseHeaders = ""
	m.ResponseBody = ""
	return &m
}

// RecordRequestLog 落盘一条请求日志并登记索引，按 min/max 阈值触发索引淘汰。
// 由写盘 worker 调用，绝不能在 relay goroutine 上执行。
//
// 顺序是"先写文件、成功后才进索引"：这保证索引 ⊆ 磁盘，详情接口永远不会拿到指向
// 缺失文件的条目；反过来则会让清理协程在两步之间把刚写的文件当孤儿删掉。
func RecordRequestLog(log *RequestLog) {
	if log == nil {
		return
	}
	if log.CreatedAt == 0 {
		log.CreatedAt = common.GetTimestamp()
	}
	if !RequestLogStoreReady() {
		return
	}
	id := atomic.AddInt64(&reqLogSeq, 1)
	log.Id = int(id)
	rel := requestLogRelPath(log.CreatedAt, log.RequestId, id)
	if err := writeRequestLogFile(rel, log); err != nil {
		reportRequestLogWriteFailure(err)
		return
	}
	appendRequestLogIndex(cloneRequestLogMeta(log), rel)
}

// reportRequestLogWriteFailure 限流打印写盘失败，避免磁盘故障时刷屏。
func reportRequestLogWriteFailure(err error) {
	if n := requestLogWriteFailures.Add(1); n == 1 || n%1000 == 0 {
		common.SysError(fmt.Sprintf("request log write failed (total=%d): %s", n, err.Error()))
	}
}

func appendRequestLogIndex(meta *RequestLog, rel string) {
	maxCount, minCount := effectiveRequestLogLimits()
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	reqLogItems = append(reqLogItems, requestLogIndexEntry{meta: meta, rel: rel})
	if len(reqLogItems) > maxCount {
		trimRequestLogIndexLocked(minCount)
	}
}

// trimRequestLogIndexLocked 只丢弃索引，不删除磁盘文件——被淘汰条目的正文由清理
// 协程统一回收，写入路径因此不含任何删除 IO。
func trimRequestLogIndexLocked(minCount int) {
	if minCount <= 0 {
		reqLogItems = nil
		return
	}
	if len(reqLogItems) <= minCount {
		return
	}
	drop := len(reqLogItems) - minCount
	copy(reqLogItems, reqLogItems[drop:])
	// 清掉尾部残留引用，否则被淘汰条目的 meta 会被底层数组一直持有而无法回收。
	for i := minCount; i < len(reqLogItems); i++ {
		reqLogItems[i] = requestLogIndexEntry{}
	}
	reqLogItems = reqLogItems[:minCount]
}

// requestLogIndexSnapshot 持锁复制一份条目切片（只复制指针与字符串头），
// 让过滤、分页、清理比对都在锁外进行。
func requestLogIndexSnapshot() []requestLogIndexEntry {
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	out := make([]requestLogIndexEntry, len(reqLogItems))
	copy(out, reqLogItems)
	return out
}

func matchRequestLog(log *RequestLog, username, modelName string, channel int, requestId string, statusCode int, startTimestamp, endTimestamp int64) bool {
	if username != "" && log.Username != username {
		return false
	}
	if modelName != "" && log.ModelName != modelName {
		return false
	}
	if channel != 0 && log.ChannelId != channel {
		return false
	}
	if requestId != "" && log.RequestId != requestId {
		return false
	}
	if statusCode != 0 && log.StatusCode != statusCode {
		return false
	}
	if startTimestamp != 0 && log.CreatedAt < startTimestamp {
		return false
	}
	if endTimestamp != 0 && log.CreatedAt > endTimestamp {
		return false
	}
	return true
}

// GetAllRequestLogs 分页查询请求日志（按创建时间从新到旧）。全程内存操作，无 IO。
func GetAllRequestLogs(username string, modelName string, channel int, requestId string, statusCode int,
	startTimestamp int64, endTimestamp int64, startIdx int, num int) (logs []*RequestLog, total int64, err error) {
	logs = make([]*RequestLog, 0, requestLogPageCapacity(num))
	pageStart, pageEnd := startIdx, startIdx+num
	wantsPage := startIdx >= 0 && num > 0

	// 持锁过滤并只物化当前页，而不是先整表复制再切片：复制会在每次翻页产生一次
	// 与索引等大的分配，而 total 又要求必须走完全表。这把锁只和写盘 worker 竞争，
	// relay goroutine 从不参与，扫描期间短暂持有可以接受。
	matched := 0
	reqLogMu.Lock()
	// 索引尾部为最新，倒序遍历即得到"最新在前"。
	for i := len(reqLogItems) - 1; i >= 0; i-- {
		meta := reqLogItems[i].meta
		if !matchRequestLog(meta, username, modelName, channel, requestId, statusCode, startTimestamp, endTimestamp) {
			continue
		}
		if wantsPage && matched >= pageStart && matched < pageEnd {
			logs = append(logs, meta)
		}
		matched++
	}
	reqLogMu.Unlock()

	return logs, int64(matched), nil
}

// requestLogPageCapacity 预分配一页的容量，同时挡住异常大的 num 造成的一次性大分配。
func requestLogPageCapacity(num int) int {
	const maxPrealloc = 1000
	if num <= 0 {
		return 0
	}
	if num > maxPrealloc {
		return maxPrealloc
	}
	return num
}

// GetRequestLogById 获取单条请求日志（含完整请求/返回体），从磁盘文件读取。
func GetRequestLogById(id int) (*RequestLog, error) {
	var rel string
	reqLogMu.Lock()
	for i := len(reqLogItems) - 1; i >= 0; i-- {
		if reqLogItems[i].meta.Id == id {
			rel = reqLogItems[i].rel
			break
		}
	}
	reqLogMu.Unlock()

	if rel == "" {
		return nil, errRequestLogNotFound
	}
	log, err := readRequestLogFile(rel)
	if err != nil {
		// 文件被外部删除属于正常淘汰结果，不必刷日志；其余（权限、损坏）需要留痕。
		if !os.IsNotExist(err) {
			common.SysError("failed to read request log body " + rel + ": " + err.Error())
		}
		return nil, errRequestLogNotFound
	}
	return log, nil
}

// DeleteOldRequestLog 删除 targetTimestamp 之前的请求日志索引，返回删除条数。
// 磁盘正文由后台定时扫描统一回收。
func DeleteOldRequestLog(targetTimestamp int64) (int64, error) {
	reqLogMu.Lock()
	kept := make([]requestLogIndexEntry, 0, len(reqLogItems))
	var deleted int64
	for _, entry := range reqLogItems {
		if entry.meta.CreatedAt < targetTimestamp {
			deleted++
			continue
		}
		kept = append(kept, entry)
	}
	reqLogItems = kept
	reqLogMu.Unlock()

	return deleted, nil
}

// ClearAllRequestLogs 清空请求日志索引，返回清除的条目数（不是文件数）。
// 正文文件由清理协程异步回收。
func ClearAllRequestLogs() (int64, error) {
	reqLogMu.Lock()
	cleared := int64(len(reqLogItems))
	reqLogItems = nil
	reqLogMu.Unlock()

	return cleared, nil
}
