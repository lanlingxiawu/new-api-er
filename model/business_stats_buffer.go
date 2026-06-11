package model

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================================
// 日统计缓冲层
//
// 写入侧（每笔消费）不再在事务中直接 upsert 日聚合表，而是将增量累加到缓冲区。
// 后台定时任务将缓冲区的增量批量刷入 DB，减少单条事务的 SQL 数量和锁竞争。
//
// Redis 可用时使用 Redis Hash 作为缓冲（集群安全），否则降级为进程内 map。
// ============================================================================

const (
	// Redis key 前缀
	platformStatBufferPrefix           = "biz_buf:platform:"            // + {statDate}:{channelId}
	commissionStatBufferPrefix         = "biz_buf:commission:"          // + {statDate}:{employeeUserId}
	customerCommissionStatBufferPrefix = "biz_buf:customer_commission:" // + {statDate}:{employeeUserId}:{customerUserId}
	commissionMonthlyStatBufferPrefix  = "biz_buf:commission_monthly:"  // + {periodStartAt}:{employeeUserId}
	redisStatProcessingPrefix          = "biz_buf:processing:"

	// Redis key 的 TTL，防止 flush 失败导致 key 永驻
	statBufferKeyTTL     = 48 * time.Hour
	statBufferMaxRetries = 10

	// 默认刷盘间隔（如果配置未指定或无效）
	DefaultBusinessStatsFlushInterval = 5 // 秒
)

var businessStatsFlushMu sync.Mutex

// ---- Redis 缓冲 ----

func platformStatRedisKey(statDate int64, channelId int) string {
	return fmt.Sprintf("%s%d:%d", platformStatBufferPrefix, statDate, channelId)
}

func commissionStatRedisKey(statDate int64, employeeUserId int) string {
	return fmt.Sprintf("%s%d:%d", commissionStatBufferPrefix, statDate, employeeUserId)
}

func customerCommissionStatRedisKey(statDate int64, employeeUserId, customerUserId int) string {
	return fmt.Sprintf("%s%d:%d:%d", customerCommissionStatBufferPrefix, statDate, employeeUserId, customerUserId)
}

func commissionMonthlyStatRedisKey(periodStartAt int64, employeeUserId int) string {
	return fmt.Sprintf("%s%d:%d", commissionMonthlyStatBufferPrefix, periodStartAt, employeeUserId)
}

func redisStatProcessingKey(key string) string {
	return redisStatProcessingPrefix + key
}

func redisStatOriginalKey(key string) string {
	return strings.TrimPrefix(key, redisStatProcessingPrefix)
}

func prepareRedisStatProcessingKey(ctx context.Context, key string) (string, bool) {
	if strings.HasPrefix(key, redisStatProcessingPrefix) {
		return key, true
	}
	processingKey := redisStatProcessingKey(key)
	renamed, err := common.RDB.RenameNX(ctx, key, processingKey).Result()
	if err != nil {
		common.SysError("prepareRedisStatProcessingKey: rename failed: " + err.Error())
		return "", false
	}
	return processingKey, renamed
}

func ensureRedisStatBatchID(ctx context.Context, key string) string {
	batchID, _ := common.RDB.HGet(ctx, key, "_batch_id").Result()
	if batchID != "" {
		return batchID
	}
	batchID = fmt.Sprintf("%s:%d", key, time.Now().UnixNano())
	if ok, err := common.RDB.HSetNX(ctx, key, "_batch_id", batchID).Result(); err != nil {
		common.SysError("ensureRedisStatBatchID: hsetnx failed: " + err.Error())
		return ""
	} else if ok {
		return batchID
	}
	batchID, _ = common.RDB.HGet(ctx, key, "_batch_id").Result()
	return batchID
}

// bufferPlatformStatRedis 将平台侧日统计增量累加到 Redis Hash。
func bufferPlatformStatRedis(statDate int64, channelId int, channelName string, revenueQuota, costQuota int64, costRatio float64, createdAt int64) {
	key := platformStatRedisKey(statDate, channelId)
	ctx := context.Background()
	pipe := common.RDB.Pipeline()
	if channelName != "" {
		pipe.HSetNX(ctx, key, "channel_name", channelName)
	}
	pipe.HIncrBy(ctx, key, "revenue_quota", revenueQuota)
	pipe.HIncrBy(ctx, key, "cost_quota", costQuota)
	pipe.HIncrBy(ctx, key, "record_count", 1)
	// cost_ratio_sum 用整数存储（乘以 1e9 精度），避免浮点累加误差
	pipe.HIncrBy(ctx, key, "cost_ratio_sum_e9", int64(costRatio*1e9))
	pipe.HIncrBy(ctx, key, "last_created_at", 0) // 确保字段存在
	// 更新 last_created_at（取最大值，用 Lua 脚本）
	luaUpdateMax := `
		local cur = tonumber(redis.call('HGET', KEYS[1], 'last_created_at') or '0')
		if tonumber(ARGV[1]) > cur then
			redis.call('HSET', KEYS[1], 'last_created_at', ARGV[1])
		end
		return 1
	`
	pipe.Eval(ctx, luaUpdateMax, []string{key}, createdAt)
	pipe.Expire(ctx, key, statBufferKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysError("bufferPlatformStatRedis: pipeline error: " + err.Error())
	}
}

// bufferCommissionStatRedis 将员工提成侧日统计增量累加到 Redis Hash。
func bufferCommissionStatRedis(statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := commissionStatRedisKey(statDate, employeeUserId)
	ctx := context.Background()
	pipe := common.RDB.Pipeline()
	pipe.HIncrBy(ctx, key, "revenue_quota", revenueQuota)
	pipe.HIncrBy(ctx, key, "cost_quota", costQuota)
	pipe.HIncrBy(ctx, key, "profit_quota", profitQuota)
	pipe.HIncrBy(ctx, key, "commission_quota", commissionQuota)
	pipe.HIncrBy(ctx, key, "record_count", 1)
	pipe.HIncrBy(ctx, key, "last_created_at", 0)
	luaUpdateMax := `
		local cur = tonumber(redis.call('HGET', KEYS[1], 'last_created_at') or '0')
		if tonumber(ARGV[1]) > cur then
			redis.call('HSET', KEYS[1], 'last_created_at', ARGV[1])
		end
		return 1
	`
	pipe.Eval(ctx, luaUpdateMax, []string{key}, createdAt)
	pipe.Expire(ctx, key, statBufferKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysError("bufferCommissionStatRedis: pipeline error: " + err.Error())
	}
}

func bufferCustomerCommissionStatRedis(statDate int64, employeeUserId, customerUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := customerCommissionStatRedisKey(statDate, employeeUserId, customerUserId)
	ctx := context.Background()
	pipe := common.RDB.Pipeline()
	pipe.HIncrBy(ctx, key, "revenue_quota", revenueQuota)
	pipe.HIncrBy(ctx, key, "cost_quota", costQuota)
	pipe.HIncrBy(ctx, key, "profit_quota", profitQuota)
	pipe.HIncrBy(ctx, key, "commission_quota", commissionQuota)
	pipe.HIncrBy(ctx, key, "record_count", 1)
	pipe.HIncrBy(ctx, key, "last_created_at", 0)
	luaUpdateMax := `
		local cur = tonumber(redis.call('HGET', KEYS[1], 'last_created_at') or '0')
		if tonumber(ARGV[1]) > cur then
			redis.call('HSET', KEYS[1], 'last_created_at', ARGV[1])
		end
		return 1
	`
	pipe.Eval(ctx, luaUpdateMax, []string{key}, createdAt)
	pipe.Expire(ctx, key, statBufferKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysError("bufferCustomerCommissionStatRedis: pipeline error: " + err.Error())
		bufferCustomerCommissionStatMem(statDate, employeeUserId, customerUserId, revenueQuota, costQuota, profitQuota, commissionQuota, createdAt)
	}
}

// ---- 内存缓冲（无 Redis 降级） ----

func bufferCommissionMonthlyStatRedis(period CommissionMonthlyPeriod, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := commissionMonthlyStatRedisKey(period.PeriodStartAt, employeeUserId)
	ctx := context.Background()
	pipe := common.RDB.Pipeline()
	pipe.HSetNX(ctx, key, "period_end_at", period.PeriodEndAt)
	pipe.HSetNX(ctx, key, "period_key", period.PeriodKey)
	pipe.HSetNX(ctx, key, "timezone", period.Timezone)
	pipe.HIncrBy(ctx, key, "revenue_quota", revenueQuota)
	pipe.HIncrBy(ctx, key, "cost_quota", costQuota)
	pipe.HIncrBy(ctx, key, "profit_quota", profitQuota)
	pipe.HIncrBy(ctx, key, "commission_quota", commissionQuota)
	pipe.HIncrBy(ctx, key, "record_count", 1)
	pipe.HIncrBy(ctx, key, "last_created_at", 0)
	luaUpdateMax := `
		local cur = tonumber(redis.call('HGET', KEYS[1], 'last_created_at') or '0')
		if tonumber(ARGV[1]) > cur then
			redis.call('HSET', KEYS[1], 'last_created_at', ARGV[1])
		end
		return 1
	`
	pipe.Eval(ctx, luaUpdateMax, []string{key}, createdAt)
	pipe.Expire(ctx, key, statBufferKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysError("bufferCommissionMonthlyStatRedis: pipeline error: " + err.Error())
		bufferCommissionMonthlyStatMem(period, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, createdAt)
	}
}

type platformStatDelta struct {
	StatDate      int64
	ChannelId     int
	ChannelName   string
	RevenueQuota  int64
	CostQuota     int64
	RecordCount   int64
	CostRatioSum  float64
	LastCreatedAt int64
	RetryCount    int
}

type commissionStatDelta struct {
	StatDate        int64
	EmployeeUserId  int
	RevenueQuota    int64
	CostQuota       int64
	ProfitQuota     int64
	CommissionQuota int64
	RecordCount     int64
	LastCreatedAt   int64
	RetryCount      int
}

type customerCommissionStatDelta struct {
	StatDate        int64
	EmployeeUserId  int
	CustomerUserId  int
	RevenueQuota    int64
	CostQuota       int64
	ProfitQuota     int64
	CommissionQuota int64
	RecordCount     int64
	LastCreatedAt   int64
	RetryCount      int
}

type commissionMonthlyStatDelta struct {
	PeriodStartAt   int64
	PeriodEndAt     int64
	PeriodKey       string
	Timezone        string
	EmployeeUserId  int
	RevenueQuota    int64
	CostQuota       int64
	ProfitQuota     int64
	CommissionQuota int64
	RecordCount     int64
	LastCreatedAt   int64
	RetryCount      int
}

var (
	memPlatformBuf  = make(map[string]*platformStatDelta)
	memPlatformLock sync.Mutex

	memCommissionBuf          = make(map[string]*commissionStatDelta)
	memCommissionLock         sync.Mutex
	memCustomerCommissionBuf  = make(map[string]*customerCommissionStatDelta)
	memCustomerCommissionLock sync.Mutex
	memCommissionMonthlyBuf   = make(map[string]*commissionMonthlyStatDelta)
	memCommissionMonthlyLock  sync.Mutex
)

func memPlatformKey(statDate int64, channelId int) string {
	return fmt.Sprintf("%d:%d", statDate, channelId)
}

func memCommissionKey(statDate int64, employeeUserId int) string {
	return fmt.Sprintf("%d:%d", statDate, employeeUserId)
}

func memCustomerCommissionKey(statDate int64, employeeUserId, customerUserId int) string {
	return fmt.Sprintf("%d:%d:%d", statDate, employeeUserId, customerUserId)
}

func memCommissionMonthlyKey(periodStartAt int64, employeeUserId int) string {
	return fmt.Sprintf("%d:%d", periodStartAt, employeeUserId)
}

func bufferPlatformStatMem(statDate int64, channelId int, channelName string, revenueQuota, costQuota int64, costRatio float64, createdAt int64) {
	key := memPlatformKey(statDate, channelId)
	memPlatformLock.Lock()
	defer memPlatformLock.Unlock()
	d, ok := memPlatformBuf[key]
	if !ok {
		d = &platformStatDelta{StatDate: statDate, ChannelId: channelId}
		memPlatformBuf[key] = d
	}
	d.RevenueQuota += revenueQuota
	d.CostQuota += costQuota
	d.RecordCount++
	d.CostRatioSum += costRatio
	if d.ChannelName == "" {
		d.ChannelName = channelName
	}
	if createdAt > d.LastCreatedAt {
		d.LastCreatedAt = createdAt
	}
}

func bufferCommissionStatMem(statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := memCommissionKey(statDate, employeeUserId)
	memCommissionLock.Lock()
	defer memCommissionLock.Unlock()
	d, ok := memCommissionBuf[key]
	if !ok {
		d = &commissionStatDelta{StatDate: statDate, EmployeeUserId: employeeUserId}
		memCommissionBuf[key] = d
	}
	d.RevenueQuota += revenueQuota
	d.CostQuota += costQuota
	d.ProfitQuota += profitQuota
	d.CommissionQuota += commissionQuota
	d.RecordCount++
	if createdAt > d.LastCreatedAt {
		d.LastCreatedAt = createdAt
	}
}

func bufferCustomerCommissionStatMem(statDate int64, employeeUserId, customerUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := memCustomerCommissionKey(statDate, employeeUserId, customerUserId)
	memCustomerCommissionLock.Lock()
	defer memCustomerCommissionLock.Unlock()
	d, ok := memCustomerCommissionBuf[key]
	if !ok {
		d = &customerCommissionStatDelta{StatDate: statDate, EmployeeUserId: employeeUserId, CustomerUserId: customerUserId}
		memCustomerCommissionBuf[key] = d
	}
	d.RevenueQuota += revenueQuota
	d.CostQuota += costQuota
	d.ProfitQuota += profitQuota
	d.CommissionQuota += commissionQuota
	d.RecordCount++
	if createdAt > d.LastCreatedAt {
		d.LastCreatedAt = createdAt
	}
}

func bufferCommissionMonthlyStatMem(period CommissionMonthlyPeriod, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	key := memCommissionMonthlyKey(period.PeriodStartAt, employeeUserId)
	memCommissionMonthlyLock.Lock()
	defer memCommissionMonthlyLock.Unlock()
	d, ok := memCommissionMonthlyBuf[key]
	if !ok {
		d = &commissionMonthlyStatDelta{
			PeriodStartAt:  period.PeriodStartAt,
			PeriodEndAt:    period.PeriodEndAt,
			PeriodKey:      period.PeriodKey,
			Timezone:       period.Timezone,
			EmployeeUserId: employeeUserId,
		}
		memCommissionMonthlyBuf[key] = d
	}
	d.RevenueQuota += revenueQuota
	d.CostQuota += costQuota
	d.ProfitQuota += profitQuota
	d.CommissionQuota += commissionQuota
	d.RecordCount++
	if createdAt > d.LastCreatedAt {
		d.LastCreatedAt = createdAt
	}
}

func requeuePlatformStatMem(d *platformStatDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("platform_daily_stat", "max retries exceeded", d.RetryCount, d)
		return
	}
	memPlatformLock.Lock()
	defer memPlatformLock.Unlock()
	key := memPlatformKey(d.StatDate, d.ChannelId)
	existing := memPlatformBuf[key]
	if existing == nil {
		copyDelta := *d
		memPlatformBuf[key] = &copyDelta
		return
	}
	existing.RevenueQuota += d.RevenueQuota
	existing.CostQuota += d.CostQuota
	existing.RecordCount += d.RecordCount
	existing.CostRatioSum += d.CostRatioSum
	if existing.ChannelName == "" {
		existing.ChannelName = d.ChannelName
	}
	if d.LastCreatedAt > existing.LastCreatedAt {
		existing.LastCreatedAt = d.LastCreatedAt
	}
	if d.RetryCount > existing.RetryCount {
		existing.RetryCount = d.RetryCount
	}
}

func requeueCommissionStatMem(d *commissionStatDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("commission_daily_stat", "max retries exceeded", d.RetryCount, d)
		return
	}
	memCommissionLock.Lock()
	defer memCommissionLock.Unlock()
	key := memCommissionKey(d.StatDate, d.EmployeeUserId)
	existing := memCommissionBuf[key]
	if existing == nil {
		copyDelta := *d
		memCommissionBuf[key] = &copyDelta
		return
	}
	existing.RevenueQuota += d.RevenueQuota
	existing.CostQuota += d.CostQuota
	existing.ProfitQuota += d.ProfitQuota
	existing.CommissionQuota += d.CommissionQuota
	existing.RecordCount += d.RecordCount
	if d.LastCreatedAt > existing.LastCreatedAt {
		existing.LastCreatedAt = d.LastCreatedAt
	}
	if d.RetryCount > existing.RetryCount {
		existing.RetryCount = d.RetryCount
	}
}

func requeueCustomerCommissionStatMem(d *customerCommissionStatDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("customer_commission_daily_stat", "max retries exceeded", d.RetryCount, d)
		return
	}
	memCustomerCommissionLock.Lock()
	defer memCustomerCommissionLock.Unlock()
	key := memCustomerCommissionKey(d.StatDate, d.EmployeeUserId, d.CustomerUserId)
	existing := memCustomerCommissionBuf[key]
	if existing == nil {
		copyDelta := *d
		memCustomerCommissionBuf[key] = &copyDelta
		return
	}
	existing.RevenueQuota += d.RevenueQuota
	existing.CostQuota += d.CostQuota
	existing.ProfitQuota += d.ProfitQuota
	existing.CommissionQuota += d.CommissionQuota
	existing.RecordCount += d.RecordCount
	if d.LastCreatedAt > existing.LastCreatedAt {
		existing.LastCreatedAt = d.LastCreatedAt
	}
	if d.RetryCount > existing.RetryCount {
		existing.RetryCount = d.RetryCount
	}
}

func requeueCommissionMonthlyStatMem(d *commissionMonthlyStatDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("commission_monthly_stat", "max retries exceeded", d.RetryCount, d)
		return
	}
	memCommissionMonthlyLock.Lock()
	defer memCommissionMonthlyLock.Unlock()
	key := memCommissionMonthlyKey(d.PeriodStartAt, d.EmployeeUserId)
	existing := memCommissionMonthlyBuf[key]
	if existing == nil {
		copyDelta := *d
		memCommissionMonthlyBuf[key] = &copyDelta
		return
	}
	existing.RevenueQuota += d.RevenueQuota
	existing.CostQuota += d.CostQuota
	existing.ProfitQuota += d.ProfitQuota
	existing.CommissionQuota += d.CommissionQuota
	existing.RecordCount += d.RecordCount
	if existing.PeriodKey == "" {
		existing.PeriodKey = d.PeriodKey
	}
	if existing.Timezone == "" {
		existing.Timezone = d.Timezone
	}
	if existing.PeriodEndAt == 0 {
		existing.PeriodEndAt = d.PeriodEndAt
	}
	if d.LastCreatedAt > existing.LastCreatedAt {
		existing.LastCreatedAt = d.LastCreatedAt
	}
	if d.RetryCount > existing.RetryCount {
		existing.RetryCount = d.RetryCount
	}
}

// ---- 公共入口 ----

// BufferPlatformDailyStat 将平台侧日统计增量写入缓冲区（Redis 或内存）。
func BufferPlatformDailyStat(rec *ConsumptionCost) {
	statDate := unixDayStart(rec.CreatedAt)
	if common.RedisEnabled {
		bufferPlatformStatRedis(statDate, rec.ChannelId, rec.ChannelName, rec.RevenueQuota, rec.CostQuota, rec.CostRatio, rec.CreatedAt)
	} else {
		bufferPlatformStatMem(statDate, rec.ChannelId, rec.ChannelName, rec.RevenueQuota, rec.CostQuota, rec.CostRatio, rec.CreatedAt)
	}
	// coverage 标记仍走进程内缓存 + 异步 DB upsert（频率极低，不需要缓冲）
	go ensureDailyCoverageAsync(statDate)
}

// BufferCommissionDailyStat 将员工提成侧日统计增量写入缓冲区（Redis 或内存）。
func BufferCommissionDailyStat(log *EmployeeCommissionLog) {
	statDate := unixDayStart(log.CreatedAt)
	period := ResolveCommissionMonthlyPeriod(log.CreatedAt)
	if common.RedisEnabled {
		bufferCommissionStatRedis(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
		bufferCustomerCommissionStatRedis(statDate, log.EmployeeUserId, log.CustomerUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
		bufferCommissionMonthlyStatRedis(period, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	} else {
		bufferCommissionStatMem(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
		bufferCustomerCommissionStatMem(statDate, log.EmployeeUserId, log.CustomerUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
		bufferCommissionMonthlyStatMem(period, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	}
}

// ensureDailyCoverageAsync 在非事务上下文中异步标记 coverage。
func ensureDailyCoverageAsync(statDate int64) {
	if _, loaded := coveredDaysCache.LoadOrStore(statDate, struct{}{}); loaded {
		return
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stat_date"}},
		DoUpdates: clause.AssignmentColumns([]string{"completed_at"}),
	}).Create(&BusinessDailyStatsCoverage{
		StatDate:    statDate,
		CompletedAt: time.Now().Unix(),
	}).Error; err != nil {
		common.SysError("ensureDailyCoverageAsync: " + err.Error())
		coveredDaysCache.Delete(statDate) // 失败回退，下次重试
	}
}

func flushPlatformStatsFromMem() {
	memPlatformLock.Lock()
	buf := memPlatformBuf
	memPlatformBuf = make(map[string]*platformStatDelta)
	memPlatformLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertPlatformDailyStat(d.StatDate, d.ChannelId, d.ChannelName, d.RevenueQuota, d.CostQuota, d.RecordCount, d.CostRatioSum, d.LastCreatedAt) {
			requeuePlatformStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: platform mem items=%d", len(buf)))
}

func flushCommissionStatsFromMem() {
	memCommissionLock.Lock()
	buf := memCommissionBuf
	memCommissionBuf = make(map[string]*commissionStatDelta)
	memCommissionLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertCommissionDailyStat(d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
			requeueCommissionStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission mem items=%d", len(buf)))
}

func flushCustomerCommissionStatsFromMem() {
	memCustomerCommissionLock.Lock()
	buf := memCustomerCommissionBuf
	memCustomerCommissionBuf = make(map[string]*customerCommissionStatDelta)
	memCustomerCommissionLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertCustomerCommissionDailyStat(d.StatDate, d.EmployeeUserId, d.CustomerUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
			requeueCustomerCommissionStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: customer commission mem items=%d", len(buf)))
}

func flushCommissionMonthlyStatsFromMem() {
	memCommissionMonthlyLock.Lock()
	buf := memCommissionMonthlyBuf
	memCommissionMonthlyBuf = make(map[string]*commissionMonthlyStatDelta)
	memCommissionMonthlyLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertCommissionMonthlyStat(d.PeriodStartAt, d.PeriodEndAt, d.PeriodKey, d.Timezone, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
			requeueCommissionMonthlyStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission monthly mem items=%d", len(buf)))
}

func flushPlatformStatsFromRedis() {
	ctx := context.Background()
	flushed := flushRedisStatKeys(ctx, redisStatProcessingPrefix+platformStatBufferPrefix+"*", flushOnePlatformKey)
	flushed += flushRedisStatKeys(ctx, platformStatBufferPrefix+"*", flushOnePlatformKey)
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: platform redis keys=%d", flushed))
	}
}

func flushCommissionStatsFromRedis() {
	ctx := context.Background()
	flushed := flushRedisStatKeys(ctx, redisStatProcessingPrefix+commissionStatBufferPrefix+"*", flushOneCommissionKey)
	flushed += flushRedisStatKeys(ctx, commissionStatBufferPrefix+"*", flushOneCommissionKey)
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: commission redis keys=%d", flushed))
	}
}

func flushCustomerCommissionStatsFromRedis() {
	ctx := context.Background()
	flushed := flushRedisStatKeys(ctx, redisStatProcessingPrefix+customerCommissionStatBufferPrefix+"*", flushOneCustomerCommissionKey)
	flushed += flushRedisStatKeys(ctx, customerCommissionStatBufferPrefix+"*", flushOneCustomerCommissionKey)
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: customer commission redis keys=%d", flushed))
	}
}

func flushCommissionMonthlyStatsFromRedis() {
	ctx := context.Background()
	flushed := flushRedisStatKeys(ctx, redisStatProcessingPrefix+commissionMonthlyStatBufferPrefix+"*", flushOneCommissionMonthlyKey)
	flushed += flushRedisStatKeys(ctx, commissionMonthlyStatBufferPrefix+"*", flushOneCommissionMonthlyKey)
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: commission monthly redis keys=%d", flushed))
	}
}

func flushRedisStatKeys(ctx context.Context, pattern string, flush func(context.Context, string) bool) int {
	var cursor uint64
	var flushed int
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			common.SysError("flushRedisStatKeys: scan error: " + err.Error())
			return flushed
		}
		for _, key := range keys {
			if flush(ctx, key) {
				flushed++
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return flushed
}

func applyRedisStatBatch(batchKey string, apply func(tx *gorm.DB) error) (bool, bool) {
	if batchKey == "" {
		return false, false
	}
	applied := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		row := BusinessStatsAppliedBatch{BatchKey: batchKey, AppliedAt: time.Now().Unix()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			applied = false
			return nil
		}
		applied = true
		return apply(tx)
	})
	if err != nil {
		common.SysError("applyRedisStatBatch: " + err.Error())
		return false, false
	}
	return true, !applied
}

func flushOnePlatformKey(ctx context.Context, key string) bool {
	processingKey, ok := prepareRedisStatProcessingKey(ctx, key)
	if !ok {
		return false
	}
	key = processingKey
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	// 解析 key: "biz_buf:platform:{statDate}:{channelId}"
	originalKey := redisStatOriginalKey(key)
	parts := strings.TrimPrefix(originalKey, platformStatBufferPrefix)
	sepIdx := strings.Index(parts, ":")
	if sepIdx < 0 {
		common.RDB.Del(ctx, key)
		return false
	}
	statDate, _ := strconv.ParseInt(parts[:sepIdx], 10, 64)
	channelId, _ := strconv.Atoi(parts[sepIdx+1:])
	if statDate == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	revenueQuota, _ := strconv.ParseInt(vals["revenue_quota"], 10, 64)
	costQuota, _ := strconv.ParseInt(vals["cost_quota"], 10, 64)
	recordCount, _ := strconv.ParseInt(vals["record_count"], 10, 64)
	costRatioSumE9, _ := strconv.ParseInt(vals["cost_ratio_sum_e9"], 10, 64)
	lastCreatedAt, _ := strconv.ParseInt(vals["last_created_at"], 10, 64)
	channelName := vals["channel_name"]
	costRatioSum := float64(costRatioSumE9) / 1e9

	if recordCount == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	batchID := ensureRedisStatBatchID(ctx, key)
	ok, duplicate := applyRedisStatBatch(batchID, func(tx *gorm.DB) error {
		return upsertPlatformDailyStatTx(tx, statDate, channelId, channelName, revenueQuota, costQuota, recordCount, costRatioSum, lastCreatedAt)
	})
	if !ok {
		handleRedisStatFlushFailure(ctx, key, "platform_daily_stat", "db upsert failed", vals)
		return false
	}
	if duplicate {
		common.SysLog("flushOnePlatformKey: skip duplicate batch " + batchID)
	}
	common.RDB.Del(ctx, key)
	return true
}

func handleRedisStatFlushFailure(ctx context.Context, key, kind, reason string, vals map[string]string) {
	retryCount, _ := strconv.Atoi(vals["retry_count"])
	retryCount++
	if retryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter(kind, reason, retryCount, map[string]any{
			"key":    key,
			"values": vals,
		})
		common.RDB.Del(ctx, key)
		return
	}
	pipe := common.RDB.Pipeline()
	pipe.HSet(ctx, key, "retry_count", retryCount)
	pipe.Expire(ctx, key, statBufferKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysError("handleRedisStatFlushFailure: pipeline error: " + err.Error())
	}
}

func flushOneCommissionKey(ctx context.Context, key string) bool {
	processingKey, ok := prepareRedisStatProcessingKey(ctx, key)
	if !ok {
		return false
	}
	key = processingKey
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	originalKey := redisStatOriginalKey(key)
	parts := strings.TrimPrefix(originalKey, commissionStatBufferPrefix)
	sepIdx := strings.Index(parts, ":")
	if sepIdx < 0 {
		common.RDB.Del(ctx, key)
		return false
	}
	statDate, _ := strconv.ParseInt(parts[:sepIdx], 10, 64)
	employeeUserId, _ := strconv.Atoi(parts[sepIdx+1:])
	if statDate == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	revenueQuota, _ := strconv.ParseInt(vals["revenue_quota"], 10, 64)
	costQuota, _ := strconv.ParseInt(vals["cost_quota"], 10, 64)
	profitQuota, _ := strconv.ParseInt(vals["profit_quota"], 10, 64)
	commissionQuota, _ := strconv.ParseInt(vals["commission_quota"], 10, 64)
	recordCount, _ := strconv.ParseInt(vals["record_count"], 10, 64)
	lastCreatedAt, _ := strconv.ParseInt(vals["last_created_at"], 10, 64)

	if recordCount == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	batchID := ensureRedisStatBatchID(ctx, key)
	ok, duplicate := applyRedisStatBatch(batchID, func(tx *gorm.DB) error {
		return upsertCommissionDailyStatTx(tx, statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt)
	})
	if !ok {
		handleRedisStatFlushFailure(ctx, key, "commission_daily_stat", "db upsert failed", vals)
		return false
	}
	if duplicate {
		common.SysLog("flushOneCommissionKey: skip duplicate batch " + batchID)
	}
	common.RDB.Del(ctx, key)
	return true
}

func flushOneCustomerCommissionKey(ctx context.Context, key string) bool {
	processingKey, ok := prepareRedisStatProcessingKey(ctx, key)
	if !ok {
		return false
	}
	key = processingKey
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	originalKey := redisStatOriginalKey(key)
	parts := strings.TrimPrefix(originalKey, customerCommissionStatBufferPrefix)
	pieces := strings.Split(parts, ":")
	if len(pieces) != 3 {
		common.RDB.Del(ctx, key)
		return false
	}
	statDate, _ := strconv.ParseInt(pieces[0], 10, 64)
	employeeUserId, _ := strconv.Atoi(pieces[1])
	customerUserId, _ := strconv.Atoi(pieces[2])
	if statDate == 0 || employeeUserId == 0 || customerUserId == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	revenueQuota, _ := strconv.ParseInt(vals["revenue_quota"], 10, 64)
	costQuota, _ := strconv.ParseInt(vals["cost_quota"], 10, 64)
	profitQuota, _ := strconv.ParseInt(vals["profit_quota"], 10, 64)
	commissionQuota, _ := strconv.ParseInt(vals["commission_quota"], 10, 64)
	recordCount, _ := strconv.ParseInt(vals["record_count"], 10, 64)
	lastCreatedAt, _ := strconv.ParseInt(vals["last_created_at"], 10, 64)

	if recordCount == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	batchID := ensureRedisStatBatchID(ctx, key)
	ok, duplicate := applyRedisStatBatch(batchID, func(tx *gorm.DB) error {
		return upsertCustomerCommissionDailyStatTx(tx, statDate, employeeUserId, customerUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt)
	})
	if !ok {
		handleRedisStatFlushFailure(ctx, key, "customer_commission_daily_stat", "db upsert failed", vals)
		return false
	}
	if duplicate {
		common.SysLog("flushOneCustomerCommissionKey: skip duplicate batch " + batchID)
	}
	common.RDB.Del(ctx, key)
	return true
}

// ---- DB upsert（批量刷盘时调用）----

func flushOneCommissionMonthlyKey(ctx context.Context, key string) bool {
	processingKey, ok := prepareRedisStatProcessingKey(ctx, key)
	if !ok {
		return false
	}
	key = processingKey
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	originalKey := redisStatOriginalKey(key)
	parts := strings.TrimPrefix(originalKey, commissionMonthlyStatBufferPrefix)
	sepIdx := strings.Index(parts, ":")
	if sepIdx < 0 {
		common.RDB.Del(ctx, key)
		return false
	}
	periodStartAt, _ := strconv.ParseInt(parts[:sepIdx], 10, 64)
	employeeUserId, _ := strconv.Atoi(parts[sepIdx+1:])
	if periodStartAt == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	periodEndAt, _ := strconv.ParseInt(vals["period_end_at"], 10, 64)
	revenueQuota, _ := strconv.ParseInt(vals["revenue_quota"], 10, 64)
	costQuota, _ := strconv.ParseInt(vals["cost_quota"], 10, 64)
	profitQuota, _ := strconv.ParseInt(vals["profit_quota"], 10, 64)
	commissionQuota, _ := strconv.ParseInt(vals["commission_quota"], 10, 64)
	recordCount, _ := strconv.ParseInt(vals["record_count"], 10, 64)
	lastCreatedAt, _ := strconv.ParseInt(vals["last_created_at"], 10, 64)

	if recordCount == 0 {
		common.RDB.Del(ctx, key)
		return false
	}

	batchID := ensureRedisStatBatchID(ctx, key)
	ok, duplicate := applyRedisStatBatch(batchID, func(tx *gorm.DB) error {
		return upsertCommissionMonthlyStatTx(tx, periodStartAt, periodEndAt, vals["period_key"], vals["timezone"], employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt)
	})
	if !ok {
		handleRedisStatFlushFailure(ctx, key, "commission_monthly_stat", "db upsert failed", vals)
		return false
	}
	if duplicate {
		common.SysLog("flushOneCommissionMonthlyKey: skip duplicate batch " + batchID)
	}
	common.RDB.Del(ctx, key)
	return true
}

func upsertPlatformDailyStat(statDate int64, channelId int, channelName string, revenueQuota, costQuota, recordCount int64, costRatioSum float64, lastCreatedAt int64) bool {
	if err := upsertPlatformDailyStatTx(DB, statDate, channelId, channelName, revenueQuota, costQuota, recordCount, costRatioSum, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertPlatformDailyStat: statDate=%d channelId=%d err=%s", statDate, channelId, err.Error()))
		return false
	}
	return true
}

func upsertPlatformDailyStatTx(tx *gorm.DB, statDate int64, channelId int, channelName string, revenueQuota, costQuota, recordCount int64, costRatioSum float64, lastCreatedAt int64) error {
	row := PlatformChannelDailyStat{
		StatDate:      statDate,
		ChannelId:     channelId,
		ChannelName:   channelName,
		RevenueQuota:  revenueQuota,
		CostQuota:     costQuota,
		RecordCount:   recordCount,
		CostRatioSum:  costRatioSum,
		LastCreatedAt: lastCreatedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":   gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":      gorm.Expr("cost_quota + ?", costQuota),
			"record_count":    gorm.Expr("record_count + ?", recordCount),
			"cost_ratio_sum":  gorm.Expr("cost_ratio_sum + ?", costRatioSum),
			"channel_name":    gorm.Expr("COALESCE(NULLIF(channel_name, ''), ?)", channelName),
			"last_created_at": gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error
}

func upsertCommissionDailyStat(statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	if err := upsertCommissionDailyStatTx(DB, statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionDailyStat: statDate=%d empUserId=%d err=%s", statDate, employeeUserId, err.Error()))
		return false
	}
	return true
}

func upsertCommissionDailyStatTx(tx *gorm.DB, statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) error {
	row := EmployeeCommissionDailyStat{
		StatDate:        statDate,
		EmployeeUserId:  employeeUserId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		RecordCount:     recordCount,
		LastCreatedAt:   lastCreatedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "employee_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":    gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":       gorm.Expr("cost_quota + ?", costQuota),
			"profit_quota":     gorm.Expr("profit_quota + ?", profitQuota),
			"commission_quota": gorm.Expr("commission_quota + ?", commissionQuota),
			"record_count":     gorm.Expr("record_count + ?", recordCount),
			"last_created_at":  gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error
}

func upsertCustomerCommissionDailyStat(statDate int64, employeeUserId, customerUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	if err := upsertCustomerCommissionDailyStatTx(DB, statDate, employeeUserId, customerUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCustomerCommissionDailyStat: statDate=%d empUserId=%d customerUserId=%d err=%s", statDate, employeeUserId, customerUserId, err.Error()))
		return false
	}
	return true
}

func upsertCustomerCommissionDailyStatTx(tx *gorm.DB, statDate int64, employeeUserId, customerUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) error {
	row := EmployeeCustomerCommissionDailyStat{
		StatDate:        statDate,
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		RecordCount:     recordCount,
		LastCreatedAt:   lastCreatedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "employee_user_id"}, {Name: "customer_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":    gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":       gorm.Expr("cost_quota + ?", costQuota),
			"profit_quota":     gorm.Expr("profit_quota + ?", profitQuota),
			"commission_quota": gorm.Expr("commission_quota + ?", commissionQuota),
			"record_count":     gorm.Expr("record_count + ?", recordCount),
			"last_created_at":  gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error
}

func upsertCommissionMonthlyStat(periodStartAt, periodEndAt int64, periodKey, timezone string, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	if err := upsertCommissionMonthlyStatTx(DB, periodStartAt, periodEndAt, periodKey, timezone, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionMonthlyStat: periodStartAt=%d empUserId=%d err=%s", periodStartAt, employeeUserId, err.Error()))
		return false
	}
	return true
}

func upsertCommissionMonthlyStatTx(tx *gorm.DB, periodStartAt, periodEndAt int64, periodKey, timezone string, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) error {
	row := EmployeeCommissionMonthlyStat{
		PeriodStartAt:   periodStartAt,
		PeriodEndAt:     periodEndAt,
		PeriodKey:       periodKey,
		Timezone:        timezone,
		EmployeeUserId:  employeeUserId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		RecordCount:     recordCount,
		LastCreatedAt:   lastCreatedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "period_start_at"}, {Name: "employee_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"period_end_at":    periodEndAt,
			"period_key":       gorm.Expr("COALESCE(NULLIF(period_key, ''), ?)", periodKey),
			"timezone":         gorm.Expr("COALESCE(NULLIF(timezone, ''), ?)", timezone),
			"revenue_quota":    gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":       gorm.Expr("cost_quota + ?", costQuota),
			"profit_quota":     gorm.Expr("profit_quota + ?", profitQuota),
			"commission_quota": gorm.Expr("commission_quota + ?", commissionQuota),
			"record_count":     gorm.Expr("record_count + ?", recordCount),
			"last_created_at":  gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error
}

// ============================================================================
// 明细台账缓冲层
//
// 高并发下避免每笔消费单条 INSERT，攒批后 CreateInBatches + ON CONFLICT DO NOTHING。
// consumption_costs: 调用方不依赖 inserted，直接缓冲。
// employee_commission_logs: 调用方依赖 inserted 做后续提成结算，
//   用 Redis SET(SADD) / 内存 map 对 log_id 做前置去重，保证返回值准确。
// ============================================================================

const (
	ledgerFlushBatchSize         = 500
	pairedLedgerFlushBatchSize   = 100
	pairedLedgerFlushMaxPerCycle = 1000
	commissionLogDedupPrefix     = "biz_buf:dedup:commission_log_id:"
	commissionLogDedupTTL        = 24 * time.Hour
)

// ---- consumption_costs 缓冲 ----

var (
	costLedgerBuf  []*ConsumptionCost
	costLedgerLock sync.Mutex
)

type costCommissionLedgerPair struct {
	Cost       *ConsumptionCost
	Commission *EmployeeCommissionLog
}

var (
	costCommissionLedgerBuf  []*costCommissionLedgerPair
	costCommissionLedgerLock sync.Mutex
)

// BufferConsumptionCostRecord 将消费成本记录推入缓冲区，由后台批量入库。
func BufferConsumptionCostRecord(rec *ConsumptionCost) {
	costLedgerLock.Lock()
	costLedgerBuf = append(costLedgerBuf, rec)
	costLedgerLock.Unlock()
}

func flushConsumptionCostLedger() {
	costLedgerLock.Lock()
	buf := costLedgerBuf
	costLedgerBuf = nil
	costLedgerLock.Unlock()

	if len(buf) == 0 {
		return
	}
	// 分批入库，ON CONFLICT DO NOTHING 保证幂等
	for i := 0; i < len(buf); i += pairedLedgerFlushBatchSize {
		end := i + pairedLedgerFlushBatchSize
		if end > len(buf) {
			end = len(buf)
		}
		batch := buf[i:end]
		if err := DB.Select(
			"LogId", "UserId", "ChannelId", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(batch, ledgerFlushBatchSize).Error; err != nil {
			common.SysError(fmt.Sprintf("flushConsumptionCostLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: consumption_costs ledger items=%d", len(buf)))
}

func bufferCostAndCommissionLedger(cost *ConsumptionCost, log *EmployeeCommissionLog) {
	costCommissionLedgerLock.Lock()
	costCommissionLedgerBuf = append(costCommissionLedgerBuf, &costCommissionLedgerPair{
		Cost:       cost,
		Commission: log,
	})
	costCommissionLedgerLock.Unlock()
}

type pairedFlushResult struct {
	HadItems bool
	Failed   bool
}

func flushCostAndCommissionLedger() pairedFlushResult {
	costCommissionLedgerLock.Lock()
	buf := costCommissionLedgerBuf
	if len(buf) > pairedLedgerFlushMaxPerCycle {
		costCommissionLedgerBuf = buf[pairedLedgerFlushMaxPerCycle:]
		buf = buf[:pairedLedgerFlushMaxPerCycle]
	} else {
		costCommissionLedgerBuf = nil
	}
	costCommissionLedgerLock.Unlock()

	if len(buf) == 0 {
		return pairedFlushResult{}
	}
	for i := 0; i < len(buf); i += ledgerFlushBatchSize {
		end := i + ledgerFlushBatchSize
		if end > len(buf) {
			end = len(buf)
		}
		pairs := buf[i:end]
		costBatch := make([]*ConsumptionCost, 0, len(pairs))
		commissionBatch := make([]*EmployeeCommissionLog, 0, len(pairs))
		for _, pair := range pairs {
			costBatch = append(costBatch, pair.Cost)
			commissionBatch = append(commissionBatch, pair.Commission)
		}
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Select(
				"LogId", "UserId", "ChannelId", "GroupName", "ModelName",
				"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
			).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "log_id"}},
				DoNothing: true,
			}).CreateInBatches(costBatch, pairedLedgerFlushBatchSize).Error; err != nil {
				return err
			}
			return tx.Select(
				"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
				"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
				"CostRatio", "GroupRatio", "CreatedAt",
			).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "log_id"}},
				DoNothing: true,
			}).CreateInBatches(commissionBatch, pairedLedgerFlushBatchSize).Error
		}); err != nil {
			common.SysError(fmt.Sprintf("flushCostAndCommissionLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			costCommissionLedgerLock.Lock()
			costCommissionLedgerBuf = append(buf[i:], costCommissionLedgerBuf...)
			costCommissionLedgerLock.Unlock()
			return pairedFlushResult{HadItems: true, Failed: true}
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: paired cost/commission ledger items=%d", len(buf)))
	return pairedFlushResult{HadItems: true, Failed: false}
}

// ---- employee_commission_logs 缓冲 ----

var (
	commissionLedgerBuf  []*EmployeeCommissionLog
	commissionLedgerLock sync.Mutex
	// 内存去重集合（无 Redis 时使用），存 log_id
	commissionLogIdSet     = make(map[int]struct{})
	commissionLogIdSetLock sync.Mutex
)

// CheckAndBufferCommissionLog 检查 log_id 是否重复，若不重复则推入缓冲区。
// 返回 inserted=true 表示首次写入（调用方据此执行后续提成结算）。
// log_id 为 nil 时视为无幂等键，始终 inserted=true。
func CheckAndBufferCommissionLog(log *EmployeeCommissionLog) (inserted bool) {
	// 无 log_id 的记录没有幂等约束，直接入缓冲
	if log.LogId == nil || *log.LogId <= 0 {
		log.LogId = nil
		commissionLedgerLock.Lock()
		commissionLedgerBuf = append(commissionLedgerBuf, log)
		commissionLedgerLock.Unlock()
		return true
	}
	logId := *log.LogId

	// 去重检查
	if common.RedisEnabled {
		key := fmt.Sprintf("%s%d", commissionLogDedupPrefix, logId)
		ctx := context.Background()
		added, err := common.RDB.SetNX(ctx, key, "1", commissionLogDedupTTL).Result()
		if err != nil {
			common.SysError("CheckAndBufferCommissionLog: redis SETNX error: " + err.Error())
			// Redis 故障时回退到 DB 直接插入
			return directInsertCommissionLog(log)
		}
		if !added {
			return false // 已存在，重复
		}
	} else {
		commissionLogIdSetLock.Lock()
		if _, exists := commissionLogIdSet[logId]; exists {
			commissionLogIdSetLock.Unlock()
			return false
		}
		commissionLogIdSet[logId] = struct{}{}
		commissionLogIdSetLock.Unlock()
	}

	commissionLedgerLock.Lock()
	commissionLedgerBuf = append(commissionLedgerBuf, log)
	commissionLedgerLock.Unlock()
	return true
}

func CheckAndBufferCostAndCommission(cost *ConsumptionCost, log *EmployeeCommissionLog) (inserted bool) {
	if log.LogId == nil || *log.LogId <= 0 {
		log.LogId = nil
		if cost.LogId != nil && *cost.LogId <= 0 {
			cost.LogId = nil
		}
		bufferCostAndCommissionLedger(cost, log)
		return true
	}
	logId := *log.LogId
	if cost.LogId == nil || *cost.LogId != logId {
		cost.LogId = common.GetPointer(logId)
	}

	if common.RedisEnabled {
		key := fmt.Sprintf("%s%d", commissionLogDedupPrefix, logId)
		ctx := context.Background()
		added, err := common.RDB.SetNX(ctx, key, "1", commissionLogDedupTTL).Result()
		if err != nil {
			common.SysError("CheckAndBufferCostAndCommission: redis SETNX error: " + err.Error())
			return directInsertCostAndCommission(cost, log)
		}
		if !added {
			return false
		}
	} else {
		commissionLogIdSetLock.Lock()
		if _, exists := commissionLogIdSet[logId]; exists {
			commissionLogIdSetLock.Unlock()
			return false
		}
		commissionLogIdSet[logId] = struct{}{}
		commissionLogIdSetLock.Unlock()
	}

	bufferCostAndCommissionLedger(cost, log)
	return true
}

// directInsertCommissionLog Redis 故障时回退：直接单条入库。
func directInsertCommissionLog(log *EmployeeCommissionLog) bool {
	result := DB.Select(
		"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
		"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
		"CostRatio", "GroupRatio", "CreatedAt",
	).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "log_id"}},
		DoNothing: true,
	}).Create(log)
	return result.Error == nil && result.RowsAffected > 0
}

func directInsertCostAndCommission(cost *ConsumptionCost, log *EmployeeCommissionLog) bool {
	inserted := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Select(
			"LogId", "UserId", "ChannelId", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).Create(cost).Error; err != nil {
			return err
		}
		result := tx.Select(
			"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
			"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
			"CostRatio", "GroupRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).Create(log)
		if result.Error != nil {
			return result.Error
		}
		inserted = result.RowsAffected > 0
		return nil
	})
	return err == nil && inserted
}

func flushCommissionLogLedger() {
	commissionLedgerLock.Lock()
	buf := commissionLedgerBuf
	commissionLedgerBuf = nil
	commissionLedgerLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for i := 0; i < len(buf); i += ledgerFlushBatchSize {
		end := i + ledgerFlushBatchSize
		if end > len(buf) {
			end = len(buf)
		}
		batch := buf[i:end]
		if err := DB.Select(
			"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
			"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
			"CostRatio", "GroupRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(batch, ledgerFlushBatchSize).Error; err != nil {
			common.SysError(fmt.Sprintf("flushCommissionLogLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission_logs ledger items=%d", len(buf)))
}

// ============================================================================
// 员工汇总缓冲层（user_extensions + tier 升级）
//
// AddCommissionQuota / AddProfitStats / TryAutoUpgradeTier 不再即时写 DB，
// 而是按 userId 累加增量到 Redis Hash / 内存 map，定时批量刷入。
// ============================================================================

const (
	employeeExtBufferPrefix = "biz_buf:emp_ext:" // + {userId}
)

// employeeExtDelta 单个员工在一个刷盘周期内的累计增量。
type employeeExtDelta struct {
	CommissionDelta int64
	ProfitDelta     int64
	RetryCount      int
}

var (
	memEmployeeExtBuf  = make(map[int]*employeeExtDelta)
	memEmployeeExtLock sync.Mutex
)

// BufferCommissionAndProfit 将提成和利润增量写入缓冲区。
// 替代原来的 AddCommissionQuota + AddProfitStats 即时 DB 操作。
func BufferCommissionAndProfit(userId int, commissionDelta, profitDelta int64) {
	if commissionDelta == 0 && profitDelta == 0 {
		return
	}
	if common.RedisEnabled {
		key := fmt.Sprintf("%s%d", employeeExtBufferPrefix, userId)
		ctx := context.Background()
		pipe := common.RDB.Pipeline()
		if commissionDelta != 0 {
			pipe.HIncrBy(ctx, key, "commission_delta", commissionDelta)
		}
		if profitDelta != 0 {
			pipe.HIncrBy(ctx, key, "profit_delta", profitDelta)
		}
		pipe.Expire(ctx, key, statBufferKeyTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			common.SysError("BufferCommissionAndProfit: redis pipeline error: " + err.Error())
			// Redis 故障降级到内存
			bufferEmployeeExtMem(userId, commissionDelta, profitDelta)
		}
	} else {
		bufferEmployeeExtMem(userId, commissionDelta, profitDelta)
	}
}

func bufferEmployeeExtMem(userId int, commissionDelta, profitDelta int64) {
	bufferEmployeeExtMemWithRetry(userId, commissionDelta, profitDelta, 0)
}

func bufferEmployeeExtMemWithRetry(userId int, commissionDelta, profitDelta int64, retryCount int) {
	memEmployeeExtLock.Lock()
	defer memEmployeeExtLock.Unlock()
	d, ok := memEmployeeExtBuf[userId]
	if !ok {
		d = &employeeExtDelta{}
		memEmployeeExtBuf[userId] = d
	}
	d.CommissionDelta += commissionDelta
	d.ProfitDelta += profitDelta
	if retryCount > d.RetryCount {
		d.RetryCount = retryCount
	}
}

// flushEmployeeExtBuffers 批量刷入 user_extensions 并检查等级升级。
func requeueEmployeeExtDelta(userId int, d *employeeExtDelta) {
	if d == nil || (d.CommissionDelta == 0 && d.ProfitDelta == 0) {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("employee_ext_delta", "max retries exceeded", d.RetryCount, map[string]any{
			"user_id": userId,
			"delta":   d,
		})
		return
	}
	if common.RedisEnabled {
		key := fmt.Sprintf("%s%d", employeeExtBufferPrefix, userId)
		ctx := context.Background()
		pipe := common.RDB.Pipeline()
		if d.CommissionDelta != 0 {
			pipe.HIncrBy(ctx, key, "commission_delta", d.CommissionDelta)
		}
		if d.ProfitDelta != 0 {
			pipe.HIncrBy(ctx, key, "profit_delta", d.ProfitDelta)
		}
		pipe.HSet(ctx, key, "retry_count", d.RetryCount)
		pipe.Expire(ctx, key, statBufferKeyTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			common.SysError("requeueEmployeeExtDelta: redis pipeline error: " + err.Error())
			bufferEmployeeExtMemWithRetry(userId, d.CommissionDelta, d.ProfitDelta, d.RetryCount)
		}
		return
	}
	bufferEmployeeExtMemWithRetry(userId, d.CommissionDelta, d.ProfitDelta, d.RetryCount)
}

func flushEmployeeExtBuffers() {
	var items map[int]*employeeExtDelta

	if common.RedisEnabled {
		items = drainEmployeeExtFromRedis()
		// 合并内存中的降级数据（Redis 故障期间可能有）
		memItems := drainEmployeeExtFromMem()
		for uid, d := range memItems {
			if existing, ok := items[uid]; ok {
				existing.CommissionDelta += d.CommissionDelta
				existing.ProfitDelta += d.ProfitDelta
			} else {
				items[uid] = d
			}
		}
	} else {
		items = drainEmployeeExtFromMem()
	}

	if len(items) == 0 {
		return
	}

	// 批量确保 user_extensions 记录存在
	userIds := make([]int, 0, len(items))
	for uid := range items {
		userIds = append(userIds, uid)
	}
	if !ensureUserExtensionsBatch(userIds) {
		for uid, d := range items {
			requeueEmployeeExtDelta(uid, d)
		}
		return
	}

	// 逐个 UPDATE + 等级升级检查
	for uid, d := range items {
		if !applyEmployeeExtDelta(uid, d) {
			requeueEmployeeExtDelta(uid, d)
		}
	}

	common.SysLog(fmt.Sprintf("flush_business_stats: employee_ext items=%d", len(items)))
}

func drainEmployeeExtFromMem() map[int]*employeeExtDelta {
	memEmployeeExtLock.Lock()
	buf := memEmployeeExtBuf
	memEmployeeExtBuf = make(map[int]*employeeExtDelta)
	memEmployeeExtLock.Unlock()
	return buf
}

func drainEmployeeExtFromRedis() map[int]*employeeExtDelta {
	ctx := context.Background()
	pattern := employeeExtBufferPrefix + "*"
	items := make(map[int]*employeeExtDelta)
	var cursor uint64
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			common.SysError("drainEmployeeExtFromRedis: scan error: " + err.Error())
			break
		}
		for _, key := range keys {
			uidStr := strings.TrimPrefix(key, employeeExtBufferPrefix)
			uid, _ := strconv.Atoi(uidStr)
			if uid == 0 {
				common.RDB.Del(ctx, key)
				continue
			}
			vals, err := common.RDB.HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			commDelta, _ := strconv.ParseInt(vals["commission_delta"], 10, 64)
			profitDelta, _ := strconv.ParseInt(vals["profit_delta"], 10, 64)
			retryCount, _ := strconv.Atoi(vals["retry_count"])
			if commDelta == 0 && profitDelta == 0 {
				common.RDB.Del(ctx, key)
				continue
			}
			items[uid] = &employeeExtDelta{
				CommissionDelta: commDelta,
				ProfitDelta:     profitDelta,
				RetryCount:      retryCount,
			}
			common.RDB.Del(ctx, key)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return items
}

// ensureUserExtensionsBatch 批量确保 user_extensions 记录存在。
func ensureUserExtensionsBatch(userIds []int) bool {
	if len(userIds) == 0 {
		return true
	}
	rows := make([]UserExtension, 0, len(userIds))
	for _, uid := range userIds {
		rows = append(rows, UserExtension{UserId: uid})
	}
	// ON CONFLICT DO NOTHING, 批量插入
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
		common.SysError("ensureUserExtensionsBatch: " + err.Error())
		return false
	}
	return true
}

// applyEmployeeExtDelta 将累计增量写入 DB 并检查等级升级。
func applyEmployeeExtDelta(userId int, d *employeeExtDelta) bool {
	updates := map[string]interface{}{}
	if d.CommissionDelta != 0 {
		updates["commission_total_quota"] = gorm.Expr("commission_total_quota + ?", d.CommissionDelta)
		updates["commission_pending_quota"] = gorm.Expr("commission_pending_quota + ?", d.CommissionDelta)
	}
	if d.ProfitDelta != 0 {
		updates["profit_total_quota"] = gorm.Expr("profit_total_quota + ?", d.ProfitDelta)
	}
	if len(updates) == 0 {
		return true
	}
	result := DB.Model(&UserExtension{}).Where("user_id = ?", userId).Updates(updates)
	if result.Error != nil {
		common.SysError(fmt.Sprintf("applyEmployeeExtDelta: userId=%d err=%s", userId, result.Error.Error()))
		return false
	}
	if result.RowsAffected == 0 {
		common.SysError(fmt.Sprintf("applyEmployeeExtDelta: userId=%d no rows affected", userId))
		return false
	}
	// 等级升级检查（仅利润有正增量时）
	if d.ProfitDelta > 0 {
		var ext UserExtension
		if err := DB.Select("profit_total_quota").Where("user_id = ?", userId).First(&ext).Error; err != nil {
			common.SysError(fmt.Sprintf("applyEmployeeExtDelta: read profit failed userId=%d err=%s", userId, err.Error()))
			return true
		}
		TryAutoUpgradeTier(userId, ext.ProfitTotalQuota)
	}
	return true
}

// ---- 刷盘入口 ----

// FlushBusinessStatBuffers 将缓冲区的增量批量刷入 DB。由后台定时任务调用。
func FlushBusinessStatBuffers() {
	businessStatsFlushMu.Lock()
	defer businessStatsFlushMu.Unlock()

	// 1. 先刷明细台账（顺序保证一致性，但三类台账互不阻塞）
	paired := flushCostAndCommissionLedger()
	flushConsumptionCostLedger()
	flushCommissionLogLedger()
	// 如果本轮有成对台账但写入失败，跳过聚合刷盘，避免日统计/员工汇总超前于明细
	if paired.HadItems && paired.Failed {
		return
	}
	// 2. 再刷日统计增量
	if common.RedisEnabled {
		flushPlatformStatsFromRedis()
		flushCommissionStatsFromRedis()
		flushCustomerCommissionStatsFromRedis()
		flushCommissionMonthlyStatsFromRedis()
	} else {
		flushPlatformStatsFromMem()
		flushCommissionStatsFromMem()
		flushCustomerCommissionStatsFromMem()
		flushCommissionMonthlyStatsFromMem()
	}
	// 3. 刷员工汇总（user_extensions + 等级升级）
	flushEmployeeExtBuffers()
}

// StartBusinessStatsFlushLoop 启动后台定时刷盘循环。在 main.go 中调用。
// 从配置 BUSINESS_STATS_FLUSH_INTERVAL 读取刷盘间隔，默认 5 秒。
func StartBusinessStatsFlushLoop() {
	interval := common.BusinessStatsFlushInterval
	if interval <= 0 {
		interval = DefaultBusinessStatsFlushInterval
	}
	common.SysLog(fmt.Sprintf("business stats flush interval: %d seconds", interval))
	go func() {
		// Drain any leftover Redis/mem data from before restart
		FlushBusinessStatBuffers()
		for {
			time.Sleep(time.Duration(interval) * time.Second)
			FlushBusinessStatBuffers()
		}
	}()
}
