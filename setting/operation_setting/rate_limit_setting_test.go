package operation_setting

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestRateLimitSnapshotIsImmutableAfterPublish(t *testing.T) {
	original := rateLimitSetting
	t.Cleanup(func() { rateLimitSetting = original; PublishRateLimitSetting() })

	rateLimitSetting.GlobalAPINum = 10
	PublishRateLimitSetting()
	old := GetRateLimitSnapshot()
	require.Equal(t, 10, old.GlobalAPI.Num)

	rateLimitSetting.GlobalAPINum = 20
	PublishRateLimitSetting()
	require.Equal(t, 10, old.GlobalAPI.Num)
	require.Equal(t, 20, GetRateLimitSnapshot().GlobalAPI.Num)
}

func TestRateLimitSnapshotClampsInvalidDraft(t *testing.T) {
	original := rateLimitSetting
	t.Cleanup(func() { rateLimitSetting = original; PublishRateLimitSetting() })

	rateLimitSetting.GlobalAPINum = 0
	rateLimitSetting.GlobalAPIDurationSec = 999999
	PublishRateLimitSetting()
	snapshot := GetRateLimitSnapshot()
	require.Equal(t, defaultGlobalAPINum, snapshot.GlobalAPI.Num)
	require.Equal(t, int64(maxRateLimitWindowSec), snapshot.GlobalAPI.Duration)
}

// ---------------------------------------------------------------------------
// Redis 超时预算
// ---------------------------------------------------------------------------

func TestRedisTimeoutSnapshotClamping(t *testing.T) {
	original := GetRateLimitSetting()
	t.Cleanup(func() { ReplaceRateLimitSetting(original) })

	cases := []struct {
		name     string
		input    int
		expectMs int
	}{
		{"未配置回落默认值", 0, defaultRateLimitRedisTimeoutMs},
		{"负数回落默认值", -20, defaultRateLimitRedisTimeoutMs},
		{"低于下界抬到下界而不是回落默认值", 1, minRateLimitRedisTimeoutMs},
		{"下界", minRateLimitRedisTimeoutMs, minRateLimitRedisTimeoutMs},
		{"常规取值原样保留", 250, 250},
		{"上界", maxRateLimitRedisTimeoutMs, maxRateLimitRedisTimeoutMs},
		{"超过上界钳到上界", 60_000, maxRateLimitRedisTimeoutMs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := original
			draft.RedisTimeoutMs = tc.input
			ReplaceRateLimitSetting(draft)
			require.Equal(t, time.Duration(tc.expectMs)*time.Millisecond,
				GetRateLimitSnapshot().RedisTimeout)
		})
	}
}

// 快照永远给出可用值，但管理接口保存时必须明确拒绝越界输入，
// 否则运维会以为存进去的是自己填的数。
func TestValidateRateLimitSettingRejectsOutOfRangeRedisTimeout(t *testing.T) {
	base := GetRateLimitSetting()
	base.RedisTimeoutMs = defaultRateLimitRedisTimeoutMs
	require.NoError(t, ValidateRateLimitSetting(base))

	for _, invalid := range []int{0, -1, minRateLimitRedisTimeoutMs - 1, maxRateLimitRedisTimeoutMs + 1} {
		draft := base
		draft.RedisTimeoutMs = invalid
		require.Error(t, ValidateRateLimitSetting(draft), "%d 应当被拒绝", invalid)
	}
	for _, valid := range []int{minRateLimitRedisTimeoutMs, 100, maxRateLimitRedisTimeoutMs} {
		draft := base
		draft.RedisTimeoutMs = valid
		require.NoError(t, ValidateRateLimitSetting(draft), "%d 应当被接受", valid)
	}
}

func TestApplyRateLimitEnvDefaultsReadsRedisTimeout(t *testing.T) {
	original := GetRateLimitSetting()
	t.Cleanup(func() { ReplaceRateLimitSetting(original) })

	t.Setenv("RATE_LIMIT_REDIS_TIMEOUT_MS", "250")
	ApplyRateLimitEnvDefaults()

	require.Equal(t, 250, GetRateLimitSetting().RedisTimeoutMs)
	require.Equal(t, 250*time.Millisecond, GetRateLimitSnapshot().RedisTimeout)
}

// 默认值必须是正数：为 0 会让 middleware 跳过超时包装，
// 等于悄悄退回到 go-redis 的 12 秒行为。
func TestRedisTimeoutDefaultIsPositive(t *testing.T) {
	require.Positive(t, defaultRateLimitRedisTimeoutMs)
	require.Positive(t, GetRateLimitSnapshot().RedisTimeout)
}

// TestRateLimitDraftAccessIsSerialized is the regression for a torn read of the
// draft struct: Replace used to assign every field with no lock while the
// config export path read them by reflection. Every reader here must observe one
// writer's struct in full, never a mix of two.
func TestRateLimitDraftAccessIsSerialized(t *testing.T) {
	original := GetRateLimitSetting()
	t.Cleanup(func() { ReplaceRateLimitSetting(original) })

	const writers, readers, rounds = 4, 4, 500
	// 读者可能先于任何写者运行；先写入一份字段一致的初值，避免把默认配置
	// （各字段本就不同）误判为撕裂读。
	baseline := original
	baseline.GlobalAPINum = writers + 1
	baseline.GlobalAPIDurationSec = writers + 1
	baseline.CriticalNum = writers + 1
	baseline.SearchNum = writers + 1
	baseline.LogExportNum = writers + 1
	ReplaceRateLimitSetting(baseline)
	var writersWG, observersWG sync.WaitGroup
	stop := make(chan struct{})

	for writer := 1; writer <= writers; writer++ {
		writersWG.Add(1)
		go func(seed int) {
			defer writersWG.Done()
			for round := 0; round < rounds; round++ {
				draft := original
				// Every field carries the same writer identity, so any torn read
				// shows up as two different identities inside one struct.
				draft.GlobalAPINum = seed
				draft.GlobalAPIDurationSec = seed
				draft.CriticalNum = seed
				draft.SearchNum = seed
				draft.LogExportNum = seed
				ReplaceRateLimitSetting(draft)
			}
		}(writer)
	}

	for reader := 0; reader < readers; reader++ {
		observersWG.Add(1)
		go func() {
			defer observersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				observed := GetRateLimitSetting()
				identity := observed.GlobalAPINum
				if identity != observed.GlobalAPIDurationSec ||
					identity != observed.CriticalNum ||
					identity != observed.SearchNum ||
					identity != observed.LogExportNum {
					t.Errorf("torn read of rate limit draft: %+v", observed)
					return
				}
			}
		}()
	}

	// Exporting through the ConfigManager reads the same fields by reflection,
	// which is the other half of the race.
	observersWG.Add(1)
	go func() {
		defer observersWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			config.GlobalConfig.ExportAllConfigs()
		}
	}()

	writersWG.Wait()
	close(stop)
	observersWG.Wait()
}

// ---------------------------------------------------------------------------
// 注册间隔
// ---------------------------------------------------------------------------

func TestRegisterCooldownSnapshot(t *testing.T) {
	original := GetRateLimitSetting()
	t.Cleanup(func() { ReplaceRateLimitSetting(original) })

	cases := []struct {
		name         string
		enabled      bool
		num, seconds int
		expectWindow time.Duration
		expectNum    int
	}{
		{"开启时按配置生效", true, 3, 300, 300 * time.Second, 3},
		{"关闭时为 0，注册不做检查", false, 3, 300, 0, 0},
		{"未配置回落默认值", true, 0, 0, defaultRegisterCooldownSec * time.Second, defaultRegisterCooldownNum},
		{"超过上界钳到上界", true, maxRegisterCooldownNum + 1, maxRegisterCooldownSec + 1, maxRegisterCooldownSec * time.Second, maxRegisterCooldownNum},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := original
			draft.RegisterCooldownEnabled = tc.enabled
			draft.RegisterCooldownNum = tc.num
			draft.RegisterCooldownSec = tc.seconds
			ReplaceRateLimitSetting(draft)
			require.Equal(t, tc.expectWindow, GetRateLimitSnapshot().RegisterCooldown)
			require.Equal(t, tc.expectNum, GetRateLimitSnapshot().RegisterCooldownNum)
		})
	}
}

func TestValidateRateLimitSettingRegisterCooldownRange(t *testing.T) {
	base := GetRateLimitSetting()
	base.RedisTimeoutMs = defaultRateLimitRedisTimeoutMs
	base.RegisterCooldownNum = defaultRegisterCooldownNum
	base.RegisterCooldownSec = defaultRegisterCooldownSec
	require.NoError(t, ValidateRateLimitSetting(base))

	for _, invalid := range []int{-1, 0, maxRegisterCooldownSec + 1} {
		draft := base
		draft.RegisterCooldownSec = invalid
		require.Error(t, ValidateRateLimitSetting(draft), "窗口 %d 应当被拒绝", invalid)
	}
	// 注册窗口不受其它桶 1200 秒窗口上限的约束。
	for _, valid := range []int{1, 120, maxRateLimitWindowSec + 1, maxRegisterCooldownSec} {
		draft := base
		draft.RegisterCooldownSec = valid
		require.NoError(t, ValidateRateLimitSetting(draft), "窗口 %d 应当被接受", valid)
	}

	for _, invalid := range []int{-1, 0, maxRegisterCooldownNum + 1} {
		draft := base
		draft.RegisterCooldownNum = invalid
		require.Error(t, ValidateRateLimitSetting(draft), "次数 %d 应当被拒绝", invalid)
	}
	for _, valid := range []int{1, 5, maxRegisterCooldownNum} {
		draft := base
		draft.RegisterCooldownNum = valid
		require.NoError(t, ValidateRateLimitSetting(draft), "次数 %d 应当被接受", valid)
	}
}

func TestApplyRateLimitEnvDefaultsReadsRegisterCooldown(t *testing.T) {
	original := GetRateLimitSetting()
	t.Cleanup(func() { ReplaceRateLimitSetting(original) })

	t.Setenv("REGISTER_COOLDOWN_ENABLE", "true")
	t.Setenv("REGISTER_COOLDOWN_NUM", "3")
	t.Setenv("REGISTER_COOLDOWN_SEC", "300")
	ApplyRateLimitEnvDefaults()

	require.True(t, GetRateLimitSetting().RegisterCooldownEnabled)
	require.Equal(t, 3, GetRateLimitSetting().RegisterCooldownNum)
	require.Equal(t, 300, GetRateLimitSetting().RegisterCooldownSec)
	require.Equal(t, 3, GetRateLimitSnapshot().RegisterCooldownNum)
	require.Equal(t, 300*time.Second, GetRateLimitSnapshot().RegisterCooldown)
}
