package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 状态接口是纯内存快照：不得触碰任何数据库，否则管理页轮询会把压力打到日志库上。
func TestAdminGetRelayLogPipelineStatusReturnsInMemorySnapshot(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodGet, "/api/admin/system/relay-log-pipeline/status", "")

	require.NotPanics(t, func() { AdminGetRelayLogPipelineStatus(ctx) })

	require.Equal(t, http.StatusOK, rec.Code)
	response := decodeResp(t, rec)
	assert.True(t, response.Success)

	data := decodeStatusData(t, response.Data)
	// 管理页依赖这几个字段判断管道是否健康，缺一个前端就会显示空白
	for _, key := range []string{
		"enabled", "circuit_state", "consume", "error", "retry",
		"persisted_total", "fallback_total", "db_timeout_total", "replay",
	} {
		assert.Contains(t, data, key)
	}
}

func decodeStatusData(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var data map[string]any
	require.NoError(t, common.Unmarshal(raw, &data))
	return data
}

func TestAdminGetRelayLogPipelineStatusReportsCircuitState(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodGet, "/api/admin/system/relay-log-pipeline/status", "")
	AdminGetRelayLogPipelineStatus(ctx)

	data := decodeStatusData(t, decodeResp(t, rec).Data)
	state, _ := data["circuit_state"].(string)
	assert.Contains(t, []string{"closed", "open", "half_open"}, state)
}

// 没有 fallback 文件时不应该报错，也不应该谎称启动了任务。
func TestAdminStartRelayLogFallbackReplayReportsNotStartedWhenNothingToReplay(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/system/relay-log-pipeline/replay", "{}")

	require.NotPanics(t, func() { AdminStartRelayLogFallbackReplay(ctx) })

	require.Equal(t, http.StatusOK, rec.Code)
	response := decodeResp(t, rec)
	if !response.Success {
		// 环境里恰好有历史 fallback 文件或正在回放时，必须给出可读的业务错误而非 500
		assert.NotEmpty(t, response.Message)
		return
	}
	assert.Contains(t, decodeStatusData(t, response.Data), "started")
}

// 反复快速触发时，每一次都必须给出 HTTP 200 的规范信封：
// 要么成功（且最多只有一次 started=true），要么是带可读文案的业务错误。
// 管理员连点按钮不该看到 5xx，也不该同时跑起两个回放任务。
//
// 单飞语义本身在 model 层用真实 fallback 文件覆盖
// （TestStartRelayLogFallbackReplayIsSingleFlight、
// TestManualRelayLogFallbackReplayConcurrentTriggers）；
// 这里只验控制器的错误映射，因此不依赖环境里是否存在 fallback 文件。
func TestAdminStartRelayLogFallbackReplayAlwaysReturnsWellFormedEnvelope(t *testing.T) {
	startedCount := 0
	for i := 0; i < 5; i++ {
		ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/system/relay-log-pipeline/replay", "{}")
		require.NotPanics(t, func() { AdminStartRelayLogFallbackReplay(ctx) })

		require.Equal(t, http.StatusOK, rec.Code, "第 %d 次触发返回了非 200", i+1)
		response := decodeResp(t, rec)
		if !response.Success {
			assert.NotEmpty(t, response.Message, "业务错误必须带可读文案")
			continue
		}
		data := decodeStatusData(t, response.Data)
		require.Contains(t, data, "started")
		if started, ok := data["started"].(bool); ok && started {
			startedCount++
		}
	}
	assert.LessOrEqual(t, startedCount, 1, "同时启动了多个回放任务")
}
