package model

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// Cleanup tests exercise global maintenance entry points, so they run only in
// a database explicitly dedicated to this suite, never in the shared dev DB.
func requireFullScanFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("FULL_SCAN_ISOLATED_TEST") != "1" {
		t.Skip("requires an isolated full-scan regression database")
	}
	requireDB(t)
}

func TestFullScanExportOptionsAdvanceEmptyPage(t *testing.T) {
	requireFullScanFixture(t)
	user := mkUser(t, nil)
	logCleanupUser(t, user.Id)
	now := time.Now().Unix()
	rows := make([]Log, 501)
	for i := range rows {
		rows[i] = Log{UserId: user.Id, CreatedAt: now - int64(len(rows)-i), ModelName: "irrelevant",
			RequestId: fmt.Sprintf("scan-options-%d-%d", user.Id, i)}
	}
	rows[500].ModelName = "needle"
	require.NoError(t, LOG_DB.CreateInBatches(rows, 100).Error)
	filter := LogExportFilter{StartTimestamp: now - 86400, EndTimestamp: now}
	items, cursor, err := LogExportFilterOptions(context.Background(), filter, "model_name", "needle", "", []int{user.Id})
	require.NoError(t, err)
	require.Empty(t, items)
	require.NotEmpty(t, cursor, "an empty search page must still advance its raw candidate cursor")
	items, cursor, err = LogExportFilterOptions(context.Background(), filter, "model_name", "needle", cursor, []int{user.Id})
	require.NoError(t, err)
	require.Equal(t, []LogExportFilterOption{{Value: "needle", Label: "needle"}}, items)
	require.Empty(t, cursor)
}

func TestFullScanEmployeeQueriesRespectCancellation(t *testing.T) {
	requireFullScanFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := GetEmployeeCustomerLogsWithContext(ctx, EmployeeCustomerLogFilter{EmployeeUserId: nextTestID()})
	require.ErrorIs(t, err, context.Canceled)
	_, err = SumEmployeeCustomerUsedQuotaWithContext(ctx, EmployeeCustomerLogFilter{EmployeeUserId: nextTestID()})
	require.ErrorIs(t, err, context.Canceled)
}

func TestFullScanUserLogCountLimit(t *testing.T) {
	requireFullScanFixture(t)
	user := mkUser(t, nil)
	logCleanupUser(t, user.Id)
	rows := make([]Log, 10005)
	for i := range rows {
		rows[i] = Log{UserId: user.Id, CreatedAt: time.Now().Unix(), Type: LogTypeConsume,
			RequestId: fmt.Sprintf("scan-count-%d-%d", user.Id, i), Quota: 7}
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 100).Error)
	logs, total, err := GetUserLogs(user.Id, LogFilter{}, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 10000, total)
	require.Len(t, logs, 20)
	require.Equal(t, 7, logs[0].Quota, "the count subquery must not leak its select into the list")
	_, total, err = GetUserLogs(user.Id, LogFilter{RequestId: rows[0].RequestId}, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
}

func TestFullScanTokenCountLimit(t *testing.T) {
	requireFullScanFixture(t)
	setting := operation_setting.GetTokenSetting()
	previous := setting.MaxUserTokens
	setting.MaxUserTokens = 3
	t.Cleanup(func() { setting.MaxUserTokens = previous })
	user := mkUser(t, nil)
	var target *Token
	for range 5 {
		target = mkToken(t, user.Id, nil)
	}
	deleted := mkToken(t, user.Id, nil)
	require.NoError(t, DB.Delete(deleted).Error)
	other := mkUser(t, nil)
	otherToken := mkToken(t, other.Id, nil)
	rows, total, err := SearchUserTokens(user.Id, "", "", 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, rows, 5)
	rows, total, err = SearchUserTokens(user.Id, target.Name, "", 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, target.Key, rows[0].Key)
	_, _, err = SearchUserTokens(user.Id, target.Name+"%", "", 0, 20)
	require.ErrorContains(t, err, "exceeds the limit")
	rows, total, err = SearchUserTokens(other.Id, otherToken.Name+"%", "", 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
}

func TestFullScanOrphanCleanupBounds(t *testing.T) {
	requireFullScanFixture(t)
	live := mkChannel(t, nil)
	ghost := nextTestID() + 1000000
	daily := make([]ChannelDailyUsage, 1505)
	period := make([]ChannelLimitPeriodUsage, len(daily))
	for i := range daily {
		daily[i] = ChannelDailyUsage{StatDate: int64(1700000000 + i*86400), ChannelId: ghost, CostQuota: 1}
		period[i] = ChannelLimitPeriodUsage{PeriodStart: int64(1700000000 + i), ChannelId: ghost, CostQuota: 1}
	}
	require.NoError(t, DB.CreateInBatches(daily, 100).Error)
	require.NoError(t, DB.CreateInBatches(period, 100).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id = ?", ghost).Delete(&ChannelDailyUsage{}).Error)
		require.NoError(t, DB.Where("channel_id = ?", ghost).Delete(&ChannelLimitPeriodUsage{}).Error)
	})
	mkDailyUsageRow(t, 1700000000, live.Id, 5)
	for _, tc := range []struct {
		name    string
		model   any
		cleanup func() (int64, error)
	}{
		{"daily", &ChannelDailyUsage{}, DeleteOrphanChannelDailyUsage},
		{"period", &ChannelLimitPeriodUsage{}, DeleteOrphanChannelLimitPeriodUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := tc.cleanup()
			require.NoError(t, err)
			require.LessOrEqual(t, deleted, int64(1000), "one tick must not delete an unbounded table")
			for range 5 {
				deleted, err = tc.cleanup()
				require.NoError(t, err)
				require.LessOrEqual(t, deleted, int64(1000))
			}
			var count int64
			require.NoError(t, DB.Model(tc.model).Where("channel_id = ?", ghost).Count(&count).Error)
			require.Zero(t, count, "a completed sweep must eventually remove every orphan")
		})
	}
	rows, err := GetChannelDailyUsages(1700000000, []int{live.Id})
	require.NoError(t, err)
	require.Contains(t, rows, live.Id)
}

func TestFullScanOrphanSweepVisitsLivePagesAndNewTail(t *testing.T) {
	requireFullScanFixture(t)
	live := mkChannel(t, nil)
	ghost := nextTestID() + 1000000
	rows := make([]ChannelDailyUsage, 1001)
	for i := range rows {
		rows[i] = ChannelDailyUsage{ChannelId: live.Id, StatDate: int64(i + 1)}
	}
	require.NoError(t, DB.CreateInBatches(rows, 100).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id IN ?", []int{live.Id, ghost}).Delete(&ChannelDailyUsage{}).Error)
	})
	// Start at this fixture's first row, to make the fixed candidate page and
	// snapshot boundary observable even when earlier fixture IDs have gaps.
	sweep := channelUsageOrphanSweep{afterID: rows[0].Id - 1}
	deleted, err := sweep.run(&ChannelDailyUsage{})
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.Equal(t, rows[999].Id, sweep.afterID)
	late := ChannelDailyUsage{ChannelId: ghost, StatDate: 1}
	require.NoError(t, DB.Create(&late).Error)
	deleted, err = sweep.run(&ChannelDailyUsage{})
	require.NoError(t, err)
	require.Zero(t, deleted, "rows arriving after the snapshot wait for the next sweep")
	var count int64
	require.NoError(t, DB.Model(&ChannelDailyUsage{}).Where("id = ?", late.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	for range 5 {
		_, err = sweep.run(&ChannelDailyUsage{})
		require.NoError(t, err)
	}
	require.NoError(t, DB.Model(&ChannelDailyUsage{}).Where("id = ?", late.Id).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&ChannelDailyUsage{}).Where("channel_id = ?", live.Id).Count(&count).Error)
	require.EqualValues(t, 1001, count)
	// A real SQL error leaves the last successful position intact.
	failed := channelUsageOrphanSweep{afterID: 1, lastID: 5}
	_, err = failed.run(&struct{ Id int }{})
	require.Error(t, err)
	require.Equal(t, 1, failed.afterID)
	require.Equal(t, 5, failed.lastID)
}
