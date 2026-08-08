package model

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 请求日志索引的 Redis 快照。
//
// Redis 在本功能里只承担"跨重启保住内存索引"这一件事：进程退出写一个键、启动读完
// 立即删掉。稳态零 Redis 流量——正文在磁盘，列表在内存。
const (
	requestLogSnapshotKey = "request_log:snapshot"
	// TTL 防止进程再没起来时快照永久驻留 Redis。
	requestLogSnapshotTTL = 24 * time.Hour
	requestLogRedisOpTime = 5 * time.Second
)

// 旧版本（正文存 Redis）遗留的键，升级后一次性清理。
const (
	legacyRequestLogSeqKey   = "request_log:seq"
	legacyRequestLogIndexKey = "request_log:index"
	legacyRequestLogMetaKey  = "request_log:meta:"
	legacyRequestLogBodyKey  = "request_log:body:"
)

type requestLogSnapshotEntry struct {
	Meta *RequestLog `json:"meta"`
	Rel  string      `json:"rel"`
}

func requestLogRedisReady() bool {
	return common.RedisEnabled && common.RequestLogRDB != nil
}

// SnapshotRequestLogs 在进程退出前把内存索引整体写入 Redis。
// 失败只记日志：请求日志允许丢失，绝不能拖住关停。
func SnapshotRequestLogs() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("SnapshotRequestLogs: panic recovered: %v", r))
		}
	}()
	if !requestLogRedisReady() {
		return
	}
	snapshot := requestLogIndexSnapshot()
	maxCount, _ := effectiveRequestLogLimits()
	if len(snapshot) > maxCount {
		snapshot = snapshot[len(snapshot)-maxCount:]
	}
	if len(snapshot) == 0 {
		return
	}
	entries := make([]requestLogSnapshotEntry, 0, len(snapshot))
	for _, item := range snapshot {
		entries = append(entries, requestLogSnapshotEntry{Meta: item.meta, Rel: item.rel})
	}
	data, err := common.Marshal(entries)
	if err != nil {
		common.SysError("SnapshotRequestLogs: marshal failed: " + err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestLogRedisOpTime)
	defer cancel()
	if err = common.RequestLogRDB.Set(ctx, requestLogSnapshotKey, string(data), requestLogSnapshotTTL).Err(); err != nil {
		common.SysError("SnapshotRequestLogs: write failed: " + err.Error())
		return
	}
	common.SysLog(fmt.Sprintf("request log index snapshot saved (%d entries)", len(entries)))
}

// RestoreRequestLogs 在启动时读回索引快照并删除该键。
// 必须早于 StartRequestLogSweeper：否则首轮清理会把有效正文当孤儿删掉。
func RestoreRequestLogs() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("RestoreRequestLogs: panic recovered: %v", r))
		}
	}()
	if !requestLogRedisReady() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestLogRedisOpTime)
	defer cancel()

	raw, err := common.RequestLogRDB.Get(ctx, requestLogSnapshotKey).Result()
	if err != nil {
		// 键不存在是常态（首次启动、上次没能写成功），不必打日志。
		return
	}
	// 无论解析结果如何都删掉：快照是一次性的，残留只会在下次启动被重复加载。
	defer common.RequestLogRDB.Del(ctx, requestLogSnapshotKey)

	var entries []requestLogSnapshotEntry
	if err = common.UnmarshalJsonStr(raw, &entries); err != nil {
		common.SysError("RestoreRequestLogs: corrupted snapshot discarded: " + err.Error())
		return
	}
	maxCount, _ := effectiveRequestLogLimits()
	if len(entries) > maxCount {
		entries = entries[len(entries)-maxCount:]
	}

	items := make([]requestLogIndexEntry, 0, len(entries))
	var maxId int64
	for _, entry := range entries {
		if entry.Meta == nil || entry.Rel == "" {
			continue
		}
		// 正文已被清理的条目直接丢弃，避免详情点开是 404。
		if !requestLogFileExists(entry.Rel) {
			continue
		}
		items = append(items, requestLogIndexEntry{meta: entry.Meta, rel: entry.Rel})
		if int64(entry.Meta.Id) > maxId {
			maxId = int64(entry.Meta.Id)
		}
	}

	reqLogMu.Lock()
	reqLogItems = items
	reqLogMu.Unlock()

	// 自增 id 必须接着快照里的最大值继续，否则新日志会与恢复条目撞 id，
	// 详情按 id 查将命中错误的那条。
	if maxId > atomic.LoadInt64(&reqLogSeq) {
		atomic.StoreInt64(&reqLogSeq, maxId)
	}
	common.SysLog(fmt.Sprintf("request log index restored from snapshot (%d/%d entries)", len(items), len(entries)))
}

// StartLegacyRequestLogCleanup 延迟启动遗留键清理，避开启动期各类缓存预热。
func StartLegacyRequestLogCleanup() {
	if !requestLogRedisReady() {
		return
	}
	go func() {
		time.Sleep(30 * time.Second)
		CleanupLegacyRequestLogRedisKeys()
	}()
}

// CleanupLegacyRequestLogRedisKeys 删除旧实现遗留的请求日志键（正文曾经存在 Redis）。
//
// 只有在遗留索引键仍存在时才扫描：SCAN MATCH 是"先遍历后过滤"，在与主库共用的
// 默认配置下会走一遍全键空间，代价不能每次启动都付。清理成功后索引键消失，
// 之后每次启动只剩一次 EXISTS。
func CleanupLegacyRequestLogRedisKeys() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("CleanupLegacyRequestLogRedisKeys: panic recovered: %v", r))
		}
	}()
	if !requestLogRedisReady() {
		return
	}
	ctx := context.Background()
	exists, err := common.RequestLogRDB.Exists(ctx, legacyRequestLogIndexKey, legacyRequestLogSeqKey).Result()
	if err != nil || exists == 0 {
		return
	}
	common.SysLog("cleaning up legacy request log keys in Redis")

	var removed int64
	for _, prefix := range []string{legacyRequestLogMetaKey, legacyRequestLogBodyKey} {
		var cursor uint64
		for {
			keys, next, serr := common.RequestLogRDB.Scan(ctx, cursor, prefix+"*", 500).Result()
			if serr != nil {
				common.SysError("legacy request log cleanup aborted: " + serr.Error())
				return
			}
			if len(keys) > 0 {
				if derr := common.RequestLogRDB.Del(ctx, keys...).Err(); derr == nil {
					removed += int64(len(keys))
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
			// 主动让出，避免一次性把 Redis 单线程占满影响 relay 的缓存读。
			time.Sleep(10 * time.Millisecond)
		}
	}
	// 索引/序号键最后删：中途失败时下次启动还能重试。
	common.RequestLogRDB.Del(ctx, legacyRequestLogIndexKey, legacyRequestLogSeqKey)
	common.SysLog(fmt.Sprintf("legacy request log cleanup done, %d keys removed", removed))
}
