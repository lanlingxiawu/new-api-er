package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 注册次数限制：同一客户端 IP 在任意 W 秒内最多注册成功 N 次，W、N 都在后台配置。
//
// 只有成功才算一次，所以做不成限流中间件（中间件跑在 handler 之前，看不到结果）。
// 做法是写库前原子预占一个名额，写库后确认成功或归还：
//   - "先查后写"在并发下会让同一 IP 的多个请求全部通过，占名额必须是原子操作；
//   - 每个名额带唯一 token，归还只删自己那一个：不会误删别人的名额，也不会把计数减成负数。
//
// 用滑动窗口（每个名额记自己的到期时间）而不是固定窗口：固定窗口在两个窗口交界处能连续
// 放行 2N 次，对"注册"这种低频、要求准确的限制不合适。N 有上限，单个 IP 的记录量可控。
//
// 设计文档：docs/design/register-rate-limit.md

const (
	registerCooldownKeyPrefix = "register:cooldown:ip:"
	// 预占保护期：只需覆盖一次注册写库的耗时；进程崩溃或确认失败时，名额最多多占这么久。
	registerCooldownReservationTTL = 120 * time.Second
)

// KEYS[1]: key；ARGV: token, now(ms), window(ms), limit, reservation(ms)。
// ZSET 的 score 是名额的到期时间。预占先使用保护期，写库成功后再从成功时刻开始正式窗口。
const registerCooldownClaimScript = `
local now = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if redis.call('ZCARD', KEYS[1]) < tonumber(ARGV[4]) then
  local reservation = tonumber(ARGV[5])
  redis.call('ZADD', KEYS[1], now + reservation, ARGV[1])
  redis.call('PEXPIRE', KEYS[1], math.max(reservation, redis.call('PTTL', KEYS[1])))
  return {1, 0}
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
return {0, tonumber(oldest[2]) - now}
`

// 成功时把自己的 token 改为“成功时间 + W”；失败时只删除自己的 token。
// 同时按剩余最晚的成员刷新 key TTL，避免已经缩短的名额留下无意义的长 TTL。
const registerCooldownFinishScript = `
local now = tonumber(ARGV[2])
if ARGV[3] == '1' then
  redis.call('ZADD', KEYS[1], 'XX', now + tonumber(ARGV[4]), ARGV[1])
else
  redis.call('ZREM', KEYS[1], ARGV[1])
end
local newest = redis.call('ZRANGE', KEYS[1], -1, -1, 'WITHSCORES')
if #newest == 0 then
  redis.call('DEL', KEYS[1])
else
  local ttl = tonumber(newest[2]) - now
  if ttl > 0 then
    redis.call('PEXPIRE', KEYS[1], ttl)
  else
    redis.call('DEL', KEYS[1])
  end
end
return 1
`

var (
	registerCooldownNow    = time.Now
	registerCooldownMemory = newRegisterCooldownStore()
)

func noopRegisterCooldownFinish(bool) {}

// ClaimRegisterCooldown 在写库前为 clientIP 占一个注册名额。
//
// ok 为 false 时 retryAfterSec 是最早空出名额还要等待的秒数（至少为 1）。
// ok 为 true 时，调用方必须在写库结束后调用 finish：成功传 true，失败传 false。
// finish 永远非 nil，重复调用只执行第一次。
//
// Redis 不可用时降级到进程内存，与网关限流同一取舍：不放行，也不让注册报错。
func ClaimRegisterCooldown(ctx context.Context, clientIP string) (finish func(success bool), retryAfterSec int64, ok bool) {
	snapshot := operation_setting.GetRateLimitSnapshot()
	window, limit := snapshot.RegisterCooldown, snapshot.RegisterCooldownNum
	if window <= 0 || limit <= 0 {
		return noopRegisterCooldownFinish, 0, true
	}
	key := registerCooldownKeyPrefix + clientIP
	token := common.GetUUID()
	now := registerCooldownNow()

	if common.RedisEnabled {
		claimed, retryAfter, err := claimRegisterCooldownRedis(ctx, key, token, now, window, limit)
		if err == nil {
			if !claimed {
				return noopRegisterCooldownFinish, retryAfter, false
			}
			var once sync.Once
			return func(success bool) {
				once.Do(func() { finishRegisterCooldownRedis(ctx, key, token, success, window) })
			}, 0, true
		}
		logger.LogError(ctx, fmt.Sprintf("register cooldown degraded to in-memory: %v", err))
	}
	return registerCooldownMemory.claim(key, token, now, window, limit)
}

// ceilRetryAfterSeconds 向上取整到秒：剩余不足一秒时提示"0 秒后重试"没有意义。
func ceilRetryAfterSeconds(remaining time.Duration) int64 {
	seconds := int64((remaining + time.Second - 1) / time.Second)
	return max(seconds, 1)
}

// registerCooldownRedisContext 复用网关限流的 Redis 超时预算，超时即降级。
func registerCooldownRedisContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if budget := operation_setting.GetRateLimitSnapshot().RedisTimeout; budget > 0 {
		return context.WithTimeout(ctx, budget)
	}
	return context.WithCancel(ctx)
}

func claimRegisterCooldownRedis(ctx context.Context, key string, token string, now time.Time, window time.Duration, limit int) (bool, int64, error) {
	if common.RDB == nil {
		return false, 0, errors.New("Redis client is not initialized")
	}
	ctx, cancel := registerCooldownRedisContext(ctx)
	defer cancel()

	reservation := max(window, registerCooldownReservationTTL)
	values, err := common.RDB.Eval(ctx, registerCooldownClaimScript, []string{key},
		token, now.UnixMilli(), window.Milliseconds(), limit, reservation.Milliseconds()).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(values) != 2 {
		return false, 0, fmt.Errorf("unexpected register cooldown reply length %d", len(values))
	}
	claimed, claimedOK := values[0].(int64)
	remainingMs, remainingOK := values[1].(int64)
	if !claimedOK || !remainingOK {
		return false, 0, fmt.Errorf("unexpected register cooldown reply %v", values)
	}
	if claimed == 1 {
		return true, 0, nil
	}
	return false, ceilRetryAfterSeconds(time.Duration(remainingMs) * time.Millisecond), nil
}

func finishRegisterCooldownRedis(ctx context.Context, key string, token string, success bool, window time.Duration) {
	// 客户端可能已经断开；确认或归还不能跟着请求一起被取消。
	ctx, cancel := registerCooldownRedisContext(context.WithoutCancel(ctx))
	defer cancel()
	if common.RDB == nil {
		logger.LogError(ctx, "register cooldown finish failed: Redis client is not initialized")
		return
	}
	successValue := 0
	if success {
		successValue = 1
	}
	if err := common.RDB.Eval(ctx, registerCooldownFinishScript, []string{key},
		token, registerCooldownNow().UnixMilli(), successValue, window.Milliseconds()).Err(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("register cooldown finish failed: %v", err))
	}
}

type registerCooldownEntry struct {
	token     string
	expiresAt time.Time
}

// registerCooldownStore 是没开 Redis 或 Redis 故障时的兜底，只在本进程内生效：
// M 个实例时同一 IP 每个窗口最多能注册 M×N 次，与网关限流降级时的取舍一致。
type registerCooldownStore struct {
	mu      sync.Mutex
	entries map[string][]registerCooldownEntry
}

func newRegisterCooldownStore() *registerCooldownStore {
	return &registerCooldownStore{entries: make(map[string][]registerCooldownEntry)}
}

func (s *registerCooldownStore) claim(key string, token string, now time.Time, window time.Duration, limit int) (func(bool), int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.pruneKeyLocked(key, now)
	if len(held) >= limit {
		earliest := held[0].expiresAt
		for _, entry := range held[1:] {
			if entry.expiresAt.Before(earliest) {
				earliest = entry.expiresAt
			}
		}
		return noopRegisterCooldownFinish, ceilRetryAfterSeconds(earliest.Sub(now)), false
	}
	reservation := max(window, registerCooldownReservationTTL)
	s.entries[key] = append(held, registerCooldownEntry{token: token, expiresAt: now.Add(reservation)})
	var once sync.Once
	return func(success bool) {
		once.Do(func() { s.finish(key, token, success, registerCooldownNow(), window) })
	}, 0, true
}

// pruneKeyLocked 只清理当前 IP，避免每个注册请求扫描整个降级存储。
func (s *registerCooldownStore) pruneKeyLocked(key string, now time.Time) []registerCooldownEntry {
	held := s.entries[key]
	kept := held[:0]
	for _, entry := range held {
		if now.Before(entry.expiresAt) {
			kept = append(kept, entry)
		}
	}
	if len(kept) == 0 {
		delete(s.entries, key)
	} else {
		s.entries[key] = kept
	}
	return kept
}

func (s *registerCooldownStore) finish(key string, token string, success bool, now time.Time, window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.entries[key]
	for i, entry := range held {
		if entry.token == token {
			if success {
				held[i].expiresAt = now.Add(window)
			} else {
				held = append(held[:i], held[i+1:]...)
			}
			break
		}
	}
	if len(held) == 0 {
		delete(s.entries, key)
	} else {
		s.entries[key] = held
	}
}

// size 返回内存兜底里仍在计数的名额总数。
func (s *registerCooldownStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, held := range s.entries {
		total += len(held)
	}
	return total
}
