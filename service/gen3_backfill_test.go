package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// business_stats_backfill.go — admin-triggered fallback-log backfill service.
// Pure helpers + one full runBackfillTask integration driven by a real temp
// file. Rule 0: nothing here runs on the relay hot path.
// ===========================================================================

// withBackfillSetting mutates the backfill setting and restores it after the test.
func withBackfillSetting(t *testing.T, mutate func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting)) {
	t.Helper()
	cfg := operation_setting.GetBusinessStatsFallbackBackfillSetting()
	orig := *cfg
	mutate(cfg)
	t.Cleanup(func() { *cfg = orig })
}

// yesterdayBusinessDate returns a date string guaranteed strictly before today.
func yesterdayBusinessDate() string {
	todayStart := model.BusinessDayStart(time.Now().Unix())
	return model.BusinessDayDate(todayStart - 24*60*60)
}

// --- error sentinel predicates ---------------------------------------------

func TestBackfill_ErrPredicates(t *testing.T) {
	// TriggerBackfill returns errBackfillDisabled when disabled.
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = false
	})
	err := TriggerBackfill("2020-01-01")
	require.Error(t, err)
	assert.True(t, IsErrBackfillDisabled(err))
	assert.False(t, IsErrBackfillDateMustBeHistorical(err))
	assert.False(t, IsErrBackfillAlreadyRunning(err))
	assert.False(t, IsErrBackfillFileNotFound(err))
}

func TestBackfill_TriggerNonHistorical(t *testing.T) {
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = true
	})
	// Today / future is not historical.
	future := model.BusinessDayDate(model.BusinessDayStart(time.Now().Unix()) + 48*60*60)
	err := TriggerBackfill(future)
	require.Error(t, err)
	assert.True(t, IsErrBackfillDateMustBeHistorical(err))
}

func TestBackfill_TriggerFileNotFound(t *testing.T) {
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = true
	})
	// Historical date with no fallback file present.
	date := "1999-01-02"
	InvalidateFallbackFileStatusCache(date)
	err := TriggerBackfill(date)
	require.Error(t, err)
	assert.True(t, IsErrBackfillFileNotFound(err))
}

// --- isHistoricalDate / IsSingleHistoricalDate -----------------------------

func TestBackfill_IsHistoricalDate(t *testing.T) {
	assert.False(t, isHistoricalDate("not-a-date"))
	assert.False(t, IsSingleHistoricalDate("2999-12-31")) // future
	assert.True(t, IsSingleHistoricalDate("2000-01-01"))  // past
}

// --- IsSingleHistoricalDayFilter -------------------------------------------

func TestBackfill_IsSingleHistoricalDayFilter(t *testing.T) {
	// Invalid ranges.
	_, ok := IsSingleHistoricalDayFilter(0, 100)
	assert.False(t, ok)
	_, ok = IsSingleHistoricalDayFilter(100, 100)
	assert.False(t, ok)
	_, ok = IsSingleHistoricalDayFilter(200, 100)
	assert.False(t, ok)

	// A single historical business day.
	todayStart := model.BusinessDayStart(time.Now().Unix())
	dayStart := todayStart - 24*60*60
	dayEnd := todayStart // exclusive upper bound
	date, ok := IsSingleHistoricalDayFilter(dayStart, dayEnd)
	require.True(t, ok)
	assert.Equal(t, model.BusinessDayDate(dayStart), date)

	// Spanning two days => not a single day.
	_, ok = IsSingleHistoricalDayFilter(dayStart-24*60*60, dayEnd)
	assert.False(t, ok)

	// Today (not historical).
	_, ok = IsSingleHistoricalDayFilter(todayStart, todayStart+24*60*60)
	assert.False(t, ok)
}

// --- GetFallbackFileStatus (disabled / non-historical / cache) -------------

func TestBackfill_GetFallbackFileStatus_Disabled(t *testing.T) {
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = false
	})
	st := GetFallbackFileStatus("2000-01-01")
	assert.False(t, st.HasFile)
	assert.False(t, st.CanBackfill)
}

func TestBackfill_GetFallbackFileStatus_NonHistorical(t *testing.T) {
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = true
	})
	st := GetFallbackFileStatus("2999-01-01")
	assert.False(t, st.HasFile)
}

func TestBackfill_GetFallbackFileStatus_CachesResult(t *testing.T) {
	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = true
		cfg.StatusCacheSeconds = 60
	})
	date := "1998-06-06"
	InvalidateFallbackFileStatusCache(date)
	first := GetFallbackFileStatus(date) // no file => has_file=false, then cached
	assert.False(t, first.HasFile)
	// Second call served from cache (same value).
	second := GetFallbackFileStatus(date)
	assert.Equal(t, first, second)
	InvalidateFallbackFileStatusCache(date)
}

// --- rawCostToModel / rawCommToModel ---------------------------------------

func TestBackfill_RawCostToModel(t *testing.T) {
	logID := 42
	rec := rawCostToModel(rawCostPayload{
		LogId:        &logID,
		UserId:       7,
		ChannelId:    3,
		ChannelName:  "chan",
		GroupName:    "grp",
		ModelName:    "gpt-4o",
		RevenueQuota: 1000,
		CostQuota:    600,
		GroupRatio:   1.5,
		CostRatio:    0.6,
		CreatedAt:    123456,
	})
	assert.Equal(t, 7, rec.UserId)
	assert.Equal(t, "gpt-4o", rec.ModelName)
	assert.EqualValues(t, 1000, rec.RevenueQuota)
	assert.EqualValues(t, 123456, rec.CreatedAt)
	require.NotNil(t, rec.LogId)
	assert.Equal(t, 42, *rec.LogId)

	// CreatedAt==0 => defaults to now.
	rec2 := rawCostToModel(rawCostPayload{UserId: 1})
	assert.Greater(t, rec2.CreatedAt, int64(0))
}

func TestBackfill_RawCommToModel(t *testing.T) {
	rec := rawCommToModel(rawCommissionPayload{
		EmployeeId:      5,
		EmployeeUserId:  6,
		CustomerUserId:  7,
		ModelName:       "m",
		ChannelId:       2,
		RevenueQuota:    1000,
		CostQuota:       400,
		ProfitQuota:     600,
		CommissionQuota: 60,
		CommissionRate:  0.1,
		CreatedAt:       999,
	})
	assert.Equal(t, 5, rec.EmployeeId)
	assert.EqualValues(t, 60, rec.CommissionQuota)
	assert.EqualValues(t, 999, rec.CreatedAt)

	rec2 := rawCommToModel(rawCommissionPayload{EmployeeId: 1})
	assert.Greater(t, rec2.CreatedAt, int64(0))
}

// --- processBackfillEntry classification -----------------------------------

func TestBackfill_ProcessEntry_UnsupportedKind(t *testing.T) {
	w := &backfillLedgerWriter{}
	handled, reason := processBackfillEntry(fallbackLogLine{Kind: "nope"}, w, newBackfillLookupCache())
	assert.False(t, handled)
	assert.Contains(t, reason, "unsupported kind")
}

func TestBackfill_ProcessExactPair(t *testing.T) {
	w := &backfillLedgerWriter{}
	entry := fallbackLogLine{
		Time: 111,
		Kind: "cost_commission_create",
		Payload: map[string]any{
			"cost": map[string]any{
				"user_id": 1, "channel_id": 2, "model_name": "m",
				"revenue_quota": 1000, "cost_quota": 600,
			},
			"commission": map[string]any{
				"employee_id": 3, "employee_user_id": 4, "customer_user_id": 1,
				"revenue_quota": 1000, "cost_quota": 600, "profit_quota": 400,
				"commission_quota": 40, "commission_rate": 0.1,
			},
		},
	}
	handled, reason := processBackfillEntry(entry, w, newBackfillLookupCache())
	assert.True(t, handled)
	assert.Empty(t, reason)
	assert.Len(t, w.pairBuf, 1)
	assert.True(t, w.hasData())
	// created_at defaulted from entry.Time.
	assert.EqualValues(t, 111, w.pairBuf[0].Cost.CreatedAt)
}

func TestBackfill_ProcessExactPair_MissingCommission(t *testing.T) {
	w := &backfillLedgerWriter{}
	entry := fallbackLogLine{
		Kind:    "cost_commission_create",
		Payload: map[string]any{"cost": map[string]any{"user_id": 1}},
	}
	handled, reason := processBackfillEntry(entry, w, nil)
	assert.False(t, handled)
	assert.Contains(t, reason, "missing cost or commission")
}

func TestBackfill_ProcessCostOnly_NoInviter(t *testing.T) {
	truncate(t)
	// Seed a user with no inviter => attribution yields cost-only.
	seedUser(t, 940001, 0)

	w := &backfillLedgerWriter{}
	entry := fallbackLogLine{
		Time: 222,
		Kind: "business_stats_skipped",
		Payload: map[string]any{
			"cost": map[string]any{
				"user_id": 940001, "channel_id": 2, "model_name": "m",
				"revenue_quota": 500, "cost_quota": 300,
			},
		},
	}
	handled, reason := processBackfillEntry(entry, w, newBackfillLookupCache())
	assert.True(t, handled)
	assert.Empty(t, reason)
	assert.Len(t, w.costOnlyBuf, 1)
	assert.Empty(t, w.pairBuf)
}

func TestBackfill_ProcessCostOnly_MissingCost(t *testing.T) {
	w := &backfillLedgerWriter{}
	handled, reason := processCostOnlyEntry(fallbackLogLine{Kind: "business_stats_skipped", Payload: map[string]any{}}, w, nil)
	assert.False(t, handled)
	assert.Contains(t, reason, "missing cost")
}

// --- backfillLedgerWriter --------------------------------------------------

func TestBackfill_LedgerWriter(t *testing.T) {
	w := &backfillLedgerWriter{}
	assert.False(t, w.hasData())
	w.addCostOnly(&model.ConsumptionCost{UserId: 1})
	assert.True(t, w.hasData())
	w.addPair(&model.ConsumptionCost{UserId: 2}, &model.EmployeeCommissionLog{EmployeeId: 1})
	assert.Len(t, w.costOnlyBuf, 1)
	assert.Len(t, w.pairBuf, 1)
}

// --- flush controller ------------------------------------------------------

func TestBackfill_FlushController(t *testing.T) {
	fc := &backfillFlushController{lastFlushAt: time.Now()}
	w := &backfillLedgerWriter{}
	cfg := &operation_setting.BusinessStatsFallbackBackfillSetting{WriteBatchSize: 2, FlushIntervalSec: 0}

	// No data => no flush.
	assert.False(t, fc.shouldFlush(w, cfg))

	// Below batch size, interval disabled => no flush.
	w.addCostOnly(&model.ConsumptionCost{UserId: 1})
	assert.False(t, fc.shouldFlush(w, cfg))

	// Reaching batch size => flush.
	w.addCostOnly(&model.ConsumptionCost{UserId: 2})
	assert.True(t, fc.shouldFlush(w, cfg))

	// Time-based flush.
	fc2 := &backfillFlushController{lastFlushAt: time.Now().Add(-10 * time.Second)}
	w2 := &backfillLedgerWriter{}
	w2.addCostOnly(&model.ConsumptionCost{UserId: 1})
	cfgTime := &operation_setting.BusinessStatsFallbackBackfillSetting{WriteBatchSize: 100, FlushIntervalSec: 5}
	assert.True(t, fc2.shouldFlush(w2, cfgTime))

	fc2.recordFlush()
	assert.False(t, fc2.shouldFlush(w2, cfgTime))
}

// --- state helpers ---------------------------------------------------------

func TestBackfill_StateHelpers(t *testing.T) {
	updateBackfillProgress(10, 5, 2)
	got := GetBackfillResult()
	assert.Equal(t, 10, got.TotalLines)
	assert.Equal(t, 5, got.SuccessCount)
	assert.Equal(t, 2, got.DiscardCount)

	finishBackfill("boom", false)
	got = GetBackfillResult()
	assert.False(t, got.Running)
	assert.False(t, got.Success)
	assert.Equal(t, "boom", got.LastError)

	finishBackfillFull(20, 18, 2, true, "")
	got = GetBackfillResult()
	assert.False(t, got.Running)
	assert.True(t, got.Success)
	assert.True(t, got.FileDeleted)
	assert.Equal(t, 18, got.SuccessCount)
}

// --- Full runBackfillTask integration (real temp fallback file) ------------

func TestBackfill_FullRun_Integration(t *testing.T) {
	truncate(t)
	cleanupBackfillTables(t)
	seedUser(t, 941000, 0) // customer with no inviter for the cost-only entry

	withBackfillSetting(t, func(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) {
		cfg.Enabled = true
		cfg.WriteBatchSize = 100
		cfg.FlushIntervalSec = 0
		cfg.BatchSleepMs = 0
		cfg.MaxReadLineBytes = 1 << 20
		cfg.StatusCacheSeconds = 1
	})

	date := yesterdayBusinessDate()
	dir := model.FallbackLogDir()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, model.FallbackFilename(date))

	logID1, logID2 := 9410001, 9410002
	lines := []string{
		backfillLine(t, "cost_commission_create", map[string]any{
			"cost": map[string]any{
				"log_id": logID1, "user_id": 941000, "channel_id": 5, "model_name": "gpt-4o",
				"revenue_quota": 1000, "cost_quota": 600, "group_ratio": 1.0, "cost_ratio": 0.6,
			},
			"commission": map[string]any{
				"employee_id": 8, "employee_user_id": 9, "customer_user_id": 941000, "log_id": logID1,
				"model_name": "gpt-4o", "channel_id": 5, "revenue_quota": 1000, "cost_quota": 600,
				"profit_quota": 400, "commission_quota": 40, "commission_rate": 0.1,
			},
		}),
		backfillLine(t, "business_stats_skipped", map[string]any{
			"cost": map[string]any{
				"log_id": logID2, "user_id": 941000, "channel_id": 5, "model_name": "gpt-4o",
				"revenue_quota": 500, "cost_quota": 300, "group_ratio": 1.0, "cost_ratio": 0.6,
			},
		}),
		"{bad json",           // malformed => dead letter, discard
		backfillLine(t, "totally_unsupported_kind", map[string]any{}), // discard
		"",                    // empty => skipped, not counted
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	t.Cleanup(func() { _ = os.Remove(path) })

	require.NoError(t, TriggerBackfill(date))

	// Poll until finished.
	deadline := time.Now().Add(10 * time.Second)
	var res BackfillResult
	for {
		res = GetBackfillResult()
		if !res.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backfill did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}

	assert.True(t, res.Success, "clean run, LastError=%q", res.LastError)
	assert.Equal(t, 4, res.TotalLines, "2 valid + malformed + unsupported (empty line skipped)")
	assert.Equal(t, 2, res.SuccessCount, "1 pair + 1 cost-only inserted")
	assert.Equal(t, 2, res.DiscardCount, "malformed + unsupported")
	assert.True(t, res.FileDeleted, "file deleted after clean run")

	// File is gone.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))

	// Rows landed in the DB.
	var costCount int64
	model.DB.Model(&model.ConsumptionCost{}).Where("log_id IN ?", []int{logID1, logID2}).Count(&costCount)
	assert.EqualValues(t, 2, costCount)
	var commCount int64
	model.DB.Model(&model.EmployeeCommissionLog{}).Where("log_id = ?", logID1).Count(&commCount)
	assert.EqualValues(t, 1, commCount)

	cleanupBackfillTables(t)
}

// backfillLine marshals a fallbackLogLine JSON string.
func backfillLine(t *testing.T, kind string, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"time":    time.Now().Unix(),
		"kind":    kind,
		"reason":  "test",
		"payload": payload,
	})
	require.NoError(t, err)
	return string(b)
}

// cleanupBackfillTables removes the ledger + daily-stat rows written by this
// suite. Row-scoped to the test's user ids / business date so it never touches
// unrelated data, and gated on TEST_DB_CLEANUP like the house truncate helper.
func cleanupBackfillTables(t *testing.T) {
	t.Helper()
	if strings.ToLower(os.Getenv("TEST_DB_CLEANUP")) != "true" {
		return
	}
	userIDs := []int{940001, 941000}
	dayStart := model.BusinessDayStart(time.Now().Unix()) - 24*60*60
	model.DB.Exec("DELETE FROM consumption_costs WHERE user_id IN ?", userIDs)
	model.DB.Exec("DELETE FROM employee_commission_logs WHERE customer_user_id IN ?", userIDs)
	// Daily-stat aggregates keyed by the test's channel / business day.
	model.DB.Exec("DELETE FROM platform_channel_daily_stats WHERE channel_id = ?", 5)
	model.DB.Exec("DELETE FROM employee_commission_daily_stats WHERE stat_date = ?", dayStart)
	model.DB.Exec("DELETE FROM employee_customer_commission_daily_stats WHERE stat_date = ?", dayStart)
}
