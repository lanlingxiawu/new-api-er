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
// task_polling.go updateSunoTasks — success + failure(refund) upstream paths.
// In-process fake adaptor; no real network. Background poller (off hot path).
// ===========================================================================

type sunoAdaptor struct {
	items []dto.SunoDataResponse
}

func (a *sunoAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (a *sunoAdaptor) FetchTask(_ string, _ string, _ map[string]any, _ string) (*http.Response, error) {
	resp := dto.TaskResponse[[]dto.SunoDataResponse]{Code: dto.TaskSuccessCode, Data: a.items}
	b, err := common.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(b))}, nil
}
func (a *sunoAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (a *sunoAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}

func seedSunoChannel(t *testing.T, id int) {
	t.Helper()
	svcCleanupRow(t, &model.Channel{}, id)
	base := "http://suno.local"
	ch := &model.Channel{
		Id:      id,
		Type:    constant.ChannelTypeKling,
		Name:    "suno_ch",
		Key:     "sk-suno",
		BaseURL: &base,
		Status:  common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(ch).Error)
}

func TestSuno_UpdateTasks_FailureRefunds(t *testing.T) {
	truncate(t)
	const uid, chid, quota = 8401, 8401, 2000
	seedUser(t, uid, 50000)
	seedSunoChannel(t, chid)

	task := makeTask(uid, chid, quota, 0, BillingSourceWallet, 0)
	task.TaskID = "suno-1"
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	prev := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &sunoAdaptor{items: []dto.SunoDataResponse{{
			TaskID:     "up-suno-1",
			Status:     string(model.TaskStatusFailure),
			FailReason: "generation failed",
		}}}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = prev })

	err := updateSunoTasks(context.Background(), chid, []string{"up-suno-1"},
		map[string]*model.Task{"up-suno-1": task})
	require.NoError(t, err)

	// Failure => quota refunded to the wallet.
	assert.Equal(t, 50000+quota, getUserQuota(t, uid))
}

func TestSuno_UpdateTasks_Success(t *testing.T) {
	truncate(t)
	const uid, chid = 8402, 8402
	seedUser(t, uid, 50000)
	seedSunoChannel(t, chid)

	task := makeTask(uid, chid, 0, 0, BillingSourceWallet, 0)
	task.TaskID = "suno-2"
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "50%"
	require.NoError(t, model.DB.Create(task).Error)

	prev := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &sunoAdaptor{items: []dto.SunoDataResponse{{
			TaskID:     "up-suno-2",
			Status:     string(model.TaskStatusSuccess),
			FinishTime: time.Now().Unix(),
		}}}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = prev })

	err := updateSunoTasks(context.Background(), chid, []string{"up-suno-2"},
		map[string]*model.Task{"up-suno-2": task})
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "100%", reloaded.Progress)
}
