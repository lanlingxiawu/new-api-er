package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

const (
	ConsumptionCostLedgerTagReversal    = "reversal"
	ConsumptionCostLedgerTagLoss        = "loss"
	ConsumptionCostLedgerTagProfit      = "profit"
	ConsumptionCostLedgerTagZeroRevenue = "zero_revenue"

	ConsumptionCostLedgerStatsStatusReady       = "ready"
	ConsumptionCostLedgerStatsStatusPending     = "pending"
	ConsumptionCostLedgerStatsStatusUnsupported = "unsupported"
)

const (
	consumptionCostLedgerAsyncStatsLiveCacheTTL    = time.Minute
	consumptionCostLedgerAsyncStatsHistoryCacheTTL = 5 * time.Minute
	consumptionCostLedgerAsyncStatsLockTTL         = 6 * time.Minute
	consumptionCostLedgerAsyncStatsTimeout         = 5 * time.Minute
	consumptionCostLedgerAsyncStatsMaxRunning      = 2
	consumptionCostLedgerStatsSliceSeconds = int64(3600)
)

type ConsumptionCostLedgerCommonFilter struct {
	Id        int    `json:"id,omitempty"`
	LogId     int    `json:"log_id,omitempty"`
	UserId    int    `json:"user_id,omitempty"`
	ChannelId int    `json:"channel_id,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	Tag       string `json:"tag,omitempty"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

type ConsumptionCostLedgerFilter struct {
	ConsumptionCostLedgerCommonFilter
	CursorCreated int64
	CursorId      int
	Limit         int
}

type ConsumptionCostLedgerItem struct {
	Id           int      `json:"id"`
	LogId        *int     `json:"log_id"`
	UserId       int      `json:"user_id"`
	ChannelId    int      `json:"channel_id"`
	ChannelName  string   `json:"channel_name"`
	GroupName    string   `json:"group_name"`
	ModelName    string   `json:"model_name"`
	RevenueQuota int64    `json:"revenue_quota"`
	CostQuota    int64    `json:"cost_quota"`
	ProfitQuota  int64    `json:"profit_quota"`
	GrossMargin  *float64 `json:"gross_margin"`
	GroupRatio   float64  `json:"group_ratio"`
	CostRatio    float64  `json:"cost_ratio"`
	CreatedAt    int64    `json:"created_at"`
	Tags         []string `json:"tags"`
}

type ConsumptionCostLedgerCursor struct {
	CreatedAt int64 `json:"created_at"`
	Id        int   `json:"id"`
}

type ConsumptionCostLedgerPage struct {
	Items      []ConsumptionCostLedgerItem  `json:"items"`
	HasMore    bool                         `json:"has_more"`
	NextCursor *ConsumptionCostLedgerCursor `json:"next_cursor"`
}

type ConsumptionCostLedgerStatsFilter struct {
	ConsumptionCostLedgerCommonFilter
}

type ConsumptionCostLedgerStats struct {
	RecordCount       int64    `json:"record_count"`
	TotalRevenueQuota int64    `json:"total_revenue_quota"`
	TotalCostQuota    int64    `json:"total_cost_quota"`
	TotalProfitQuota  int64    `json:"total_profit_quota"`
	GrossMargin       *float64 `json:"gross_margin"`
}

type ConsumptionCostLedgerStatsResult struct {
	Stats             *ConsumptionCostLedgerStats `json:"stats,omitempty"`
	StatsStatus       string                      `json:"stats_status"`
	StatsRunningCount int                         `json:"stats_running_count"`
	StatsRunningLimit int                         `json:"stats_running_limit"`
}

type consumptionCostLedgerAsyncStatsCacheEntry struct {
	Stats     ConsumptionCostLedgerStats `json:"stats"`
	ExpiresAt int64                      `json:"expires_at"`
}

func ValidConsumptionCostLedgerTag(tag string) bool {
	switch tag {
	case "", ConsumptionCostLedgerTagReversal, ConsumptionCostLedgerTagLoss, ConsumptionCostLedgerTagProfit, ConsumptionCostLedgerTagZeroRevenue:
		return true
	default:
		return false
	}
}

// FillConsumptionCostLedgerChannelNames 解析并回填明细行的 ChannelName。
// 名称解析仅经 channels 表 + 日聚合快照（ResolveChannelDisplayNamesWithoutLogs，不扫描 logs）；
// 解析不到的渠道保持原值不变。
//
// 交互式单页查询传 nil cache 即可；导出按页调用时应传一个跨页复用的 map：
// 同一渠道在成千上万页里反复出现，借助缓存（含未解析的负缓存占位）每个 channel_id 至多解析一次，
// 后续页全部命中内存，几乎不产生额外 DB 查询。
func FillConsumptionCostLedgerChannelNames(items []ConsumptionCostLedgerItem, cache map[int]string) {
	if len(items) == 0 {
		return
	}
	if cache == nil {
		cache = make(map[int]string, 8)
	}
	missing := make([]int, 0)
	for i := range items {
		id := items[i].ChannelId
		if id == 0 {
			continue
		}
		if _, ok := cache[id]; ok {
			continue
		}
		// 先占位空串作为负缓存，避免同页/跨页对同一未解析 id 重复查询
		cache[id] = ""
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		for id, name := range ResolveChannelDisplayNamesWithoutLogs(missing) {
			if name != "" {
				cache[id] = name
			}
		}
	}
	for i := range items {
		if name := cache[items[i].ChannelId]; name != "" {
			items[i].ChannelName = name
		}
	}
}

func BuildConsumptionCostLedgerItem(row ConsumptionCost) ConsumptionCostLedgerItem {
	profit := row.RevenueQuota - row.CostQuota
	var grossMargin *float64
	if row.RevenueQuota != 0 {
		value := float64(profit) / float64(row.RevenueQuota)
		grossMargin = &value
	}
	return ConsumptionCostLedgerItem{
		Id:           row.Id,
		LogId:        row.LogId,
		UserId:       row.UserId,
		ChannelId:    row.ChannelId,
		ChannelName:  row.ChannelName,
		GroupName:    row.GroupName,
		ModelName:    row.ModelName,
		RevenueQuota: row.RevenueQuota,
		CostQuota:    row.CostQuota,
		ProfitQuota:  profit,
		GrossMargin:  grossMargin,
		GroupRatio:   row.GroupRatio,
		CostRatio:    row.CostRatio,
		CreatedAt:    row.CreatedAt,
		Tags:         BuildConsumptionCostLedgerTags(row),
	}
}

func BuildConsumptionCostLedgerTags(row ConsumptionCost) []string {
	profit := row.RevenueQuota - row.CostQuota
	tags := make([]string, 0, 2)
	if row.RevenueQuota == 0 && row.CostQuota == 0 {
		tags = append(tags, ConsumptionCostLedgerTagZeroRevenue)
	} else if profit < 0 {
		tags = append(tags, ConsumptionCostLedgerTagLoss)
	} else if profit > 0 {
		tags = append(tags, ConsumptionCostLedgerTagProfit)
	}
	if row.RevenueQuota < 0 {
		tags = append(tags, ConsumptionCostLedgerTagReversal)
	}
	return tags
}

func matchConsumptionCostLedgerTag(row ConsumptionCost, tag string) bool {
	if tag == "" {
		return true
	}
	for _, rowTag := range BuildConsumptionCostLedgerTags(row) {
		if rowTag == tag {
			return true
		}
	}
	return false
}

func shouldFilterConsumptionCostLedgerTagInApp(tag string) bool {
	return false
}

func applyConsumptionCostLedgerFilters(tx *gorm.DB, filter ConsumptionCostLedgerStatsFilter) (*gorm.DB, error) {
	if filter.Id > 0 {
		tx = tx.Where("id = ?", filter.Id)
	}
	if filter.LogId > 0 {
		tx = tx.Where("log_id = ?", filter.LogId)
	}
	if filter.UserId > 0 {
		tx = tx.Where("user_id = ?", filter.UserId)
	}
	if filter.ChannelId > 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelId)
	}
	if filter.ModelName != "" {
		tx = tx.Where("model_name = ?", filter.ModelName)
	}
	if filter.GroupName != "" {
		tx = tx.Where("group_name = ?", filter.GroupName)
	}
	if filter.StartTime > 0 {
		tx = tx.Where("created_at >= ?", filter.StartTime)
	}
	if filter.EndTime > 0 {
		tx = tx.Where("created_at <= ?", filter.EndTime)
	}
	switch filter.Tag {
	case "":
	case ConsumptionCostLedgerTagReversal:
		tx = tx.Where("revenue_quota < 0")
	case ConsumptionCostLedgerTagLoss:
		tx = tx.Where("revenue_quota < cost_quota")
	case ConsumptionCostLedgerTagProfit:
		tx = tx.Where("revenue_quota > cost_quota")
	case ConsumptionCostLedgerTagZeroRevenue:
		tx = tx.Where("revenue_quota = 0 AND cost_quota = 0")
	default:
		return nil, errors.New("invalid tag")
	}
	return tx, nil
}

func applyConsumptionCostLedgerListFilters(tx *gorm.DB, filter ConsumptionCostLedgerStatsFilter) (*gorm.DB, bool, error) {
	inAppTagFilter := shouldFilterConsumptionCostLedgerTagInApp(filter.Tag)
	if inAppTagFilter {
		filter.Tag = ""
	}
	tx, err := applyConsumptionCostLedgerFilters(tx, filter)
	return tx, inAppTagFilter, err
}

func ListConsumptionCostLedger(ctx context.Context, filter ConsumptionCostLedgerFilter) (ConsumptionCostLedgerPage, error) {
	statsFilter := ConsumptionCostLedgerStatsFilter{
		ConsumptionCostLedgerCommonFilter: filter.ConsumptionCostLedgerCommonFilter,
	}
	statsFilter.ModelName = strings.TrimSpace(statsFilter.ModelName)
	statsFilter.GroupName = strings.TrimSpace(statsFilter.GroupName)
	statsFilter.Tag = strings.TrimSpace(statsFilter.Tag)
	db := DB.WithContext(safeDBContext(ctx))
	tx, inAppTagFilter, err := applyConsumptionCostLedgerListFilters(db.Model(&ConsumptionCost{}), statsFilter)
	if err != nil {
		return ConsumptionCostLedgerPage{}, err
	}
	if filter.CursorCreated > 0 && filter.CursorId > 0 {
		tx = tx.Where("(created_at < ? OR (created_at = ? AND id < ?))", filter.CursorCreated, filter.CursorCreated, filter.CursorId)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if inAppTagFilter {
		return listConsumptionCostLedgerWithInAppTagFilter(tx, statsFilter.Tag, limit)
	}
	var rows []ConsumptionCost
	if err := tx.Order("created_at desc, id desc").Limit(limit + 1).Find(&rows).Error; err != nil {
		return ConsumptionCostLedgerPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]ConsumptionCostLedgerItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, BuildConsumptionCostLedgerItem(row))
	}
	var nextCursor *ConsumptionCostLedgerCursor
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = &ConsumptionCostLedgerCursor{CreatedAt: last.CreatedAt, Id: last.Id}
	}
	return ConsumptionCostLedgerPage{Items: items, HasMore: hasMore, NextCursor: nextCursor}, nil
}

func listConsumptionCostLedgerWithInAppTagFilter(tx *gorm.DB, tag string, limit int) (ConsumptionCostLedgerPage, error) {
	items := make([]ConsumptionCostLedgerItem, 0, limit)
	scanned := 0
	cfg := operation_setting.GetLedgerDetailSetting()
	batchLimit := cfg.GetListScanBatchSize()
	scanRowsPerReq := cfg.GetListScanRowsPerReq()
	if batchLimit < limit+1 {
		batchLimit = limit + 1
	}
	var scanCursorCreated int64
	var scanCursorId int

	for len(items) <= limit && scanned < scanRowsPerReq {
		remainingScan := scanRowsPerReq - scanned
		if remainingScan < batchLimit {
			batchLimit = remainingScan
		}
		var rows []ConsumptionCost
		batchTx := tx.Session(&gorm.Session{})
		if scanCursorCreated > 0 && scanCursorId > 0 {
			batchTx = batchTx.Where("(created_at < ? OR (created_at = ? AND id < ?))", scanCursorCreated, scanCursorCreated, scanCursorId)
		}
		if err := batchTx.Order("created_at desc, id desc").Limit(batchLimit).Find(&rows).Error; err != nil {
			return ConsumptionCostLedgerPage{}, err
		}
		if len(rows) == 0 {
			break
		}
		scanned += len(rows)
		lastScanned := rows[len(rows)-1]
		scanCursorCreated = lastScanned.CreatedAt
		scanCursorId = lastScanned.Id
		for _, row := range rows {
			if !matchConsumptionCostLedgerTag(row, tag) {
				continue
			}
			items = append(items, BuildConsumptionCostLedgerItem(row))
			if len(items) > limit {
				break
			}
		}
		if len(rows) < batchLimit {
			break
		}
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextCursor *ConsumptionCostLedgerCursor
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = &ConsumptionCostLedgerCursor{CreatedAt: last.CreatedAt, Id: last.Id}
	} else if scanned >= scanRowsPerReq && scanCursorCreated > 0 && scanCursorId > 0 {
		hasMore = true
		nextCursor = &ConsumptionCostLedgerCursor{CreatedAt: scanCursorCreated, Id: scanCursorId}
	}
	return ConsumptionCostLedgerPage{Items: items, HasMore: hasMore, NextCursor: nextCursor}, nil
}

func GetConsumptionCostLedgerStats(ctx context.Context, filter ConsumptionCostLedgerStatsFilter) (ConsumptionCostLedgerStatsResult, error) {
	stats, status, err := getConsumptionCostLedgerStats(ctx, filter)
	if err != nil {
		return ConsumptionCostLedgerStatsResult{}, err
	}
	return ConsumptionCostLedgerStatsResult{
		Stats:             stats,
		StatsStatus:       status,
		StatsRunningCount: getConsumptionCostLedgerAsyncStatsRunningCount(),
		StatsRunningLimit: consumptionCostLedgerAsyncStatsMaxRunning,
	}, nil
}

func getConsumptionCostLedgerStats(ctx context.Context, filter ConsumptionCostLedgerStatsFilter) (*ConsumptionCostLedgerStats, string, error) {
	if filter.Id > 0 || filter.LogId > 0 {
		stats, err := AggregateConsumptionCostLedgerStats(ctx, filter)
		if err != nil {
			return nil, "", err
		}
		return stats, ConsumptionCostLedgerStatsStatusReady, nil
	}
	if filter.StartTime <= 0 || filter.EndTime <= filter.StartTime {
		return nil, ConsumptionCostLedgerStatsStatusUnsupported, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		stats, err := AggregateConsumptionCostLedgerStats(ctx, filter)
		if err != nil {
			return nil, "", err
		}
		return stats, ConsumptionCostLedgerStatsStatusReady, nil
	}
	cacheKey := consumptionCostLedgerAsyncStatsCacheKey(filter)
	if stats, ok := getCachedConsumptionCostLedgerAsyncStats(cacheKey); ok {
		return stats, ConsumptionCostLedgerStatsStatusReady, nil
	}
	startConsumptionCostLedgerAsyncStats(cacheKey, filter)
	return nil, ConsumptionCostLedgerStatsStatusPending, nil
}

func consumptionCostLedgerAsyncStatsCacheKey(filter ConsumptionCostLedgerStatsFilter) string {
	normalized := strings.Join([]string{
		fmt.Sprintf("id=%d", filter.Id),
		fmt.Sprintf("log_id=%d", filter.LogId),
		fmt.Sprintf("user_id=%d", filter.UserId),
		fmt.Sprintf("channel_id=%d", filter.ChannelId),
		"model_name=" + strings.TrimSpace(filter.ModelName),
		"group_name=" + strings.TrimSpace(filter.GroupName),
		"tag=" + strings.TrimSpace(filter.Tag),
		fmt.Sprintf("start_time=%d", filter.StartTime),
		fmt.Sprintf("end_time=%d", filter.EndTime),
	}, "|")
	sum := sha256.Sum256([]byte(normalized))
	return "ledger:stats:v2:" + hex.EncodeToString(sum[:])
}

func consumptionCostLedgerAsyncStatsLockKey(cacheKey string) string {
	return "ledger:stats:lock:v2:" + strings.TrimPrefix(cacheKey, "ledger:stats:v2:")
}

func consumptionCostLedgerAsyncStatsRunningKey() string {
	return "ledger:stats:running:v2"
}

func getCachedConsumptionCostLedgerAsyncStats(cacheKey string) (*ConsumptionCostLedgerStats, bool) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, false
	}
	raw, err := common.RedisGet(cacheKey)
	if err != nil || raw == "" {
		return nil, false
	}
	var entry consumptionCostLedgerAsyncStatsCacheEntry
	if err := common.UnmarshalJsonStr(raw, &entry); err != nil {
		common.SysError("getCachedConsumptionCostLedgerAsyncStats: unmarshal failed: " + err.Error())
		return nil, false
	}
	return &entry.Stats, true
}

func startConsumptionCostLedgerAsyncStats(cacheKey string, filter ConsumptionCostLedgerStatsFilter) {
	if !common.RedisEnabled || common.RDB == nil {
		common.SysError("startConsumptionCostLedgerAsyncStats: redis is required for async stats")
		return
	}
	if !acquireConsumptionCostLedgerAsyncStatsLock(cacheKey) {
		return
	}
	if !acquireConsumptionCostLedgerAsyncStatsRunningSlot(cacheKey) {
		releaseConsumptionCostLedgerAsyncStatsLock(cacheKey)
		return
	}
	go func() {
		defer releaseConsumptionCostLedgerAsyncStatsRunningSlot(cacheKey)
		defer releaseConsumptionCostLedgerAsyncStatsLock(cacheKey)
		ctx, cancel := context.WithTimeout(context.Background(), consumptionCostLedgerAsyncStatsTimeout)
		defer cancel()
		stats, err := AggregateConsumptionCostLedgerStats(ctx, filter)
		if err != nil {
			common.SysError("startConsumptionCostLedgerAsyncStats: aggregate failed: " + err.Error())
			return
		}
		setCachedConsumptionCostLedgerAsyncStats(cacheKey, filter, stats)
	}()
}

func acquireConsumptionCostLedgerAsyncStatsLock(cacheKey string) bool {
	lockKey := consumptionCostLedgerAsyncStatsLockKey(cacheKey)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := common.RDB.SetNX(ctx, lockKey, "1", consumptionCostLedgerAsyncStatsLockTTL).Result()
	if err != nil {
		common.SysError("acquireConsumptionCostLedgerAsyncStatsLock: redis SETNX failed: " + err.Error())
		return false
	}
	return ok
}

func acquireConsumptionCostLedgerAsyncStatsRunningSlot(cacheKey string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Now().UnixMilli()
	expiresAt := now + consumptionCostLedgerAsyncStatsLockTTL.Milliseconds()
	script := `
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", ARGV[1])
if redis.call("ZSCORE", KEYS[1], ARGV[2]) then
  redis.call("ZADD", KEYS[1], ARGV[3], ARGV[2])
  redis.call("EXPIRE", KEYS[1], ARGV[4])
  return 1
end
if redis.call("ZCARD", KEYS[1]) >= tonumber(ARGV[5]) then
  redis.call("EXPIRE", KEYS[1], ARGV[4])
  return 0
end
redis.call("ZADD", KEYS[1], ARGV[3], ARGV[2])
redis.call("EXPIRE", KEYS[1], ARGV[4])
return 1
`
	result, err := common.RDB.Eval(
		ctx,
		script,
		[]string{consumptionCostLedgerAsyncStatsRunningKey()},
		now,
		cacheKey,
		expiresAt,
		int(consumptionCostLedgerAsyncStatsLockTTL/time.Second),
		consumptionCostLedgerAsyncStatsMaxRunning,
	).Int()
	if err != nil {
		common.SysError("acquireConsumptionCostLedgerAsyncStatsRunningSlot: redis EVAL failed: " + err.Error())
		return false
	}
	return result == 1
}

func releaseConsumptionCostLedgerAsyncStatsRunningSlot(cacheKey string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.RDB.ZRem(ctx, consumptionCostLedgerAsyncStatsRunningKey(), cacheKey).Err(); err != nil {
		common.SysError("releaseConsumptionCostLedgerAsyncStatsRunningSlot: redis ZREM failed: " + err.Error())
	}
}

func getConsumptionCostLedgerAsyncStatsRunningCount() int {
	if !common.RedisEnabled || common.RDB == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := consumptionCostLedgerAsyncStatsRunningKey()
	now := time.Now().UnixMilli()
	if err := common.RDB.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now, 10)).Err(); err != nil {
		common.SysError("getConsumptionCostLedgerAsyncStatsRunningCount: redis cleanup failed: " + err.Error())
		return 0
	}
	count, err := common.RDB.ZCard(ctx, key).Result()
	if err != nil {
		common.SysError("getConsumptionCostLedgerAsyncStatsRunningCount: redis ZCARD failed: " + err.Error())
		return 0
	}
	return int(count)
}

func releaseConsumptionCostLedgerAsyncStatsLock(cacheKey string) {
	lockKey := consumptionCostLedgerAsyncStatsLockKey(cacheKey)
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.RDB.Del(ctx, lockKey).Err(); err != nil {
		common.SysError("releaseConsumptionCostLedgerAsyncStatsLock: redis DEL failed: " + err.Error())
	}
}

func consumptionCostLedgerAsyncStatsCacheTTL(filter ConsumptionCostLedgerStatsFilter) time.Duration {
	now := time.Now().Unix()
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location()).Unix()
	tomorrowStart := todayStart + int64(24*time.Hour/time.Second)
	containsToday := filter.StartTime < tomorrowStart && filter.EndTime > todayStart
	endsNearNow := filter.EndTime >= now-int64(consumptionCostLedgerAsyncStatsLiveCacheTTL/time.Second)
	if containsToday || endsNearNow {
		return consumptionCostLedgerAsyncStatsLiveCacheTTL
	}
	return consumptionCostLedgerAsyncStatsHistoryCacheTTL
}

func setCachedConsumptionCostLedgerAsyncStats(cacheKey string, filter ConsumptionCostLedgerStatsFilter, stats *ConsumptionCostLedgerStats) {
	if stats == nil {
		return
	}
	ttl := consumptionCostLedgerAsyncStatsCacheTTL(filter)
	entry := consumptionCostLedgerAsyncStatsCacheEntry{
		Stats:     *stats,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	data, err := common.Marshal(entry)
	if err != nil {
		common.SysError("setCachedConsumptionCostLedgerAsyncStats: marshal failed: " + err.Error())
		return
	}
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	if err := common.RedisSet(cacheKey, string(data), ttl); err != nil {
		common.SysError("setCachedConsumptionCostLedgerAsyncStats: redis SET failed: " + err.Error())
	}
}

func AggregateConsumptionCostLedgerStats(ctx context.Context, filter ConsumptionCostLedgerStatsFilter) (*ConsumptionCostLedgerStats, error) {
	filter.ModelName = strings.TrimSpace(filter.ModelName)
	filter.GroupName = strings.TrimSpace(filter.GroupName)
	filter.Tag = strings.TrimSpace(filter.Tag)
	if filter.StartTime > 0 && filter.EndTime > filter.StartTime && filter.EndTime-filter.StartTime > consumptionCostLedgerStatsSliceSeconds {
		return aggregateConsumptionCostLedgerStatsBySlices(ctx, filter)
	}
	return aggregateConsumptionCostLedgerStatsRange(ctx, filter)
}

func aggregateConsumptionCostLedgerStatsBySlices(ctx context.Context, filter ConsumptionCostLedgerStatsFilter) (*ConsumptionCostLedgerStats, error) {
	merged := &ConsumptionCostLedgerStats{}
	for start := filter.StartTime; start < filter.EndTime; start += consumptionCostLedgerStatsSliceSeconds {
		end := start + consumptionCostLedgerStatsSliceSeconds
		if end > filter.EndTime {
			end = filter.EndTime
		}
		sliceFilter := filter
		sliceFilter.StartTime = start
		sliceFilter.EndTime = end
		stats, err := aggregateConsumptionCostLedgerStatsRange(ctx, sliceFilter)
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostLedgerStats(merged, stats)
	}
	finalizeConsumptionCostLedgerStats(merged)
	return merged, nil
}

func mergeConsumptionCostLedgerStats(dst, src *ConsumptionCostLedgerStats) {
	if dst == nil || src == nil {
		return
	}
	dst.RecordCount += src.RecordCount
	dst.TotalRevenueQuota += src.TotalRevenueQuota
	dst.TotalCostQuota += src.TotalCostQuota
	dst.TotalProfitQuota += src.TotalProfitQuota
}

func finalizeConsumptionCostLedgerStats(stats *ConsumptionCostLedgerStats) {
	if stats == nil {
		return
	}
	stats.GrossMargin = nil
	if stats.TotalRevenueQuota != 0 {
		value := float64(stats.TotalProfitQuota) / float64(stats.TotalRevenueQuota)
		stats.GrossMargin = &value
	}
}

func aggregateConsumptionCostLedgerStatsRange(ctx context.Context, filter ConsumptionCostLedgerStatsFilter) (*ConsumptionCostLedgerStats, error) {
	tx, err := applyConsumptionCostLedgerFilters(DB.WithContext(safeDBContext(ctx)).Model(&ConsumptionCost{}), filter)
	if err != nil {
		return nil, err
	}
	var row struct {
		RecordCount       int64
		TotalRevenueQuota int64
		TotalCostQuota    int64
	}
	err = tx.Select(
		"COUNT(*) as record_count, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue_quota, " +
			"COALESCE(SUM(cost_quota),0) as total_cost_quota",
	).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	stats := &ConsumptionCostLedgerStats{
		RecordCount:       row.RecordCount,
		TotalRevenueQuota: row.TotalRevenueQuota,
		TotalCostQuota:    row.TotalCostQuota,
		TotalProfitQuota:  row.TotalRevenueQuota - row.TotalCostQuota,
	}
	finalizeConsumptionCostLedgerStats(stats)
	return stats, nil
}
