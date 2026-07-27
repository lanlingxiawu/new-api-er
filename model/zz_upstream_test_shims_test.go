package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

// ---------------------------------------------------------------------------
// 上游测试用到、但定义在我们没有引入的上游测试文件里的助手。
//
// 目的：让从上游原样取回的测试（user_session_test.go 等）能直接编译运行，
// 而不必修改那些文件——它们要与 upstream 保持逐字节一致，便于后续同步。
// 这里只放「与我们 harness 不冲突」的助手；上游的 truncateTables 走全局清表，
// 与本仓 harness 的行级清理原则冲突（共享库里还有开发数据），因此不提供，
// 依赖它的上游测试也就不引入。
// ---------------------------------------------------------------------------

// useUserCacheMiniRedis 与 upstream/main:model/user_cache_auth_version_test.go 中的实现一致。
func useUserCacheMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	oldRedisEnabled := common.RedisEnabled
	oldRDB := common.RDB
	oldSyncFrequency := common.SyncFrequency
	common.RedisEnabled = true
	common.SyncFrequency = 2
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRDB
		common.SyncFrequency = oldSyncFrequency
	})
	return server
}
