package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 同一渠道上并发的「清除限额标记」合并。设计见 docs/design/channel-daily-quota-limit.md。

// TestClearDailyLimitMarksIfPresent_LateArrivalsGetTheirOwnClear 锁住合并的正确性边界：
// 到达时已经在跑的那次清除不能算数（它的 UPDATE 可能早于另一节点写入标记），
// 但同一批后到者可以共用下一次。
func TestClearDailyLimitMarksIfPresent_LateArrivalsGetTheirOwnClear(t *testing.T) {
	channelID := nextTestID()
	var calls int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	original := dailyLimitMarkClearExec
	dailyLimitMarkClearExec = func(int) {
		atomic.AddInt32(&calls, 1)
		entered <- struct{}{}
		<-release
	}
	t.Cleanup(func() { dailyLimitMarkClearExec = original })

	var wg sync.WaitGroup
	call := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clearDailyLimitMarksIfPresent(channelID)
		}()
	}

	call()
	<-entered // 第一次清除正在跑
	call()
	call()
	time.Sleep(50 * time.Millisecond) // 让两个后到者进入等待
	release <- struct{}{}             // 放行第一次
	<-entered                         // 后到者触发的第二次
	close(release)
	wg.Wait()

	assert.EqualValues(t, 2, atomic.LoadInt32(&calls),
		"late arrivals must not piggyback on the clear already running, but may share the next one")
}

// panic 必须被吞掉并复位合并状态，否则该渠道之后的所有调用都会永久阻塞。
func TestClearDailyLimitMarksIfPresent_SurvivesPanic(t *testing.T) {
	channelID := nextTestID()
	var calls int32
	original := dailyLimitMarkClearExec
	dailyLimitMarkClearExec = func(int) {
		if atomic.AddInt32(&calls, 1) == 1 {
			panic("boom")
		}
	}
	t.Cleanup(func() { dailyLimitMarkClearExec = original })

	assert.NotPanics(t, func() { clearDailyLimitMarksIfPresent(channelID) })
	done := make(chan struct{})
	go func() {
		clearDailyLimitMarksIfPresent(channelID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a panic in one clear must not wedge later callers")
	}
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

func cacheChannelForDailyLimitTest(t *testing.T, ch *Channel) {
	t.Helper()
	origCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	cached := *ch
	channelSyncLock.Lock()
	// 测试进程没跑过 InitChannelCache，map 可能是 nil。
	mapWasNil := channelsIDM == nil
	if mapWasNil {
		channelsIDM = make(map[int]*Channel)
	}
	prev, had := channelsIDM[ch.Id]
	channelsIDM[ch.Id] = &cached
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		if mapWasNil {
			channelsIDM = nil
		} else if had {
			channelsIDM[ch.Id] = prev
		} else {
			delete(channelsIDM, ch.Id)
		}
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = origCache
	})
}

// 上游故障时，已禁用渠道上的在途失败会并发调用 UpdateChannelStatus。它们必须合并成同一时刻
// 最多一条 UPDATE，不能各占一条与 relay 共用的连接。
func TestUpdateChannelStatus_ConcurrentRepeatDisablesAreCoalesced(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyQuotaLimit = 100
	})
	cacheChannelForDailyLimitTest(t, ch)
	total, maxInflight := countConcurrentChannelUpdates(t, ch.Id, 100)

	assert.EqualValues(t, 1, maxInflight, "at most one clear per channel may be in flight")
	assert.Less(t, total, int64(100), "concurrent callers must share clears")
}

// 没有配置上限、也不带标记的渠道（绝大多数渠道）不可能有限额标记，状态变更时不多写一次库。
func TestUpdateChannelStatus_UnlimitedChannelSkipsMarkClear(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.Status = common.ChannelStatusAutoDisabled })
	cacheChannelForDailyLimitTest(t, ch)
	total, _ := countConcurrentChannelUpdates(t, ch.Id, 20)
	assert.Zero(t, total, "a channel without a limit must not issue mark-clearing UPDATEs")

	// 缓存里带标记（上限已被清掉的限额禁用渠道）仍要清。
	marked := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyLimitDisabledAt = 1_700_000_100
		c.DailyLimitDisabledDate = 1_700_000_000
	})
	cacheChannelForDailyLimitTest(t, marked)
	UpdateChannelStatus(marked.Id, "", common.ChannelStatusAutoDisabled, "upstream error")
	assert.Zero(t, reloadChannel(t, marked.Id).DailyLimitDisabledAt)
}

// countConcurrentChannelUpdates 让 callers 个 goroutine 同时对同一渠道重复禁用，返回期间发出的
// UPDATE channels 条数与最大并发数。
func countConcurrentChannelUpdates(t *testing.T, channelId int, callers int) (int64, int64) {
	t.Helper()
	var total, inflight, maxInflight int64
	const before, after = "daily_limit_coalesce:before", "daily_limit_coalesce:after"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(before, func(tx *gorm.DB) {
		if tx.Statement.Table != "channels" {
			return
		}
		atomic.AddInt64(&total, 1)
		current := atomic.AddInt64(&inflight, 1)
		for {
			seen := atomic.LoadInt64(&maxInflight)
			if current <= seen || atomic.CompareAndSwapInt64(&maxInflight, seen, current) {
				break
			}
		}
	}))
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(after, func(tx *gorm.DB) {
		if tx.Statement.Table == "channels" {
			atomic.AddInt64(&inflight, -1)
		}
	}))
	t.Cleanup(func() {
		_ = DB.Callback().Update().Remove(before)
		_ = DB.Callback().Update().Remove(after)
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			UpdateChannelStatus(channelId, "", common.ChannelStatusAutoDisabled, "upstream error")
		}()
	}
	close(start)
	wg.Wait()

	t.Logf("concurrent repeat disables=%d  UPDATE channels issued=%d  max in flight=%d", callers, total, maxInflight)
	return atomic.LoadInt64(&total), atomic.LoadInt64(&maxInflight)
}

// 合并之后，标记仍然必须被清掉：限额禁用后上游报错的并发突发，结束时不能留下标记。
func TestUpdateChannelStatus_ConcurrentRepeatDisablesStillClearMarks(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_691_200)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)
	cacheChannelForDailyLimitTest(t, reloadChannel(t, ch.Id))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			UpdateChannelStatus(ch.Id, "", common.ChannelStatusAutoDisabled, "upstream error")
		}()
	}
	wg.Wait()

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.Zero(t, got.DailyLimitDisabledDate)
}
