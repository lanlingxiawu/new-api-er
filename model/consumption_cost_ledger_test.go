package model

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMatchConsumptionCostLedgerTag(t *testing.T) {
	tests := []struct {
		name string
		row  ConsumptionCost
		tag  string
		want bool
	}{
		{
			name: "zero revenue without cost is zero revenue",
			row:  ConsumptionCost{RevenueQuota: 0, CostQuota: 0},
			tag:  ConsumptionCostLedgerTagZeroRevenue,
			want: true,
		},
		{
			name: "loss",
			row:  ConsumptionCost{RevenueQuota: 100, CostQuota: 120},
			tag:  ConsumptionCostLedgerTagLoss,
			want: true,
		},
		{
			name: "zero revenue with cost is loss",
			row:  ConsumptionCost{RevenueQuota: 0, CostQuota: 120},
			tag:  ConsumptionCostLedgerTagLoss,
			want: true,
		},
		{
			name: "zero revenue with cost does not show zero revenue tag",
			row:  ConsumptionCost{RevenueQuota: 0, CostQuota: 120},
			tag:  ConsumptionCostLedgerTagZeroRevenue,
			want: false,
		},
		{
			name: "profit",
			row:  ConsumptionCost{RevenueQuota: 120, CostQuota: 100},
			tag:  ConsumptionCostLedgerTagProfit,
			want: true,
		},
		{
			name: "reversal",
			row:  ConsumptionCost{RevenueQuota: -100, CostQuota: 10},
			tag:  ConsumptionCostLedgerTagReversal,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchConsumptionCostLedgerTag(tt.row, tt.tag); got != tt.want {
				t.Fatalf("matchConsumptionCostLedgerTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumptionCostLedgerAsyncStatsCacheTTL(t *testing.T) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	liveFilter := ConsumptionCostLedgerStatsFilter{
		ConsumptionCostLedgerCommonFilter: ConsumptionCostLedgerCommonFilter{
			StartTime: todayStart,
			EndTime:   now.Unix(),
		},
	}
	if got := consumptionCostLedgerAsyncStatsCacheTTL(liveFilter); got != consumptionCostLedgerAsyncStatsLiveCacheTTL {
		t.Fatalf("today TTL = %v, want %v", got, consumptionCostLedgerAsyncStatsLiveCacheTTL)
	}

	historyFilter := ConsumptionCostLedgerStatsFilter{
		ConsumptionCostLedgerCommonFilter: ConsumptionCostLedgerCommonFilter{
			StartTime: todayStart - int64(48*time.Hour/time.Second),
			EndTime:   todayStart - int64(24*time.Hour/time.Second),
		},
	}
	if got := consumptionCostLedgerAsyncStatsCacheTTL(historyFilter); got != consumptionCostLedgerAsyncStatsHistoryCacheTTL {
		t.Fatalf("history TTL = %v, want %v", got, consumptionCostLedgerAsyncStatsHistoryCacheTTL)
	}
}

func TestConsumptionCostLedgerAsyncStatsLockKeyUsesCacheKey(t *testing.T) {
	first := consumptionCostLedgerAsyncStatsLockKey("ledger:stats:v2:first")
	second := consumptionCostLedgerAsyncStatsLockKey("ledger:stats:v2:second")
	if first == second {
		t.Fatalf("lock keys should differ, got %q", first)
	}
	if strings.Contains(first, "global") || strings.Contains(second, "global") {
		t.Fatalf("lock key should not use global lock: %q %q", first, second)
	}
}

func TestBuildConsumptionCostLedgerTagsPutsPrimaryClassificationFirst(t *testing.T) {
	tags := BuildConsumptionCostLedgerTags(ConsumptionCost{RevenueQuota: 0, CostQuota: 50})
	if len(tags) != 1 {
		t.Fatalf("tags = %v, want only loss", tags)
	}
	if tags[0] != ConsumptionCostLedgerTagLoss {
		t.Fatalf("first tag = %q, want %q; tags=%v", tags[0], ConsumptionCostLedgerTagLoss, tags)
	}
}

func TestApplyConsumptionCostLedgerFiltersZeroRevenueCondition(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}
	tx, err := applyConsumptionCostLedgerFilters(db.Model(&ConsumptionCost{}), ConsumptionCostLedgerStatsFilter{
		ConsumptionCostLedgerCommonFilter: ConsumptionCostLedgerCommonFilter{Tag: ConsumptionCostLedgerTagZeroRevenue},
	})
	if err != nil {
		t.Fatalf("apply filters: %v", err)
	}
	sql := tx.Find(&[]ConsumptionCost{}).Statement.SQL.String()
	if !strings.Contains(sql, "cost_quota = 0") {
		t.Fatalf("filter SQL = %s, want condition %q", sql, "cost_quota = 0")
	}
}

func TestApplyConsumptionCostLedgerFiltersUsesColumnComparisonsForProfitTags(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}

	for _, tag := range []string{
		ConsumptionCostLedgerTagLoss,
		ConsumptionCostLedgerTagProfit,
	} {
		t.Run(tag, func(t *testing.T) {
			tx, err := applyConsumptionCostLedgerFilters(db.Model(&ConsumptionCost{}), ConsumptionCostLedgerStatsFilter{
				ConsumptionCostLedgerCommonFilter: ConsumptionCostLedgerCommonFilter{Tag: tag},
			})
			if err != nil {
				t.Fatalf("apply filters: %v", err)
			}
			stmt := tx.Find(&[]ConsumptionCost{}).Statement
			sql := stmt.SQL.String()
			if strings.Contains(sql, "revenue_quota - cost_quota") {
				t.Fatalf("filter SQL should avoid arithmetic diff expression, got %s", sql)
			}
			if strings.Contains(sql, "revenue_quota > 0") {
				t.Fatalf("filter SQL should not exclude zero-revenue rows from profit classification, got %s", sql)
			}
			if !strings.Contains(sql, "revenue_quota") || !strings.Contains(sql, "cost_quota") {
				t.Fatalf("filter SQL should compare revenue_quota and cost_quota, got %s", sql)
			}
		})
	}
}
