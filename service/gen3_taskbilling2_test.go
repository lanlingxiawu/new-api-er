package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// task_billing.go — LogTaskConsumption + RecalculateTaskQuotaByTokens.
// Task billing runs off the relay hot path (async task lifecycle).
// ===========================================================================

func TestTaskBilling_LogTaskConsumption(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 8201, 8201, 8201
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-tasklog", 90000)
	seedChannel(t, chid)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set("token_name", "tkn")

	info := &relaycommon.RelayInfo{
		UserId:          uid,
		TokenId:         tid,
		UsingGroup:      "default",
		OriginModelName: "kling-video",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{Action: "generate"},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         chid,
			IsModelMapped:     true,
			UpstreamModelName: "kling-v1",
		},
		PriceData: hosttypes.PriceData{
			ModelPrice:     0.5,
			ModelRatio:     2,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
			Quota:          3000,
		},
	}

	LogTaskConsumption(c, info, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, "kling-video", log.ModelName)
	assert.Equal(t, 3000, log.Quota)
	assert.Equal(t, chid, log.ChannelId)
}

func TestTaskBilling_RecalculateByTokens_LegacyThirdPartySD2UsesSnapshot(t *testing.T) {
	previousRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})
	// 全局倍率与快照不同，确认旧 SD2 任务只按快照倍率结算。
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"dreamina-seedance-2-0-260128":9}`))

	tests := []struct {
		name     string
		platform string
		metadata map[string]string
	}{
		{name: "matrix pricing metadata", platform: "thirdpartysd2", metadata: map[string]string{"channel": "thirdpartysd2", "pricing_mode": "resolution_video_matrix", "resolution": "720p", "video_input": "false"}},
		{name: "numeric legacy platform", platform: "58"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const uid, tid, chid = 8202, 8202, 8202
			const initQuota, preConsumed = 100000, 1000
			seedUser(t, uid, initQuota)
			seedToken(t, tid, uid, "sk-taskrecalc", 90000)
			seedChannel(t, chid)

			task := makeTask(uid, chid, preConsumed, tid, BillingSourceWallet, 0)
			task.Platform = constant.TaskPlatform(testCase.platform)
			task.Properties.OriginModelName = "dreamina-seedance-2-0-260128"
			task.PrivateData.BillingContext.OriginModelName = "dreamina-seedance-2-0-260128"
			task.PrivateData.BillingContext.ModelPrice = -1
			task.PrivateData.BillingContext.PricingMetadata = testCase.metadata
			// Snapshot: modelRatio 2, groupRatio 1 => 1000 tokens => 2000 quota.
			task.PrivateData.BillingContext.ModelRatio = 2
			task.PrivateData.BillingContext.GroupRatio = 1
			require.NoError(t, model.DB.Create(task).Error)

			assert.True(t, RecalculateTaskQuotaByTokens(context.Background(), task, 1000))

			// actualQuota 2000, delta +1000 charged from wallet.
			assert.Equal(t, initQuota-(2000-preConsumed), getUserQuota(t, uid))
			assert.Equal(t, 2000, task.Quota)
		})
	}
}

// A task whose ratio resolves to 0 (per-call priced, or unconfigured) must
// keep its pre-consumed charge instead of settling to 0 and refunding it all.
func TestTaskBilling_RecalculateByTokens_ZeroRatioKeepsPreConsumed(t *testing.T) {
	previousRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})

	tests := []struct {
		name          string
		modelRatios   string
		snapshotRatio float64
	}{
		{name: "configured zero ratio", modelRatios: `{"test-model":0}`},
		{name: "configured zero ratio ignores snapshot ratio", modelRatios: `{"test-model":0}`, snapshotRatio: 2},
		{name: "unconfigured ratio", modelRatios: `{}`},
		{name: "legacy SD2 snapshot with zero ratio", modelRatios: `{}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(testCase.modelRatios))
			truncate(t)
			const uid, tid, chid = 8204, 8204, 8204
			const initQuota, preConsumed = 100000, 1000
			seedUser(t, uid, initQuota)
			seedToken(t, tid, uid, "sk-taskrecalc-zero", 90000)
			seedChannel(t, chid)

			task := makeTask(uid, chid, preConsumed, tid, BillingSourceWallet, 0)
			task.Platform = "kling"
			if testCase.name == "legacy SD2 snapshot with zero ratio" {
				task.Platform = "58"
			}
			task.PrivateData.BillingContext.ModelRatio = testCase.snapshotRatio
			require.NoError(t, model.DB.Create(task).Error)

			assert.False(t, RecalculateTaskQuotaByTokens(context.Background(), task, 1000))

			assert.Equal(t, initQuota, getUserQuota(t, uid))
			assert.Equal(t, preConsumed, task.Quota)
			assert.Equal(t, preConsumed, getTaskQuota(t, task.ID))
		})
	}
}

func TestTaskBilling_RecalculateByTokens_ConfiguredRatio(t *testing.T) {
	previousRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":2}`))

	truncate(t)
	const uid, tid, chid = 8205, 8205, 8205
	const initQuota, preConsumed = 100000, 3000
	seedUser(t, uid, initQuota)
	seedToken(t, tid, uid, "sk-taskrecalc-ratio", 90000)
	seedChannel(t, chid)

	task := makeTask(uid, chid, preConsumed, tid, BillingSourceWallet, 0)
	task.Platform = "kling"
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RecalculateTaskQuotaByTokens(context.Background(), task, 1000))

	// 1000 tokens x ratio 2 x default group ratio 1 = 2000; 1000 refunded.
	assert.Equal(t, initQuota+(preConsumed-2000), getUserQuota(t, uid))
	assert.Equal(t, 2000, task.Quota)
}

func TestTaskBilling_RecalculateByTokens_ZeroTokens(t *testing.T) {
	truncate(t)
	const uid = 8203
	seedUser(t, uid, 5000)
	task := makeTask(uid, 0, 1000, 0, BillingSourceWallet, 0)
	// totalTokens <= 0 => early return, no change.
	RecalculateTaskQuotaByTokens(context.Background(), task, 0)
	assert.Equal(t, 5000, getUserQuota(t, uid))
}
