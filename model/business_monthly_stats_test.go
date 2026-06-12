package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestResolveCommissionMonthlyPeriodDefaultChinaTimezone(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	originalDay := cfg.ResetDay
	originalHour := cfg.ResetHour
	originalMinute := cfg.ResetMinute
	originalSecond := cfg.ResetSecond
	originalTimezone := cfg.Timezone
	defer func() {
		cfg.ResetDay = originalDay
		cfg.ResetHour = originalHour
		cfg.ResetMinute = originalMinute
		cfg.ResetSecond = originalSecond
		cfg.Timezone = originalTimezone
	}()

	cfg.ResetDay = 10
	cfg.ResetHour = 0
	cfg.ResetMinute = 0
	cfg.ResetSecond = 0
	cfg.Timezone = "Asia/Shanghai"

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	beforeReset := time.Date(2026, time.June, 9, 23, 59, 59, 0, loc).Unix()
	beforePeriod := ResolveCommissionMonthlyPeriod(beforeReset)
	if beforePeriod.PeriodKey != "2026-05-10" {
		t.Fatalf("expected previous period key 2026-05-10, got %s", beforePeriod.PeriodKey)
	}
	if beforePeriod.Timezone != "Asia/Shanghai" {
		t.Fatalf("expected Asia/Shanghai timezone, got %s", beforePeriod.Timezone)
	}

	atReset := time.Date(2026, time.June, 10, 0, 0, 0, 0, loc).Unix()
	currentPeriod := ResolveCommissionMonthlyPeriod(atReset)
	if currentPeriod.PeriodKey != "2026-06-10" {
		t.Fatalf("expected current period key 2026-06-10, got %s", currentPeriod.PeriodKey)
	}
	if currentPeriod.PeriodStartAt != atReset {
		t.Fatalf("expected period start %d, got %d", atReset, currentPeriod.PeriodStartAt)
	}
}
