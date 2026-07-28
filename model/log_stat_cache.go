package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/samber/hot"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 使用日志列表 / 统计的小时缓存。
//
// 行数与额度都是「按时间可加」的量，因此区间被拆成
//   头残段(实时) + 若干完整整点(缓存) + 当前整点(短 TTL) + 尾段(实时)
// 四段相加。结果与直接扫整段逐位相等，所以总数仍然精确、翻页深度不受影响。
//
// 命中缓存时每次查询只剩两端各 ≤1 小时的扫描；纯历史区间零数据库访问。
//
// 关键不变量：**已完成的整点，其行数与额度不再变化**。因此缓存不需要任何
// 失效逻辑。当前整点单独用短 TTL，保证新写入的日志很快可见。
//
// 只加速「无筛选（类型除外）」的查询——带 username/channel 等筛选时本来就能
// 走各自的索引，且为任意筛选组合预热等于要建维度立方体。详见
// docs/design/usage-log-list-performance.md §6.5。
// ---------------------------------------------------------------------------

const (
	logStatCacheNamespace = "logstat"
	// logStatAllFingerprint 是「无筛选」的指纹。留出这一段，
	// 将来若支持按筛选组合预热，新键不会与既有键冲突。
	logStatAllFingerprint = "all"

	logStatSecondsPerHour = int64(3600)
)

// logStatCacheVersion 是口径版本号。改变分桶或过滤语义时递增，
// 一次性作废全部旧缓存，无需逐键清理。
// 声明为 var 而非 const：测试借此取得互不干扰的键空间。
var logStatCacheVersion = "v1"

var (
	logStatCacheOnce sync.Once
	logStatCacheInst *cachex.HybridCache[logHourStat]
	logStatCacheMu   sync.Mutex
)

// logHourStat 是一个时间段内所有「按时间可加」的聚合量。
// 字段名在 JSON 里刻意压到一个字母：Redis 里每个整点一个键，键多了体积敏感。
type logHourStat struct {
	CountAll    int64         `json:"a"`
	CountByType map[int]int64 `json:"c"`
	Quota       int64         `json:"q"`
	Tokens      int64         `json:"t"`
}

// addFrom 把另一段的聚合量并进来。可加性是整个方案的前提。
func (s *logHourStat) addFrom(o logHourStat) {
	s.CountAll += o.CountAll
	s.Quota += o.Quota
	s.Tokens += o.Tokens
	if len(o.CountByType) == 0 {
		return
	}
	if s.CountByType == nil {
		s.CountByType = make(map[int]int64, len(o.CountByType))
	}
	for t, c := range o.CountByType {
		s.CountByType[t] += c
	}
}

// countFor 返回指定类型的行数。LogTypeUnknown 的语义是「不加类型过滤」，
// 因此取全量而不是 CountByType[0]。
func (s logHourStat) countFor(logType int) int64 {
	if logType == LogTypeUnknown {
		return s.CountAll
	}
	return s.CountByType[logType]
}

// logStatCountedTypes 是会被单独计数的日志类型。
// 与 model/log.go 的 LogType* 常量保持一致；新增类型时必须同步，
// 否则按该类型筛选的列表总数会恒为 0。
var logStatCountedTypes = []int{
	LogTypeTopup, LogTypeConsume, LogTypeManage,
	LogTypeSystem, LogTypeError, LogTypeRefund, LogTypeLogin,
}

func floorHour(ts int64) int64 {
	// Go 的 % 对负数返回负余数，直接减会把对齐算到错误的一侧。
	return ts - ((ts%logStatSecondsPerHour)+logStatSecondsPerHour)%logStatSecondsPerHour
}

func ceilHour(ts int64) int64 {
	f := floorHour(ts)
	if f == ts {
		return ts
	}
	return f + logStatSecondsPerHour
}

func getLogStatCache() *cachex.HybridCache[logHourStat] {
	logStatCacheMu.Lock()
	defer logStatCacheMu.Unlock()
	logStatCacheOnce.Do(buildLogStatCacheLocked)
	return logStatCacheInst
}

func buildLogStatCacheLocked() {
	s := operation_setting.GetLogQuerySetting()
	capacity := s.GetStatMemoryEntries()
	defaultTTL := s.GetStatCacheTTL()
	logStatCacheInst = cachex.NewHybridCache[logHourStat](cachex.HybridCacheConfig[logHourStat]{
		Namespace: cachex.Namespace(logStatCacheNamespace),
		Redis:     common.RDB,
		RedisEnabled: func() bool {
			return common.RedisEnabled && common.RDB != nil
		},
		RedisCodec: cachex.JSONCodec[logHourStat]{},
		Memory: func() *hot.HotCache[string, logHourStat] {
			// 无 Redis 时的单机退化路径。容量在首次使用时固定，
			// 之后改配置需重启——这是可接受的，因为它只是降级形态。
			//
			// WithTTL 必须先于 WithJanitor：janitor 拿默认 TTL 当清理间隔，
			// 缺省为 0 时 time.NewTicker 会直接 panic。每个键实际的过期时间
			// 仍由 SetWithTTL 单独指定，这里的默认值只用于喂 janitor。
			return hot.NewHotCache[string, logHourStat](hot.LRU, capacity).
				WithTTL(defaultTTL).
				WithJanitor().
				Build()
		},
	})
}

// resetLogStatCache 丢弃当前缓存实例，下次使用时按最新的 Redis / 容量配置重建。
// 供测试隔离键空间，以及 Redis 连接切换后重新绑定。
func resetLogStatCache() {
	logStatCacheMu.Lock()
	defer logStatCacheMu.Unlock()
	logStatCacheOnce = sync.Once{}
	logStatCacheInst = nil
}

func logStatCacheKey(kind string, hour int64) string {
	return fmt.Sprintf("%s:%s:%d:%s", kind, logStatCacheVersion, hour, logStatAllFingerprint)
}

// logStatCacheUsable 判断一次查询能否走小时缓存分解。
// 任何一条不满足都回落到原来的整段查询——行为与改造前一致，不劣化。
func logStatCacheUsable(startTimestamp, endTimestamp int64, hasNonTypeFilter bool) bool {
	if hasNonTypeFilter {
		return false
	}
	// Rule 8.3：不查无界时间范围。缺任一端都直接回落。
	if startTimestamp <= 0 || endTimestamp <= 0 || startTimestamp > endTimestamp {
		return false
	}
	// ClickHouse 是列存，count/sum 本来就快，拆成多次小查询反而更慢。
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return false
	}
	s := operation_setting.GetLogQuerySetting()
	if !s.StatCacheEnabled {
		return false
	}
	span := endTimestamp - startTimestamp + 1
	if span > int64(s.GetStatMaxCachedHours())*logStatSecondsPerHour {
		return false
	}
	return true
}

// logStatAggRow 承接一次聚合查询的结果。
type logStatAggRow struct {
	CountAll int64 `gorm:"column:count_all"`
	C1       int64 `gorm:"column:c1"`
	C2       int64 `gorm:"column:c2"`
	C3       int64 `gorm:"column:c3"`
	C4       int64 `gorm:"column:c4"`
	C5       int64 `gorm:"column:c5"`
	C6       int64 `gorm:"column:c6"`
	C7       int64 `gorm:"column:c7"`
	Quota    int64 `gorm:"column:quota"`
	Tokens   int64 `gorm:"column:tokens"`
}

// logStatAggSelect 用 SUM(CASE WHEN ...) 而不是 PostgreSQL 专有的
// COUNT(*) FILTER，以便同一条 SQL 在 SQLite / MySQL / PostgreSQL 上都能跑（Rule 2）。
// 一次扫描同时产出列表所需的分类型行数和统计所需的额度 / token。
const logStatAggSelect = `COUNT(*) AS count_all,
COALESCE(SUM(CASE WHEN type = 1 THEN 1 ELSE 0 END), 0) AS c1,
COALESCE(SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END), 0) AS c2,
COALESCE(SUM(CASE WHEN type = 3 THEN 1 ELSE 0 END), 0) AS c3,
COALESCE(SUM(CASE WHEN type = 4 THEN 1 ELSE 0 END), 0) AS c4,
COALESCE(SUM(CASE WHEN type = 5 THEN 1 ELSE 0 END), 0) AS c5,
COALESCE(SUM(CASE WHEN type = 6 THEN 1 ELSE 0 END), 0) AS c6,
COALESCE(SUM(CASE WHEN type = 7 THEN 1 ELSE 0 END), 0) AS c7,
COALESCE(SUM(CASE WHEN type = 2 THEN quota ELSE 0 END), 0) AS quota,
COALESCE(SUM(CASE WHEN type = 2 THEN prompt_tokens + completion_tokens ELSE 0 END), 0) AS tokens`

// queryLogHourStatRange 直接查 logs，聚合 [from, to] 闭区间。
// 闭区间是为了与既有查询的 created_at >= ? AND created_at <= ? 语义完全一致。
func queryLogHourStatRange(ctx context.Context, from, to int64) (logHourStat, error) {
	var out logHourStat
	if from > to {
		return out, nil
	}
	if LOG_DB == nil {
		return out, errors.New("log database is not initialized")
	}
	var row logStatAggRow
	err := LOG_DB.WithContext(ctx).
		Table("logs").
		Select(logStatAggSelect).
		Where("created_at >= ? AND created_at <= ?", from, to).
		Scan(&row).Error
	if err != nil {
		return out, err
	}
	out = logHourStat{
		CountAll: row.CountAll,
		Quota:    row.Quota,
		Tokens:   row.Tokens,
		CountByType: map[int]int64{
			LogTypeTopup:   row.C1,
			LogTypeConsume: row.C2,
			LogTypeManage:  row.C3,
			LogTypeSystem:  row.C4,
			LogTypeError:   row.C5,
			LogTypeRefund:  row.C6,
			LogTypeLogin:   row.C7,
		},
	}
	return out, nil
}

// logStatHourCached 取单个整点 [hour, hour+3599] 的聚合量，优先走缓存。
// 缓存读写的任何失败都只降级回源，绝不把错误抛给调用方（Rule 0）。
func logStatHourCached(ctx context.Context, hour int64, kind string, ttl time.Duration) (logHourStat, error) {
	cache := getLogStatCache()
	key := logStatCacheKey(kind, hour)

	if cached, found, err := cache.Get(key); err != nil {
		common.SysLog("log stat cache read failed, falling back to logs: " + err.Error())
	} else if found {
		return cached, nil
	}

	value, err := queryLogHourStatRange(ctx, hour, hour+logStatSecondsPerHour-1)
	if err != nil {
		return logHourStat{}, err
	}
	if ttl > 0 {
		if err := cache.SetWithTTL(key, value, ttl); err != nil {
			common.SysLog("log stat cache write failed: " + err.Error())
		}
	}
	return value, nil
}

// SumLogStatRange 返回 [startTimestamp, endTimestamp] 闭区间内的可加聚合量。
// 调用前必须先用 logStatCacheUsable 判定；不满足时调用方应走原查询。
func SumLogStatRange(ctx context.Context, startTimestamp, endTimestamp int64) (logHourStat, error) {
	return sumLogStatRangeAt(ctx, startTimestamp, endTimestamp, time.Now().Unix())
}

// sumLogStatRangeAt 是 SumLogStatRange 的可注入时钟版本，供测试把「当前整点」
// 放到确定的位置。区间被切成互不重叠的三段依次推进游标：
//
//	[start, ceilHour(start))      头残段    实时查，≤1 小时
//	[.., ..) 逐个完整整点          主力      长 TTL 缓存
//	[curHour, curHour+3600)       当前整点  短 TTL 缓存（TTL=0 时并入尾段）
//	[cursor, end]                 尾段      实时查，≤1 小时
func sumLogStatRangeAt(ctx context.Context, startTimestamp, endTimestamp, now int64) (logHourStat, error) {
	var total logHourStat
	if startTimestamp > endTimestamp {
		return total, nil
	}

	s := operation_setting.GetLogQuerySetting()
	longTTL := s.GetStatCacheTTL()
	currentTTL := s.GetCurrentHourTTL()
	currentHour := floorHour(now)
	// 闭区间 [start, end] 的右开边界，用它判断某个整点是否完整落在区间内。
	limit := endTimestamp + 1
	cursor := startTimestamp

	// 1. 头残段：把游标推到整点边界上。
	if headEnd := min64(ceilHour(cursor), limit); cursor < headEnd {
		seg, err := queryLogHourStatRange(ctx, cursor, headEnd-1)
		if err != nil {
			return total, err
		}
		total.addFrom(seg)
		cursor = headEnd
	}

	// 2. 已完成的整点：整点完整落在区间内（cursor+3600 <= limit）
	//    且已经走完（cursor+3600 <= now）。
	for cursor+logStatSecondsPerHour <= limit && cursor+logStatSecondsPerHour <= now {
		seg, err := logStatHourCached(ctx, cursor, "h", longTTL)
		if err != nil {
			return total, err
		}
		total.addFrom(seg)
		cursor += logStatSecondsPerHour
	}

	// 3. 当前整点：只有整点被区间完整覆盖时才能复用整点缓存值。
	//    TTL 为 0 表示运营选择了「当前整点永远实时」，此时跳过，由尾段实时查。
	if currentTTL > 0 && cursor == currentHour && cursor+logStatSecondsPerHour <= limit {
		seg, err := logStatHourCached(ctx, cursor, "cur", currentTTL)
		if err != nil {
			return total, err
		}
		total.addFrom(seg)
		cursor += logStatSecondsPerHour
	}

	// 4. 尾段。
	if cursor <= endTimestamp {
		seg, err := queryLogHourStatRange(ctx, cursor, endTimestamp)
		if err != nil {
			return total, err
		}
		total.addFrom(seg)
	}

	return total, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// logStatQueryContext 给一次聚合查询套上超时。
// 宁可给用户一个「请缩小范围」的提示，也不要让一条查询占住 LOG_DB 连接十几秒——
// 连接被长时间占住才是这次改造真正要解决的问题。
func logStatQueryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(),
		operation_setting.GetLogQuerySetting().GetQueryTimeout())
}

// countLogsWithHourCache 返回列表总数：条件允许时走小时缓存分解，否则回落原 COUNT。
// fallback 是惰性的——走缓存时不去构造那条查询，免得在 tx 上留下多余的语句状态。
func countLogsWithHourCache(logType int, startTimestamp, endTimestamp int64,
	hasNonTypeFilter bool, fallback func() *gorm.DB) (int64, error) {
	if logStatCacheUsable(startTimestamp, endTimestamp, hasNonTypeFilter) {
		ctx, cancel := logStatQueryContext()
		defer cancel()
		if stat, err := SumLogStatRange(ctx, startTimestamp, endTimestamp); err == nil {
			return stat.countFor(logType), nil
		} else {
			// 缓存路径失败不该让页面报错，退回原查询即可（Rule 0）。
			common.SysLog("log list count via hour cache failed, falling back to logs: " + err.Error())
		}
	}
	// 回落路径恰恰是最慢的那条（带筛选、无法走缓存），所以它更需要超时兜底。
	// 默认 30s 相对实测最坏值（无筛选 COUNT 约 6s）留了足够余量，只用来掐住
	// 真正失控的查询，不会误杀正常请求。
	ctx, cancel := logStatQueryContext()
	defer cancel()
	var total int64
	err := fallback().WithContext(ctx).Count(&total).Error
	return total, err
}

// sumConsumeQuotaWithHourCache 返回区间内的消费额度。
// 与 countLogsWithHourCache 同构：条件允许走缓存，否则回落调用方给的原查询。
func sumConsumeQuotaWithHourCache(startTimestamp, endTimestamp int64,
	hasNonTypeFilter bool) (quota int64, ok bool) {
	if !logStatCacheUsable(startTimestamp, endTimestamp, hasNonTypeFilter) {
		return 0, false
	}
	ctx, cancel := logStatQueryContext()
	defer cancel()
	stat, err := SumLogStatRange(ctx, startTimestamp, endTimestamp)
	if err != nil {
		common.SysLog("log stat quota via hour cache failed, falling back to logs: " + err.Error())
		return 0, false
	}
	return stat.Quota, true
}
