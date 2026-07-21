package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// task_polling.go video path — updateVideoSingleTask settle/refund branches.
// Rule 0: polling is a background worker, off the relay hot path.
// Upstream is a fake in-process adaptor; no real network.
// ===========================================================================

// videoStatusAdaptor returns a NEXAXIS-format response with a fixed status,
// letting a test drive the success / failure branches of updateVideoSingleTask.
type videoStatusAdaptor struct {
	status model.TaskStatus
	url    string
	reason string
}

func (a *videoStatusAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *videoStatusAdaptor) FetchTask(_ string, _ string, body map[string]any, _ string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	resp := dto.TaskResponse[model.Task]{
		Code: dto.TaskSuccessCode,
		Data: model.Task{
			TaskID:     taskID,
			Status:     a.status,
			Progress:   "100%",
			FailReason: a.reason,
		},
	}
	if a.url != "" {
		resp.Data.PrivateData.ResultURL = a.url
	}
	b, err := common.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(b))}, nil
}

func (a *videoStatusAdaptor) ParseTaskResult(_ []byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{Status: string(a.status)}, nil
}

func (a *videoStatusAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func seedVideoBillingTask(t *testing.T, channelID int, userID, tokenID, quota int, publicID, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    userID,
		ChannelId: channelID,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		Quota:     quota,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
			BillingSource:  BillingSourceWallet,
			TokenId:        tokenID,
			BillingContext: &model.TaskBillingContext{
				PerCallBilling:  true, // per-call: settle keeps pre-consumed quota, no adaptor recompute
				OriginModelName: "video-model",
			},
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func withVideoAdaptor(t *testing.T, a TaskPollingAdaptor) {
	t.Helper()
	prev := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return a }
	t.Cleanup(func() { GetTaskAdaptorFunc = prev })
}

func TestVideoPoll_SuccessSettles(t *testing.T) {
	truncate(t)
	const uid, tid, chid, quota = 8001, 8001, 8001, 4000
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-video-succ", 90000)
	seedTaskPollingChannel(t, chid, true)

	task := seedVideoBillingTask(t, chid, uid, tid, quota, "vid-succ", "up-succ")
	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusSuccess, url: "https://cdn/x.mp4"})

	err := UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"),
		map[int][]string{chid: {"up-succ"}},
		map[string]*model.Task{"up-succ": task},
	)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)
	// Per-call billing: quota unchanged, no refund/extra charge.
	assert.Equal(t, 100000, getUserQuota(t, uid))
}

func TestVideoPoll_FailureRefunds(t *testing.T) {
	truncate(t)
	const uid, tid, chid, quota = 8002, 8002, 8002, 3000
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-video-fail", 90000)
	seedTaskPollingChannel(t, chid, true)

	task := seedVideoBillingTask(t, chid, uid, tid, quota, "vid-fail", "up-fail")
	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusFailure, reason: "upstream boom"})

	err := UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"),
		map[int][]string{chid: {"up-fail"}},
		map[string]*model.Task{"up-fail": task},
	)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	// Failure refunds the pre-consumed quota to the wallet.
	assert.Equal(t, 100000+quota, getUserQuota(t, uid))
}

func TestVideoPoll_ChannelMissingMarksFailure(t *testing.T) {
	truncate(t)
	task := seedVideoBillingTask(t, 899001, 1, 0, 0, "vid-nochan", "up-nochan")
	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusSuccess})

	// CacheGetChannel fails -> tasks bulk-marked FAILURE, updateVideoTasks errors,
	// but UpdateVideoTasks swallows per-channel errors and returns nil.
	err := UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"),
		map[int][]string{899001: {"up-nochan"}},
		map[string]*model.Task{"up-nochan": task},
	)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
}

func TestVideoPoll_EmptyAndCancelled(t *testing.T) {
	// Empty channel map => no error.
	assert.NoError(t, UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"),
		map[int][]string{}, map[string]*model.Task{}))

	// Cancelled context surfaces after the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusInProgress})
	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"),
		map[int][]string{1: {"x"}}, map[string]*model.Task{})
	assert.Error(t, err)
}

var _ = common.RedisEnabled
