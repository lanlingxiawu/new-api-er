package common

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
)

// redisReady gates the Redis integration tests. When Redis is not reachable the
// corresponding tests t.Skip instead of failing, so the pure unit tests still
// run on a bare CI box (Rule 15.5). The common package has no DB-backed code in
// scope (redis.go uses RDB only), so no MySQL/PG bootstrap is needed here.
var redisReady bool

// findRoot walks up from this test file to the directory containing .env so the
// integration tests use the real project Redis (Rule 15.5).
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

func TestMain(m *testing.M) {
	if root := findRoot(); root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	setupRedis()
	os.Exit(m.Run())
}

func setupRedis() {
	conn := os.Getenv("REDIS_CONN_STRING")
	if conn == "" {
		return
	}
	opt, err := redis.ParseURL(conn)
	if err != nil {
		return
	}
	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return
	}
	RDB = client
	RedisEnabled = true
	redisReady = true
}

// requireRedis skips a test unless the real Redis is reachable.
func requireRedis(t *testing.T) {
	t.Helper()
	if !redisReady {
		t.Skip("REDIS_CONN_STRING not set or Redis unreachable; skipping Redis integration test")
	}
}

// newBrokenRedisClient returns a client pointed at a closed port so every
// command deterministically fails, exercising the infra error branches without
// any external dependency (mirrors the pkg/cachex approach).
func newBrokenRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
}

// withBrokenRDB temporarily swaps the package RDB for a broken client and
// restores the original afterwards.
func withBrokenRDB(t *testing.T, fn func()) {
	t.Helper()
	orig := RDB
	broken := newBrokenRedisClient()
	RDB = broken
	t.Cleanup(func() {
		RDB = orig
		_ = broken.Close()
	})
	fn()
}
