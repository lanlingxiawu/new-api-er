package settingsaccess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 每日金额上限的设置作用域接入测试。
// 设计见 docs/design/channel-daily-quota-limit.md §11.7。
//
// 这组用例锁的是一个容易漏掉的接入点：新配置若不登记进作用域白名单，管理页面既读不出
// 也存不进；而且 ConfigManager 支撑的配置必须用 configScope（带 GroupKeys），
// 普通 scope() 构造出来的 GroupKeys 是空 map，AllowsGroup 会一律拒绝。

const dailyLimitScope = "models.routing-reliability"

func TestChannelDailyLimit_OptionKeysAllowed(t *testing.T) {
	allowed, ok := OptionKeys(dailyLimitScope)
	require.True(t, ok, "scope must exist")

	for _, key := range []string{
		"channel_daily_limit_setting.enabled",
		"channel_daily_limit_setting.timezone",
		"channel_daily_limit_setting.retention_days",
	} {
		_, present := allowed[key]
		assert.True(t, present, "option key %q must be allowed under %s", key, dailyLimitScope)
	}
}

func TestChannelDailyLimit_ExistingKeysStillAllowed(t *testing.T) {
	// 把 scope 换成 configScope 不能弄丢该作用域原有的键。
	allowed, ok := OptionKeys(dailyLimitScope)
	require.True(t, ok)
	for _, key := range []string{
		"RetryTimes",
		"ChannelDisableThreshold",
		"AutomaticDisableChannelEnabled",
		"AutomaticEnableChannelEnabled",
		"AutomaticDisableKeywords",
		"AutomaticDisableStatusCodes",
		"AutomaticRetryStatusCodes",
		"monitor_setting.auto_test_channel_enabled",
		"monitor_setting.auto_test_channel_minutes",
		"monitor_setting.channel_test_mode",
	} {
		_, present := allowed[key]
		assert.True(t, present, "pre-existing option key %q must stay allowed", key)
	}
}

func TestChannelDailyLimit_GroupSaveAllowed(t *testing.T) {
	// AllowsGroup 要求 GroupKeys[module] 存在——这正是必须用 configScope 的原因。
	assert.True(t, AllowsGroup(dailyLimitScope, "channel_daily_limit_setting", map[string]string{
		"enabled": "true",
	}))
	assert.True(t, AllowsGroup(dailyLimitScope, "channel_daily_limit_setting", map[string]string{
		"timezone":       "Asia/Shanghai",
		"retention_days": "90",
	}))

	// 未登记的字段被拒绝，防止通过配置组接口写入任意键。
	assert.False(t, AllowsGroup(dailyLimitScope, "channel_daily_limit_setting", map[string]string{
		"enabled":     "true",
		"unknown_key": "1",
	}))
	// 空值集合被拒绝。
	assert.False(t, AllowsGroup(dailyLimitScope, "channel_daily_limit_setting", map[string]string{}))
	// 其它模块不因此获得写入权限。
	assert.False(t, AllowsGroup(dailyLimitScope, "relay_timeout_setting", map[string]string{"enabled": "true"}))
}
