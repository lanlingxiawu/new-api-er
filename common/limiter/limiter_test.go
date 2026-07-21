package limiter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test harness
//
// The RedisLimiter is a Redis token-bucket. Its Allow/New paths require a live
// Redis (Rule 15.5: prefer real infra). Tests dial REDIS_CONN_STRING from the
// project .env and SKIP cleanly when Redis is unreachable, so a CI box without
// Redis still passes. Determinism (<1s, no wall-clock waits, Rule 15.4) is
// achieved by pre-seeding the bucket hash with a back-dated last_time to force
// a known refill amount instead of sleeping.
// ---------------------------------------------------------------------------

// findRoot walks up from this file to the directory containing .env.
func findRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// dialRedis returns a client to the configured Redis, or skips the test.
func dialRedis(t *testing.T) *redis.Client {
	t.Helper()
	if root := findRoot(); root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	conn := os.Getenv("REDIS_CONN_STRING")
	if conn == "" {
		t.Skip("REDIS_CONN_STRING not set; skipping Redis-backed limiter tests")
	}
	opt, err := redis.ParseURL(conn)
	require.NoError(t, err, "REDIS_CONN_STRING must be a valid redis URL")
	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis unreachable (%v); skipping Redis-backed limiter tests", err)
	}
	return client
}

// newTestLimiter returns the singleton limiter bound to a live Redis client.
// New() uses sync.Once, so the first caller in the binary binds the client and
// loads the Lua script; that is exactly the client we seed with below (the test
// is white-box, package limiter, so rl.client is the same instance).
func newTestLimiter(t *testing.T) (*RedisLimiter, *redis.Client) {
	t.Helper()
	client := dialRedis(t)
	rl := New(context.Background(), client)
	require.NotNil(t, rl)
	require.NotEmpty(t, rl.limitScriptSHA, "Lua rate-limit script must load on New()")
	return rl, rl.client
}

// key returns a unique, self-cleaning bucket key for the current test.
func key(t *testing.T, client *redis.Client) string {
	t.Helper()
	k := "test:limiter:" + t.Name()
	require.NoError(t, client.Del(context.Background(), k).Err())
	t.Cleanup(func() { _ = client.Del(context.Background(), k).Err() })
	return k
}

// serverNow returns the Redis server clock in whole seconds (matches the Lua
// script's use of TIME[1]).
func serverNow(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	tm, err := client.Time(context.Background()).Result()
	require.NoError(t, err)
	return tm.Unix()
}

// seedBucket writes a bucket hash with a known token count and back-dated
// last_time, so the next Allow computes a deterministic refill without sleeping.
func seedBucket(t *testing.T, client *redis.Client, k string, tokens int64, ageSeconds int64) {
	t.Helper()
	last := serverNow(t, client) - ageSeconds
	require.NoError(t, client.HSet(context.Background(), k,
		"tokens", tokens, "last_time", last).Err())
}

// ---------------------------------------------------------------------------
// Option builders — pure logic, no Redis (equivalence: each builder sets exactly
// its own field and nothing else).
// ---------------------------------------------------------------------------

func TestWithCapacity_SetsOnlyCapacity(t *testing.T) {
	cfg := &Config{Capacity: 1, Rate: 2, Requested: 3}
	WithCapacity(99)(cfg)
	assert.Equal(t, int64(99), cfg.Capacity)
	assert.Equal(t, int64(2), cfg.Rate, "Rate must be untouched")
	assert.Equal(t, int64(3), cfg.Requested, "Requested must be untouched")
}

func TestWithRate_SetsOnlyRate(t *testing.T) {
	cfg := &Config{Capacity: 1, Rate: 2, Requested: 3}
	WithRate(88)(cfg)
	assert.Equal(t, int64(88), cfg.Rate)
	assert.Equal(t, int64(1), cfg.Capacity)
	assert.Equal(t, int64(3), cfg.Requested)
}

func TestWithRequested_SetsOnlyRequested(t *testing.T) {
	cfg := &Config{Capacity: 1, Rate: 2, Requested: 3}
	WithRequested(77)(cfg)
	assert.Equal(t, int64(77), cfg.Requested)
	assert.Equal(t, int64(1), cfg.Capacity)
	assert.Equal(t, int64(2), cfg.Rate)
}

func TestOptions_ComposeInOrder(t *testing.T) {
	cfg := &Config{}
	for _, opt := range []Option{WithCapacity(5), WithRate(2), WithRequested(1)} {
		opt(cfg)
	}
	assert.Equal(t, Config{Capacity: 5, Rate: 2, Requested: 1}, *cfg)
}

// ---------------------------------------------------------------------------
// New — singleton semantics.
// ---------------------------------------------------------------------------

func TestNew_ReturnsSingleton(t *testing.T) {
	client := dialRedis(t)
	first := New(context.Background(), client)
	second := New(context.Background(), redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	assert.Same(t, first, second, "sync.Once: subsequent New must return the first instance")
}

// ---------------------------------------------------------------------------
// Allow — init branch (fresh key => tokens = capacity).
// ---------------------------------------------------------------------------

func TestAllow_FreshKeyWithinCapacityAllows(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	ok, err := rl.Allow(context.Background(), k, WithCapacity(5), WithRate(0), WithRequested(1))
	require.NoError(t, err)
	assert.True(t, ok, "fresh bucket starts full (5) so a 1-token request is allowed")
}

func TestAllow_FreshKeyRequestExceedingCapacityDenied(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	// init tokens = capacity = 1; requested = 5 => 1 >= 5 is false => denied.
	ok, err := rl.Allow(context.Background(), k, WithCapacity(1), WithRate(0), WithRequested(5))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestAllow_RequestedEqualsCapacityBoundary(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	// requested == capacity: allowed exactly once, then bucket is empty.
	ok, err := rl.Allow(context.Background(), k, WithCapacity(3), WithRate(0), WithRequested(3))
	require.NoError(t, err)
	assert.True(t, ok, "requested == capacity is allowed on a full bucket")

	ok, err = rl.Allow(context.Background(), k, WithCapacity(3), WithRate(0), WithRequested(3))
	require.NoError(t, err)
	assert.False(t, ok, "second identical request drains below requested => denied")
}

// ---------------------------------------------------------------------------
// Allow — exhaustion (deplete branch + tokens < requested deny branch).
// ---------------------------------------------------------------------------

func TestAllow_ExhaustsBucketWithoutRefill(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	ctx := context.Background()
	// capacity 2, rate 0 => no refill. Expect: allow, allow, deny.
	results := make([]bool, 0, 3)
	for i := 0; i < 3; i++ {
		ok, err := rl.Allow(ctx, k, WithCapacity(2), WithRate(0), WithRequested(1))
		require.NoError(t, err)
		results = append(results, ok)
	}
	assert.Equal(t, []bool{true, true, false}, results)
}

// ---------------------------------------------------------------------------
// Allow — refill branch (elapsed > 0 => add_tokens = elapsed*rate), driven by a
// back-dated last_time instead of sleeping.
// ---------------------------------------------------------------------------

func TestAllow_RefillsFromElapsedTime(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	// Empty bucket last touched 10s ago, rate 1/s => +10 tokens available.
	seedBucket(t, client, k, 0, 10)
	ok, err := rl.Allow(context.Background(), k, WithCapacity(100), WithRate(1), WithRequested(5))
	require.NoError(t, err)
	assert.True(t, ok, "10s * 1/s = 10 refilled tokens covers a 5-token request")
}

func TestAllow_RefillCapsAtCapacity(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	ctx := context.Background()
	// Huge elapsed would add far more than capacity; min() clamps to capacity=8.
	seedBucket(t, client, k, 0, 100000)
	ok, err := rl.Allow(ctx, k, WithCapacity(8), WithRate(1), WithRequested(8))
	require.NoError(t, err)
	assert.True(t, ok, "refill is capped at capacity (8), which exactly covers an 8-token request")

	// Immediately after (same second => elapsed 0 => no refill), the bucket is
	// empty, so any further request is denied.
	ok, err = rl.Allow(ctx, k, WithCapacity(8), WithRate(1), WithRequested(1))
	require.NoError(t, err)
	assert.False(t, ok, "bucket drained to 0 and cannot refill within the same second")
}

// TestAllow_DefaultConfig verifies the built-in defaults (Capacity 10, Rate 1,
// Requested 1) when no Option is supplied. Seeding an empty, 3s-old bucket and
// passing no options must refill exactly 3 tokens (proving Rate defaults to 1),
// which covers the single default request.
func TestAllow_DefaultConfig(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	seedBucket(t, client, k, 0, 3)
	ok, err := rl.Allow(context.Background(), k)
	require.NoError(t, err)
	assert.True(t, ok, "default Rate=1 refills 3 tokens over 3s, covering default Requested=1")
}

// ---------------------------------------------------------------------------
// Allow — error path (EvalSha against a missing script returns an error).
// ---------------------------------------------------------------------------

func TestAllow_EvalErrorIsPropagated(t *testing.T) {
	rl, client := newTestLimiter(t)
	k := key(t, client)
	// White-box: point the limiter at a SHA Redis does not know, forcing a
	// NOSCRIPT EvalSha error. Restore afterward so other tests keep working.
	original := rl.limitScriptSHA
	rl.limitScriptSHA = "0000000000000000000000000000000000000000"
	t.Cleanup(func() { rl.limitScriptSHA = original })

	ok, err := rl.Allow(context.Background(), k, WithCapacity(1), WithRate(0), WithRequested(1))
	require.Error(t, err, "unknown script SHA must surface as an error")
	assert.Contains(t, err.Error(), "rate limit failed")
	assert.False(t, ok, "on error, Allow must fail closed (false)")
}
