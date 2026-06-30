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
	// 默认刷盘间隔（如果配置未指定或无效）
	DefaultBusinessStatsFlushInterval = 8 // 秒，均衡模式：100k RPM 下 8s×15000/cycle = 112k/min 吞吐
)

var businessStatsFlushMu sync.Mutex

// retryQueueMu 仅保护 costRetryQueue / pairRetryQueue slice 和对应 flush state，
// 与 businessStatsFlushMu 解耦，使重试 goroutine 的 DB 写入不阻塞主刷盘循环。
// 加锁顺序：若需同时持有两锁，必须先 businessStatsFlushMu 再 retryQueueMu。
var retryQueueMu sync.Mutex

var (
	flushLoopStopCh = make(chan struct{})
	flushLoopDoneCh = make(chan struct{})
	flushLoopOnce   sync.Once // guards close(flushLoopStopCh)

	retryLoopDoneCh = make(chan struct{})
)

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
	if d.RetryCount >= operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries() {
		if !writeStatPlatformFallback("max_retries_exceeded", d) {
			common.SysError("business-stats: stat_platform delta permanently lost: fallback write failed")
		}
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
	if d.RetryCount >= operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries() {
		if !writeStatCommissionFallback("max_retries_exceeded", d) {
			common.SysError("business-stats: stat_commission delta permanently lost: fallback write failed")
		}
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
	if d.RetryCount >= operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries() {
		if !writeStatCustomerCommissionFallback("max_retries_exceeded", d) {
			common.SysError("business-stats: stat_customer_commission delta permanently lost: fallback write failed")
		}
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

func requeueCommissionResetPeriodDailyStatMem(d *commissionResetPeriodDailyDelta) {
	if d == nil {
		return
	}
	d.RetryCount++
	if d.RetryCount >= operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries() {
		if !writeStatResetDailyFallback("max_retries_exceeded", d) {
			common.SysError("business-stats: stat_reset_daily delta permanently lost: fallback write failed")
		}
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
	level, err := GetOrCreateTierLevel(log.EmployeeUserId, true)
	if err != nil {
		common.SysError("BufferCommissionDailyStat: get tier level failed: " + err.Error())
		level = nil
	}
	bufferCommissionDailyStatWithLevel(log, level)
}

// bufferCommissionDailyStatWithLevel 接受调用方已预取的 tier level，避免批量刷盘时逐条查 Redis/DB。
func bufferCommissionDailyStatWithLevel(log *EmployeeCommissionLog, level *EmployeeTierLevel) {
	statDate := localDayStart(log.CreatedAt)
	resetAt := effectiveResetStartedAt(level, log.CreatedAt)
	bufferCommissionStatMem(statDate, log.EmployeeUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
	bufferCustomerCommissionStatMem(statDate, log.EmployeeUserId, log.CustomerUserId, log.RevenueQuota, log.CostQuota, log.ProfitQuota, log.CommissionQuota, log.CreatedAt)
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
	// 对本批有正利润的员工触发等级升级检查（upsert 完成后再做，避免超前于 daily stats 写入）。
	// 使用批量版本：1 次 SUM 查询替代 N 次串行查询。
	upgradeCandidates := make([]int, 0, len(buf))
	seenUpgrade := make(map[int]bool, len(buf))
	for _, d := range buf {
		if d.ProfitQuota > 0 && d.EmployeeUserId > 0 && !seenUpgrade[d.EmployeeUserId] {
			seenUpgrade[d.EmployeeUserId] = true
			upgradeCandidates = append(upgradeCandidates, d.EmployeeUserId)
		}
	}
	if len(upgradeCandidates) > 0 {
		tryAutoUpgradeTierBatch(upgradeCandidates)
	}
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
		common.SysError(fmt.Sprintf("upsertCustomerCommissionDailyStat: statDate=%d empUserId=%d custUserId=%d err=%s", statDate, employeeUserId, customerUserId, err.Error()))
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

func upsertCommissionResetPeriodDailyStat(resetStartedAt, statDate int64, employeeUserId int, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount int64, lastCreatedAt int64) bool {
	db, cancel := flushDBWithTimeout()
	defer cancel()
	if err := upsertCommissionResetPeriodDailyStatTx(db, resetStartedAt, statDate, employeeUserId, revenueQuota, costQuota, profitQuota, commissionQuota, recordCount, lastCreatedAt); err != nil {
		common.SysError(fmt.Sprintf("upsertCommissionResetPeriodDailyStat: resetStartedAt=%d statDate=%d empUserId=%d err=%s", resetStartedAt, statDate, employeeUserId, err.Error()))
		return false
	}
	return true
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

// shutdownParentCtx is set to a deadline context when ShutdownStatsFlush begins,
// so that all subsequent flushDBWithTimeout calls are also bounded by the shutdown
// deadline (whichever expires first: config DB timeout or remaining shutdown time).
var (
	shutdownParentCtx   = context.Background()
	shutdownParentCtxMu sync.Mutex
)

// flushDBWithTimeout 返回带刷盘超时期限的 DB 句柄，调用方必须调用返回的 cancel。
// 超时时长由后台可调的 LedgerPipelineSetting.FlushDBTimeoutSec 控制（<=0 取默认 30s）；
// 在 ShutdownStatsFlush 期间，parent 为 shutdown deadline context，因此实际超时取
// min(configTimeout, remainingShutdownTime)，保证进程退出不会无限期等待 DB。
func flushDBWithTimeout() (*gorm.DB, context.CancelFunc) {
	timeout := operation_setting.GetLedgerPipelineSetting().GetFlushDBTimeout()
	shutdownParentCtxMu.Lock()
	parent := shutdownParentCtx
	shutdownParentCtxMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, timeout)
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
	costLedgerFlushState ledgerFlushState
	pairLedgerFlushState ledgerFlushState

	// 独立重试队列 — 仅在持有 businessStatsFlushMu 时访问，无需单独锁。
	// 刷盘失败的批次进此队列，由 retryLoop goroutine 每 RetryFlushIntervalSec 秒独立消费，
	// 避免失败批次堵塞主缓冲新流量。
	costRetryQueue      []*costLedgerItem
	pairRetryQueue      []*costCommissionLedgerPair
	costRetryFlushState ledgerFlushState
	pairRetryFlushState ledgerFlushState
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
	CostRetry  ledgerPipelineQueueSnapshot `json:"cost_retry"`
	PairRetry  ledgerPipelineQueueSnapshot `json:"pair_retry"`
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
	case "cost_retry":
		target = &ledgerPipelineStatus.CostRetry
	case "pair_retry":
		target = &ledgerPipelineStatus.PairRetry
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
	costLedgerBuf     []*costLedgerItem
	costLedgerLock    sync.Mutex
	costLedgerDropped int64 // 累计因缓冲超限丢弃的条数，guarded by costLedgerLock
)

type costCommissionLedgerPair struct {
	Cost       *ConsumptionCost
	Commission *EmployeeCommissionLog
	RetryCount int
}

// costLedgerItem wraps a ConsumptionCost with a retry counter so that
// flush failures can be retried a bounded number of times before the
// record is written to the fallback file rather than silently dropped.
type costLedgerItem struct {
	Cost       *ConsumptionCost
	RetryCount int
}

var (
	costCommissionLedgerBuf  []*costCommissionLedgerPair
	costCommissionLedgerLock sync.Mutex
	pairLedgerDropped        int64 // 累计因缓冲超限丢弃的条数，guarded by costCommissionLedgerLock
)

// BufferConsumptionCostRecord 将消费成本记录推入缓冲区，由后台批量入库。
// 缓冲超限时将最旧记录写入 fallback 文件而非静默丢弃，防止 DB 长时间故障导致数据丢失。
func BufferConsumptionCostRecord(rec *ConsumptionCost) {
	var evicted []*costLedgerItem
	costLedgerLock.Lock()
	costLedgerBuf = append(costLedgerBuf, &costLedgerItem{Cost: rec})
	if over := len(costLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		evicted = costLedgerBuf[:over]
		costLedgerBuf = costLedgerBuf[over:]
		costLedgerDropped += int64(over)
	}
	backlog := len(costLedgerBuf)
	dropped := costLedgerDropped
	costLedgerLock.Unlock()
	setLedgerPipelineBacklog("cost", backlog)
	setLedgerPipelineDropped("cost", dropped)
	for _, item := range evicted {
		if !writeBusinessStatsFallback("business_stats_skipped", "buffer_overflow", map[string]any{"cost": item.Cost}) {
			common.SysError("business-stats: cost ledger record permanently lost: buffer_overflow fallback write failed")
		}
	}
}

// requeueCostLedger 刷盘失败时将未入库记录送入独立重试队列（costRetryQueue）。
// 超过最大重试次数的记录写入 fallback 文件而非静默丢弃；
// 重试队列溢出时同样写 fallback 文件并触发熔断计数。
// 可从主刷盘循环或重试 goroutine 调用，内部持 retryQueueMu。
func requeueCostLedger(items []*costLedgerItem) {
	if len(items) == 0 {
		return
	}
	maxRetries := operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries()
	toRequeue := items[:0:0]
	for _, item := range items {
		item.RetryCount++
		if item.RetryCount >= maxRetries {
			if !writeBusinessStatsFallback("business_stats_skipped", "max_retries_exceeded", map[string]any{"cost": item.Cost}) {
				common.SysError("business-stats: cost ledger record permanently lost: max_retries fallback write failed")
			}
			continue
		}
		toRequeue = append(toRequeue, item)
	}
	if len(toRequeue) == 0 {
		return
	}
	retryQueueMu.Lock()
	costRetryQueue = append(costRetryQueue, toRequeue...)
	if over := len(costRetryQueue) - operation_setting.GetLedgerRetryQueueSetting().GetRetryQueueMaxEntries(); over > 0 {
		evicted := costRetryQueue[:over]
		costRetryQueue = costRetryQueue[over:]
		for _, item := range evicted {
			if !writeBusinessStatsFallback("business_stats_skipped", "retry_overflow", map[string]any{"cost": item.Cost}) {
				common.SysError("business-stats: cost ledger record permanently lost: retry_overflow fallback write failed")
			}
			// 重试队列溢出意味着数据持续丢失，此时才触发熔断计数。
			ReportBusinessStatsFailure("cost_retry_overflow", "cost retry queue exceeded capacity", nil)
		}
	}
	backlog := len(costRetryQueue)
	retryQueueMu.Unlock()
	setLedgerPipelineBacklog("cost_retry", backlog)
}

func flushConsumptionCostLedger() {
	if !costLedgerFlushState.canFlush(time.Now()) {
		return
	}
	cfg := operation_setting.GetLedgerPipelineSetting()
	costMaxPerCycle := cfg.GetCostFlushMaxPerCycle()
	costLedgerLock.Lock()
	buf := costLedgerBuf
	if !cfg.FullDrain && costMaxPerCycle > 0 && len(buf) > costMaxPerCycle {
		costLedgerBuf = buf[costMaxPerCycle:]
		buf = buf[:costMaxPerCycle]
	} else {
		costLedgerBuf = nil
	}
	remainingBacklog := len(costLedgerBuf)
	droppedTotal := costLedgerDropped
	costLedgerLock.Unlock()
	setLedgerPipelineBacklog("cost", remainingBacklog)
	setLedgerPipelineDropped("cost", droppedTotal)

	if len(buf) == 0 {
		return
	}
	outerBatch := cfg.GetCostOuterBatchSize()
	innerBatch := cfg.GetInnerBatchSize()
	start := time.Now()
	// 分批入库，ON CONFLICT DO NOTHING 保证幂等
	for i := 0; i < len(buf); i += outerBatch {
		end := i + outerBatch
		if end > len(buf) {
			end = len(buf)
		}
		batchItems := buf[i:end]
		costBatch := make([]*ConsumptionCost, 0, len(batchItems))
		for _, item := range batchItems {
			costBatch = append(costBatch, item.Cost)
		}
		db, cancel := flushDBWithTimeout()
		err := db.Select(
			"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(costBatch, innerBatch).Error
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushConsumptionCostLedger: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			// 注意：此处不调用 ReportBusinessStatsFailure —— 刷盘 INSERT 失败是后台重试逻辑，
			// 不代表 relay 结算副作用不可用；误触熔断会导致新记录被跳过。
			requeueCostLedger(buf[i:])
			costLedgerFlushState.onFailure(time.Now())
			return
		}
		// 入库成功后再聚合平台日统计（CreateInBatches 默认整批包在单事务里，失败已整体
		// 回滚，故这里只会聚合确实落库的记录），与成对路径一致，避免超前计数。
		for _, item := range batchItems {
			if item.Cost != nil {
				BufferPlatformDailyStat(item.Cost)
			}
		}
	}
	costLedgerFlushState.onSuccess()
	markLedgerPipelineFlush("cost", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: consumption_costs ledger items=%d took=%dms dropped_total=%d",
		len(buf), time.Since(start).Milliseconds(), droppedTotal))
}

func bufferCostAndCommissionLedger(cost *ConsumptionCost, log *EmployeeCommissionLog) {
	var evicted []*costCommissionLedgerPair
	costCommissionLedgerLock.Lock()
	costCommissionLedgerBuf = append(costCommissionLedgerBuf, &costCommissionLedgerPair{
		Cost:       cost,
		Commission: log,
	})
	if over := len(costCommissionLedgerBuf) - operation_setting.GetLedgerPipelineSetting().GetBufMaxEntries(); over > 0 {
		evicted = costCommissionLedgerBuf[:over]
		costCommissionLedgerBuf = costCommissionLedgerBuf[over:]
		pairLedgerDropped += int64(over)
	}
	backlog := len(costCommissionLedgerBuf)
	dropped := pairLedgerDropped
	costCommissionLedgerLock.Unlock()
	setLedgerPipelineBacklog("pair", backlog)
	setLedgerPipelineDropped("pair", dropped)
	for _, p := range evicted {
		if !writeBusinessStatsFallback("cost_commission_create", "buffer_overflow", map[string]any{
			"cost": p.Cost, "commission": p.Commission,
		}) {
			common.SysError("business-stats: cost+commission pair permanently lost: buffer_overflow fallback write failed")
		}
	}
}

// requeuePairLedger 刷盘失败时将未入库的成对记录送入独立重试队列（pairRetryQueue）。
// 超过最大重试次数的记录写入 fallback 文件而非静默丢弃；
// 重试队列溢出时同样写 fallback 文件并触发熔断计数。
// 可从主刷盘循环或重试 goroutine 调用，内部持 retryQueueMu。
func requeuePairLedger(items []*costCommissionLedgerPair) {
	if len(items) == 0 {
		return
	}
	maxRetries := operation_setting.GetLedgerRetryQueueSetting().GetStatUpsertMaxRetries()
	toRequeue := items[:0:0]
	for _, item := range items {
		item.RetryCount++
		if item.RetryCount >= maxRetries {
			if !writeBusinessStatsFallback("cost_commission_create", "max_retries_exceeded", map[string]any{
				"cost": item.Cost, "commission": item.Commission,
			}) {
				common.SysError("business-stats: cost+commission pair permanently lost: max_retries fallback write failed")
			}
			continue
		}
		toRequeue = append(toRequeue, item)
	}
	if len(toRequeue) == 0 {
		return
	}
	retryQueueMu.Lock()
	pairRetryQueue = append(pairRetryQueue, toRequeue...)
	if over := len(pairRetryQueue) - operation_setting.GetLedgerRetryQueueSetting().GetRetryQueueMaxEntries(); over > 0 {
		evicted := pairRetryQueue[:over]
		pairRetryQueue = pairRetryQueue[over:]
		for _, p := range evicted {
			if !writeBusinessStatsFallback("cost_commission_create", "retry_overflow", map[string]any{
				"cost": p.Cost, "commission": p.Commission,
			}) {
				common.SysError("business-stats: cost+commission pair permanently lost: retry_overflow fallback write failed")
			}
			ReportBusinessStatsFailure("pair_retry_overflow", "pair retry queue exceeded capacity", nil)
		}
	}
	backlog := len(pairRetryQueue)
	retryQueueMu.Unlock()
	setLedgerPipelineBacklog("pair_retry", backlog)
}

func flushCostAndCommissionLedger() {
	if !pairLedgerFlushState.canFlush(time.Now()) {
		return
	}
	pairCfg := operation_setting.GetLedgerPipelineSetting()
	pairedMax := pairCfg.GetSettlementFlushMaxPerCycle()
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
				"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
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
			// 注意：此处不调用 ReportBusinessStatsFailure —— 刷盘 INSERT 失败是后台重试逻辑，
			// 不代表 relay 结算副作用不可用；误触熔断会导致新记录被跳过。
			requeuePairLedger(buf[i:])
			pairLedgerFlushState.onFailure(time.Now())
			return
		}
		// 缓冲区内的记录已在入队前经过权威去重（单实例内存 / 多实例 Redis），
		// 同一 log_id 全局只会被一个进程入队一次，故此处对本批全部聚合即可，无需再判重。
		// 入库失败时上面已 requeue + return，不会执行到这里，故聚合只发生在成功提交之后。

		// 预取本批次所有唯一员工的 tier level，避免逐条查 Redis/DB（瓶颈：N 条记录 × 1 次 Redis 查询）。
		tierCache := make(map[int]*EmployeeTierLevel, len(pairs))
		for _, pair := range pairs {
			if pair.Commission != nil && pair.Commission.EmployeeUserId > 0 {
				tierCache[pair.Commission.EmployeeUserId] = nil
			}
		}
		for uid := range tierCache {
			level, err := GetOrCreateTierLevel(uid, true)
			if err != nil {
				common.SysError(fmt.Sprintf("flushCostAndCommissionLedger: get tier level for user %d failed: %s", uid, err.Error()))
			}
			tierCache[uid] = level
		}

		for _, pair := range pairs {
			if pair.Cost != nil {
				BufferPlatformDailyStat(pair.Cost)
			}
			if pair.Commission != nil {
				bufferCommissionDailyStatWithLevel(pair.Commission, tierCache[pair.Commission.EmployeeUserId])
			}
			aggregated++
		}
	}
	pairLedgerFlushState.onSuccess()
	markLedgerPipelineFlush("pair", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: paired cost/commission ledger items=%d aggregated=%d took=%dms dropped_total=%d",
		len(buf), aggregated, time.Since(start).Milliseconds(), droppedTotal))
}

// ---- 独立重试队列刷盘 ----

// flushCostRetryQueue 消费 costRetryQueue 中积压的失败记录。
// flushCostRetryQueue 消费 costRetryQueue 中的失败记录，由重试 goroutine 调用。
// 仅在 drain/state 更新时持 retryQueueMu；DB 写入期间不持任何大锁，不阻塞主刷盘循环。
func flushCostRetryQueue() {
	retryQueueMu.Lock()
	if !costRetryFlushState.canFlush(time.Now()) || len(costRetryQueue) == 0 {
		retryQueueMu.Unlock()
		return
	}
	buf := costRetryQueue
	costRetryQueue = nil
	retryQueueMu.Unlock()

	cfg := operation_setting.GetLedgerPipelineSetting()
	outerBatch := cfg.GetCostOuterBatchSize()
	innerBatch := cfg.GetInnerBatchSize()
	start := time.Now()
	for i := 0; i < len(buf); i += outerBatch {
		end := i + outerBatch
		if end > len(buf) {
			end = len(buf)
		}
		batchItems := buf[i:end]
		costBatch := make([]*ConsumptionCost, 0, len(batchItems))
		for _, item := range batchItems {
			costBatch = append(costBatch, item.Cost)
		}
		db, cancel := flushDBWithTimeout()
		err := db.Select(
			"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(costBatch, innerBatch).Error
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushCostRetryQueue: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			requeueCostLedger(buf[i:]) // 内部持 retryQueueMu
			retryQueueMu.Lock()
			costRetryFlushState.onFailure(time.Now())
			retryQueueMu.Unlock()
			return
		}
		for _, item := range batchItems {
			if item.Cost != nil {
				BufferPlatformDailyStat(item.Cost)
			}
		}
	}
	retryQueueMu.Lock()
	costRetryFlushState.onSuccess()
	retryQueueMu.Unlock()
	setLedgerPipelineBacklog("cost_retry", 0)
	markLedgerPipelineFlush("cost_retry", len(buf), time.Since(start))
}

// flushPairRetryQueue 消费 pairRetryQueue 中的失败记录，由重试 goroutine 调用。
// 仅在 drain/state 更新时持 retryQueueMu；DB 写入期间不持任何大锁，不阻塞主刷盘循环。
func flushPairRetryQueue() {
	retryQueueMu.Lock()
	if !pairRetryFlushState.canFlush(time.Now()) || len(pairRetryQueue) == 0 {
		retryQueueMu.Unlock()
		return
	}
	buf := pairRetryQueue
	pairRetryQueue = nil
	retryQueueMu.Unlock()

	cfg := operation_setting.GetLedgerPipelineSetting()
	outerBatch := cfg.GetOuterBatchSize()
	innerBatch := cfg.GetInnerBatchSize()
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
			if pair.Commission != nil {
				commBatch = append(commBatch, pair.Commission)
			}
		}
		db, cancel := flushDBWithTimeout()
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Select(
				"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
				"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
			).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "log_id"}},
				DoNothing: true,
			}).CreateInBatches(costBatch, innerBatch).Error; err != nil {
				return err
			}
			if len(commBatch) > 0 {
				if err := tx.Select(
					"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName",
					"ChannelId", "RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota",
					"CommissionRate", "CostRatio", "GroupRatio", "CreatedAt",
				).Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "log_id"}},
					DoNothing: true,
				}).CreateInBatches(commBatch, innerBatch).Error; err != nil {
					return err
				}
			}
			return nil
		})
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("flushPairRetryQueue: batch insert error (batch %d-%d): %s", i, end, err.Error()))
			requeuePairLedger(buf[i:]) // 内部持 retryQueueMu
			retryQueueMu.Lock()
			pairRetryFlushState.onFailure(time.Now())
			retryQueueMu.Unlock()
			return
		}
		for _, pair := range pairs {
			if pair.Cost != nil {
				BufferPlatformDailyStat(pair.Cost)
			}
			if pair.Commission != nil {
				BufferCommissionDailyStat(pair.Commission)
			}
			aggregated++
		}
	}
	retryQueueMu.Lock()
	pairRetryFlushState.onSuccess()
	retryQueueMu.Unlock()
	setLedgerPipelineBacklog("pair_retry", 0)
	markLedgerPipelineFlush("pair_retry", len(buf), time.Since(start))
	common.SysLog(fmt.Sprintf("flush_business_stats: pair retry queue items=%d aggregated=%d took=%dms",
		len(buf), aggregated, time.Since(start).Milliseconds()))
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

// ============================================================================
// 员工汇总缓冲层（user_extensions + tier 升级）
//
// 按 userId 累加提成/利润增量到进程内 map，定时批量写入 user_extensions，
// 避免高并发下每笔请求竞争同一行记录导致锁等待。
// ============================================================================

// employeeExtDelta 单个员工在一个刷盘周期内的累计增量。
// ---- 刷盘入口 ----

// FlushBusinessStatBuffers 将缓冲区的增量批量刷入 DB。由后台定时任务调用。
func FlushBusinessStatBuffers() {
	businessStatsFlushMu.Lock()
	defer businessStatsFlushMu.Unlock()

	// 1. 先刷明细台账
	flushCostAndCommissionLedger()
	flushConsumptionCostLedger()
	// 2. 刷日统计增量
	// 聚合 delta 仅在 flushCostAndCommissionLedger 确认 RowsAffected=1 后才入 buffer，
	// 因此聚合永远不会超前于明细，无需在 paired 失败时阻断聚合刷盘。
	flushPlatformStatsFromMem()
	flushCommissionStatsFromMem()
	flushCustomerCommissionStatsFromMem()
	flushCommissionResetPeriodDailyStatsFromMem() // 包含等级升级检查
}

// StartBusinessStatsFlushLoop 启动后台定时刷盘循环和独立重试循环。在 main.go 中调用。
// 主循环间隔：LedgerPipelineSetting.FlushIntervalSec（默认 8s）。
// 重试循环间隔：LedgerPipelineSetting.RetryFlushIntervalSec（默认 2s）。
// 收到 flushLoopStopCh 信号后两个 goroutine 均退出，分别关闭 flushLoopDoneCh / retryLoopDoneCh。
func StartBusinessStatsFlushLoop() {
	cfg := operation_setting.GetLedgerPipelineSetting()
	InitFallbackQueue(cfg.GetFallbackQueueCapacity())
	initInterval := cfg.FlushIntervalSec
	if initInterval <= 0 {
		initInterval = DefaultBusinessStatsFlushInterval
	}
	common.SysLog(fmt.Sprintf("business stats flush interval: %d seconds, retry interval: %d seconds, ledger batch: outer=%d inner=%d",
		initInterval, operation_setting.GetLedgerRetryQueueSetting().GetRetryFlushIntervalSec(), cfg.GetOuterBatchSize(), cfg.GetInnerBatchSize()))

	// 主刷盘 goroutine
	go func() {
		defer close(flushLoopDoneCh)
		FlushBusinessStatBuffers()
		for {
			interval := operation_setting.GetLedgerPipelineSetting().FlushIntervalSec
			if interval <= 0 {
				interval = DefaultBusinessStatsFlushInterval
			}
			select {
			case <-time.After(time.Duration(interval) * time.Second):
				FlushBusinessStatBuffers()
			case <-flushLoopStopCh:
				return
			}
		}
	}()

	// 独立重试 goroutine — 每 RetryFlushIntervalSec 消费 costRetryQueue / pairRetryQueue。
	// 通过 TryLock(businessStatsFlushMu) 与主刷盘互斥，避免并发写同一 DB 表竞争连接池：
	//   主刷盘持锁期间 → 重试跳过本轮，等下一个 retryInterval 再尝试；
	//   主刷盘空闲时   → 重试持锁写入并释放；若主刷盘此时触发，等待重试完成（写入量小，通常 < 1 s）。
	// 共享队列 slice / flush state 通过 retryQueueMu 保护。
	go func() {
		defer close(retryLoopDoneCh)
		for {
			interval := operation_setting.GetLedgerRetryQueueSetting().GetRetryFlushIntervalSec()
			select {
			case <-time.After(time.Duration(interval) * time.Second):
				if operation_setting.GetLedgerRetryQueueSetting().AllowConcurrentFlush {
					// 允许并发：与主刷盘同时写库，重试吞吐更高，但可能竞争连接池。
					flushCostRetryQueue()
					flushPairRetryQueue()
				} else if businessStatsFlushMu.TryLock() {
					// 互斥模式（默认）：主刷盘持锁时跳过本轮，避免同时写相同 DB 表。
					flushCostRetryQueue()
					flushPairRetryQueue()
					businessStatsFlushMu.Unlock()
				}
			case <-flushLoopStopCh:
				return
			}
		}
	}()
}

// ShutdownStatsFlush 是优雅退出的入口：
//  1. 停止后台 flush loop goroutine；
//  2. 对所有内存缓冲做最终一次 DB 刷盘；
//  3. 把 DB 写失败后仍留在内存的数据 drain 到 fallback 文件（与熔断器格式相同，可用 Backfill 恢复）；
//  4. 刷出并关闭 fallback 文件 fd（Windows 进程退出前必须释放 fd）。
//
// timeout 建议传 20–25 s，为后续 HTTP Shutdown 留出时间。
// timeout 同时作为所有 DB flush 调用的父 context deadline（与 GetFlushDBTimeout 取较小值），
// 确保单条卡死的 SQL 不会使进程挂死超过 timeout。
func ShutdownStatsFlush(timeout time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("ShutdownStatsFlush: panic recovered: %v", r))
		}
	}()

	// 将 shutdown deadline 注入 flushDBWithTimeout，使所有 DB 操作受 timeout 约束。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)
	defer shutdownCancel()
	shutdownParentCtxMu.Lock()
	shutdownParentCtx = shutdownCtx
	shutdownParentCtxMu.Unlock()
	defer func() {
		shutdownParentCtxMu.Lock()
		shutdownParentCtx = context.Background()
		shutdownParentCtxMu.Unlock()
	}()

	// 1. 通知所有后台 goroutine 退出（sync.Once 防止重复 close）。
	flushLoopOnce.Do(func() { close(flushLoopStopCh) })

	// 等待两个 goroutine 退出，最多 5 秒或剩余 timeout 的一半（取较小值）。
	goroutineWait := 5 * time.Second
	if half := timeout / 2; half < goroutineWait {
		goroutineWait = half
	}
	waitCh := make(chan struct{})
	go func() {
		<-flushLoopDoneCh
		<-retryLoopDoneCh
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-time.After(goroutineWait):
		common.SysLog("ShutdownStatsFlush: goroutine stop timeout, proceeding")
	}

	// 2+3. 最终刷盘 + drain。持有 businessStatsFlushMu 保证与 goroutine 互斥。
	// TryLock 轮询直至 shutdownCtx 超期，防止 goroutine 意外永久阻塞导致进程挂死。
	// 正常情况下 goroutine 会在 shutdownCtx 超期前释放锁（其内部 DB 操作均受同一 ctx 约束）。
	const lockPollInterval = 10 * time.Millisecond
	lockAcquired := false
	for {
		if businessStatsFlushMu.TryLock() {
			lockAcquired = true
			break
		}
		select {
		case <-shutdownCtx.Done():
		case <-time.After(lockPollInterval):
			continue
		}
		break
	}
	if !lockAcquired {
		common.SysError("ShutdownStatsFlush: timed out waiting for flush mutex; remaining in-memory buffer data may be lost")
		FlushAndCloseFallbackFiles()
		common.SysLog("ShutdownStatsFlush: complete (timeout)")
		return
	}
	if shutdownCtx.Err() == nil {
		// 先刷主缓冲明细台账，再消费重试队列（重试队列内容源自主缓冲刷盘失败，顺序一致）。
		flushCostAndCommissionLedger()
		flushConsumptionCostLedger()
		flushPairRetryQueue()
		flushCostRetryQueue()
		flushPlatformStatsFromMem()
		flushCommissionStatsFromMem()
		flushCustomerCommissionStatsFromMem()
		flushCommissionResetPeriodDailyStatsFromMem()
	} else {
		common.SysLog("ShutdownStatsFlush: deadline reached before final flush; draining buffer to fallback file")
	}
	drainRemainingToFallbackFile("shutdown_drain")
	businessStatsFlushMu.Unlock()

	// 4. 刷队列、关闭所有 fallback 文件 fd。
	FlushAndCloseFallbackFiles()
	common.SysLog("ShutdownStatsFlush: complete")
}

// drainRemainingToFallbackFile 将所有内存缓冲中仍有数据的条目序列化并写入
// fallback 文件，供后续 Backfill 恢复。调用方必须持有 businessStatsFlushMu。
// kind 与熔断器写文件时一致，Backfill 无需感知数据来源。
func drainRemainingToFallbackFile(reason string) {
	// ── cost+commission pairs (主缓冲 + 重试队列) ──────────────────────────
	costCommissionLedgerLock.Lock()
	pairs := costCommissionLedgerBuf
	costCommissionLedgerBuf = nil
	costCommissionLedgerLock.Unlock()
	// 调用时重试 goroutine 已停止（ShutdownStatsFlush 等待 retryLoopDoneCh），
	// retryQueueMu 仍加锁以维持一致的访问规范。
	retryQueueMu.Lock()
	pairs = append(pairs, pairRetryQueue...)
	pairRetryQueue = nil
	retryQueueMu.Unlock()
	for _, p := range pairs {
		if p == nil {
			continue
		}
		if !writeBusinessStatsFallback("cost_commission_create", reason, map[string]any{
			"cost":       p.Cost,
			"commission": p.Commission,
		}) {
			common.SysError("business-stats: cost+commission pair permanently lost during drain: fallback write failed")
		}
	}

	// ── cost-only records (主缓冲 + 重试队列) ────────────────────────────
	costLedgerLock.Lock()
	costItems := costLedgerBuf
	costLedgerBuf = nil
	costLedgerLock.Unlock()
	retryQueueMu.Lock()
	costItems = append(costItems, costRetryQueue...)
	costRetryQueue = nil
	retryQueueMu.Unlock()
	for _, item := range costItems {
		if item == nil || item.Cost == nil {
			continue
		}
		if !writeBusinessStatsFallback("business_stats_skipped", reason, map[string]any{
			"cost": item.Cost,
		}) {
			common.SysError("business-stats: cost ledger record permanently lost during drain: fallback write failed")
		}
	}

	// ── stat buffers ──────────────────────────────────────────────────────
	memPlatformLock.Lock()
	platform := memPlatformBuf
	memPlatformBuf = make(map[string]*platformStatDelta)
	memPlatformLock.Unlock()
	for _, d := range platform {
		if !writeStatPlatformFallback(reason, d) {
			common.SysError("business-stats: stat_platform delta permanently lost during drain: fallback write failed")
		}
	}

	memCommissionLock.Lock()
	commission := memCommissionBuf
	memCommissionBuf = make(map[string]*commissionStatDelta)
	memCommissionLock.Unlock()
	for _, d := range commission {
		if !writeStatCommissionFallback(reason, d) {
			common.SysError("business-stats: stat_commission delta permanently lost during drain: fallback write failed")
		}
	}

	memCustomerCommissionLock.Lock()
	customerCommission := memCustomerCommissionBuf
	memCustomerCommissionBuf = make(map[string]*customerCommissionStatDelta)
	memCustomerCommissionLock.Unlock()
	for _, d := range customerCommission {
		if !writeStatCustomerCommissionFallback(reason, d) {
			common.SysError("business-stats: stat_customer_commission delta permanently lost during drain: fallback write failed")
		}
	}

	memCommissionResetPeriodDailyLock.Lock()
	resetDaily := memCommissionResetPeriodDailyBuf
	memCommissionResetPeriodDailyBuf = make(map[string]*commissionResetPeriodDailyDelta)
	memCommissionResetPeriodDailyLock.Unlock()
	for _, d := range resetDaily {
		if !writeStatResetDailyFallback(reason, d) {
			common.SysError("business-stats: stat_reset_daily delta permanently lost during drain: fallback write failed")
		}
	}

	statItems := len(platform) + len(commission) + len(customerCommission) + len(resetDaily)
	total := len(pairs) + len(costItems) + statItems
	if total > 0 {
		common.SysLog(fmt.Sprintf(
			"ShutdownStatsFlush: drained %d items to fallback file (pairs=%d costs=%d stats=%d)",
			total, len(pairs), len(costItems), statItems,
		))
	}
}

// ============================================================================
// 回填重放 — 由 service/business_stats_backfill 调用
// ============================================================================

// WriteBackfillDeadLetter 将无法解析的原始日志行写入死信文件。
func WriteBackfillDeadLetter(rawLine []byte, reason string) {
	writeBusinessStatsDeadLetter("parse_error", reason, 0, string(rawLine))
}

// ReplayStatPlatformFallback 反序列化 stat_platform payload 并直接 upsert 到 DB。
func ReplayStatPlatformFallback(payloadBytes []byte) error {
	var d platformStatDelta
	if err := common.Unmarshal(payloadBytes, &d); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if !upsertPlatformDailyStat(d.StatDate, d.ChannelId, d.ChannelName, d.RevenueQuota, d.CostQuota, d.RecordCount, d.CostRatioSum, d.LastCreatedAt) {
		return fmt.Errorf("upsert failed")
	}
	return nil
}

// ReplayStatCommissionFallback 反序列化 stat_commission payload 并直接 upsert 到 DB。
func ReplayStatCommissionFallback(payloadBytes []byte) error {
	var d commissionStatDelta
	if err := common.Unmarshal(payloadBytes, &d); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if !upsertCommissionDailyStat(d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
		return fmt.Errorf("upsert failed")
	}
	return nil
}

// ReplayStatCustomerCommissionFallback 反序列化 stat_customer_commission payload 并直接 upsert 到 DB。
func ReplayStatCustomerCommissionFallback(payloadBytes []byte) error {
	var d customerCommissionStatDelta
	if err := common.Unmarshal(payloadBytes, &d); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if !upsertCustomerCommissionDailyStat(d.StatDate, d.EmployeeUserId, d.CustomerUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
		return fmt.Errorf("upsert failed")
	}
	return nil
}

// ReplayStatResetDailyFallback 反序列化 stat_reset_daily payload 并直接 upsert 到 DB。
func ReplayStatResetDailyFallback(payloadBytes []byte) error {
	var d commissionResetPeriodDailyDelta
	if err := common.Unmarshal(payloadBytes, &d); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if !upsertCommissionResetPeriodDailyStat(d.ResetStartedAt, d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt) {
		return fmt.Errorf("upsert failed")
	}
	return nil
}

// ReplayStatEmployeeExtFallback 是历史 fallback 文件中 stat_employee_ext 条目的重放入口。
// user_extensions 的 profit/commission 累计字段已废弃（不再写入），历史 fallback 条目直接跳过。
func ReplayStatEmployeeExtFallback(payloadBytes []byte) error {
	common.SysLog(fmt.Sprintf("ReplayStatEmployeeExtFallback: skipping legacy stat_employee_ext entry (%d bytes)", len(payloadBytes)))
	return nil
}
