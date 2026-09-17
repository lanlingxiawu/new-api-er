package model

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cases from upstream/main:model/task_cas_test.go, run on the fork's shared
// MySQL/PG harness (zz_harness_test.go owns TestMain). Upstream relies on a
// private SQLite DB plus global table truncation and fixed ids; here every
// task gets a unique task_id / user id and is deleted by primary key.

func insertTask(t *testing.T, task *Task) {
	t.Helper()
	requireDB(t)
	task.CreatedAt = time.Now().Unix()
	task.UpdatedAt = time.Now().Unix()
	require.NoError(t, DB.Create(task).Error)
	id := task.ID
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Delete(&Task{}, id)
		}
	})
}

func TestGetTaskForProtocolObservationScopesOwnerAndPlatform(t *testing.T) {
	ownerID := nextTestID()
	otherUserID := nextTestID()
	platformA := constant.TaskPlatform(uniq("plugin-a"))
	platformB := constant.TaskPlatform(uniq("plugin-b"))
	task := &Task{
		TaskID:   uniq("task_protocol_scope"),
		UserId:   ownerID,
		Platform: platformA,
		Status:   TaskStatusInProgress,
	}
	insertTask(t, task)

	got, exists, err := GetTaskForProtocolObservation(context.Background(), ownerID, platformA, task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, task.ID, got.ID)

	for _, query := range []struct {
		userID   int
		platform constant.TaskPlatform
	}{
		{userID: otherUserID, platform: platformA},
		{userID: ownerID, platform: platformB},
	} {
		got, exists, err = GetTaskForProtocolObservation(context.Background(), query.userID, query.platform, task.TaskID)
		require.NoError(t, err)
		assert.False(t, exists)
		assert.Nil(t, got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = GetTaskForProtocolObservation(cancelled, ownerID, platformA, task.TaskID)
	require.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// Snapshot / Equal — pure logic tests (no DB)
// ---------------------------------------------------------------------------

func TestSnapshotEqual_Same(t *testing.T) {
	s := taskSnapshot{
		Status:     TaskStatusInProgress,
		Progress:   "50%",
		StartTime:  1000,
		FinishTime: 0,
		FailReason: "",
		ResultURL:  "",
		Data:       json.RawMessage(`{"key":"value"}`),
	}
	assert.True(t, s.Equal(s))
}

func TestSnapshotEqual_DifferentStatus(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{}`)}
	b := taskSnapshot{Status: TaskStatusSuccess, Data: json.RawMessage(`{}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_DifferentProgress(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Progress: "30%", Data: json.RawMessage(`{}`)}
	b := taskSnapshot{Status: TaskStatusInProgress, Progress: "60%", Data: json.RawMessage(`{}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_DifferentData(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{"a":1}`)}
	b := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{"a":2}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_NilVsEmpty(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: nil}
	b := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage{}}
	// bytes.Equal(nil, []byte{}) == true
	assert.True(t, a.Equal(b))
}

func TestSnapshotEqual_PluginStateAndPollFailures(t *testing.T) {
	base := taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 2,
	}
	assert.True(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 2,
	}))
	assert.False(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"b"}`),
		PollFailures: 2,
	}))
	assert.False(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 3,
	}))
}

func TestSnapshot_Roundtrip(t *testing.T) {
	task := &Task{
		Status:     TaskStatusInProgress,
		Progress:   "42%",
		StartTime:  1234,
		FinishTime: 5678,
		FailReason: "timeout",
		PrivateData: TaskPrivateData{
			ResultURL:    "https://example.com/result.mp4",
			PluginState:  json.RawMessage(`{"req_key":"keep"}`),
			PollFailures: 3,
		},
		Data: json.RawMessage(`{"model":"test-model"}`),
	}
	snap := task.Snapshot()
	assert.Equal(t, task.Status, snap.Status)
	assert.Equal(t, task.Progress, snap.Progress)
	assert.Equal(t, task.StartTime, snap.StartTime)
	assert.Equal(t, task.FinishTime, snap.FinishTime)
	assert.Equal(t, task.FailReason, snap.FailReason)
	assert.Equal(t, task.PrivateData.ResultURL, snap.ResultURL)
	assert.JSONEq(t, string(task.Data), string(snap.Data))
	assert.Equal(t, task.PrivateData.PluginState, snap.PluginState)
	assert.Equal(t, task.PrivateData.PollFailures, snap.PollFailures)
}

// ---------------------------------------------------------------------------
// UpdateWithStatus CAS — DB integration tests
// ---------------------------------------------------------------------------

func TestUpdateWithStatus_Win(t *testing.T) {
	task := &Task{
		TaskID:   uniq("task_cas_win"),
		Status:   TaskStatusInProgress,
		Progress: "50%",
		Data:     json.RawMessage(`{}`),
	}
	insertTask(t, task)

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	assert.True(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusSuccess, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
}

func TestUpdateWithStatus_Lose(t *testing.T) {
	task := &Task{
		TaskID: uniq("task_cas_lose"),
		Status: TaskStatusFailure,
		Data:   json.RawMessage(`{}`),
	}
	insertTask(t, task)

	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithStatus(TaskStatusInProgress) // wrong fromStatus
	require.NoError(t, err)
	assert.False(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusFailure, reloaded.Status) // unchanged
}

func TestUpdateWithStatus_ConcurrentWinner(t *testing.T) {
	task := &Task{
		TaskID: uniq("task_cas_race"),
		Status: TaskStatusInProgress,
		Quota:  1000,
		Data:   json.RawMessage(`{}`),
	}
	insertTask(t, task)

	const goroutines = 5
	wins := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			candidate := &Task{
				ID:       task.ID,
				TaskID:   task.TaskID,
				Status:   TaskStatusSuccess,
				Progress: "100%",
				Quota:    task.Quota,
				Data:     json.RawMessage(`{}`),
			}
			candidate.CreatedAt = task.CreatedAt
			candidate.UpdatedAt = time.Now().Unix()
			won, err := candidate.UpdateWithStatus(TaskStatusInProgress)
			if err == nil {
				wins[idx] = won
			}
		}(i)
	}
	wg.Wait()

	winCount := 0
	for _, w := range wins {
		if w {
			winCount++
		}
	}
	assert.Equal(t, 1, winCount, "exactly one goroutine should win the CAS")
}

func TestUpdateWithStatus_PersistsPluginStateAndPollFailures(t *testing.T) {
	task := &Task{
		TaskID: uniq("task_cas_plugin_state"),
		Status: TaskStatusInProgress,
		Data:   json.RawMessage(`{}`),
		PrivateData: TaskPrivateData{
			PluginState:  json.RawMessage(`{"req_key":"old"}`),
			PollFailures: 1,
		},
	}
	insertTask(t, task)

	task.PrivateData.PluginState = json.RawMessage(`{"req_key":"new"}`)
	task.PrivateData.PollFailures = 4
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusInProgress, reloaded.Status)
	assert.JSONEq(t, `{"req_key":"new"}`, string(reloaded.PrivateData.PluginState))
	assert.Equal(t, 4, reloaded.PrivateData.PollFailures)
}
