package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

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
		PriceData: types.PriceData{
			ModelPrice:     0.5,
			ModelRatio:     2,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			Quota:          3000,
		},
	}

	LogTaskConsumption(c, info)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, "kling-video", log.ModelName)
	assert.Equal(t, 3000, log.Quota)
	assert.Equal(t, chid, log.ChannelId)
}

func TestTaskBilling_RecalculateByTokens_Snapshot(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 8202, 8202, 8202
	const initQuota, preConsumed = 100000, 1000
	seedUser(t, uid, initQuota)
	seedToken(t, tid, uid, "sk-taskrecalc", 90000)
	seedChannel(t, chid)

	task := makeTask(uid, chid, preConsumed, tid, BillingSourceWallet, 0)
	// BillingContext snapshot: modelRatio 2, groupRatio 1 => 1000 tokens => 2000 quota.
	task.PrivateData.BillingContext.ModelRatio = 2
	task.PrivateData.BillingContext.GroupRatio = 1
	require.NoError(t, model.DB.Create(task).Error)

	RecalculateTaskQuotaByTokens(context.Background(), task, 1000)

	// actualQuota 2000, delta +1000 charged from wallet.
	assert.Equal(t, initQuota-(2000-preConsumed), getUserQuota(t, uid))
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
