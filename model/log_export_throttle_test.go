package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withLogExportSetting 临时改配置并在测试结束后还原。
// 同时验证「配置在使用点现取」——闸门必须立刻看到新值。
func withLogExportSetting(t *testing.T, mut func(s *operation_setting.LogExportSetting)) {
	t.Helper()
	s := operation_setting.GetLogExportSetting()
	prev := *s
	mut(s)
	t.Cleanup(func() { *operation_setting.GetLogExportSetting() = prev })
}

// fakeGate 返回一个不真正睡眠的闸门，累计请求的睡眠时长供断言。
func fakeGate(cpu float64, cpuOK bool, now time.Time) (*exportGate, *time.Duration) {
	var slept time.Duration
	g := newExportGate()
	g.sleepFn = func(_ context.Context, d time.Duration) { slept += d }
	g.nowFn = func() time.Time { return now }
	g.cpuFn = func() (float64, bool) { return cpu, cpuOK }
	return g, &slept
}

func TestExportGate_BelowSoftLimitSleepsBaseOnly(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
		s.MaxRowsPerSec = 1000000 // 令牌桶不参与本用例
	})
	g, slept := fakeGate(10, true, time.Now())
	g.Wait(context.Background(), 100)
	assert.Equal(t, 100*time.Millisecond, *slept)
}

func TestExportGate_SoftLimitScalesSleepLinearly(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 90
		s.MaxRowsPerSec = 1000000
	})
	// 恰好在软限：额外休眠为 0。
	g, slept := fakeGate(70, true, time.Now())
	g.Wait(context.Background(), 10)
	assert.Equal(t, 100*time.Millisecond, *slept)

	// 软硬限中点：额外休眠 = base * 0.5 * 4 = 200ms。
	g, slept = fakeGate(80, true, time.Now())
	g.Wait(context.Background(), 10)
	assert.Equal(t, 300*time.Millisecond, *slept)

	// 逼近硬限：额外休眠趋近 base * 4。
	g, slept = fakeGate(89, true, time.Now())
	g.Wait(context.Background(), 10)
	assert.InDelta(t, float64(480*time.Millisecond), float64(*slept), float64(30*time.Millisecond))
}

func TestExportGate_HardLimitPausesUntilCPURecovers(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 10
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
		s.CPUCheckIntervalMs = 50
		s.MaxRowsPerSec = 1000000
	})
	g, slept := fakeGate(95, true, time.Now())
	calls := 0
	// 前 3 次检查仍在硬限之上，第 4 次恢复。
	g.cpuFn = func() (float64, bool) {
		calls++
		if calls <= 3 {
			return 95, true
		}
		return 10, true
	}
	g.Wait(context.Background(), 10)
	assert.Equal(t, 3*50*time.Millisecond+10*time.Millisecond, *slept)
	assert.Equal(t, (3*50+10)*int(time.Millisecond/time.Millisecond), int(g.ThrottledMs()))
}

// cpu_hard_limit=0 是「一键刹车」：运行中的任务必须立刻进入暂停。
func TestExportGate_HardLimitZeroActsAsBrake(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 10
		s.CPUHardLimit = 0
		s.CPUCheckIntervalMs = 20
		s.MaxRowsPerSec = 1000000
	})
	ctx, cancel := context.WithCancel(context.Background())
	g, slept := fakeGate(1, true, time.Now())
	checks := 0
	g.sleepFn = func(_ context.Context, d time.Duration) {
		*slept += d
		checks++
		if checks >= 5 {
			cancel() // 模拟任务被取消，避免用例无限等待
		}
	}
	g.Wait(ctx, 10)
	assert.GreaterOrEqual(t, checks, 5, "hard limit 0 must keep the job paused")
}

// GetSystemStatus() 在性能监控关闭时恒为零值。零值必须被当作「无 CPU 信息」，
// 退化为固定节流，绝不能被当成「CPU 空闲」而放开限速。
func TestExportGate_NoCPUReadingKeepsFixedThrottle(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
		s.MaxRowsPerSec = 1000000
	})
	g, slept := fakeGate(0, false, time.Now())
	g.Wait(context.Background(), 10)
	assert.Equal(t, 100*time.Millisecond, *slept, "must still sleep the base interval without CPU data")
}

func TestCurrentCPUUsage_ZeroMeansUnavailable(t *testing.T) {
	// 未启动性能监控时 GetSystemStatus() 返回零值，此时读数不可用。
	usage, ok := currentCPUUsage()
	if !ok {
		assert.Zero(t, usage)
	} else {
		assert.Greater(t, usage, float64(0))
	}
}

func TestExportGate_TokenBucketCapsRowRate(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.MaxRowsPerSec = 10000
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
	})
	// 3000 行 @ 10000 行/s 需要 300ms，基础休眠已占 100ms，应补 200ms。
	g, slept := fakeGate(10, true, time.Now())
	g.Wait(context.Background(), 3000)
	assert.Equal(t, 300*time.Millisecond, *slept)

	// 批量小于配额时不额外补时间。
	g, slept = fakeGate(10, true, time.Now())
	g.Wait(context.Background(), 100)
	assert.Equal(t, 100*time.Millisecond, *slept)
}

func TestExportGate_ObserveAdjustsPenalty(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
	})
	g, _ := fakeGate(10, true, time.Now())

	g.Observe(20 * time.Millisecond) // 建立基线
	assert.Zero(t, g.penalty)

	g.Observe(100 * time.Millisecond) // 慢 5 倍 → 加压
	assert.Equal(t, 100*time.Millisecond, g.penalty)

	g.Observe(100 * time.Millisecond) // 持续慢 → 继续加压
	assert.Equal(t, 200*time.Millisecond, g.penalty)

	g.Observe(21 * time.Millisecond) // 恢复 → 衰减
	assert.Equal(t, 100*time.Millisecond, g.penalty)
	g.Observe(21 * time.Millisecond)
	assert.Equal(t, 50*time.Millisecond, g.penalty)
}

func TestExportGate_ObservePenaltyIsCapped(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
	})
	g, _ := fakeGate(10, true, time.Now())
	g.Observe(10 * time.Millisecond)
	for i := 0; i < 20; i++ {
		g.Observe(time.Second)
	}
	assert.Equal(t, 800*time.Millisecond, g.penalty, "penalty must be capped at 8x base sleep")
}

func TestExportGate_OffpeakWaitsOutsideWindow(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.OffpeakOnly = true
		s.OffpeakWindow = "02:00-06:00"
		s.BatchSleepMs = 10
		s.MaxRowsPerSec = 1000000
		s.CPUHardLimit = 85
		s.CPUSoftLimit = 70
	})
	// 窗口内：不等待。
	inWindow := time.Date(2026, 7, 27, 3, 0, 0, 0, time.Local)
	g, slept := fakeGate(10, true, inWindow)
	g.Wait(context.Background(), 10)
	assert.Equal(t, 10*time.Millisecond, *slept)

	// 窗口外：持续等待，直到 ctx 取消。
	outWindow := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	ctx, cancel := context.WithCancel(context.Background())
	g, _ = fakeGate(10, true, outWindow)
	waits := 0
	g.sleepFn = func(_ context.Context, _ time.Duration) {
		waits++
		if waits >= 3 {
			cancel()
		}
	}
	g.Wait(ctx, 10)
	assert.GreaterOrEqual(t, waits, 3)
}

func TestExportGate_CanceledContextReturnsImmediately(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g, slept := fakeGate(10, true, time.Now())
	g.Wait(ctx, 100)
	assert.Zero(t, *slept)
}

func TestSleepCtx_ReturnsEarlyOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, 2*time.Second)
	require.Less(t, time.Since(start), 500*time.Millisecond)
}
