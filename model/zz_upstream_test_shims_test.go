package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 上游测试用到、但定义在我们没有引入的上游测试文件里的助手。
//
// 目的：让从上游原样取回的测试（user_session_test.go 等）能直接编译运行，
// 而不必修改那些文件——它们要与 upstream 保持逐字节一致，便于后续同步。
// 这里只放「与我们 harness 不冲突」的助手；上游的 truncateTables 走全局清表，
// 与本仓 harness 的行级清理原则冲突（共享库里还有开发数据），因此不提供。
// 依赖它的上游测试要么不引入，要么在测试文件内把 truncateTables(t) 换成行级清理
// （quota_reserve / channel_status / task_plugin_channel_select / system_task）。
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

// ---------------------------------------------------------------------------
// upstream/main:model/pricing_endpoint_test.go 的助手（pricing_usage_schema_test.go 依赖）。
//
// 上游版本会 DELETE 整张 abilities/channels/models/vendors 表，并使用 901~930 这类
// 低位渠道 id——在共享开发库里既会清掉真实数据，又会与真实渠道主键冲突。
// 本仓改为行级：测试渠道 id 统一落在 pricingTestChannelIDBase 起的专用区间
// （低于 harness 的 testIDBase，互不重叠），reset 只清理该区间内的渠道与能力行。
// ---------------------------------------------------------------------------

const (
	pricingTestChannelIDBase = 799_000_000
	pricingTestChannelIDSpan = 1_000
)

// pricingTestChannelID maps an upstream fixture channel id (e.g. 901) into the
// fork's reserved pricing-test id block.
func pricingTestChannelID(n int) int {
	return pricingTestChannelIDBase + n
}

func purgePricingTestChannels() {
	lo, hi := pricingTestChannelIDBase, pricingTestChannelIDBase+pricingTestChannelIDSpan
	DB.Where("channel_id >= ? AND channel_id < ?", lo, hi).Delete(&Ability{})
	DB.Where("id >= ? AND id < ?", lo, hi).Delete(&Channel{})
}

func resetPricingEndpointTestTables(t *testing.T) {
	t.Helper()
	requireDB(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	purgePricingTestChannels()
	InitChannelCache()
	InvalidatePricingCache()
	t.Cleanup(func() {
		purgePricingTestChannels()
		InitChannelCache()
		InvalidatePricingCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

func insertPricingEndpointChannel(t *testing.T, channelID int, channelType int, settings dto.ChannelOtherSettings) {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   channelType,
		Key:    fmt.Sprintf("key-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", channelID),
	}
	if settings.AdvancedCustom != nil {
		channel.SetOtherSettings(settings)
	}
	require.NoError(t, DB.Create(channel).Error)
}

func insertPricingEndpointAbility(t *testing.T, channelID int, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
}
