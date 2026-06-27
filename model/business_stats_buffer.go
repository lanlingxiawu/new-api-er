package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================================
// 日统计缓冲层
//
// 写入侧（每笔消费）将增量累加到进程内 map，后台定时任务批量刷入 DB，
// 减少单条事务的 SQL 数量和锁竞争。
// ============================================================================

const (
	statBufferMaxRetries = 10

	// 默认刷盘间隔（如果配置未指定或无效）
	DefaultBusinessStatsFlushInterval = 8 // 秒，均衡模式：100k RPM 下 8s×15000/cycle = 112k/min 吞吐
)

var businessStatsFlushMu sync.Mutex

// ---- 统计累积缓冲 ----
// 以下 buffer*Mem / requeue*Mem / flush*FromMem 均为进程内 map 实现，
// 函数名保留 Mem 后缀以区分同名的 DB upsert 和 flush 入口。

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

type commissionResetPeriodDelta struct {
	ResetStartedAt  int64
	ResetEndedAt    int64
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

type commissionResetPeriodDailyDelta struct {
	ResetStartedAt  int64
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

var (
	memPlatformBuf  = make(map[string]*platformStatDelta)
	memPlatformLock sync.Mutex

	memCommissionBuf                  = make(map[string]*commissionStatDelta)
	memCommissionLock                 sync.Mutex
	memCustomerCommissionBuf          = make(map[string]*customerCommissionStatDelta)
	memCustomerCommissionLock         sync.Mutex
	memCommissionResetPeriodBuf       = make(map[string]*commissionResetPeriodDelta)
	memCommissionResetPeriodLock      sync.Mutex
	memCommissionResetPeriodDailyBuf  = make(map[string]*commissionResetPeriodDailyDelta)
	memCommissionResetPeriodDailyLock sync.Mutex
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

func memCommissionResetPeriodKey(resetStartedAt int64, employeeUserId int) string {
	return fmt.Sprintf("%d:%d", resetStartedAt, employeeUserId)
}

func memCommissionResetPeriodDailyKey(resetStartedAt, statDate int64, employeeUserId int) string {
	return fmt.Sprintf("%d:%d:%d", resetStartedAt, statDate, employeeUserId)
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

// commissionResetPeriodMeta 返回 reset_started_at 对应的 period_key（配置时区下的日期字符串）
// 和 timezone 名称，用于写入 EmployeeCommissionResetPeriodStat 的元数据字段。
func commissionResetPeriodMeta(resetStartedAt int64) (periodKey, timezone string) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	loc, tz := commissionMonthlyStatLocation(cfg.Timezone)
	return time.Unix(resetStartedAt, 0).In(loc).Format("2006-01-02"), tz
}

// effectiveResetStartedAt 返回该条提成记录应归属的 reset_started_at。
// 若员工已有明确的 BaselineResetAt（最近一次重置时间），直接使用，使重置前后的数据
// 落在不同的 reset_started_at 桶中，从而实现"重置清空日历"的语义。
// 若尚未执行过任何重置（BaselineResetAt=0），退回到 ResolveCommissionMonthlyPeriod
// 推算出的周期起始时间，保证无重置时的数据也能正常显示。
func effectiveResetStartedAt(level *EmployeeTierLevel, createdAt int64) int64 {
	if level != nil && level.BaselineResetAt > 0 {
		return level.BaselineResetAt
	}
	return ResolveCommissionMonthlyPeriod(createdAt).PeriodStartAt
}

func bufferCommissionResetPeriodStatMem(resetStartedAt int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	if resetStartedAt <= 0 {
		return
	}
	key := memCommissionResetPeriodKey(resetStartedAt, employeeUserId)
	memCommissionResetPeriodLock.Lock()
	defer memCommissionResetPeriodLock.Unlock()
	d, ok := memCommissionResetPeriodBuf[key]
	if !ok {
		pk, tz := commissionResetPeriodMeta(resetStartedAt)
		d = &commissionResetPeriodDelta{
			ResetStartedAt: resetStartedAt,
			ResetEndedAt:   0,
			PeriodKey:      pk,
			Timezone:       tz,
			EmployeeUserId: employeeUserId,
		}
		memCommissionResetPeriodBuf[key] = d
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

func bufferCommissionResetPeriodDailyStatMem(resetStartedAt, statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota int64, createdAt int64) {
	if resetStartedAt <= 0 {
		return
	}
	key := memCommissionResetPeriodDailyKey(resetStartedAt, statDate, employeeUserId)
	memCommissionResetPeriodDailyLock.Lock()
	defer memCommissionResetPeriodDailyLock.Unlock()
	d, ok := memCommissionResetPeriodDailyBuf[key]
	if !ok {
		d = &commissionResetPeriodDailyDelta{
			ResetStartedAt: resetStartedAt,
			StatDate:       statDate,
			EmployeeUserId: employeeUserId,
		}
		memCommissionResetPeriodDailyBuf[key] = d
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
		ReportBusinessStatsFailure("platform_daily_stat", "max retries exceeded", d)
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
		ReportBusinessStatsFailure("commission_daily_stat", "max retries exceeded", d)
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
		ReportBusinessStatsFailure("customer_commission_daily_stat", "max retries exceeded", d)
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

func requeueCommissionResetPeriodStatMem(d *commissionResetPeriodDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("commission_reset_period_stat", "max retries exceeded", d.RetryCount, d)
		ReportBusinessStatsFailure("commission_reset_period_stat", "max retries exceeded", d)
		return
	}
	memCommissionResetPeriodLock.Lock()
	defer memCommissionResetPeriodLock.Unlock()
	key := memCommissionResetPeriodKey(d.ResetStartedAt, d.EmployeeUserId)
	existing := memCommissionResetPeriodBuf[key]
	if existing == nil {
		copyDelta := *d
		memCommissionResetPeriodBuf[key] = &copyDelta
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
	if d.LastCreatedAt > existing.LastCreatedAt {
		existing.LastCreatedAt = d.LastCreatedAt
	}
	if d.RetryCount > existing.RetryCount {
		existing.RetryCount = d.RetryCount
	}
}

func requeueCommissionResetPeriodDailyStatMem(d *commissionResetPeriodDailyDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= statBufferMaxRetries {
		writeBusinessStatsDeadLetter("commission_reset_period_daily_stat", "max retries exceeded", d.RetryCount, d)
		ReportBusinessStatsFailure("commission_reset_period_daily_stat", "max retries exceeded", d)
		return
	}
	memCommissionResetPeriodDailyLock.Lock()
	defer memCommissionResetPeriodDailyLock.Unlock()
	key := memCommissionResetPeriodDailyKey(d.ResetStartedAt, d.StatDate, d.EmployeeUserId)
	existing := memCommissionResetPeriodDailyBuf[key]
	if existing == nil {
		copyDelta := *d
		memCommissionResetPeriodDailyBuf[key] = &copyDelta
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

// ---- 公共入口 ----

// BufferPlatformDailyStat 将平台侧日统计增量写入进程内缓冲区。
func BufferPlatformDailyStat(rec *ConsumptionCost) {
	statDate := localDayStart(rec.CreatedAt)
	bufferPlatformStatMem(statDate, rec.ChannelId, rec.ChannelName, rec.RevenueQuota, rec.CostQuota, rec.CostRatio, rec.CreatedAt)
	ensureDailyCoverage(statDate)
}

// BufferCommissionDailyStat 将员工提成侧日统计增量写入进程内缓冲区。
func BufferCommissionDailyStat(log *EmployeeCommissionLog) {
	statDate := localDayStart(log.CreatedAt)
	level, err := GetOrCreateTierLevel(log.EmployeeUserId, true)
	if err != nil {
		common.SysError("BufferCommissionDailyStat: get tier level failed: " + err.Error())
		level = nil
	}
	resetAt := effectiveResetStartedAt(level, log.CreatedAt)
	bufferCommissionStatMem(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	bufferCustomerCommissionStatMem(statDate, log.EmployeeUserId, log.CustomerUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	bufferCommissionResetPeriodStatMem(resetAt, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	bufferCommissionResetPeriodDailyStatMem(resetAt, statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
}

// ensureDailyCoverage 确保当日 coverage 记录已落库。
// 命中进程内缓存（本进程已处理过该天）时为纯内存的 sync.Map 读，热路径零额外开销；
// 仅本进程首次遇到某天时才异步 upsert 一次，避免每笔消费都新建 goroutine（Rule 8.2）。
func ensureDailyCoverage(statDate int64) {
	if _, loaded := coveredDaysCache.LoadOrStore(statDate, struct{}{}); loaded {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("ensureDailyCoverage panic: %v", r))
				coveredDaysCache.Delete(statDate) // panic 也回退缓存，下次重试
			}
		}()
		if err := DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "stat_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"completed_at"}),
		}).Create(&BusinessDailyStatsCoverage{
			StatDate:    statDate,
			CompletedAt: time.Now().Unix(),
		}).Error; err != nil {
			common.SysError("ensureDailyCoverage: " + err.Error())
			coveredDaysCache.Delete(statDate) // 失败回退，下次重试
		}
	}()
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

func flushCommissionResetPeriodStatsFromMem() {
	memCommissionResetPeriodLock.Lock()
	buf := memCommissionResetPeriodBuf
	memCommissionResetPeriodBuf = make(map[string]*commissionResetPeriodDelta)
	memCommissionResetPeriodLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertCommissionResetPeriodStat(d.ResetStartedAt, d.ResetEndedAt, d.PeriodKey, d.Timezone, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
			requeueCommissionResetPeriodStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission reset period mem items=%d", len(buf)))
}

func flushCommissionResetPeriodDailyStatsFromMem() {
	memCommissionResetPeriodDailyLock.Lock()
	buf := memCommissionResetPeriodDailyBuf
	memCommissionResetPeriodDailyBuf = make(map[string]*commissionResetPeriodDailyDelta)
	memCommissionResetPeriodDailyLock.Unlock()

	if len(buf) == 0 {
		return
	}
	for _, d := range buf {
		if !upsertCommissionResetPeriodDailyStat(d.ResetStartedAt, d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
			requeueCommissionResetPeriodDailyStatMem(d)
		}
	}
	common.SysLog(fmt.Sprintf("flush_business_stats: commission reset period daily mem items=%d", len(buf)))
}

func upsertPlatformDailyStat(statDate int64, channelId int, channelName string, revenueQuota, costQuota, recordCount int64, costRatioSum float64, lastCreatedAt int64) bool {
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertPlatformDailyStatTx(db, statDate, channelId, channelName, revenueQuota, costQuota, recordCount, costRatioSum, lastCreatedAt); err != nil {
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
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertCommissionDailyStatTx(db, statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
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
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertCustomerCommissionDailyStatTx(db, statDate, employeeUserId, customerUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
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

func upsertCommissionResetPeriodStat(resetStartedAt, resetEndedAt int64, periodKey, timezone string, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertCommissionResetPeriodStatTx(db, resetStartedAt, resetEndedAt, periodKey, timezone, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionResetPeriodStat: resetStartedAt=%d empUserId=%d err=%s", resetStartedAt, employeeUserId, err.Error()))
		return false
	}
	return true
}

func upsertCommissionResetPeriodDailyStat(resetStartedAt, statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertCommissionResetPeriodDailyStatTx(db, resetStartedAt, statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionResetPeriodDailyStat: resetStartedAt=%d statDate=%d empUserId=%d err=%s", resetStartedAt, statDate, employeeUserId, err.Error()))
		return false
	}
	return true
}

func upsertCommissionResetPeriodStatTx(tx *gorm.DB, resetStartedAt, resetEndedAt int64, periodKey, timezone string, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) error {
	row := EmployeeCommissionResetPeriodStat{
		ResetStartedAt:  resetStartedAt,
		ResetEndedAt:    resetEndedAt,
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
		Columns: []clause.Column{{Name: "reset_started_at"}, {Name: "employee_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"reset_ended_at":   gorm.Expr("CASE WHEN reset_ended_at = 0 THEN ? ELSE reset_ended_at END", resetEndedAt),
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

func upsertCommissionResetPeriodDailyStatTx(tx *gorm.DB, resetStartedAt, statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) error {
	row := EmployeeCommissionResetPeriodDailyStat{
		ResetStartedAt:  resetStartedAt,
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
		Columns: []clause.Column{{Name: "reset_started_at"}, {Name: "stat_date"}, {Name: "employee_user_id"}},
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

// ============================================================================
// 明细台账缓冲层
//
// 高并发下避免每笔消费单条 INSERT，攒批后 CreateInBatches + ON CONFLICT DO NOTHING。
// consumption_costs: 调用方不依赖 inserted，直接缓冲。
// employee_commission_logs（成对路径）: 入队前做「权威去重」（单实例进程内 / 多实例 Redis SETNX，
//   开关控制），保证同一 log_id 全局只入队一次；刷盘 cost/commission 均 CreateInBatches 批量入库
//   并对本批全部聚合，无需逐条 RowsAffected 判重。DB 侧 ON CONFLICT(log_id) 兜底行幂等。
//
// 连接占用约束：本层在请求路径上不做任何 DB 访问，所有入库集中在单刷盘协程，峰值占用 1 个连接。
// 去重在入队时（异步结算协程，非 relay 主协程）完成；Redis 模式为每笔一次 SETNX。
// 护栏：缓冲条数有上限（超限丢最旧并计数），刷盘失败 requeue + 指数退避。
// ============================================================================

const (
	// ledgerFlushBackoffMax 刷盘连续失败的最大退避时长。
	// 上限刻意取小（关闭流程的最终刷盘也受退避约束，过长会扩大丢数窗口）。
	ledgerFlushBackoffMax = 30 * time.Second
)

// flushDBWithTimeout 返回带刷盘超时期限的 DB 句柄，调用方必须调用返回的 cancel。
// 超时时长由后台可调的 LedgerPipelineSetting.FlushDBTimeoutSec 控制（<=0 取默认 30s）；
// 所有刷盘协程内的 DB 写入都经此入口，保证单条卡死的 SQL 不会无限期占住刷盘协程
// （Rule 8：后台 worker 的外部调用也必须有超时）。
func flushDBWithTimeout() (*gorm.DB, context.CancelFunc) {
	timeout := operation_setting.GetLedgerPipelineSetting().GetFlushDBTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return DB.WithContext(ctx), cancel
}

// ledgerFlushState 单个台账的刷盘失败退避状态。
// 仅在刷盘协程内访问（调用方持有 businessStatsFlushMu），无需加锁。
type ledgerFlushState struct {
	consecFailures int
	nextRetryAt    time.Time
}

func (s *ledgerFlushState) canFlush(now time.Time) bool {
	return !now.Before(s.nextRetryAt)
}

func (s *ledgerFlushState) onSuccess() {
	s.consecFailures = 0
	s.nextRetryAt = time.Time{}
}

func (s *ledgerFlushState) onFailure(now time.Time) {
	s.consecFailures++
	interval := operation_setting.GetLedgerPipelineSetting().FlushIntervalSec
	if interval <= 0 {
		interval = DefaultBusinessStatsFlushInterval
	}
	base := time.Duration(interval) * time.Second
	shift := s.consecFailures - 1
	if shift > 4 {
		shift = 4
	}
	backoff := base * time.Duration(1<<uint(shift))
	if backoff > ledgerFlushBackoffMax {
		backoff = ledgerFlushBackoffMax
	}
	s.nextRetryAt = now.Add(backoff)
}

var (
	costLedgerFlushState       ledgerFlushState
	pairLedgerFlushState       ledgerFlushState
	commissionLedgerFlushState ledgerFlushState
)

type ledgerPipelineQueueSnapshot struct {
	Backlog         int   `json:"backlog"`
	Dropped         int64 `json:"dropped"`
	LastFlushItems  int   `json:"last_flush_items"`
	LastFlushTookMs int64 `json:"last_flush_took_ms"`
	LastFlushAt     int64 `json:"last_flush_at"`
}

type LedgerPipelineStatusSnapshot struct {
	Cost       ledgerPipelineQueueSnapshot `json:"cost"`
	Pair       ledgerPipelineQueueSnapshot `json:"pair"`
	Commission ledgerPipelineQueueSnapshot `json:"commission"`
}

var (
	ledgerPipelineStatusMu sync.RWMutex
	ledgerPipelineStatus   LedgerPipelineStatusSnapshot
)

func updateLedgerPipelineQueueSnapshot(queue string, update func(*ledgerPipelineQueueSnapshot)) {
	ledgerPipelineStatusMu.Lock()
	defer ledgerPipelineStatusMu.Unlock()

	var target *ledgerPipelineQueueSnapshot
	switch queue {
	case "cost":
		target = &ledgerPipelineStatus.Cost
	case "pair":
		target = &ledgerPipelineStatus.Pair
	case "commission":
		target = &ledgerPipelineStatus.Commission
	default:
		return
	}
	update(target)
}

func setLedgerPipelineBacklog(queue string, backlog int) {
	updateLedgerPipelineQueueSnapshot(queue, func(target *ledgerPipelineQueueSnapshot) {
		target.Backlog = backlog
	})
}

func setLedgerPipelineDropped(queue string, dropped int64) {
	updateLedgerPipelineQueueSnapshot(queue, func(target *ledgerPipelineQueueSnapshot) {
		target.Dropped = dropped
	})
}

func markLedgerPipelineFlush(queue string, items int, took time.Duration) {
	updateLedgerPipelineQueueSnapshot(queue, func(target *ledgerPipelineQueueSnapshot) {
		target.LastFlushItems = items
		target.LastFlushTookMs = took.Milliseconds()
		target.LastFlushAt = time.Now().Unix()
	})
}

func GetLedgerPipelineStatusSnapshot() LedgerPipelineStatusSnapshot {
	ledgerPipelineStatusMu.RLock()
	defer ledgerPipelineStatusMu.RUnlock()
	return ledgerPipelineStatus
}

// ---- consumption_costs 缓冲 ----

var (
	costLedgerBuf     []*ConsumptionCost
	costLedgerLock    sync.Mutex
	costLedgerDropped int64 // 累计因缓冲超限丢弃的条数，guarded by costLedgerLock
)

type costCommissionLedgerPair struct {
	Cost       *ConsumptionCost
	Commission *EmployeeCommissionLog
}

var (
	costCommissionLedgerBuf  []*costCommissionLedgerPair
	costCommissionLedgerLock sync.Mutex
	pairLedgerDropped        int64 // 累计因缓冲超限丢弃的条数，guarded by costCommissionLedgerLock
)

// BufferConsumptionCostRecord 将消费成本记录推入缓冲区，由后台批量入库。
// 缓冲超限时丢弃最旧记录并计数，防止 DB 长时间故障导致内存无限增长。
func BufferConsumptionCostRecord(rec *ConsumptionCost) {
	costLedgerLock.Lock()
	costLedgerBuf = append(costLedgerBuf, rec)
	if over := len(costLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		costLedgerBuf = costLedgerBuf[over:]
		costLedgerDropped += int64(over)
		ReportBusinessStatsFailure("consumption_cost_ledger_buffer", "buffer overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(costLedgerBuf)
	dropped := costLedgerDropped
	costLedgerLock.Unlock()
	setLedgerPipelineBacklog("cost", backlog)
	setLedgerPipelineDropped("cost", dropped)
}

// requeueCostLedger 刷盘失败时将未入库的记录放回缓冲区头部（保持最旧在前），并执行上限保护。
func requeueCostLedger(items []*ConsumptionCost) {
	if len(items) == 0 {
		return
	}
	costLedgerLock.Lock()
	costLedgerBuf = append(items, costLedgerBuf...)
	if over := len(costLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		costLedgerBuf = costLedgerBuf[over:]
		costLedgerDropped += int64(over)
		ReportBusinessStatsFailure("consumption_cost_ledger_buffer", "requeue overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(costLedgerBuf)
	dropped := costLedgerDropped
	costLedgerLock.Unlock()
	setLedgerPipelineBacklog("cost", backlog)
	setLedgerPipelineDropped("cost", dropped)
}

func flushConsumptionCostLedger() {
	if !costLedgerFlushState.canFlush(time.Now()) {
		return
	}
	costLedgerLock.Lock()
	buf := costLedgerBuf
	costLedgerBuf = nil
	droppedTotal := costLedgerDropped
	costLedgerLock.Unlock()
	setLedgerPipelineBacklog("cost", 0)
	setLedgerPipelineDropped("cost", droppedTotal)

	if len(buf) == 0 {
		return
	}
	cfg := operation_setting.GetLedgerPipelineSetting()
	outerBatch := cfg.GetOuterBatchSize()
	innerBatch := cfg.GetInnerBatchSize()
	start := time.Now()
	// 分批入库，ON CONFLICT DO NOTHING 保证幂等
	for i := 0; i < len(buf); i += outerBatch {
		end := i + outerBatch
		if end > len(buf) {
			end = len(buf)
		}
		batch := buf[i:end]
		db, cancel := flushDBWithTimeout()
		err := db.Select(
			"LogId", "UserId", "ChannelId", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(batch, innerBatch).Error
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushConsumptionCostLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			ReportBusinessStatsFailure("consumption_cost_ledger_flush", err.Error(), map[string]any{"batch_start": i, "batch_end": end})
			// 失败批次及其后的记录放回缓冲，按退避节奏重试，不再静默丢弃
			requeueCostLedger(buf[i:])
			costLedgerFlushState.onFailure(time.Now())
			return
		}
		// 入库成功后再聚合平台日统计（CreateInBatches 默认整批包在单事务里，失败已整体
		// 回滚，故这里只会聚合确实落库的记录），与成对路径一致，避免超前计数。
		for _, rec := range batch {
			if rec != nil {
				BufferPlatformDailyStat(rec)
			}
		}
	}
	costLedgerFlushState.onSuccess()
	markLedgerPipelineFlush("cost", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: consumption_costs ledger items=%d took=%dms dropped_total=%d",
		len(buf), time.Since(start).Milliseconds(), droppedTotal))
}

func bufferCostAndCommissionLedger(cost *ConsumptionCost, log *EmployeeCommissionLog) {
	costCommissionLedgerLock.Lock()
	costCommissionLedgerBuf = append(costCommissionLedgerBuf, &costCommissionLedgerPair{
		Cost:       cost,
		Commission: log,
	})
	if over := len(costCommissionLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		costCommissionLedgerBuf = costCommissionLedgerBuf[over:]
		pairLedgerDropped += int64(over)
		ReportBusinessStatsFailure("cost_commission_ledger_buffer", "buffer overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(costCommissionLedgerBuf)
	dropped := pairLedgerDropped
	costCommissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("pair", backlog)
	setLedgerPipelineDropped("pair", dropped)
}

// requeuePairLedger 刷盘失败时将未入库的成对记录放回缓冲区头部，并执行上限保护。
func requeuePairLedger(items []*costCommissionLedgerPair) {
	if len(items) == 0 {
		return
	}
	costCommissionLedgerLock.Lock()
	costCommissionLedgerBuf = append(items, costCommissionLedgerBuf...)
	if over := len(costCommissionLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		costCommissionLedgerBuf = costCommissionLedgerBuf[over:]
		pairLedgerDropped += int64(over)
		ReportBusinessStatsFailure("cost_commission_ledger_buffer", "requeue overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(costCommissionLedgerBuf)
	dropped := pairLedgerDropped
	costCommissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("pair", backlog)
	setLedgerPipelineDropped("pair", dropped)
}

func flushCostAndCommissionLedger() {
	if !pairLedgerFlushState.canFlush(time.Now()) {
		return
	}
	pairCfg := operation_setting.GetLedgerPipelineSetting()
	pairedMax := pairCfg.GetPairedFlushMaxPerCycle()
	costCommissionLedgerLock.Lock()
	buf := costCommissionLedgerBuf
	if !pairCfg.FullDrain && len(buf) > pairedMax {
		costCommissionLedgerBuf = buf[pairedMax:]
		buf = buf[:pairedMax]
	} else {
		costCommissionLedgerBuf = nil
	}
	remainingBacklog := len(costCommissionLedgerBuf)
	droppedTotal := pairLedgerDropped
	costCommissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("pair", remainingBacklog)
	setLedgerPipelineDropped("pair", droppedTotal)

	if len(buf) == 0 {
		return
	}
	outerBatch := pairCfg.GetOuterBatchSize()
	innerBatch := pairCfg.GetInnerBatchSize()
	start := time.Now()
	aggregated := 0
	for i := 0; i < len(buf); i += outerBatch {
		end := i + outerBatch
		if end > len(buf) {
			end = len(buf)
		}
		pairs := buf[i:end]
		costBatch := make([]*ConsumptionCost, 0, len(pairs))
		commBatch := make([]*EmployeeCommissionLog, 0, len(pairs))
		for _, pair := range pairs {
			costBatch = append(costBatch, pair.Cost)
			commBatch = append(commBatch, pair.Commission)
		}
		// cost 与 commission 均批量入库；行幂等由 ON CONFLICT(log_id) DO NOTHING 兜底，
		// 重复行不会落库，重试重复入库也无害。
		db, cancel := flushDBWithTimeout()
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Select(
				"LogId", "UserId", "ChannelId", "GroupName", "ModelName",
				"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
			).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "log_id"}},
				DoNothing: true,
			}).CreateInBatches(costBatch, innerBatch).Error; err != nil {
				return err
			}
			if err := tx.Select(
				"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
				"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
				"CostRatio", "GroupRatio", "CreatedAt",
			).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "log_id"}},
				DoNothing: true,
			}).CreateInBatches(commBatch, innerBatch).Error; err != nil {
				return err
			}
			return nil
		})
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushCostAndCommissionLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			ReportBusinessStatsFailure("cost_commission_ledger_flush", err.Error(), map[string]any{"batch_start": i, "batch_end": end})
			requeuePairLedger(buf[i:])
			pairLedgerFlushState.onFailure(time.Now())
			return
		}
		// 缓冲区内的记录已在入队前经过权威去重（单实例内存 / 多实例 Redis），
		// 同一 log_id 全局只会被一个进程入队一次，故此处对本批全部聚合即可，无需再判重。
		// 入库失败时上面已 requeue + return，不会执行到这里，故聚合只发生在成功提交之后。
		for _, pair := range pairs {
			if pair.Cost != nil {
				BufferPlatformDailyStat(pair.Cost)
			}
			if pair.Commission != nil {
				BufferCommissionDailyStat(pair.Commission)
				if pair.Commission.EmployeeUserId > 0 {
					BufferCommissionAndProfit(pair.Commission.EmployeeUserId, pair.Commission.CommissionQuota, pair.Commission.ProfitQuota)
				}
			}
			aggregated++
		}
	}
	pairLedgerFlushState.onSuccess()
	markLedgerPipelineFlush("pair", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: paired cost/commission ledger items=%d aggregated=%d took=%dms dropped_total=%d",
		len(buf), aggregated, time.Since(start).Milliseconds(), droppedTotal))
}

// ---- 入队前权威去重（log_id 维度）----

func ledgerDedupCommissionKey(logId int) string {
	return fmt.Sprintf("ledger:dedup:commission:%d", logId)
}

// dedupCommissionLogId 入队前权威去重，返回 true 表示首次出现（应入队 + 后续聚合）。
// 默认进程内去重（单实例足够权威）；DedupUseRedis=true 时用 Redis SETNX 跨实例去重，
// 使多实例下同一 log_id 全局只被一个进程放行。Redis 不可用/出错时降级为进程内去重
// （不丢数据；Redis 故障期间多实例可能短暂重复累加，已在设计中接受）。
func dedupCommissionLogId(logId int) bool {
	if operation_setting.GetLedgerPipelineSetting().DedupUseRedis {
		if first, ok := redisDedupCommissionLogId(logId); ok {
			return first
		}
		common.SysError("dedupCommissionLogId: redis dedup failed, fallback to in-memory")
		ReportBusinessStatsFailure("cost_commission_dedup_redis", "redis dedup failed, fell back to memory", nil)
	}
	return memDedupCommissionLogId(logId)
}

// redisDedupCommissionLogId 用 SETNX 做跨实例去重：键新建返回 first=true（首次）。
// ok=false 表示 Redis 不可用/出错，调用方据此降级到进程内去重。
func redisDedupCommissionLogId(logId int) (first bool, ok bool) {
	if !common.RedisEnabled || common.RDB == nil {
		return false, false
	}
	ttl := time.Duration(operation_setting.GetLedgerPipelineSetting().GetDedupRedisTTLSec()) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	created, err := common.RDB.SetNX(ctx, ledgerDedupCommissionKey(logId), "1", ttl).Result()
	if err != nil {
		return false, false
	}
	return created, true
}

// ---- employee_commission_logs 缓冲 ----

var (
	commissionLedgerBuf     []*EmployeeCommissionLog
	commissionLedgerLock    sync.Mutex
	commissionLedgerDropped int64 // 累计因缓冲超限丢弃的条数，guarded by commissionLedgerLock
	// 内存去重集合，存 log_id
	commissionLogIdSet     = make(map[int]struct{})
	commissionLogIdSetLock sync.Mutex
)

// memDedupCommissionLogId 进程内 log_id 去重，返回 true 表示首次出现。
// 集合超限时整体重建——重建后可能产生重复缓冲，
// 幂等性由刷盘侧 ON CONFLICT(log_id) DO NOTHING 兜底，不会重复入库。
func memDedupCommissionLogId(logId int) bool {
	commissionLogIdSetLock.Lock()
	defer commissionLogIdSetLock.Unlock()
	if _, exists := commissionLogIdSet[logId]; exists {
		return false
	}
	if len(commissionLogIdSet) >= operation_setting.GetLedgerPipelineSetting().GetDedupMemMaxEntries() {
		commissionLogIdSet = make(map[int]struct{})
	}
	commissionLogIdSet[logId] = struct{}{}
	return true
}

// appendCommissionLedger 入缓冲并执行上限保护。
func appendCommissionLedger(log *EmployeeCommissionLog) {
	commissionLedgerLock.Lock()
	commissionLedgerBuf = append(commissionLedgerBuf, log)
	if over := len(commissionLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		commissionLedgerBuf = commissionLedgerBuf[over:]
		commissionLedgerDropped += int64(over)
		ReportBusinessStatsFailure("commission_ledger_buffer", "buffer overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(commissionLedgerBuf)
	dropped := commissionLedgerDropped
	commissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("commission", backlog)
	setLedgerPipelineDropped("commission", dropped)
}

// requeueCommissionLedger 刷盘失败时将未入库的记录放回缓冲区头部，并执行上限保护。
func requeueCommissionLedger(items []*EmployeeCommissionLog) {
	if len(items) == 0 {
		return
	}
	commissionLedgerLock.Lock()
	commissionLedgerBuf = append(items, commissionLedgerBuf...)
	if over := len(commissionLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		commissionLedgerBuf = commissionLedgerBuf[over:]
		commissionLedgerDropped += int64(over)
		ReportBusinessStatsFailure("commission_ledger_buffer", "requeue overflow dropped entries", map[string]any{"dropped": over})
	}
	backlog := len(commissionLedgerBuf)
	dropped := commissionLedgerDropped
	commissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("commission", backlog)
	setLedgerPipelineDropped("commission", dropped)
}

// CheckAndBufferCommissionLog 检查 log_id 是否重复，若不重复则推入缓冲区。
// 返回 inserted=true 表示首次写入（调用方据此执行后续提成结算）。
// log_id 为 nil 时视为无幂等键，始终 inserted=true。
func CheckAndBufferCommissionLog(log *EmployeeCommissionLog) (inserted bool) {
	if log.LogId == nil || *log.LogId <= 0 {
		log.LogId = nil
		appendCommissionLedger(log)
		return true
	}
	if !memDedupCommissionLogId(*log.LogId) {
		return false
	}
	appendCommissionLedger(log)
	return true
}

// CheckAndBufferCostAndCommission 入队前权威去重后将成对记录推入缓冲，由后台刷盘批量入库。
// 去重权威性：单实例靠进程内集合，多实例靠 Redis SETNX（DedupUseRedis 开关）。去重权威后，
// 缓冲区里每个 log_id 全局只入队一次，刷盘可直接批量入库 + 全部聚合，无需逐条 RowsAffected 判重。
// 结算均经 gopool/go func 异步调用，故去重（含 Redis 调用）不在 relay 主协程上（Rule 0）。
// 返回 inserted=true 表示首次入队；false 表示重复、未入队。log_id 为 nil 时无幂等键，恒入队。
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
	if !dedupCommissionLogId(logId) {
		return false // 重复，不入队（聚合也不会发生）
	}
	bufferCostAndCommissionLedger(cost, log)
	return true
}

func flushCommissionLogLedger() {
	if !commissionLedgerFlushState.canFlush(time.Now()) {
		return
	}
	commissionLedgerLock.Lock()
	buf := commissionLedgerBuf
	commissionLedgerBuf = nil
	droppedTotal := commissionLedgerDropped
	commissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("commission", 0)
	setLedgerPipelineDropped("commission", droppedTotal)

	if len(buf) == 0 {
		return
	}
	commFlushCfg := operation_setting.GetLedgerPipelineSetting()
	outerBatch := commFlushCfg.GetOuterBatchSize()
	innerBatch := commFlushCfg.GetInnerBatchSize()
	start := time.Now()
	for i := 0; i < len(buf); i += outerBatch {
		end := i + outerBatch
		if end > len(buf) {
			end = len(buf)
		}
		batch := buf[i:end]
		db, cancel := flushDBWithTimeout()
		err := db.Select(
			"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
			"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
			"CostRatio", "GroupRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(batch, innerBatch).Error
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushCommissionLogLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			ReportBusinessStatsFailure("commission_ledger_flush", err.Error(), map[string]any{"batch_start": i, "batch_end": end})
			// 失败批次及其后的记录放回缓冲，按退避节奏重试，不再静默丢弃
			requeueCommissionLedger(buf[i:])
			commissionLedgerFlushState.onFailure(time.Now())
			return
		}
	}
	commissionLedgerFlushState.onSuccess()
	markLedgerPipelineFlush("commission", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: commission_logs ledger items=%d took=%dms dropped_total=%d",
		len(buf), time.Since(start).Milliseconds(), droppedTotal))
}

// ============================================================================
// 员工汇总缓冲层（user_extensions + tier 升级）
//
// 按 userId 累加提成/利润增量到进程内 map，定时批量写入 user_extensions，
// 避免高并发下每笔请求竞争同一行记录导致锁等待。
// ============================================================================

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

// BufferCommissionAndProfit 将提成和利润增量写入进程内缓冲区。
// 替代原来的 AddCommissionQuota + AddProfitStats 即时 DB 操作。
func BufferCommissionAndProfit(userId int, commissionDelta, profitDelta int64) {
	if commissionDelta == 0 && profitDelta == 0 {
		return
	}
	bufferEmployeeExtMem(userId, commissionDelta, profitDelta)
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
	bufferEmployeeExtMemWithRetry(userId, d.CommissionDelta, d.ProfitDelta, d.RetryCount)
}

func flushEmployeeExtBuffers() {
	items := drainEmployeeExtFromMem()
	if len(items) == 0 {
		return
	}
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
		ReportBusinessStatsFailure("employee_ext_ensure", err.Error(), userIds)
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
		ReportBusinessStatsFailure("employee_ext_apply", result.Error.Error(), map[string]any{"user_id": userId, "delta": d})
		return false
	}
	if result.RowsAffected == 0 {
		common.SysError(fmt.Sprintf("applyEmployeeExtDelta: userId=%d no rows affected", userId))
		ReportBusinessStatsFailure("employee_ext_apply", "no rows affected", map[string]any{"user_id": userId, "delta": d})
		return false
	}
	// 等级升级检查（仅利润有正增量时）
	if d.ProfitDelta > 0 {
		var ext UserExtension
		if err := DB.Select("profit_total_quota").Where("user_id = ?", userId).First(&ext).Error; err != nil {
			common.SysError(fmt.Sprintf("applyEmployeeExtDelta: read profit failed userId=%d err=%s", userId, err.Error()))
			ReportBusinessStatsFailure("employee_ext_read_profit", err.Error(), map[string]any{"user_id": userId})
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

	// 1. 先刷明细台账
	flushCostAndCommissionLedger()
	flushConsumptionCostLedger()
	flushCommissionLogLedger()
	// 2. 刷日统计增量
	// 聚合 delta 仅在 flushCostAndCommissionLedger 确认 RowsAffected=1 后才入 buffer，
	// 因此聚合永远不会超前于明细，无需在 paired 失败时阻断聚合刷盘。
	flushPlatformStatsFromMem()
	flushCommissionStatsFromMem()
	flushCustomerCommissionStatsFromMem()
	flushCommissionResetPeriodStatsFromMem()
	flushCommissionResetPeriodDailyStatsFromMem()
	// 3. 刷员工汇总（user_extensions + 等级升级）
	flushEmployeeExtBuffers()
}

// StartBusinessStatsFlushLoop 启动后台定时刷盘循环。在 main.go 中调用。
// 刷盘间隔从 LedgerPipelineSetting 动态读取（每次 sleep 前重新取值，支持不重启调整）；
// 若该值 <= 0 则回退到 DefaultBusinessStatsFlushInterval（8 秒）。
func StartBusinessStatsFlushLoop() {
	cfg := operation_setting.GetLedgerPipelineSetting()
	initInterval := cfg.FlushIntervalSec
	if initInterval <= 0 {
		initInterval = DefaultBusinessStatsFlushInterval
	}
	common.SysLog(fmt.Sprintf("business stats flush interval: %d seconds, ledger batch: outer=%d inner=%d",
		initInterval, cfg.GetOuterBatchSize(), cfg.GetInnerBatchSize()))
	go func() {
		// Drain any leftover in-memory data from before restart
		FlushBusinessStatBuffers()
		for {
			interval := operation_setting.GetLedgerPipelineSetting().FlushIntervalSec
			if interval <= 0 {
				interval = DefaultBusinessStatsFlushInterval
			}
			time.Sleep(time.Duration(interval) * time.Second)
			FlushBusinessStatBuffers()
		}
	}()
}
