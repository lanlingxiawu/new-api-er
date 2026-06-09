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
	platformStatBufferPrefix   = "biz_buf:platform:"   // + {statDate}:{channelId}
	commissionStatBufferPrefix = "biz_buf:commission:" // + {statDate}:{employeeUserId}

	// Redis key 的 TTL，防止 flush 失败导致 key 永驻
	statBufferKeyTTL = 48 * time.Hour

	// 默认刷盘间隔（如果配置未指定或无效）
	DefaultBusinessStatsFlushInterval = 5 // 秒
)

// ---- Redis 缓冲 ----

func platformStatRedisKey(statDate int64, channelId int) string {
	return fmt.Sprintf("%s%d:%d", platformStatBufferPrefix, statDate, channelId)
}

func commissionStatRedisKey(statDate int64, employeeUserId int) string {
	return fmt.Sprintf("%s%d:%d", commissionStatBufferPrefix, statDate, employeeUserId)
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

// ---- 内存缓冲（无 Redis 降级） ----

type platformStatDelta struct {
	StatDate      int64
	ChannelId     int
	ChannelName   string
	RevenueQuota  int64
	CostQuota     int64
	RecordCount   int64
	CostRatioSum  float64
	LastCreatedAt int64
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
}

var (
	memPlatformBuf  = make(map[string]*platformStatDelta)
	memPlatformLock sync.Mutex

	memCommissionBuf  = make(map[string]*commissionStatDelta)
	memCommissionLock sync.Mutex
)

func memPlatformKey(statDate int64, channelId int) string {
	return fmt.Sprintf("%d:%d", statDate, channelId)
}

func memCommissionKey(statDate int64, employeeUserId int) string {
	return fmt.Sprintf("%d:%d", statDate, employeeUserId)
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
	if common.RedisEnabled {
		bufferCommissionStatRedis(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	} else {
		bufferCommissionStatMem(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
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
		upsertPlatformDailyStat(d.StatDate, d.ChannelId, d.ChannelName, d.RevenueQuota, d.CostQuota, d.RecordCount, d.CostRatioSum, d.LastCreatedAt)
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
		upsertCommissionDailyStat(d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt)
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission mem items=%d", len(buf)))
}

func flushPlatformStatsFromRedis() {
	ctx := context.Background()
	pattern := platformStatBufferPrefix + "*"
	var cursor uint64
	var flushed int
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			common.SysError("flushPlatformStatsFromRedis: scan error: " + err.Error())
			return
		}
		for _, key := range keys {
			if flushOnePlatformKey(ctx, key) {
				flushed++
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: platform redis keys=%d", flushed))
	}
}

func flushCommissionStatsFromRedis() {
	ctx := context.Background()
	pattern := commissionStatBufferPrefix + "*"
	var cursor uint64
	var flushed int
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			common.SysError("flushCommissionStatsFromRedis: scan error: " + err.Error())
			return
		}
		for _, key := range keys {
			if flushOneCommissionKey(ctx, key) {
				flushed++
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if flushed > 0 {
		common.SysLog(fmt.Sprintf("flush_business_stats: commission redis keys=%d", flushed))
	}
}

func flushOnePlatformKey(ctx context.Context, key string) bool {
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	// 解析 key: "biz_buf:platform:{statDate}:{channelId}"
	parts := strings.TrimPrefix(key, platformStatBufferPrefix)
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

	upsertPlatformDailyStat(statDate, channelId, channelName, revenueQuota, costQuota, recordCount, costRatioSum, lastCreatedAt)
	common.RDB.Del(ctx, key)
	return true
}

func flushOneCommissionKey(ctx context.Context, key string) bool {
	vals, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return false
	}
	parts := strings.TrimPrefix(key, commissionStatBufferPrefix)
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

	upsertCommissionDailyStat(statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt)
	common.RDB.Del(ctx, key)
	return true
}

// ---- DB upsert（批量刷盘时调用）----

func upsertPlatformDailyStat(statDate int64, channelId int, channelName string, revenueQuota, costQuota, recordCount int64, costRatioSum float64, lastCreatedAt int64) {
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
	if err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":   gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":      gorm.Expr("cost_quota + ?", costQuota),
			"record_count":    gorm.Expr("record_count + ?", recordCount),
			"cost_ratio_sum":  gorm.Expr("cost_ratio_sum + ?", costRatioSum),
			"channel_name":    gorm.Expr("COALESCE(NULLIF(channel_name, ''), ?)", channelName),
			"last_created_at": gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error; err != nil {
		common.SysError(fmt.Sprintf("upsertPlatformDailyStat: statDate=%d channelId=%d err=%s", statDate, channelId, err.Error()))
	}
}

func upsertCommissionDailyStat(statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) {
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
	if err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "employee_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":    gorm.Expr("revenue_quota + ?", revenueQuota),
			"cost_quota":       gorm.Expr("cost_quota + ?", costQuota),
			"profit_quota":     gorm.Expr("profit_quota + ?", profitQuota),
			"commission_quota": gorm.Expr("commission_quota + ?", commissionQuota),
			"record_count":     gorm.Expr("record_count + ?", recordCount),
			"last_created_at":  gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", lastCreatedAt, lastCreatedAt),
		}),
	}).Create(&row).Error; err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionDailyStat: statDate=%d empUserId=%d err=%s", statDate, employeeUserId, err.Error()))
	}
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
	memEmployeeExtLock.Lock()
	defer memEmployeeExtLock.Unlock()
	d, ok := memEmployeeExtBuf[userId]
	if !ok {
		d = &employeeExtDelta{}
		memEmployeeExtBuf[userId] = d
	}
	d.CommissionDelta += commissionDelta
	d.ProfitDelta += profitDelta
}

// flushEmployeeExtBuffers 批量刷入 user_extensions 并检查等级升级。
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
	ensureUserExtensionsBatch(userIds)

	// 逐个 UPDATE + 等级升级检查
	for uid, d := range items {
		applyEmployeeExtDelta(uid, d)
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
			if commDelta == 0 && profitDelta == 0 {
				common.RDB.Del(ctx, key)
				continue
			}
			items[uid] = &employeeExtDelta{
				CommissionDelta: commDelta,
				ProfitDelta:     profitDelta,
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
func ensureUserExtensionsBatch(userIds []int) {
	if len(userIds) == 0 {
		return
	}
	rows := make([]UserExtension, 0, len(userIds))
	for _, uid := range userIds {
		rows = append(rows, UserExtension{UserId: uid})
	}
	// ON CONFLICT DO NOTHING, 批量插入
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
		common.SysError("ensureUserExtensionsBatch: " + err.Error())
	}
}

// applyEmployeeExtDelta 将累计增量写入 DB 并检查等级升级。
func applyEmployeeExtDelta(userId int, d *employeeExtDelta) {
	updates := map[string]interface{}{}
	if d.CommissionDelta != 0 {
		updates["commission_total_quota"] = gorm.Expr("commission_total_quota + ?", d.CommissionDelta)
		updates["commission_pending_quota"] = gorm.Expr("commission_pending_quota + ?", d.CommissionDelta)
	}
	if d.ProfitDelta != 0 {
		updates["profit_total_quota"] = gorm.Expr("profit_total_quota + ?", d.ProfitDelta)
	}
	if len(updates) == 0 {
		return
	}
	if err := DB.Model(&UserExtension{}).Where("user_id = ?", userId).Updates(updates).Error; err != nil {
		common.SysError(fmt.Sprintf("applyEmployeeExtDelta: userId=%d err=%s", userId, err.Error()))
		return
	}
	// 等级升级检查（仅利润有正增量时）
	if d.ProfitDelta > 0 {
		var ext UserExtension
		if err := DB.Select("profit_total_quota").Where("user_id = ?", userId).First(&ext).Error; err != nil {
			common.SysError(fmt.Sprintf("applyEmployeeExtDelta: read profit failed userId=%d err=%s", userId, err.Error()))
			return
		}
		TryAutoUpgradeTier(userId, ext.ProfitTotalQuota)
	}
}

// ---- 刷盘入口 ----

// FlushBusinessStatBuffers 将缓冲区的增量批量刷入 DB。由后台定时任务调用。
func FlushBusinessStatBuffers() {
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
	} else {
		flushPlatformStatsFromMem()
		flushCommissionStatsFromMem()
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
