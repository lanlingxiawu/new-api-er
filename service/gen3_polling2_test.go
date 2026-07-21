package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// task_polling.go — sweepTimedOutTasks / RunTaskPollingOnce / DispatchPlatformUpdate.
// Rule 0: background poller, off the relay hot path.
// ===========================================================================

func withTaskTimeout(t *testing.T, minutes int) {
	t.Helper()
	orig := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = minutes
	t.Cleanup(func() { constant.TaskTimeoutMinutes = orig })
}

func withTaskQueryLimit(t *testing.T, limit int) {
	t.Helper()
	orig := constant.TaskQueryLimit
	constant.TaskQueryLimit = limit
	t.Cleanup(func() { constant.TaskQueryLimit = orig })
}

func TestPoll2_SweepTimedOut_RefundsWallet(t *testing.T) {
	truncate(t)
	withTaskTimeout(t, 10) // cutoff = now - 600s

	const uid, chid, quota = 8101, 8101, 2500
	seedUser(t, uid, 50000)
	seedChannel(t, chid)

	task := makeTask(uid, chid, quota, 0, BillingSourceWallet, 0)
	task.TaskID = "timeout-task"
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "30%"
	task.SubmitTime = time.Now().Unix() - 1000 // older than cutoff, not legacy
	require.NoError(t, model.DB.Create(task).Error)

	sweepTimedOutTasks(context.Background())

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
	// Non-legacy timeout refunds the pre-consumed quota.
	assert.Equal(t, 50000+quota, getUserQuota(t, uid))
}

func TestPoll2_SweepTimedOut_Disabled(t *testing.T) {
	withTaskTimeout(t, 0)
	// TaskTimeoutMinutes <= 0 => early return, no panic.
	sweepTimedOutTasks(context.Background())
}

func TestPoll2_RunTaskPollingOnce(t *testing.T) {
	truncate(t)
	withTaskTimeout(t, 0) // skip sweep
	withTaskQueryLimit(t, 100)

	const uid, tid, chid = 8102, 8102, 8102
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-poll-once", 90000)
	seedTaskPollingChannel(t, chid, true)

	// One pollable video task.
	seedVideoBillingTask(t, chid, uid, tid, 1000, "poll-good", "up-good")

	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusInProgress})

	var reportCalls int
	summary := RunTaskPollingOnce(context.Background(), func(processed, total int) { reportCalls++ })

	assert.GreaterOrEqual(t, summary.UnfinishedTasks, 1)
	assert.GreaterOrEqual(t, summary.PlatformsScanned, 1)
	assert.Greater(t, reportCalls, 0)
}

func TestPoll2_RunTaskPollingOnce_NilAdaptorFactory(t *testing.T) {
	prev := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = nil
	t.Cleanup(func() { GetTaskAdaptorFunc = prev })
	summary := RunTaskPollingOnce(context.Background(), nil)
	assert.Equal(t, 0, summary.UnfinishedTasks)
}

func TestPoll2_DispatchPlatformUpdate(t *testing.T) {
	withVideoAdaptor(t, &videoStatusAdaptor{status: model.TaskStatusInProgress})
	empty := map[int][]string{}
	taskM := map[string]*model.Task{}

	// Midjourney is a reserved no-op branch.
	DispatchPlatformUpdate(context.Background(), constant.TaskPlatformMidjourney, empty, taskM)
	// Suno branch.
	DispatchPlatformUpdate(context.Background(), constant.TaskPlatformSuno, empty, taskM)
	// Default (video) branch.
	DispatchPlatformUpdate(context.Background(), constant.TaskPlatform("kling"), empty, taskM)
	// nil ctx is normalized.
	DispatchPlatformUpdate(nil, constant.TaskPlatformSuno, empty, taskM)
}
