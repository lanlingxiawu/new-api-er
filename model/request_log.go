package model

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// RequestLog 记录中转(relay)请求的下游请求体/请求头 以及 返回给下游的返回头/返回体。
// 仅供超级管理员排查使用，写入受 common.RequestLogEnabled / RequestLogUsername 控制。
// 注意：请求日志不写数据库。优先保存在 Redis；未启用 Redis 时退化为进程内内存存储（重启丢失）。
type RequestLog struct {
	Id               int    `json:"id"`
	CreatedAt        int64  `json:"created_at"`
	UserId           int    `json:"user_id"`
	Username         string `json:"username"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	ChannelId        int    `json:"channel_id"`
	Method           string `json:"method"`
	Url              string `json:"url"`
	StatusCode       int    `json:"status_code"`
	Ip               string `json:"ip"`
	RequestId        string `json:"request_id"`
	UseTime          int    `json:"use_time"`
	IsStream         bool   `json:"is_stream"`
	RequestBodySize  int64  `json:"request_body_size"`
	ResponseBodySize int64  `json:"response_body_size"`
	// 大字段仅在详情接口返回
	RequestHeaders  string `json:"request_headers,omitempty"`
	RequestBody     string `json:"request_body,omitempty"`
	ResponseHeaders string `json:"response_headers,omitempty"`
	ResponseBody    string `json:"response_body,omitempty"`
}

// requestLogBody 拆分出大字段单独存储，使列表查询无需加载请求/返回体。
type requestLogBody struct {
	RequestHeaders  string `json:"request_headers"`
	RequestBody     string `json:"request_body"`
	ResponseHeaders string `json:"response_headers"`
	ResponseBody    string `json:"response_body"`
}

const (
	requestLogSeqKey   = "request_log:seq"
	requestLogIndexKey = "request_log:index"
	requestLogMetaKey  = "request_log:meta:"
	requestLogBodyKey  = "request_log:body:"
)

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

// 内存兜底存储（Redis 未启用时使用），memRequestLogs 头部为最新。
var (
	memRequestLogMu sync.Mutex
	memRequestLogs  []*RequestLog
	memRequestSeq   int
)

func requestLogCtx() context.Context {
	return context.Background()
}

func useRedisForRequestLog() bool {
	return common.RedisEnabled && common.RequestLogRDB != nil
}

// cloneRequestLogMeta 返回去除大字段的浅拷贝，用于列表展示。
func cloneRequestLogMeta(log *RequestLog) *RequestLog {
	m := *log
	m.RequestHeaders = ""
	m.RequestBody = ""
	m.ResponseHeaders = ""
	m.ResponseBody = ""
	return &m
}

// RecordRequestLog 持久化一条请求日志，并按 min/max 阈值触发清理。
// 应在请求结束后异步调用。
func RecordRequestLog(log *RequestLog) {
	if log == nil {
		return
	}
	if log.CreatedAt == 0 {
		log.CreatedAt = common.GetTimestamp()
	}
	if !useRedisForRequestLog() {
		recordRequestLogMemory(log)
		return
	}
	recordRequestLogRedis(log)
}

func recordRequestLogMemory(log *RequestLog) {
	memRequestLogMu.Lock()
	defer memRequestLogMu.Unlock()
	memRequestSeq++
	log.Id = memRequestSeq
	// 头部为最新
	memRequestLogs = append([]*RequestLog{log}, memRequestLogs...)

	maxCount, minCount := effectiveRequestLogLimits()
	if len(memRequestLogs) > maxCount {
		memRequestLogs = memRequestLogs[:minCount]
	}
}

func recordRequestLogRedis(log *RequestLog) {
	ctx := requestLogCtx()
	id, err := common.RequestLogRDB.Incr(ctx, requestLogSeqKey).Result()
	if err != nil {
		common.SysLog("failed to gen request log id: " + err.Error())
		return
	}
	log.Id = int(id)

	body := requestLogBody{
		RequestHeaders:  log.RequestHeaders,
		RequestBody:     log.RequestBody,
		ResponseHeaders: log.ResponseHeaders,
		ResponseBody:    log.ResponseBody,
	}
	meta := cloneRequestLogMeta(log)

	metaStr, err := common.Marshal(meta)
	if err != nil {
		common.SysLog("failed to marshal request log meta: " + err.Error())
		return
	}
	bodyStr, err := common.Marshal(body)
	if err != nil {
		common.SysLog("failed to marshal request log body: " + err.Error())
		return
	}

	idStr := strconv.FormatInt(id, 10)
	pipe := common.RequestLogRDB.Pipeline()
	pipe.Set(ctx, requestLogMetaKey+idStr, string(metaStr), 0)
	pipe.Set(ctx, requestLogBodyKey+idStr, string(bodyStr), 0)
	pipe.LPush(ctx, requestLogIndexKey, idStr)
	if _, err = pipe.Exec(ctx); err != nil {
		common.SysLog("failed to store request log: " + err.Error())
		return
	}

	trimRequestLogsRedis(ctx)
}

// trimRequestLogsRedis 当条数超过最大值时，仅保留最新的最小值条数，并删除被淘汰条目的明细。
func trimRequestLogsRedis(ctx context.Context) {
	maxCount, minCount := effectiveRequestLogLimits()
	total, err := common.RequestLogRDB.LLen(ctx, requestLogIndexKey).Result()
	if err != nil || total <= int64(maxCount) {
		return
	}
	staleIds, err := common.RequestLogRDB.LRange(ctx, requestLogIndexKey, int64(minCount), -1).Result()
	if err != nil {
		return
	}
	pipe := common.RequestLogRDB.Pipeline()
	for _, idStr := range staleIds {
		pipe.Del(ctx, requestLogMetaKey+idStr)
		pipe.Del(ctx, requestLogBodyKey+idStr)
	}
	if minCount == 0 {
		// LTRIM key 0 -1 会保留整个 list，无法清空；minCount==0 时须直接删除索引键，
		// 否则索引会残留全部 id 而其明细键已被删除，形成孤儿索引并无限增长。
		pipe.Del(ctx, requestLogIndexKey)
	} else {
		pipe.LTrim(ctx, requestLogIndexKey, 0, int64(minCount)-1)
	}
	if _, err = pipe.Exec(ctx); err != nil {
		common.SysLog("failed to trim request logs: " + err.Error())
	}
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

func getRequestLogMetaByIds(ctx context.Context, ids []string) []*RequestLog {
	logs := make([]*RequestLog, 0, len(ids))
	if len(ids) == 0 {
		return logs
	}
	keys := make([]string, len(ids))
	for i, idStr := range ids {
		keys[i] = requestLogMetaKey + idStr
	}
	values, err := common.RequestLogRDB.MGet(ctx, keys...).Result()
	if err != nil {
		return logs
	}
	for _, v := range values {
		str, ok := v.(string)
		if !ok || str == "" {
			continue
		}
		var log RequestLog
		if err := common.UnmarshalJsonStr(str, &log); err != nil {
			continue
		}
		logs = append(logs, &log)
	}
	return logs
}

// GetAllRequestLogs 分页查询请求日志（按创建时间从新到旧）。
func GetAllRequestLogs(username string, modelName string, channel int, requestId string, statusCode int,
	startTimestamp int64, endTimestamp int64, startIdx int, num int) (logs []*RequestLog, total int64, err error) {
	if !useRedisForRequestLog() {
		return getAllRequestLogsMemory(username, modelName, channel, requestId, statusCode, startTimestamp, endTimestamp, startIdx, num)
	}
	return getAllRequestLogsRedis(username, modelName, channel, requestId, statusCode, startTimestamp, endTimestamp, startIdx, num)
}

func getAllRequestLogsMemory(username string, modelName string, channel int, requestId string, statusCode int,
	startTimestamp int64, endTimestamp int64, startIdx int, num int) ([]*RequestLog, int64, error) {
	logs := make([]*RequestLog, 0)
	memRequestLogMu.Lock()
	snapshot := make([]*RequestLog, len(memRequestLogs))
	copy(snapshot, memRequestLogs)
	memRequestLogMu.Unlock()

	filtered := make([]*RequestLog, 0, len(snapshot))
	for _, log := range snapshot {
		if matchRequestLog(log, username, modelName, channel, requestId, statusCode, startTimestamp, endTimestamp) {
			filtered = append(filtered, log)
		}
	}
	total := int64(len(filtered))
	if startIdx >= len(filtered) || num <= 0 {
		return logs, total, nil
	}
	end := startIdx + num
	if end > len(filtered) {
		end = len(filtered)
	}
	for _, log := range filtered[startIdx:end] {
		logs = append(logs, cloneRequestLogMeta(log))
	}
	return logs, total, nil
}

func getAllRequestLogsRedis(username string, modelName string, channel int, requestId string, statusCode int,
	startTimestamp int64, endTimestamp int64, startIdx int, num int) ([]*RequestLog, int64, error) {
	logs := make([]*RequestLog, 0)
	ctx := requestLogCtx()

	hasFilter := username != "" || modelName != "" || channel != 0 || requestId != "" ||
		statusCode != 0 || startTimestamp != 0 || endTimestamp != 0

	if !hasFilter {
		// 无过滤：先对 id 分页，再仅加载该页 meta
		total, err := common.RequestLogRDB.LLen(ctx, requestLogIndexKey).Result()
		if err != nil {
			return logs, 0, err
		}
		if int64(startIdx) >= total || num <= 0 {
			return logs, total, nil
		}
		ids, err := common.RequestLogRDB.LRange(ctx, requestLogIndexKey, int64(startIdx), int64(startIdx+num-1)).Result()
		if err != nil {
			return logs, total, err
		}
		return getRequestLogMetaByIds(ctx, ids), total, nil
	}

	// 有过滤：加载全部 meta（条数受 max 限制，规模可控），过滤后分页
	allIds, err := common.RequestLogRDB.LRange(ctx, requestLogIndexKey, 0, -1).Result()
	if err != nil {
		return logs, 0, err
	}
	all := getRequestLogMetaByIds(ctx, allIds)
	filtered := make([]*RequestLog, 0, len(all))
	for _, log := range all {
		if matchRequestLog(log, username, modelName, channel, requestId, statusCode, startTimestamp, endTimestamp) {
			filtered = append(filtered, log)
		}
	}
	total := int64(len(filtered))
	if startIdx >= len(filtered) || num <= 0 {
		return logs, total, nil
	}
	end := startIdx + num
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[startIdx:end], total, nil
}

// GetRequestLogById 获取单条请求日志（含完整请求/返回体）。
func GetRequestLogById(id int) (*RequestLog, error) {
	if !useRedisForRequestLog() {
		memRequestLogMu.Lock()
		defer memRequestLogMu.Unlock()
		for _, log := range memRequestLogs {
			if log.Id == id {
				cp := *log
				return &cp, nil
			}
		}
		return nil, errRequestLogNotFound
	}

	ctx := requestLogCtx()
	idStr := strconv.Itoa(id)
	metaStr, err := common.RequestLogRDB.Get(ctx, requestLogMetaKey+idStr).Result()
	if err != nil {
		return nil, err
	}
	var log RequestLog
	if err := common.UnmarshalJsonStr(metaStr, &log); err != nil {
		return nil, err
	}
	if bodyStr, bErr := common.RequestLogRDB.Get(ctx, requestLogBodyKey+idStr).Result(); bErr == nil {
		var body requestLogBody
		if common.UnmarshalJsonStr(bodyStr, &body) == nil {
			log.RequestHeaders = body.RequestHeaders
			log.RequestBody = body.RequestBody
			log.ResponseHeaders = body.ResponseHeaders
			log.ResponseBody = body.ResponseBody
		}
	}
	return &log, nil
}

// DeleteOldRequestLog 删除 targetTimestamp 之前的请求日志，返回删除条数。
func DeleteOldRequestLog(targetTimestamp int64) (int64, error) {
	if !useRedisForRequestLog() {
		memRequestLogMu.Lock()
		defer memRequestLogMu.Unlock()
		kept := make([]*RequestLog, 0, len(memRequestLogs))
		var deleted int64
		for _, log := range memRequestLogs {
			if log.CreatedAt < targetTimestamp {
				deleted++
				continue
			}
			kept = append(kept, log)
		}
		memRequestLogs = kept
		return deleted, nil
	}

	ctx := requestLogCtx()
	allIds, err := common.RequestLogRDB.LRange(ctx, requestLogIndexKey, 0, -1).Result()
	if err != nil {
		return 0, err
	}
	all := getRequestLogMetaByIds(ctx, allIds)
	var deleted int64
	pipe := common.RequestLogRDB.Pipeline()
	for _, log := range all {
		if log.CreatedAt < targetTimestamp {
			idStr := strconv.Itoa(log.Id)
			pipe.LRem(ctx, requestLogIndexKey, 0, idStr)
			pipe.Del(ctx, requestLogMetaKey+idStr)
			pipe.Del(ctx, requestLogBodyKey+idStr)
			deleted++
		}
	}
	if deleted > 0 {
		if _, err = pipe.Exec(ctx); err != nil {
			return 0, err
		}
	}
	return deleted, nil
}

// ClearAllRequestLogs 清除 Redis（或内存兜底）中存储的全部请求日志，返回清除条数。
// 主路径：RENAME index → 批量删 meta/body（快）；兜底：SCAN 补删孤儿键。
func ClearAllRequestLogs() (int64, error) {
	if !useRedisForRequestLog() {
		memRequestLogMu.Lock()
		defer memRequestLogMu.Unlock()
		cleared := int64(len(memRequestLogs))
		memRequestLogs = nil
		return cleared, nil
	}

	ctx := requestLogCtx()
	const clearBatchSize = 1000
	var cleared int64

	batchDelete := func(keys []string) error {
		pipe := common.RequestLogRDB.Pipeline()
		pending := 0
		for _, key := range keys {
			pipe.Del(ctx, key)
			pending++
			cleared++
			if pending >= clearBatchSize {
				if _, err := pipe.Exec(ctx); err != nil {
					return err
				}
				pipe = common.RequestLogRDB.Pipeline()
				pending = 0
			}
		}
		if pending > 0 {
			_, err := pipe.Exec(ctx)
			return err
		}
		return nil
	}

	// 主路径：RENAME index 原子切断，再按 id 批量删 meta/body。
	// RENAME 失败说明 index 不存在，跳过主路径直接走 SCAN 兜底。
	tmpKey := requestLogIndexKey + ":clearing"
	if err := common.RequestLogRDB.Rename(ctx, requestLogIndexKey, tmpKey).Err(); err == nil {
		ids, err := common.RequestLogRDB.LRange(ctx, tmpKey, 0, -1).Result()
		if err == nil {
			metaKeys := make([]string, len(ids))
			bodyKeys := make([]string, len(ids))
			for i, id := range ids {
				metaKeys[i] = requestLogMetaKey + id
				bodyKeys[i] = requestLogBodyKey + id
			}
			_ = batchDelete(metaKeys)
			_ = batchDelete(bodyKeys)
		}
		common.RequestLogRDB.Del(ctx, tmpKey)
	}

	// 兜底：SCAN 扫出 index 未记录的孤儿键并删除。
	// 正常情况下孤儿极少，SCAN 几乎空转；有残留时才有实际删除操作。
	const scanCount = 100
	scanAndDelete := func(pattern string) error {
		var cursor uint64
		for {
			keys, next, err := common.RequestLogRDB.Scan(ctx, cursor, pattern, scanCount).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := batchDelete(keys); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		return nil
	}

	if err := scanAndDelete(requestLogMetaKey + "*"); err != nil {
		return cleared, err
	}
	if err := scanAndDelete(requestLogBodyKey + "*"); err != nil {
		return cleared, err
	}
	return cleared, nil
}
