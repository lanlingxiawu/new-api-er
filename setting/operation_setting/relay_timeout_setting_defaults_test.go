package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 重试相关的两个参数是增强功能：默认必须关闭，未配置时请求的尝试次数与重试
// 时机和引入它们之前完全一致（只由"失败请求的重试次数"决定）。

func TestRelayTimeoutRetryEnhancementsOffByDefault(t *testing.T) {
	var fresh RelayTimeoutSetting
	withDefaults := relayTimeoutDefaultsForTest()

	for name, s := range map[string]RelayTimeoutSetting{"zero value": fresh, "package default": withDefaults} {
		assert.Zero(t, s.RetryMinBudgetSeconds, "%s: remaining-budget check must be off", name)
		assert.Zero(t, s.MaxTotalAttempts, "%s: attempts must be unlimited", name)
		assert.Zero(t, s.RelayMaxTotalAttempts(), "%s: 0 means no cap", name)
	}
}

func TestApplyRelayTimeoutEnvDefaultsKeepsRetryEnhancementsOff(t *testing.T) {
	previousSetting := GetRelayTimeoutSetting()
	previousResponse := constant.StreamingTimeout
	previousTotal := common.RelayTimeout
	t.Cleanup(func() {
		constant.StreamingTimeout = previousResponse
		common.RelayTimeout = previousTotal
		ReplaceRelayTimeoutSetting(previousSetting)
	})

	constant.StreamingTimeout = 120
	common.RelayTimeout = 600
	ApplyRelayTimeoutEnvDefaults()

	snapshot := GetRelayTimeoutSnapshot()
	require.NotNil(t, snapshot)
	assert.Zero(t, snapshot.RetryMinBudgetSeconds)
	assert.Zero(t, snapshot.MaxTotalAttempts)
}

// relayTimeoutDefaultsForTest 读取包级默认值（进程启动时、未从 DB 加载前的取值）。
func relayTimeoutDefaultsForTest() RelayTimeoutSetting {
	return RelayTimeoutSetting{
		Enabled:                true,
		ResponseTimeoutSeconds: defaultRelayResponseTimeoutSecs,
		RetryMinBudgetSeconds:  defaultRetryMinBudgetSecs,
		MaxTotalAttempts:       defaultMaxTotalAttempts,
	}
}
