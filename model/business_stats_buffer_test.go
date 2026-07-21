package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// bsClearMemBuffers resets every package-global in-memory buffer and flush state
// so a test never sees leftovers from an earlier one (no background loop runs in
// tests, so buffers only ever hold what a test explicitly enqueues).
func bsClearMemBuffers() {
	memPlatformLock.Lock()
	memPlatformBuf = make(map[string]*platformStatDelta)
	memPlatformLock.Unlock()
	memCommissionLock.Lock()
	memCommissionBuf = make(map[string]*commissionStatDelta)
	memCommissionLock.Unlock()
	memCustomerCommissionLock.Lock()
	memCustomerCommissionBuf = make(map[string]*customerCommissionStatDelta)
	memCustomerCommissionLock.Unlock()
	memCommissionResetPeriodDailyLock.Lock()
	memCommissionResetPeriodDailyBuf = make(map[string]*commissionResetPeriodDailyDelta)
	memCommissionResetPeriodDailyLock.Unlock()

	costLedgerLock.Lock()
	costLedgerBuf = nil
	costLedgerDropped = 0
	costLedgerLock.Unlock()
	costCommissionLedgerLock.Lock()
	costCommissionLedgerBuf = nil
	pairLedgerDropped = 0
	costCommissionLedgerLock.Unlock()

	retryQueueMu.Lock()
	costRetryQueue = nil
	pairRetryQueue = nil
	costRetryFlushState = ledgerFlushState{}
	pairRetryFlushState = ledgerFlushState{}
	retryQueueMu.Unlock()

	costLedgerFlushState = ledgerFlushState{}
	pairLedgerFlushState = ledgerFlushState{}

	commissionLogIdSetLock.Lock()
	commissionLogIdSet = make(map[int]struct{})
	commissionLogIdSetLock.Unlock()
}

// ---------------------------------------------------------------------------
// key helpers
// ---------------------------------------------------------------------------

func TestMemKeys(t *testing.T) {
	assert.Equal(t, "100:7", memPlatformKey(100, 7))
	assert.Equal(t, "100:7", memCommissionKey(100, 7))
	assert.Equal(t, "100:7:9", memCustomerCommissionKey(100, 7, 9))
	assert.Equal(t, "5:100:7", memCommissionResetPeriodDailyKey(5, 100, 7))
}

// ---------------------------------------------------------------------------
// in-memory aggregation
// ---------------------------------------------------------------------------

func TestBufferPlatformStatMem_Aggregate(t *testing.T) {
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	ch := nextTestID()
	bufferPlatformStatMem(statDate, ch, "name-a", 100, 40, 0.8, 111)
	bufferPlatformStatMem(statDate, ch, "ignored", 50, 10, 0.2, 222) // later createdAt wins
	bufferPlatformStatMem(statDate, ch, "", 25, 5, 0.1, 50)          // earlier createdAt ignored

	memPlatformLock.Lock()
	d := memPlatformBuf[memPlatformKey(statDate, ch)]
	memPlatformLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 175, d.RevenueQuota)
	assert.EqualValues(t, 55, d.CostQuota)
	assert.EqualValues(t, 3, d.RecordCount)
	assert.InDelta(t, 1.1, d.CostRatioSum, 1e-9)
	assert.Equal(t, "name-a", d.ChannelName) // first non-empty kept
	assert.EqualValues(t, 222, d.LastCreatedAt)
	bsClearMemBuffers()
}

func TestBufferCommissionStatMem_Aggregate(t *testing.T) {
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	emp := nextTestID()
	bufferCommissionStatMem(statDate, emp, 100, 40, 60, 6, 10)
	bufferCommissionStatMem(statDate, emp, 200, 80, 120, 12, 5)
	memCommissionLock.Lock()
	d := memCommissionBuf[memCommissionKey(statDate, emp)]
	memCommissionLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 300, d.RevenueQuota)
	assert.EqualValues(t, 120, d.CostQuota)
	assert.EqualValues(t, 180, d.ProfitQuota)
	assert.EqualValues(t, 18, d.CommissionQuota)
	assert.EqualValues(t, 2, d.RecordCount)
	assert.EqualValues(t, 10, d.LastCreatedAt)
	bsClearMemBuffers()
}

func TestBufferCustomerCommissionStatMem_Aggregate(t *testing.T) {
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	emp, cust := nextTestID(), nextTestID()
	bufferCustomerCommissionStatMem(statDate, emp, cust, 10, 4, 6, 1, 1)
	bufferCustomerCommissionStatMem(statDate, emp, cust, 20, 8, 12, 2, 3)
	memCustomerCommissionLock.Lock()
	d := memCustomerCommissionBuf[memCustomerCommissionKey(statDate, emp, cust)]
	memCustomerCommissionLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 30, d.RevenueQuota)
	assert.EqualValues(t, 18, d.ProfitQuota)
	assert.EqualValues(t, 2, d.RecordCount)
	assert.EqualValues(t, 3, d.LastCreatedAt)
	bsClearMemBuffers()
}

func TestBufferCommissionResetPeriodDailyStatMem(t *testing.T) {
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	emp := nextTestID()

	// resetStartedAt <= 0 -> skipped entirely
	bufferCommissionResetPeriodDailyStatMem(0, statDate, emp, 100, 40, 60, 6, 1)
	memCommissionResetPeriodDailyLock.Lock()
	assert.Empty(t, memCommissionResetPeriodDailyBuf)
	memCommissionResetPeriodDailyLock.Unlock()

	// resetStartedAt > 0 -> accumulated
	resetAt := int64(4_100_000_000)
	bufferCommissionResetPeriodDailyStatMem(resetAt, statDate, emp, 100, 40, 60, 6, 1)
	bufferCommissionResetPeriodDailyStatMem(resetAt, statDate, emp, 50, 20, 30, 3, 9)
	memCommissionResetPeriodDailyLock.Lock()
	d := memCommissionResetPeriodDailyBuf[memCommissionResetPeriodDailyKey(resetAt, statDate, emp)]
	memCommissionResetPeriodDailyLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 150, d.RevenueQuota)
	assert.EqualValues(t, 90, d.ProfitQuota)
	assert.EqualValues(t, 2, d.RecordCount)
	assert.EqualValues(t, 9, d.LastCreatedAt)
	bsClearMemBuffers()
}

func TestEffectiveResetStartedAt(t *testing.T) {
	createdAt := time.Now().Unix()
	// nil level -> monthly period start
	want := ResolveCommissionMonthlyPeriod(createdAt).PeriodStartAt
	assert.Equal(t, want, effectiveResetStartedAt(nil, createdAt))
	// level with BaselineResetAt>0 -> that value
	assert.EqualValues(t, 12345, effectiveResetStartedAt(&EmployeeTierLevel{BaselineResetAt: 12345}, createdAt))
	// level with BaselineResetAt==0 -> monthly period start
	assert.Equal(t, want, effectiveResetStartedAt(&EmployeeTierLevel{BaselineResetAt: 0}, createdAt))
}

// ---------------------------------------------------------------------------
// requeue (retry accounting)
// ---------------------------------------------------------------------------

func TestRequeuePlatformStatMem(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 3
	})

	statDate := int64(4_200_000_000)
	ch := nextTestID()

	// fresh delta -> RetryCount becomes 1, re-buffered
	d := &platformStatDelta{StatDate: statDate, ChannelId: ch, RevenueQuota: 100, RecordCount: 1}
	requeuePlatformStatMem(d)
	memPlatformLock.Lock()
	got := memPlatformBuf[memPlatformKey(statDate, ch)]
	memPlatformLock.Unlock()
	require.NotNil(t, got)
	assert.Equal(t, 1, got.RetryCount)
	assert.EqualValues(t, 100, got.RevenueQuota)

	// requeue a delta that is already at max-1 -> exceeds max, dropped to fallback (not buffered)
	bsClearMemBuffers()
	ch2 := nextTestID()
	d2 := &platformStatDelta{StatDate: statDate, ChannelId: ch2, RevenueQuota: 5, RetryCount: 2}
	requeuePlatformStatMem(d2) // -> 3 >= 3 -> fallback
	memPlatformLock.Lock()
	assert.Nil(t, memPlatformBuf[memPlatformKey(statDate, ch2)])
	memPlatformLock.Unlock()

	// nil is a no-op
	requeuePlatformStatMem(nil)
	bsClearMemBuffers()
}

func TestRequeueCustomerAndResetPeriodStatMem(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 5
	})
	statDate := int64(4_200_000_000)
	emp, cust := nextTestID(), nextTestID()

	// customer commission requeue: fresh delta -> re-buffered with RetryCount=1
	requeueCustomerCommissionStatMem(&customerCommissionStatDelta{StatDate: statDate, EmployeeUserId: emp, CustomerUserId: cust, RevenueQuota: 10, RecordCount: 1})
	memCustomerCommissionLock.Lock()
	cd := memCustomerCommissionBuf[memCustomerCommissionKey(statDate, emp, cust)]
	memCustomerCommissionLock.Unlock()
	require.NotNil(t, cd)
	assert.Equal(t, 1, cd.RetryCount)
	requeueCustomerCommissionStatMem(nil) // no-op

	// reset-period requeue: fresh delta -> re-buffered
	resetAt := int64(4_100_000_000)
	requeueCommissionResetPeriodDailyStatMem(&commissionResetPeriodDailyDelta{ResetStartedAt: resetAt, StatDate: statDate, EmployeeUserId: emp, ProfitQuota: 60, RecordCount: 1})
	memCommissionResetPeriodDailyLock.Lock()
	rd := memCommissionResetPeriodDailyBuf[memCommissionResetPeriodDailyKey(resetAt, statDate, emp)]
	memCommissionResetPeriodDailyLock.Unlock()
	require.NotNil(t, rd)
	assert.Equal(t, 1, rd.RetryCount)
	requeueCommissionResetPeriodDailyStatMem(nil) // no-op
	bsClearMemBuffers()
}

func TestRequeueCommissionStatMem_MergesIntoExisting(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 5
	})
	statDate := int64(4_200_000_000)
	emp := nextTestID()
	// pre-seed the buffer, then requeue an overlapping delta -> merged
	bufferCommissionStatMem(statDate, emp, 100, 40, 60, 6, 1)
	requeueCommissionStatMem(&commissionStatDelta{StatDate: statDate, EmployeeUserId: emp, RevenueQuota: 50, ProfitQuota: 30, RecordCount: 1, RetryCount: 1})
	memCommissionLock.Lock()
	d := memCommissionBuf[memCommissionKey(statDate, emp)]
	memCommissionLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 150, d.RevenueQuota)
	assert.EqualValues(t, 90, d.ProfitQuota)
	assert.EqualValues(t, 2, d.RecordCount)
	bsClearMemBuffers()
}

// ---------------------------------------------------------------------------
// ledgerFlushState backoff
// ---------------------------------------------------------------------------

func TestLedgerFlushStateBackoff(t *testing.T) {
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.FlushIntervalSec = 8
	})
	now := time.Now()
	var s ledgerFlushState

	assert.True(t, s.canFlush(now)) // fresh: no retry gate

	s.onFailure(now) // consec=1, backoff 8s
	assert.Equal(t, 1, s.consecFailures)
	assert.False(t, s.canFlush(now))
	assert.False(t, s.canFlush(now.Add(7*time.Second)))
	assert.True(t, s.canFlush(now.Add(9*time.Second)))

	s.onFailure(now) // consec=2, backoff 16s
	assert.Equal(t, 2, s.consecFailures)
	assert.True(t, s.canFlush(now.Add(17*time.Second)))
	assert.False(t, s.canFlush(now.Add(15*time.Second)))

	// large failure count -> capped at ledgerFlushBackoffMax (30s)
	s.consecFailures = 20
	s.onFailure(now)
	assert.True(t, s.canFlush(now.Add(ledgerFlushBackoffMax+time.Second)))
	assert.False(t, s.canFlush(now.Add(ledgerFlushBackoffMax-time.Second)))

	s.onSuccess()
	assert.Equal(t, 0, s.consecFailures)
	assert.True(t, s.canFlush(now))
}

// ---------------------------------------------------------------------------
// cost ledger buffer + overflow eviction
// ---------------------------------------------------------------------------

func TestBufferConsumptionCostRecord_Overflow(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.BufMaxEntries = 2
	})

	for i := 0; i < 3; i++ {
		BufferConsumptionCostRecord(&ConsumptionCost{ChannelId: nextTestID(), RevenueQuota: int64(i)})
	}
	costLedgerLock.Lock()
	backlog := len(costLedgerBuf)
	dropped := costLedgerDropped
	costLedgerLock.Unlock()
	assert.Equal(t, 2, backlog)       // capped at BufMaxEntries
	assert.EqualValues(t, 1, dropped) // one evicted to fallback

	snap := GetLedgerPipelineStatusSnapshot()
	assert.EqualValues(t, 2, snap.Cost.Backlog)
	assert.EqualValues(t, 1, snap.Cost.Dropped)
	bsClearMemBuffers()
}

func TestRequeueCostLedger_RetryAndOverflow(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	// keep the breaker from tripping on the overflow report
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = false
	})
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 10
		s.RetryQueueMaxEntries = 1
	})

	i1 := &costLedgerItem{Cost: &ConsumptionCost{ChannelId: nextTestID()}}
	i2 := &costLedgerItem{Cost: &ConsumptionCost{ChannelId: nextTestID()}}
	requeueCostLedger([]*costLedgerItem{i1, i2})

	retryQueueMu.Lock()
	remaining := len(costRetryQueue)
	retryQueueMu.Unlock()
	assert.Equal(t, 1, remaining) // capacity 1, oldest evicted to fallback
	assert.Equal(t, 1, i1.RetryCount)
	assert.Equal(t, 1, i2.RetryCount)

	// empty slice -> no-op
	requeueCostLedger(nil)

	// item already at max retries -> dropped, never enters the queue
	bsClearMemBuffers()
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 2
		s.RetryQueueMaxEntries = 100
	})
	maxed := &costLedgerItem{Cost: &ConsumptionCost{ChannelId: nextTestID()}, RetryCount: 1}
	requeueCostLedger([]*costLedgerItem{maxed}) // -> 2 >= 2 -> fallback
	retryQueueMu.Lock()
	assert.Empty(t, costRetryQueue)
	retryQueueMu.Unlock()
	bsClearMemBuffers()
}

// ---------------------------------------------------------------------------
// dedup
// ---------------------------------------------------------------------------

func TestMemDedupCommissionLogId(t *testing.T) {
	bsClearMemBuffers()
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.DedupMemMaxEntries = 1
	})
	id1, id2 := nextTestID(), nextTestID()
	assert.True(t, memDedupCommissionLogId(id1))  // first
	assert.False(t, memDedupCommissionLogId(id1)) // duplicate
	// adding id2 hits the size ceiling (1) -> set rebuilt (cleared), id2 added
	assert.True(t, memDedupCommissionLogId(id2))
	// id1 was dropped in the rebuild, so it is "first" again
	assert.True(t, memDedupCommissionLogId(id1))
	bsClearMemBuffers()
}

func TestDedupCommissionLogId_MemoryBackend(t *testing.T) {
	bsClearMemBuffers()
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.DedupUseRedis = false
		s.DedupMemMaxEntries = 1000
	})
	id := nextTestID()
	assert.True(t, dedupCommissionLogId(id))
	assert.False(t, dedupCommissionLogId(id))
	bsClearMemBuffers()
}

func TestCheckAndBufferCostAndCommission(t *testing.T) {
	bsClearMemBuffers()
	bsInitFallback(t)
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.DedupUseRedis = false
		s.BufMaxEntries = 100000
	})

	// nil log_id -> always enqueued
	c1 := &ConsumptionCost{ChannelId: nextTestID()}
	l1 := &EmployeeCommissionLog{EmployeeUserId: nextTestID()}
	assert.True(t, CheckAndBufferCostAndCommission(c1, l1))
	assert.Nil(t, l1.LogId)
	costCommissionLedgerLock.Lock()
	assert.Len(t, costCommissionLedgerBuf, 1)
	costCommissionLedgerLock.Unlock()

	// valid log_id: first true, duplicate false
	logId := nextTestID()
	c2 := &ConsumptionCost{ChannelId: nextTestID()}
	l2 := &EmployeeCommissionLog{EmployeeUserId: nextTestID(), LogId: common.GetPointer(logId)}
	assert.True(t, CheckAndBufferCostAndCommission(c2, l2))
	// cost log_id synced to commission log_id
	require.NotNil(t, c2.LogId)
	assert.Equal(t, logId, *c2.LogId)

	c3 := &ConsumptionCost{ChannelId: nextTestID()}
	l3 := &EmployeeCommissionLog{EmployeeUserId: nextTestID(), LogId: common.GetPointer(logId)}
	assert.False(t, CheckAndBufferCostAndCommission(c3, l3)) // duplicate

	costCommissionLedgerLock.Lock()
	assert.Len(t, costCommissionLedgerBuf, 2) // c1 + c2 only
	costCommissionLedgerLock.Unlock()
	bsClearMemBuffers()
}

// ---------------------------------------------------------------------------
// pipeline status snapshot helpers
// ---------------------------------------------------------------------------

func TestLedgerPipelineStatusSnapshot(t *testing.T) {
	setLedgerPipelineBacklog("commission", 42)
	setLedgerPipelineDropped("commission", 7)
	markLedgerPipelineFlush("commission", 5, 250*time.Millisecond)
	setLedgerPipelineBacklog("unknown-queue", 99) // default branch: ignored

	snap := GetLedgerPipelineStatusSnapshot()
	assert.EqualValues(t, 42, snap.Commission.Backlog)
	assert.EqualValues(t, 7, snap.Commission.Dropped)
	assert.EqualValues(t, 5, snap.Commission.LastFlushItems)
	assert.EqualValues(t, 250, snap.Commission.LastFlushTookMs)
	assert.Greater(t, snap.Commission.LastFlushAt, int64(0))
}

// ===========================================================================
// DB-backed: upserts, flush-from-mem, ledger flush (guard + boundary), replay
// ===========================================================================

func bsCleanupCostAndDaily(t *testing.T, logId, channelId int, statDate int64) {
	t.Cleanup(func() {
		if DB == nil {
			return
		}
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, channelId).Delete(&PlatformChannelDailyStat{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})
}

func TestUpsertPlatformDailyStat_Accumulates(t *testing.T) {
	requireDB(t)
	statDate := int64(4_200_000_000) + int64(nextTestID()-testIDBase)*businessStatsDaySeconds
	statDate = statDate / businessStatsDaySeconds * businessStatsDaySeconds
	ch := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
	})

	assert.True(t, upsertPlatformDailyStat(statDate, ch, "chan", 100, 40, 2, 1.0, 111))
	assert.True(t, upsertPlatformDailyStat(statDate, ch, "", 50, 10, 1, 0.5, 222))

	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 150, row.RevenueQuota)
	assert.EqualValues(t, 50, row.CostQuota)
	assert.EqualValues(t, 3, row.RecordCount)
	assert.InDelta(t, 1.5, row.CostRatioSum, 1e-9)
	assert.Equal(t, "chan", row.ChannelName)
	assert.EqualValues(t, 222, row.LastCreatedAt)
}

func TestFlushPlatformStatsFromMem(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	ch := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
	})
	bufferPlatformStatMem(statDate, ch, "n", 100, 40, 0.8, 111)
	bufferPlatformStatMem(statDate, ch, "n", 50, 10, 0.2, 222)

	flushPlatformStatsFromMem()

	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 150, row.RevenueQuota)
	assert.EqualValues(t, 2, row.RecordCount)

	// buffer drained; second flush is a no-op (row unchanged)
	memPlatformLock.Lock()
	assert.Empty(t, memPlatformBuf)
	memPlatformLock.Unlock()
	flushPlatformStatsFromMem()
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 150, row.RevenueQuota)
	bsClearMemBuffers()
}

func TestFlushCommissionStatsFromMem(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers()
	statDate := int64(4_200_000_000)
	emp := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionDailyStat{})
	})
	bufferCommissionStatMem(statDate, emp, 100, 40, 60, 6, 5)
	bufferCommissionStatMem(statDate, emp, 200, 80, 120, 12, 9)
	flushCommissionStatsFromMem()

	var row EmployeeCommissionDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND employee_user_id = ?", statDate, emp).First(&row).Error)
	assert.EqualValues(t, 300, row.RevenueQuota)
	assert.EqualValues(t, 180, row.ProfitQuota)
	assert.EqualValues(t, 2, row.RecordCount)
	bsClearMemBuffers()
}

func TestExistingCostLogIDsTx(t *testing.T) {
	requireDB(t)
	id1, id2, id3 := nextTestID(), nextTestID(), nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id IN ?", []int{id1, id2, id3}).Delete(&ConsumptionCost{})
	})
	// insert id1 and id2 only
	require.NoError(t, DB.Create(&ConsumptionCost{LogId: common.GetPointer(id1), ChannelId: 1, CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&ConsumptionCost{LogId: common.GetPointer(id2), ChannelId: 1, CreatedAt: 1}).Error)

	costs := []*ConsumptionCost{
		{LogId: common.GetPointer(id1)},
		{LogId: common.GetPointer(id2)},
		{LogId: common.GetPointer(id3)}, // not inserted
		{LogId: nil},                    // no key
	}
	var existing map[int]struct{}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		existing, err = existingCostLogIDsTx(tx, costs)
		return err
	}))
	_, has1 := existing[id1]
	_, has2 := existing[id2]
	_, has3 := existing[id3]
	assert.True(t, has1)
	assert.True(t, has2)
	assert.False(t, has3)

	// no ids -> nil
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		m, err := existingCostLogIDsTx(tx, []*ConsumptionCost{{LogId: nil}})
		assert.Nil(t, m)
		return err
	}))
}

func TestApplyPlatformDailyStatTx_SkipsExisting(t *testing.T) {
	requireDB(t)
	statDate := bsFarFutureDay(t)
	ch := nextTestID()
	createdAt := statDate + 100
	require.Equal(t, statDate, localDayStart(createdAt))
	dupId, newId := nextTestID(), nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})

	costs := []*ConsumptionCost{
		{LogId: common.GetPointer(dupId), ChannelId: ch, RevenueQuota: 999, CostQuota: 999, CostRatio: 9, CreatedAt: createdAt}, // skipped
		{LogId: common.GetPointer(newId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt}, // counted
		{LogId: nil, ChannelId: ch, RevenueQuota: 10, CostQuota: 5, CostRatio: 0.1, CreatedAt: createdAt},                         // nil log_id -> counted
	}
	existing := map[int]struct{}{dupId: {}}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return applyPlatformDailyStatTx(tx, costs, existing)
	}))

	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	// only newId + nil-logId counted: revenue 110, cost 45, count 2
	assert.EqualValues(t, 110, row.RevenueQuota)
	assert.EqualValues(t, 45, row.CostQuota)
	assert.EqualValues(t, 2, row.RecordCount)
}

func TestFlushConsumptionCostLedger_OuterBatchZeroGuard(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	// OuterBatchSize == 0 must NOT cause an infinite loop: the getter clamps it
	// to the default (2000). Assert the clamp AND that the flush terminates.
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.OuterBatchSize = 0
		s.CostOuterBatchSize = 0
	})
	require.Equal(t, operation_setting.DefaultLedgerOuterBatchSize, operation_setting.GetLedgerPipelineSetting().GetCostOuterBatchSize())

	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	require.Equal(t, statDate, localDayStart(createdAt))
	ch := nextTestID()
	logId := nextTestID()
	bsCleanupCostAndDaily(t, logId, ch, statDate)
	BufferConsumptionCostRecord(&ConsumptionCost{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt})

	done := make(chan struct{})
	go func() {
		flushConsumptionCostLedger()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("flushConsumptionCostLedger did not terminate (possible OuterBatchSize==0 infinite loop)")
	}

	var cnt int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logId).Count(&cnt).Error)
	assert.EqualValues(t, 1, cnt)
	bsClearMemBuffers()
}

func TestFlushConsumptionCostLedger_BatchOne_And_Idempotent(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	// boundary: outer batch of 1 -> each record its own batch
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.CostOuterBatchSize = 1
		s.OuterBatchSize = 1
		s.InnerBatchSize = 1
		s.BufMaxEntries = 100000
	})

	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	require.Equal(t, statDate, localDayStart(createdAt))
	ch := nextTestID()
	logIds := []int{nextTestID(), nextTestID(), nextTestID()}
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id IN ?", logIds).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})
	for _, id := range logIds {
		BufferConsumptionCostRecord(&ConsumptionCost{LogId: common.GetPointer(id), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt})
	}
	flushConsumptionCostLedger()

	var cnt int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id IN ?", logIds).Count(&cnt).Error)
	assert.EqualValues(t, 3, cnt)
	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 300, row.RevenueQuota)
	assert.EqualValues(t, 3, row.RecordCount)

	// Re-buffer the SAME records and flush again: ON CONFLICT(log_id) skips the
	// inserts and applyPlatformDailyStatTx must NOT re-aggregate (no double count).
	bsClearMemBuffers()
	costLedgerFlushState = ledgerFlushState{}
	for _, id := range logIds {
		BufferConsumptionCostRecord(&ConsumptionCost{LogId: common.GetPointer(id), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt})
	}
	flushConsumptionCostLedger()

	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id IN ?", logIds).Count(&cnt).Error)
	assert.EqualValues(t, 3, cnt) // still 3, no duplicates
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 300, row.RevenueQuota) // NOT doubled
	assert.EqualValues(t, 3, row.RecordCount)
	bsClearMemBuffers()
}

func TestReplayStatFallbacks(t *testing.T) {
	requireDB(t)
	statDate := int64(4_200_000_000)

	t.Run("platform", func(t *testing.T) {
		ch := nextTestID()
		t.Cleanup(func() { DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{}) })
		payload, err := common.Marshal(&platformStatDelta{StatDate: statDate, ChannelId: ch, ChannelName: "r", RevenueQuota: 77, CostQuota: 33, RecordCount: 1, CostRatioSum: 0.5, LastCreatedAt: 5})
		require.NoError(t, err)
		require.NoError(t, ReplayStatPlatformFallback(payload))
		var row PlatformChannelDailyStat
		require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
		assert.EqualValues(t, 77, row.RevenueQuota)
	})

	t.Run("commission", func(t *testing.T) {
		emp := nextTestID()
		t.Cleanup(func() { DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionDailyStat{}) })
		payload, err := common.Marshal(&commissionStatDelta{StatDate: statDate, EmployeeUserId: emp, RevenueQuota: 88, ProfitQuota: 50, RecordCount: 1})
		require.NoError(t, err)
		require.NoError(t, ReplayStatCommissionFallback(payload))
		var row EmployeeCommissionDailyStat
		require.NoError(t, DB.Where("employee_user_id = ?", emp).First(&row).Error)
		assert.EqualValues(t, 88, row.RevenueQuota)
	})

	t.Run("customer commission", func(t *testing.T) {
		emp, cust := nextTestID(), nextTestID()
		t.Cleanup(func() { DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCustomerCommissionDailyStat{}) })
		payload, err := common.Marshal(&customerCommissionStatDelta{StatDate: statDate, EmployeeUserId: emp, CustomerUserId: cust, RevenueQuota: 99, RecordCount: 1})
		require.NoError(t, err)
		require.NoError(t, ReplayStatCustomerCommissionFallback(payload))
		var row EmployeeCustomerCommissionDailyStat
		require.NoError(t, DB.Where("employee_user_id = ? AND customer_user_id = ?", emp, cust).First(&row).Error)
		assert.EqualValues(t, 99, row.RevenueQuota)
	})

	t.Run("reset daily", func(t *testing.T) {
		emp := nextTestID()
		resetAt := int64(4_100_000_000)
		t.Cleanup(func() { DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionResetPeriodDailyStat{}) })
		payload, err := common.Marshal(&commissionResetPeriodDailyDelta{ResetStartedAt: resetAt, StatDate: statDate, EmployeeUserId: emp, RevenueQuota: 66, RecordCount: 1})
		require.NoError(t, err)
		require.NoError(t, ReplayStatResetDailyFallback(payload))
		var row EmployeeCommissionResetPeriodDailyStat
		require.NoError(t, DB.Where("employee_user_id = ? AND reset_started_at = ?", emp, resetAt).First(&row).Error)
		assert.EqualValues(t, 66, row.RevenueQuota)
	})

	t.Run("bad payload -> error", func(t *testing.T) {
		assert.Error(t, ReplayStatPlatformFallback([]byte("not json")))
	})

	t.Run("legacy employee-ext -> skipped, no error", func(t *testing.T) {
		assert.NoError(t, ReplayStatEmployeeExtFallback([]byte(`{"anything":1}`)))
	})
}

// ===========================================================================
// DB-backed: paired ledger flush, retry-queue flush, drain, public entrypoints
// ===========================================================================

func TestFlushCostAndCommissionLedger_HappyPath(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.BufMaxEntries = 100000
	})
	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	ch, emp, cust := nextTestID(), nextTestID(), nextTestID()
	logId := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("log_id = ?", logId).Delete(&EmployeeCommissionLog{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		DB.Unscoped().Where("user_id = ?", emp).Delete(&EmployeeTierLevel{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})

	cost := &ConsumptionCost{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt}
	comm := &EmployeeCommissionLog{LogId: common.GetPointer(logId), EmployeeUserId: emp, CustomerUserId: cust, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, CreatedAt: createdAt}
	bufferCostAndCommissionLedger(cost, comm)
	flushCostAndCommissionLedger()

	var cc, ec int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logId).Count(&cc).Error)
	require.NoError(t, DB.Model(&EmployeeCommissionLog{}).Where("log_id = ?", logId).Count(&ec).Error)
	assert.EqualValues(t, 1, cc)
	assert.EqualValues(t, 1, ec)
	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 100, row.RevenueQuota)
	// commission side is buffered to the mem stat buffer for the next stat flush
	memCommissionLock.Lock()
	_, ok := memCommissionBuf[memCommissionKey(statDate, emp)]
	memCommissionLock.Unlock()
	assert.True(t, ok)
	bsClearMemBuffers()
}

func TestFlushPairRetryQueue(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	ch, emp := nextTestID(), nextTestID()
	logId := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("log_id = ?", logId).Delete(&EmployeeCommissionLog{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		DB.Unscoped().Where("user_id = ?", emp).Delete(&EmployeeTierLevel{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})
	retryQueueMu.Lock()
	pairRetryQueue = []*costCommissionLedgerPair{{
		Cost:       &ConsumptionCost{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt},
		Commission: &EmployeeCommissionLog{LogId: common.GetPointer(logId), EmployeeUserId: emp, RevenueQuota: 100, ProfitQuota: 60, CommissionQuota: 6, CreatedAt: createdAt},
	}}
	pairRetryFlushState = ledgerFlushState{}
	retryQueueMu.Unlock()

	flushPairRetryQueue()

	var cc int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logId).Count(&cc).Error)
	assert.EqualValues(t, 1, cc)
	retryQueueMu.Lock()
	assert.Empty(t, pairRetryQueue)
	retryQueueMu.Unlock()
	bsClearMemBuffers()
}

func TestFlushCostRetryQueue(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	ch := nextTestID()
	logId := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})
	retryQueueMu.Lock()
	costRetryQueue = []*costLedgerItem{{Cost: &ConsumptionCost{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt}}}
	costRetryFlushState = ledgerFlushState{}
	retryQueueMu.Unlock()

	flushCostRetryQueue()

	var cc int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logId).Count(&cc).Error)
	assert.EqualValues(t, 1, cc)
	retryQueueMu.Lock()
	assert.Empty(t, costRetryQueue)
	retryQueueMu.Unlock()
	bsClearMemBuffers()
}

func TestRequeuePairLedger_RetryAndMaxRetries(t *testing.T) {
	bsInitFallback(t)
	bsClearMemBuffers()
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries = 3
		s.RetryQueueMaxEntries = 100
	})
	p := &costCommissionLedgerPair{Cost: &ConsumptionCost{ChannelId: nextTestID()}}
	requeuePairLedger([]*costCommissionLedgerPair{p})
	assert.Equal(t, 1, p.RetryCount)
	retryQueueMu.Lock()
	assert.Len(t, pairRetryQueue, 1)
	retryQueueMu.Unlock()

	// nil / empty -> no-op
	requeuePairLedger(nil)

	// already at max -> dropped to fallback, not requeued
	bsClearMemBuffers()
	maxed := &costCommissionLedgerPair{Cost: &ConsumptionCost{ChannelId: nextTestID()}, RetryCount: 2}
	requeuePairLedger([]*costCommissionLedgerPair{maxed})
	retryQueueMu.Lock()
	assert.Empty(t, pairRetryQueue)
	retryQueueMu.Unlock()
	bsClearMemBuffers()
}

func TestBufferPlatformDailyStat_Public(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers()
	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	ch := nextTestID()
	t.Cleanup(func() {
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})
	BufferPlatformDailyStat(&ConsumptionCost{ChannelId: ch, ChannelName: "n", RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt})
	memPlatformLock.Lock()
	d := memPlatformBuf[memPlatformKey(statDate, ch)]
	memPlatformLock.Unlock()
	require.NotNil(t, d)
	assert.EqualValues(t, 100, d.RevenueQuota)
	bsClearMemBuffers()
}

func TestBufferCommissionDailyStat_Public(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers()
	statDate := bsFarFutureDay(t)
	createdAt := statDate + 100
	emp, cust := nextTestID(), nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("user_id = ?", emp).Delete(&EmployeeTierLevel{})
	})
	BufferCommissionDailyStat(&EmployeeCommissionLog{EmployeeUserId: emp, CustomerUserId: cust, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, CreatedAt: createdAt})
	memCommissionLock.Lock()
	_, ok := memCommissionBuf[memCommissionKey(statDate, emp)]
	memCommissionLock.Unlock()
	assert.True(t, ok)
	bsClearMemBuffers()
}

func TestFlushBusinessStatBuffers_Orchestrator(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	statDate := bsFarFutureDay(t)
	ch := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
	})
	bufferPlatformStatMem(statDate, ch, "n", 100, 40, 0.8, 111)
	FlushBusinessStatBuffers()
	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 100, row.RevenueQuota)
	bsClearMemBuffers()
}

func TestDrainRemainingToFallbackFile(t *testing.T) {
	dir := bsInitFallback(t)
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	bsClearMemBuffers()
	date := uniq("2099-drain")
	bsWithCurrentDate(t, date)

	// seed one of each buffer
	BufferConsumptionCostRecord(&ConsumptionCost{ChannelId: nextTestID()})
	bufferCostAndCommissionLedger(&ConsumptionCost{ChannelId: nextTestID()}, &EmployeeCommissionLog{EmployeeUserId: nextTestID()})
	bufferPlatformStatMem(1, nextTestID(), "n", 1, 1, 0.1, 1)
	bufferCommissionStatMem(1, nextTestID(), 1, 1, 1, 1, 1)

	drainRemainingToFallbackFile("shutdown_drain")

	// all buffers drained
	costLedgerLock.Lock()
	assert.Empty(t, costLedgerBuf)
	costLedgerLock.Unlock()
	costCommissionLedgerLock.Lock()
	assert.Empty(t, costCommissionLedgerBuf)
	costCommissionLedgerLock.Unlock()
	memPlatformLock.Lock()
	assert.Empty(t, memPlatformBuf)
	memPlatformLock.Unlock()

	content := bsReadFallbackFile(t, dir, businessStatsFallbackFile, date)
	assert.Contains(t, content, `"reason":"shutdown_drain"`)
	assert.Contains(t, content, `"kind":"stat_platform"`)
	bsClearMemBuffers()
}

func TestRedisDedupCommissionLogId(t *testing.T) {
	enableRedis(t)
	bsClearMemBuffers()
	logId := nextTestID()
	assert.Equal(t, fmt.Sprintf("ledger:dedup:commission:%d", logId), ledgerDedupCommissionKey(logId))
	_ = common.RedisDelKey(ledgerDedupCommissionKey(logId))
	t.Cleanup(func() { _ = common.RedisDelKey(ledgerDedupCommissionKey(logId)) })

	first, ok := redisDedupCommissionLogId(logId)
	require.True(t, ok)
	assert.True(t, first)
	second, ok := redisDedupCommissionLogId(logId)
	require.True(t, ok)
	assert.False(t, second)

	// via the dispatcher with the Redis backend enabled
	logId2 := nextTestID()
	t.Cleanup(func() { _ = common.RedisDelKey(ledgerDedupCommissionKey(logId2)) })
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) { s.DedupUseRedis = true })
	assert.True(t, dedupCommissionLogId(logId2))
	assert.False(t, dedupCommissionLogId(logId2))
	bsClearMemBuffers()
}
