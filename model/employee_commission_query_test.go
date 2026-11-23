package model

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestCommissionLogPage(t *testing.T) {
	db := cleanupTestDB(t)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	rows := []EmployeeCommissionLog{
		{Id: 1, EmployeeId: 1, EmployeeUserId: 101, CustomerUserId: 201, ChannelId: 7, ModelName: "query-alpha", CreatedAt: 100, ProfitQuota: 0},
		{Id: 2, EmployeeId: 1, EmployeeUserId: 101, CustomerUserId: 201, ChannelId: 7, ModelName: "query-alpha", CreatedAt: 110, ProfitQuota: 20},
		{Id: 3, EmployeeId: 1, EmployeeUserId: 101, CustomerUserId: 202, ChannelId: 8, ModelName: "query-beta", CreatedAt: 110, ProfitQuota: -10},
		{Id: 4, EmployeeId: 1, EmployeeUserId: 101, CustomerUserId: 202, ChannelId: 8, ModelName: "query-beta", CreatedAt: 110, ProfitQuota: 30},
		{Id: 5, EmployeeId: 2, EmployeeUserId: 102, CustomerUserId: 203, ChannelId: 7, ModelName: "query-alpha", CreatedAt: 110, ProfitQuota: 50},
		{Id: 6, EmployeeId: 1, EmployeeUserId: 101, CustomerUserId: 201, CreatedAt: 111},
	}
	require.NoError(t, db.Create(&rows).Error)
	filter := CommissionLogPageFilter{CommissionLogFilter: CommissionLogFilter{EmployeeUserId: 101, StartTime: 100, EndTime: 110, PageSize: 2}}

	// The legacy count cannot run when its cleanup receipt store is unavailable.
	// Existing details must remain readable independently of that count.
	require.NoError(t, db.Migrator().DropTable(&DataCleanupRun{}))
	_, _, err := GetCommissionLogs(filter.CommissionLogFilter)
	require.Error(t, err)
	page, err := ListCommissionLogs(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, []int{4, 3}, []int{page.Items[0].Id, page.Items[1].Id})
	require.True(t, page.HasMore)
	require.Equal(t, &CommissionLogCursor{CreatedAt: 110, Id: 3}, page.NextCursor)
	filter.Cursor = page.NextCursor
	page, err = ListCommissionLogs(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, []int{2, 1}, []int{page.Items[0].Id, page.Items[1].Id})
	require.False(t, page.HasMore)
	require.Nil(t, page.NextCursor)
	lossPage, err := ListCommissionLogs(context.Background(), CommissionLogPageFilter{
		CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 10, LossStatus: "loss"},
	})
	require.NoError(t, err)
	require.Len(t, lossPage.Items, 1)
	require.Equal(t, 3, lossPage.Items[0].Id)

	// A deleted cursor row does not invalidate the next boundary.
	require.NoError(t, db.Delete(&EmployeeCommissionLog{}, 3).Error)
	page, err = ListCommissionLogs(context.Background(), filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, 2, page.Items[0].Id)

	for _, tc := range []struct {
		name   string
		filter CommissionLogFilter
		ids    []int
	}{
		{"all employees", CommissionLogFilter{}, []int{5, 4, 2, 1}},
		{"customer", CommissionLogFilter{CustomerUserId: 201}, []int{2, 1}},
		{"channel", CommissionLogFilter{ChannelId: 8}, []int{4}},
		{"model", CommissionLogFilter{ModelName: "alpha"}, []int{5, 2, 1}},
		{"loss after cleanup", CommissionLogFilter{LossStatus: "loss"}, []int{}},
		{"non-loss includes zero", CommissionLogFilter{LossStatus: "normal"}, []int{5, 4, 2, 1}},
		{"combined", CommissionLogFilter{EmployeeUserId: 101, CustomerUserId: 201, ChannelId: 7, ModelName: "alpha", LossStatus: "normal"}, []int{2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.filter.StartTime, tc.filter.EndTime, tc.filter.PageSize = 100, 110, 100
			page, err := ListCommissionLogs(context.Background(), CommissionLogPageFilter{CommissionLogFilter: tc.filter})
			require.NoError(t, err)
			ids := make([]int, 0, len(page.Items))
			for _, row := range page.Items {
				ids = append(ids, row.Id)
			}
			require.Equal(t, tc.ids, ids)
			require.False(t, page.HasMore)
			require.NotNil(t, page.Items)
		})
	}
	t.Run("channel cursor with equal timestamps", func(t *testing.T) {
		filter := CommissionLogPageFilter{CommissionLogFilter: CommissionLogFilter{
			StartTime: 100, EndTime: 110, PageSize: 1, ChannelId: 7,
		}}
		for index, id := range []int{5, 2, 1} {
			page, err := ListCommissionLogs(context.Background(), filter)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, id, page.Items[0].Id)
			require.Equal(t, index < 2, page.HasMore)
			filter.Cursor = page.NextCursor
		}
		require.Nil(t, filter.Cursor)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ListCommissionLogs(ctx, filter)
	require.ErrorIs(t, err, context.Canceled)
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = ListCommissionLogs(ctx, filter)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCommissionLogPageRejectsUnboundedQueries(t *testing.T) {
	for _, filter := range []CommissionLogPageFilter{
		{},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 99, PageSize: 10}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 100 + 31*86400 + 1, PageSize: 10}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 101}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: -1}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 10, EmployeeUserId: -1}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 10, LossStatus: "invalid"}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 10}, Cursor: &CommissionLogCursor{CreatedAt: 111, Id: 1}},
		{CommissionLogFilter: CommissionLogFilter{StartTime: 100, EndTime: 110, PageSize: 10}, Cursor: &CommissionLogCursor{CreatedAt: 100}},
	} {
		_, err := ListCommissionLogs(context.Background(), filter)
		require.ErrorIs(t, err, ErrInvalidCommissionLogQuery)
	}
}

func TestCommissionLogPageMySQLStopsTimedOutStatement(t *testing.T) {
	db := cleanupTestDB(t)
	if db.Dialector.Name() != "mysql" {
		t.Skip("MySQL server execution limit")
	}
	previous := DB
	t.Cleanup(func() { DB = previous })
	require.NoError(t, db.Create(&EmployeeCommissionLog{Id: 1, CreatedAt: 100}).Error)
	var connectionID uint64
	t.Cleanup(func() {
		var active int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ? AND COMMAND IN ('Query', 'Execute')", connectionID).Scan(&active).Error)
		if active > 0 {
			require.NoError(t, db.Exec("KILL CONNECTION "+strconv.FormatUint(connectionID, 10)).Error)
		}
	})
	require.NoError(t, db.Connection(func(conn *gorm.DB) error {
		require.NoError(t, conn.Raw("SELECT CONNECTION_ID()").Scan(&connectionID).Error)
		// A real, deliberately slow read of this test's private table. Client
		// cancellation alone used to leave the prepared statement executing.
		DB = conn.Table("(SELECT * FROM ? WHERE SLEEP(30) = 0) AS delayed_commission",
			clause.Table{Name: db.NamingStrategy.TableName("EmployeeCommissionLog")})
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, err := ListCommissionLogs(ctx, CommissionLogPageFilter{CommissionLogFilter: CommissionLogFilter{
			StartTime: 100, EndTime: 110, PageSize: 10,
		}})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		return nil
	}))
	require.Eventually(t, func() bool {
		var active int64
		err := db.Raw("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ? AND COMMAND IN ('Query', 'Execute')", connectionID).Scan(&active).Error
		return err == nil && active == 0
	}, 5*time.Second, 50*time.Millisecond, "timed-out MySQL statement must stop on the server")
}
