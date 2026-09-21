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

// waitFullBatch 走一遍「读满一批」的完整闸门流程。
//
// 本文件下方所有断言都建立在它之上，且数值全部继承自闸门按批大小计费的旧实现——
// 这是「满批行为逐毫秒不变」这条不变式的锁。改动闸门时若这些断言开始飘，
// 说明限速强度被悄悄改了，而不是测试过时了。
func waitFullBatch(g *exportGate, ctx context.Context, rows int) {
	g.Before(ctx)
	g.Charge(ctx, rows, rows)
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
	waitFullBatch(g, context.Background(), 100)
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
	waitFullBatch(g, context.Background(), 10)
	assert.Equal(t, 100*time.Millisecond, *slept)

	// 软硬限中点：额外休眠 = base * 0.5 * 4 = 200ms。
	g, slept = fakeGate(80, true, time.Now())
	waitFullBatch(g, context.Background(), 10)
	assert.Equal(t, 300*time.Millisecond, *slept)

	// 逼近硬限：额外休眠趋近 base * 4。
	g, slept = fakeGate(89, true, time.Now())
	waitFullBatch(g, context.Background(), 10)
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
	waitFullBatch(g, context.Background(), 10)
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
	waitFullBatch(g, ctx, 10)
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
	waitFullBatch(g, context.Background(), 10)
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
	waitFullBatch(g, context.Background(), 3000)
	assert.Equal(t, 300*time.Millisecond, *slept)

	// 批量小于配额时不额外补时间。
	g, slept = fakeGate(10, true, time.Now())
	waitFullBatch(g, context.Background(), 100)
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
	waitFullBatch(g, context.Background(), 10)
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
	waitFullBatch(g, ctx, 10)
	assert.GreaterOrEqual(t, waits, 3)
}

func TestExportGate_CanceledContextReturnsImmediately(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g, slept := fakeGate(10, true, time.Now())
	waitFullBatch(g, ctx, 100)
	assert.Zero(t, *slept)
}

func TestSleepCtx_ReturnsEarlyOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, 2*time.Second)
	require.Less(t, time.Since(start), 500*time.Millisecond)
}

// ── 按实际读到的行数计费 ──────────────────────────────────────────

// 计费必须跟着「实际读到多少行」走，而不是「请求了多大的批」。
// 这是修掉「每个窗口的最后一批与每个空窗口都按满批付钱」的核心断言。
func TestExportGate_ChargeByRowsRead(t *testing.T) {
	setup := func() {
		withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
			s.BatchSleepMs = 100
			s.MaxRowsPerSec = 20000
			s.CPUSoftLimit = 70
			s.CPUHardLimit = 85
		})
	}
	cases := []struct {
		name     string
		rowsRead int
		want     time.Duration
	}{
		// 满批：base=100ms，令牌桶需要 3000/20000=150ms，取较大者。与旧实现一致。
		{"满批", 3000, 150 * time.Millisecond},
		// 半批：base*0.5=50ms，令牌桶需要 1500/20000=75ms，取较大者。
		{"半批", 1500, 75 * time.Millisecond},
		// 零行：没做任何工作，只记下地板价的债，尚不足以兑现成一次休眠。
		// 旧实现在这里要实打实睡满 150ms。
		{"空批", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup()
			g, slept := fakeGate(10, true, time.Now())
			g.Before(context.Background())
			g.Charge(context.Background(), tc.rowsRead, 3000)
			assert.Equal(t, tc.want, *slept)
		})
	}
}

// 单批应付休眠不足阈值时不能被丢掉，要累积后一次兑现——否则大量小批次会把
// 限速整体架空。
// 空批不是完全免费：地板价保证连续的空窗口之间仍有间隔，不会退化成不带任何
// 停顿的连续查询。攒够阈值后一次兑现。
func TestExportGate_EmptyBatchesStillPayFloor(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.MaxRowsPerSec = 20000
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
	})
	g, slept := fakeGate(10, true, time.Now())
	for range 3 {
		g.Before(context.Background())
		g.Charge(context.Background(), 0, 3000)
	}
	// 3 × 2ms 地板价 = 6ms，跨过 5ms 阈值后一次睡掉。
	assert.Equal(t, 3*exportGateMinCharge, *slept)
}

// 单批应付休眠不足阈值时只能记债、不能丢账——否则大量小批次会把限速整体架空。
// 断言的是「总账分毫不差」，而不是某一批睡了多久。
func TestExportGate_MicroSleepDebtIsNotLost(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 0 // 只留令牌桶，便于精确核账
		s.MaxRowsPerSec = 20000
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
	})
	g, slept := fakeGate(10, true, time.Now())
	sleepCalls := 0
	inner := g.sleepFn
	g.sleepFn = func(ctx context.Context, d time.Duration) {
		assert.GreaterOrEqual(t, d, exportGateMinSleep, "不该出现微休眠")
		sleepCalls++
		inner(ctx, d)
	}
	// 每批 60 行 → 60/20000 = 3ms，单批不足 5ms 阈值，必须靠攒债兑现。
	const batches, rows = 10, 60
	for range batches {
		g.Before(context.Background())
		g.Charge(context.Background(), rows, 3000)
	}
	// 600 行 @ 20000 行/s = 30ms，一毫秒都不能少收。
	assert.Equal(t, 30*time.Millisecond, *slept)
	assert.EqualValues(t, 30, g.ThrottledMs())
	assert.Less(t, sleepCalls, batches, "应当合并成更少的几次休眠")
}

// penalty 与 CPU 软限额外休眠是压力信号，不随本批产出缩放——
// 数据库正在变慢时，读得少也要照样退让。
func TestExportGate_PressureSignalsNotScaledByRows(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 100
		s.MaxRowsPerSec = 1000000 // 令牌桶不参与
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 90
	})
	// CPU 处于软硬限中点 → 额外休眠 = base*0.5*4 = 200ms，与行数无关。
	g, slept := fakeGate(80, true, time.Now())
	g.penalty = 300 * time.Millisecond
	g.Before(context.Background())
	// 只读到 1 行：base 部分几乎为 0，但 200ms 软限 + 300ms penalty 必须全额生效。
	g.Charge(context.Background(), 1, 3000)
	base := 100 * time.Millisecond
	assert.Equal(t, 500*time.Millisecond+time.Duration(float64(base)/3000), *slept)
}

// CPU 硬限以上必须在**查询之前**就拦住，不能读完再说。
func TestExportGate_BeforeStillGatesCPUHardLimit(t *testing.T) {
	withLogExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs = 10
		s.CPUSoftLimit = 70
		s.CPUHardLimit = 85
		s.CPUCheckIntervalMs = 50
		s.MaxRowsPerSec = 1000000
	})
	g, slept := fakeGate(95, true, time.Now())
	calls := 0
	g.cpuFn = func() (float64, bool) {
		calls++
		if calls <= 2 {
			return 95, true
		}
		return 10, true
	}
	g.Before(context.Background())
	assert.Equal(t, 2*50*time.Millisecond, *slept, "硬限之上必须在开工前就暂停")
}
