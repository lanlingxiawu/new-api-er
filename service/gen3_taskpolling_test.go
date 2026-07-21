package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// task_polling.go — pure helpers + Suno channel-failure path.
// Rule 0: polling runs off the relay hot path (background worker).
// ===========================================================================

func TestPolling_TruncateBase64(t *testing.T) {
	assert.Equal(t, "short", truncateBase64("short"))
	long := strings.Repeat("a", 300)
	got := truncateBase64(long)
	assert.Equal(t, 256+3, len(got))
	assert.True(t, strings.HasSuffix(got, "..."))
}

func TestPolling_RedactVideoResponseBody(t *testing.T) {
	// Non-JSON returned unchanged.
	assert.Equal(t, []byte("not json"), redactVideoResponseBody([]byte("not json")))

	body := []byte(`{"response":{"bytesBase64Encoded":"secret","video":"` + strings.Repeat("x", 300) + `","videos":[{"bytesBase64Encoded":"s2","id":"v1"}]}}`)
	out := redactVideoResponseBody(body)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	resp := m["response"].(map[string]any)
	assert.NotContains(t, resp, "bytesBase64Encoded")
	assert.LessOrEqual(t, len(resp["video"].(string)), 259)
	vids := resp["videos"].([]any)
	assert.NotContains(t, vids[0].(map[string]any), "bytesBase64Encoded")
	assert.Equal(t, "v1", vids[0].(map[string]any)["id"])
}

func TestPolling_FormatPollingTaskLogID(t *testing.T) {
	assert.Equal(t, "", formatPollingTaskLogID(nil, nil))

	task := &model.Task{TaskID: "t-1"}
	// nil channel => plain task id.
	assert.Equal(t, "t-1", formatPollingTaskLogID(task, nil))
	// Non-SD2 channel => plain task id.
	assert.Equal(t, "t-1", formatPollingTaskLogID(task, &model.Channel{Type: 1}))
}

func TestPolling_TaskNeedsUpdate(t *testing.T) {
	base := &model.Task{
		SubmitTime: 1, StartTime: 2, FinishTime: 3,
		Status: model.TaskStatus("processing"), FailReason: "",
		Progress: "50%", Data: json.RawMessage(`{"a":1}`),
	}
	same := dto.SunoDataResponse{
		SubmitTime: 1, StartTime: 2, FinishTime: 3,
		Status: "processing", FailReason: "", Data: json.RawMessage(`{"a":1}`),
	}
	assert.False(t, taskNeedsUpdate(base, same))

	// Different status.
	changed := same
	changed.Status = "success"
	assert.True(t, taskNeedsUpdate(base, changed))

	// Different submit time.
	changed = same
	changed.SubmitTime = 99
	assert.True(t, taskNeedsUpdate(base, changed))

	// Different fail reason.
	changed = same
	changed.FailReason = "boom"
	assert.True(t, taskNeedsUpdate(base, changed))

	// Terminal but progress not 100%.
	term := &model.Task{Status: model.TaskStatusSuccess, Progress: "50%"}
	assert.True(t, taskNeedsUpdate(term, dto.SunoDataResponse{Status: string(model.TaskStatusSuccess)}))

	// Different data payload.
	changed = same
	changed.Data = json.RawMessage(`{"a":2}`)
	assert.True(t, taskNeedsUpdate(base, changed))
}

// --- updateSunoTasks channel-lookup failure => bulk mark FAILURE -----------

func TestPolling_UpdateSunoTasks_ChannelMissingMarksFailure(t *testing.T) {
	truncate(t)

	// Seed an in-progress task; its channel id does not exist so CacheGetChannel
	// fails and the task is bulk-marked FAILURE.
	task := makeTask(700, 987654, 1000, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	taskM := map[string]*model.Task{"upstream-1": task}
	// map upstream id -> task; task.TaskID is the DB task_id but the map key is the
	// upstream id used in taskChannelM.
	// CacheGetChannel fails; the fallback bulk-update succeeds so the call returns
	// nil but the task is persisted as FAILURE.
	err := updateSunoTasks(context.Background(), 987654, []string{"upstream-1"}, taskM)
	assert.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Contains(t, reloaded.FailReason, "获取渠道信息失败")
}

func TestPolling_UpdateSunoTasks_EmptyNoop(t *testing.T) {
	assert.NoError(t, updateSunoTasks(context.Background(), 1, nil, map[string]*model.Task{}))
	// Cancelled context short-circuits.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, updateSunoTasks(ctx, 1, []string{"x"}, map[string]*model.Task{}))
}

func TestPolling_UpdateSunoTasks_Wrapper(t *testing.T) {
	// Wrapper iterates channels; cancelled context returns ctx.Err().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := UpdateSunoTasks(ctx, map[int][]string{1: {"x"}}, map[string]*model.Task{})
	assert.Error(t, err)

	// Empty map => no error.
	assert.NoError(t, UpdateSunoTasks(context.Background(), map[int][]string{}, map[string]*model.Task{}))
}

var _ = constant.TaskPlatformSuno
