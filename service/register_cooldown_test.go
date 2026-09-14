package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRegisterCooldownSetting(t *testing.T, enabled bool, num int, seconds int) {
	t.Helper()
	previous := operation_setting.GetRateLimitSetting()
	draft := previous
	draft.RegisterCooldownEnabled = enabled
	draft.RegisterCooldownNum = num
	draft.RegisterCooldownSec = seconds
	// 满载跑 go test ./... 时 miniredis 偶尔会超过 100ms 的生产预算，一旦踩到降级，
	// 计数就换成了内存那一套，断言不再确定。降级路径由专门的用例覆盖。
	draft.RedisTimeoutMs = 5_000
	operation_setting.ReplaceRateLimitSetting(draft)
	t.Cleanup(func() { operation_setting.ReplaceRateLimitSetting(previous) })
}

// useRegisterCooldownClock 换上可拨动的时钟，返回的指针用来推进时间。
// Redis 分支也用这个时钟：当前时间由服务端传给脚本，所以两种存储都能精确控制时间。
func useRegisterCooldownClock(t *testing.T) *time.Time {
	t.Helper()
	previousNow := registerCooldownNow
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	registerCooldownNow = func() time.Time { return now }
	t.Cleanup(func() { registerCooldownNow = previousNow })
	return &now
}

// useRegisterCooldownRedis 换上一个全新的 miniredis，并清空内存兜底，
// 避免上一个用例的降级残留影响本用例。
func useRegisterCooldownRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	previousEnabled := common.RedisEnabled
	previousClient := common.RDB
	previousStore := registerCooldownMemory

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled = true
	common.RDB = client
	registerCooldownMemory = newRegisterCooldownStore()

	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = previousEnabled
		common.RDB = previousClient
		registerCooldownMemory = previousStore
	})
	return server
}

func useRegisterCooldownMemory(t *testing.T) {
	t.Helper()
	previousEnabled := common.RedisEnabled
	previousStore := registerCooldownMemory
	common.RedisEnabled = false
	registerCooldownMemory = newRegisterCooldownStore()
	t.Cleanup(func() {
		common.RedisEnabled = previousEnabled
		registerCooldownMemory = previousStore
	})
}

// 两种存储必须表现一致：Redis 是常态，内存是没开 Redis 或 Redis 故障时的兜底。
var registerCooldownBackends = []struct {
	name  string
	setup func(t *testing.T) (advance func(time.Duration))
}{
	{"redis", func(t *testing.T) func(time.Duration) {
		server := useRegisterCooldownRedis(t)
		clock := useRegisterCooldownClock(t)
		return func(d time.Duration) {
			*clock = clock.Add(d)
			server.FastForward(d)
		}
	}},
	{"memory", func(t *testing.T) func(time.Duration) {
		useRegisterCooldownMemory(t)
		clock := useRegisterCooldownClock(t)
		return func(d time.Duration) { *clock = clock.Add(d) }
	}},
}

func reserveRegister(t *testing.T, ip string) (func(bool), int64, bool) {
	t.Helper()
	return ClaimRegisterCooldown(context.Background(), ip)
}

func claimRegister(t *testing.T, ip string) (int64, bool) {
	t.Helper()
	finish, retryAfter, ok := reserveRegister(t, ip)
	if ok {
		finish(true)
	}
	return retryAfter, ok
}

func TestRegisterCooldownAllowsConfiguredCountPerIP(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			backend.setup(t)
			useRegisterCooldownSetting(t, true, 2, 120)

			for i := 1; i <= 2; i++ {
				_, ok := claimRegister(t, "192.0.2.1")
				require.True(t, ok, "registration %d is within the allowance", i)
			}
			retryAfter, ok := claimRegister(t, "192.0.2.1")
			assert.False(t, ok, "the third registration inside the window must be rejected")
			assert.Equal(t, int64(120), retryAfter, "both slots were taken just now, so the first frees up after a full window")

			_, ok = claimRegister(t, "192.0.2.2")
			assert.True(t, ok, "another IP must not share the allowance")
		})
	}
}

func TestRegisterCooldownSingleRegistrationPerWindow(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 120)

			_, ok := claimRegister(t, "192.0.2.3")
			require.True(t, ok)
			_, ok = claimRegister(t, "192.0.2.3")
			assert.False(t, ok)
		})
	}
}

// 滑动窗口：名额按各自的到期时间逐个空出，而不是整窗一起清零。
func TestRegisterCooldownIsASlidingWindow(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 2, 120)

			_, ok := claimRegister(t, "192.0.2.4") // 到期于 120s
			require.True(t, ok)
			advance(60 * time.Second)
			_, ok = claimRegister(t, "192.0.2.4") // 到期于 180s
			require.True(t, ok)

			advance(61 * time.Second) // 121s：第一个名额已到期
			_, ok = claimRegister(t, "192.0.2.4")
			require.True(t, ok, "the first slot expired, so one registration is allowed again")

			retryAfter, ok := claimRegister(t, "192.0.2.4")
			assert.False(t, ok)
			assert.Equal(t, int64(59), retryAfter, "the next slot frees up when the 60s registration expires at 180s")
		})
	}
}

func TestRegisterCooldownRedisKeyAndTTL(t *testing.T) {
	server := useRegisterCooldownRedis(t)
	useRegisterCooldownClock(t)
	useRegisterCooldownSetting(t, true, 2, 300)

	_, ok := claimRegister(t, "192.0.2.5")
	require.True(t, ok)

	key := "register:cooldown:ip:192.0.2.5"
	members, err := server.ZMembers(key)
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, 300*time.Second, server.TTL(key))
}

// 失败的注册不能占用名额——这是"只有成功才算一次"的回归测试。
func TestRegisterCooldownFailedFinishFreesExactlyOneSlot(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			backend.setup(t)
			useRegisterCooldownSetting(t, true, 2, 120)

			finish, _, ok := reserveRegister(t, "192.0.2.6")
			require.True(t, ok)
			_, ok = claimRegister(t, "192.0.2.6")
			require.True(t, ok)

			finish(false)
			_, ok = claimRegister(t, "192.0.2.6")
			assert.True(t, ok, "a released slot must be reusable")
			_, ok = claimRegister(t, "192.0.2.6")
			assert.False(t, ok, "releasing one slot must not free more than one")
		})
	}
}

func TestRegisterCooldownFinishIsIdempotent(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 60)

			staleFinish, _, ok := reserveRegister(t, "192.0.2.7")
			require.True(t, ok)
			staleFinish(true)

			advance(61 * time.Second)
			_, ok = claimRegister(t, "192.0.2.7")
			require.True(t, ok, "the window has passed")

			staleFinish(false)
			_, ok = claimRegister(t, "192.0.2.7")
			assert.False(t, ok, "a repeated finish must not free a newer registration's slot")
		})
	}
}

func TestRegisterCooldownEndsExactlyAtTheWindow(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 1)

			_, ok := claimRegister(t, "192.0.2.8")
			require.True(t, ok)

			advance(999 * time.Millisecond)
			retryAfter, ok := claimRegister(t, "192.0.2.8")
			assert.False(t, ok, "still inside the window")
			assert.Equal(t, int64(1), retryAfter, "less than a second left is reported as 1, never 0")

			advance(time.Millisecond)
			_, ok = claimRegister(t, "192.0.2.8")
			assert.True(t, ok, "the window has fully elapsed")
		})
	}
}

func TestRegisterCooldownDisabledNeverBlocks(t *testing.T) {
	server := useRegisterCooldownRedis(t)
	useRegisterCooldownSetting(t, false, 1, 120)

	for i := 0; i < 3; i++ {
		finish, _, ok := reserveRegister(t, "192.0.2.9")
		require.True(t, ok)
		require.NotNil(t, finish, "callers always get a callable finish")
		finish(true)
	}
	assert.Empty(t, server.Keys(), "a disabled limit must not touch Redis")
	assert.Zero(t, registerCooldownMemory.size())
}

func TestRegisterCooldownDegradesToMemoryWhenRedisFails(t *testing.T) {
	server := useRegisterCooldownRedis(t)
	useRegisterCooldownSetting(t, true, 1, 120)
	server.Close()

	finish, _, ok := reserveRegister(t, "192.0.2.10")
	require.True(t, ok, "a Redis outage must not block registration")

	retryAfter, ok := claimRegister(t, "192.0.2.10")
	assert.False(t, ok, "the in-memory fallback must still enforce the limit")
	assert.Greater(t, retryAfter, int64(0))

	finish(false)
	_, ok = claimRegister(t, "192.0.2.10")
	assert.True(t, ok, "failed finish must reach the store that took the claim")
}

// 同一 IP 并发提交，放行的数量必须恰好等于配置的次数——"先查后写"会让它们全部通过。
func TestRegisterCooldownConcurrentClaimsAdmitExactlyTheLimit(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			backend.setup(t)
			useRegisterCooldownSetting(t, true, 3, 120)

			const racers = 20
			var wins atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < racers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if finish, _, ok := ClaimRegisterCooldown(context.Background(), "192.0.2.11"); ok {
						finish(true)
						wins.Add(1)
					}
				}()
			}
			close(start)
			wg.Wait()
			assert.Equal(t, int32(3), wins.Load())
		})
	}
}

func TestRegisterCooldownWindowStartsWhenRegistrationSucceeds(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 60)

			finish, _, ok := reserveRegister(t, "192.0.2.12")
			require.True(t, ok)
			advance(30 * time.Second)
			finish(true)

			advance(31 * time.Second)
			retryAfter, ok := claimRegister(t, "192.0.2.12")
			assert.False(t, ok)
			assert.Equal(t, int64(29), retryAfter)
		})
	}
}

func TestRegisterCooldownPendingClaimDoesNotExpireAtConfiguredWindow(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 1)

			finish, _, ok := reserveRegister(t, "192.0.2.13")
			require.True(t, ok)
			advance(2 * time.Second)
			_, ok = claimRegister(t, "192.0.2.13")
			assert.False(t, ok, "an in-flight registration must keep its reservation")
			finish(false)
		})
	}
}

// 保护期只兜底执行中的注册：超过 120 秒仍未确认（进程崩溃、确认失败），名额自动空出。
func TestRegisterCooldownPendingClaimExpiresAfterReservation(t *testing.T) {
	for _, backend := range registerCooldownBackends {
		t.Run(backend.name, func(t *testing.T) {
			advance := backend.setup(t)
			useRegisterCooldownSetting(t, true, 1, 1)

			_, _, ok := reserveRegister(t, "192.0.2.16")
			require.True(t, ok)

			advance(120*time.Second - time.Millisecond)
			retryAfter, ok := claimRegister(t, "192.0.2.16")
			assert.False(t, ok, "still inside the reservation")
			assert.Equal(t, int64(1), retryAfter)

			advance(time.Millisecond)
			_, ok = claimRegister(t, "192.0.2.16")
			assert.True(t, ok, "an unconfirmed reservation frees up after 120 seconds")
		})
	}
}

func TestRegisterCooldownMemoryPrunesOnlyCurrentIP(t *testing.T) {
	useRegisterCooldownMemory(t)
	clock := useRegisterCooldownClock(t)
	useRegisterCooldownSetting(t, true, 1, 60)

	_, ok := claimRegister(t, "192.0.2.14")
	require.True(t, ok)
	*clock = clock.Add(61 * time.Second)
	_, ok = claimRegister(t, "192.0.2.15")
	require.True(t, ok)
	assert.Equal(t, 2, registerCooldownMemory.size(), "claiming another IP must not scan the whole store")

	_, ok = claimRegister(t, "192.0.2.14")
	require.True(t, ok)
	assert.Equal(t, 2, registerCooldownMemory.size(), "the current IP's expired entry must be replaced")
}
