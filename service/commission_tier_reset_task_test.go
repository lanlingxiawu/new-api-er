package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// scheduledTimeInMonth：按月计算调度时刻，月末天数不足时夹紧到当月最后一天
// ---------------------------------------------------------------------------

func TestScheduledTimeInMonth(t *testing.T) {
	loc := time.UTC
	cfg := &operation_setting.CommissionTierResetSetting{
		ResetDay:    31,
		ResetHour:   3,
		ResetMinute: 30,
		ResetSecond: 15,
	}

	// 1月有31天，无需夹紧
	jan := scheduledTimeInMonth(2023, time.January, cfg, loc)
	assert.Equal(t, time.Date(2023, time.January, 31, 3, 30, 15, 0, loc), jan)

	// 4月只有30天，应夹紧到30日
	apr := scheduledTimeInMonth(2023, time.April, cfg, loc)
	assert.Equal(t, time.Date(2023, time.April, 30, 3, 30, 15, 0, loc), apr)

	// 2月（非闰年）只有28天，应夹紧到28日
	feb := scheduledTimeInMonth(2023, time.February, cfg, loc)
	assert.Equal(t, time.Date(2023, time.February, 28, 3, 30, 15, 0, loc), feb)

	// 2月（闰年）有29天，应夹紧到29日
	feb2024 := scheduledTimeInMonth(2024, time.February, cfg, loc)
	assert.Equal(t, time.Date(2024, time.February, 29, 3, 30, 15, 0, loc), feb2024)
}

// ---------------------------------------------------------------------------
// lastScheduledTimeAtOrBefore：计算 now 之前（含等于）最近一次调度时刻
// ---------------------------------------------------------------------------

func TestLastScheduledTimeAtOrBefore(t *testing.T) {
	loc := time.UTC
	cfg := &operation_setting.CommissionTierResetSetting{
		ResetDay:    15,
		ResetHour:   12,
		ResetMinute: 0,
		ResetSecond: 0,
	}

	// now 在本月调度时刻之后 -> 返回本月调度时刻
	now := time.Date(2024, time.March, 20, 0, 0, 0, 0, loc)
	got := lastScheduledTimeAtOrBefore(now, cfg)
	want := time.Date(2024, time.March, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// now 在本月调度时刻之前 -> 返回上个月调度时刻
	now = time.Date(2024, time.March, 10, 0, 0, 0, 0, loc)
	got = lastScheduledTimeAtOrBefore(now, cfg)
	want = time.Date(2024, time.February, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// now 恰好等于调度时刻 -> 返回该时刻本身（含等于）
	now = time.Date(2024, time.March, 15, 12, 0, 0, 0, loc)
	got = lastScheduledTimeAtOrBefore(now, cfg)
	want = time.Date(2024, time.March, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// 跨年：1月初 -> 上一年12月调度时刻
	now = time.Date(2024, time.January, 1, 0, 0, 0, 0, loc)
	got = lastScheduledTimeAtOrBefore(now, cfg)
	want = time.Date(2023, time.December, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// 月末夹紧：ResetDay=31，本月候选(3/31)在 now 之后 -> 取2月最后一天
	cfgEom := &operation_setting.CommissionTierResetSetting{ResetDay: 31, ResetHour: 0, ResetMinute: 0, ResetSecond: 0}
	now = time.Date(2023, time.March, 1, 0, 0, 0, 0, loc)
	got = lastScheduledTimeAtOrBefore(now, cfgEom)
	want = time.Date(2023, time.February, 28, 0, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// NextScheduledResetAt：计算严格晚于 now 的下一次调度时刻
// ---------------------------------------------------------------------------

func TestNextScheduledResetAt(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	original := *cfg
	defer func() { *cfg = original }()

	cfg.ResetDay = 15
	cfg.ResetHour = 12
	cfg.ResetMinute = 0
	cfg.ResetSecond = 0

	loc := time.Local

	// now 在本月调度时刻之前 -> 下一次为本月调度时刻
	now := time.Date(2024, time.March, 10, 0, 0, 0, 0, loc)
	got := NextScheduledResetAt(now)
	want := time.Date(2024, time.March, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// now 等于本月调度时刻（不含等于）-> 下一次为下个月调度时刻
	now = time.Date(2024, time.March, 15, 12, 0, 0, 0, loc)
	got = NextScheduledResetAt(now)
	want = time.Date(2024, time.April, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)

	// 跨年：12月 -> 下一年1月
	now = time.Date(2024, time.December, 20, 0, 0, 0, 0, loc)
	got = NextScheduledResetAt(now)
	want = time.Date(2025, time.January, 15, 12, 0, 0, 0, loc).Unix()
	assert.Equal(t, want, got)
}
