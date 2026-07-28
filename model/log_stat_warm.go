package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ---------------------------------------------------------------------------
// 整点预热。
//
// 目的：让默认视图（今天 00:00 → 现在）第一次打开就全部命中缓存，
// 而不是逐个整点回源。稳态成本是**每小时 1 条查询**，每条只扫 1 小时数据。
//
// 只预热「无筛选」指纹。带筛选的组合按需惰性填充，见
// docs/design/usage-log-list-performance.md §6.5。
// ---------------------------------------------------------------------------

const (
	// logStatWarmLockTTL 是多 master 场景下的互斥窗口。
	// 单个整点的聚合只扫 1 小时数据，60s 足够覆盖一次计算。
	logStatWarmLockTTL = 60 * time.Second
	// logStatWarmSteadyHours 是稳态每轮回看的整点数。
	// 已缓存的整点会被直接跳过，所以多看一两个只是廉价的自愈：
	// 进程重启或某轮出错漏掉的整点，下一轮就能补上。
	logStatWarmSteadyHours = 3
)

func logStatWarmLockKey(hour int64) string {
	return fmt.Sprintf("%s:lock:%s:%d", logStatCacheNamespace, logStatCacheVersion, hour)
}

// acquireLogStatWarmLock 在多 master 部署下避免多个节点重复算同一个整点。
// Redis 不可用时返回 true——单机场景本就没有竞争，不该因此停掉预热。
func acquireLogStatWarmLock(ctx context.Context, hour int64) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return true
	}
	ok, err := common.RDB.SetNX(ctx, logStatWarmLockKey(hour), "1", logStatWarmLockTTL).Result()
	if err != nil {
		// 判断失败时选择继续：重复算一次只是多一条查询，
		// 而漏算会让用户实打实等 6 秒。
		common.SysError("log stat warm lock failed: " + err.Error())
		return true
	}
	return ok
}

// warmLogStatHour 预热单个已完成的整点。已在缓存里的直接跳过。
func warmLogStatHour(ctx context.Context, hour int64) {
	cache := getLogStatCache()
	if _, found, err := cache.Get(logStatCacheKey("h", hour)); err == nil && found {
		return
	}
	if !acquireLogStatWarmLock(ctx, hour) {
		return
	}
	ttl := operation_setting.GetLogQuerySetting().GetStatCacheTTL()
	if _, err := logStatHourCached(ctx, hour, "h", ttl); err != nil {
		common.SysError(fmt.Sprintf("log stat warm failed for hour %d: %v", hour, err))
	}
}

// runLogStatWarmCycle 从最近一个已完成的整点起，向前预热 hours 个整点，
// 返回实际处理的整点数。
//
// 由新到旧是有意的：用户最常看「今天」，最近的整点先就位收益最大。
// 每个整点单独设查询超时，批间 sleep，全程响应 ctx 取消——长时后台作业
// 同样要控资源，不因为它不在 relay 主链上就放任满速跑。
func runLogStatWarmCycle(ctx context.Context, now int64, hours int) int {
	if hours <= 0 {
		return 0
	}
	s := operation_setting.GetLogQuerySetting()
	sleep := s.GetWarmSleep()
	timeout := s.GetQueryTimeout()

	// 最近一个「已完成」的整点：当前整点还在写入，不能进长 TTL 缓存。
	latest := floorHour(now) - logStatSecondsPerHour
	processed := 0
	for i := 0; i < hours; i++ {
		if ctx.Err() != nil {
			return processed
		}
		hour := latest - int64(i)*logStatSecondsPerHour
		func() {
			qctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			warmLogStatHour(qctx, hour)
		}()
		processed++
		if sleep > 0 && i < hours-1 {
			select {
			case <-ctx.Done():
				return processed
			case <-time.After(sleep):
			}
		}
	}
	return processed
}

// runLogStatWarmCycleSafely 把单轮预热包在 recover 里：
// 一轮出问题不该把整个循环带走，下一轮照常继续。
func runLogStatWarmCycleSafely(ctx context.Context, hours int) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("log stat warm cycle panicked: %v", r))
		}
	}()
	runLogStatWarmCycle(ctx, time.Now().Unix(), hours)
}

// logStatWarmEnabled 汇总预热的全部前置条件。
func logStatWarmEnabled() bool {
	s := operation_setting.GetLogQuerySetting()
	if !s.StatCacheEnabled || !s.WarmEnabled {
		return false
	}
	// ClickHouse 是列存，count/sum 本来就快，预热没有意义。
	return !common.UsingLogDatabase(common.DatabaseTypeClickHouse)
}

// StartLogStatWarmLoop 启动整点预热协程。
//
// master 独占：多节点同时跑只是重复计算（值相同，不会算错），但白白多耗
// 数据库；Redis 锁是多 master 部署下的第二道保险。
// 全程 recover + 静默：预热失败只影响命中率，绝不能影响任何在线请求（Rule 0）。
func StartLogStatWarmLoop() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("log stat warm loop panicked, warming disabled: %v", r))
			}
		}()

		ctx := context.Background()
		// 启动时先向前补齐一段历史，之后每轮只照顾新完成的那几个整点。
		startupHours := operation_setting.GetLogQuerySetting().GetWarmHours()
		if logStatWarmEnabled() && startupHours > 0 {
			runLogStatWarmCycleSafely(ctx, startupHours)
		}
		for {
			time.Sleep(operation_setting.GetLogQuerySetting().GetWarmInterval())
			if !logStatWarmEnabled() {
				continue
			}
			runLogStatWarmCycleSafely(ctx, logStatWarmSteadyHours)
		}
	}()
}
