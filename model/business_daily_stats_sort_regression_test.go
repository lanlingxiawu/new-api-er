package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelStatsPaginationStableTies(t *testing.T) {
	requireDB(t)
	date := bsFarFutureDay(t)
	keyword := uniq("stable")
	channelIDs := make([]int, 0, 7)
	for range 7 {
		channelID := nextTestID()
		channelIDs = append(channelIDs, channelID)
		cleanupPlatformDailyRow(t, date, channelID)
		require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: date, ChannelId: channelID, ChannelName: keyword, RevenueQuota: 37, CostQuota: 12, RecordCount: 1, CostRatioSum: 1}).Error)
	}
	plan := &ResolvedQueryPlan{Context: context.Background(), CoveredRanges: []statDateRange{{StartDate: date, EndDate: date}}}
	for _, field := range []string{"channel_name", "cost_ratio", "consumption_quota", "est_cost_quota", "est_gross_margin", "est_profit"} {
		for _, direction := range []string{"asc", "desc"} {
			var found []int
			for page := 1; page <= 4; page++ {
				rows, total, err := GetConsumptionCostByChannelPageWithPlan(plan, page, 2, keyword, field, direction)
				require.NoError(t, err)
				require.EqualValues(t, len(channelIDs), total)
				for _, row := range rows {
					found = append(found, row.ChannelId)
				}
			}
			require.Equal(t, channelIDs, found, field+"/"+direction)
		}
	}
}

func TestChannelStatsStableTies(t *testing.T) {
	for _, field := range []string{"channel_name", "cost_ratio", "consumption_quota", "est_cost_quota", "est_gross_margin", "est_profit"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(field+"/"+direction, func(t *testing.T) {
				rows := []*ConsumptionCostChannelStat{{ChannelId: 3}, {ChannelId: 1}, {ChannelId: 2}}
				sortConsumptionCostChannels(rows, field, direction)
				require.Equal(t, []int{1, 2, 3}, []int{rows[0].ChannelId, rows[1].ChannelId, rows[2].ChannelId})
			})
		}
	}
}
