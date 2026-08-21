package model

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// System-task helpers: every test uses a UNIQUE task type so it never collides
// with the (shared) active_key unique index or with other tests' rows.
// ---------------------------------------------------------------------------

// uniqTaskType returns a unique type <=64 chars for the system_tasks table.
func uniqTaskType() string {
	return "sttype_" + strings.ReplaceAll(uniq("t"), "_", "")
}

// cleanupSystemTaskType registers hard-delete cleanup of every system task of a
// type plus the (single) lock row for that type.
func cleanupSystemTaskType(t *testing.T, taskType string) {
	t.Helper()
	t.Cleanup(func() {
		if DB == nil {
			return
		}
		DB.Unscoped().Where("type = ?", taskType).Delete(&SystemTask{})
		DB.Where("type = ?", taskType).Delete(&SystemTaskLock{})
	})
}

func newPendingSystemTask(t *testing.T, taskType string) *SystemTask {
	t.Helper()
	requireDB(t)
	task, err := CreateSystemTask(taskType, map[string]any{"foo": "bar"}, map[string]any{"n": float64(1)})
	require.NoError(t, err)
	cleanupSystemTaskType(t, taskType)
	return task
}

// ---------------------------------------------------------------------------
// Pure logic + JSON helpers
// ---------------------------------------------------------------------------

func TestGenerateSystemTaskID(t *testing.T) {
	id, err := GenerateSystemTaskID()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "systask_"))
	id2, _ := GenerateSystemTaskID()
	assert.NotEqual(t, id, id2)
}

func TestSystemTaskJSONHelpers(t *testing.T) {
	// nil marshals to empty string
	s, err := marshalSystemTaskJSON(nil)
	require.NoError(t, err)
	assert.Equal(t, "", s)

	s, err = marshalSystemTaskJSON(map[string]any{"a": float64(1)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, s)

	// decode string into typed target; empty is a no-op
	var out map[string]any
	require.NoError(t, decodeSystemTaskJSONString("", &out))
	assert.Nil(t, out)
	require.NoError(t, decodeSystemTaskJSONString(`{"a":1}`, &out))
	assert.Equal(t, float64(1), out["a"])

	// decode to value: empty -> nil, valid -> parsed, invalid -> raw string
	assert.Nil(t, decodeSystemTaskJSONValue(""))
	assert.Equal(t, float64(1), decodeSystemTaskJSONValue("1"))
	assert.Equal(t, "not-json", decodeSystemTaskJSONValue("not-json"))
}

func TestSystemTask_DecodeAndToResponse(t *testing.T) {
	task := &SystemTask{
		ID:      123,
		TaskID:  "systask_x",
		Type:    "log_cleanup",
		Status:  SystemTaskStatusRunning,
		Payload: `{"p":1}`,
		State:   `{"s":2}`,
		Result:  `{"r":3}`,
		Error:   "boom",
	}

	var payload map[string]any
	require.NoError(t, task.DecodePayload(&payload))
	assert.Equal(t, float64(1), payload["p"])

	var state map[string]any
	require.NoError(t, task.DecodeState(&state))
	assert.Equal(t, float64(2), state["s"])

	resp := task.ToResponse()
	assert.Equal(t, int64(123), resp.ID)
	assert.Equal(t, "systask_x", resp.TaskID)
	assert.Equal(t, SystemTaskStatusRunning, resp.Status)
	assert.Equal(t, "boom", resp.Error)
	assert.Equal(t, map[string]any{"p": float64(1)}, resp.Payload)
	assert.Equal(t, map[string]any{"s": float64(2)}, resp.State)
	assert.Equal(t, map[string]any{"r": float64(3)}, resp.Result)
}

func TestActiveSystemTaskStatuses(t *testing.T) {
	assert.Equal(t, []string{"pending", "running"}, activeSystemTaskStatuses())
}

// ---------------------------------------------------------------------------
// DB: create / lookup / list
// ---------------------------------------------------------------------------

func TestCreateAndGetSystemTask(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	assert.True(t, strings.HasPrefix(task.TaskID, "systask_"))
	assert.Equal(t, SystemTaskStatusPending, task.Status)
	require.NotNil(t, task.ActiveKey)
	assert.Equal(t, tt, *task.ActiveKey)
	assert.NotZero(t, task.CreatedAt)
	assert.NotZero(t, task.UpdatedAt)

	// GetSystemTaskByTaskID
	got, err := GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, task.ID, got.ID)

	// missing -> (nil, nil)
	got, err = GetSystemTaskByTaskID("systask_missing_zzz")
	require.NoError(t, err)
	assert.Nil(t, got)

	// GetActiveSystemTask returns pending
	active, err := GetActiveSystemTask(tt)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, task.ID, active.ID)

	// no active task of an unused type -> (nil, nil)
	active, err = GetActiveSystemTask(uniqTaskType())
	require.NoError(t, err)
	assert.Nil(t, active)
}

func TestActiveKeyUniqueness(t *testing.T) {
	tt := uniqTaskType()
	newPendingSystemTask(t, tt)
	// a second active task of the same type violates the active_key unique index
	_, err := CreateSystemTask(tt, nil, nil)
	assert.Error(t, err)
}

func TestFindPendingSystemTasks(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)

	// limit <= 0 clamps to 1
	got, err := FindPendingSystemTasks(tt, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, task.ID, got[0].ID)

	got, err = FindPendingSystemTasks(tt, 10)
	require.NoError(t, err)
	assert.Len(t, got, 1)

	// unused type -> empty
	got, err = FindPendingSystemTasks(uniqTaskType(), 5)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFindEarliestPendingSystemTasks(t *testing.T) {
	ttA := uniqTaskType()
	ttB := uniqTaskType()
	a := newPendingSystemTask(t, ttA)
	b := newPendingSystemTask(t, ttB)

	// empty input -> empty map
	m, err := FindEarliestPendingSystemTasks(nil)
	require.NoError(t, err)
	assert.Empty(t, m)

	m, err = FindEarliestPendingSystemTasks([]string{ttA, ttB})
	require.NoError(t, err)
	require.Len(t, m, 2)
	assert.Equal(t, a.ID, m[ttA].ID)
	assert.Equal(t, b.ID, m[ttB].ID)
}

func TestGetLatestSystemTask(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)

	latest, err := GetLatestSystemTask(tt)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, task.ID, latest.ID)

	// unused type -> (nil, nil)
	latest, err = GetLatestSystemTask(uniqTaskType())
	require.NoError(t, err)
	assert.Nil(t, latest)

	// GetLatestSystemTasks batched + empty guard
	m, err := GetLatestSystemTasks(nil)
	require.NoError(t, err)
	assert.Empty(t, m)

	m, err = GetLatestSystemTasks([]string{tt})
	require.NoError(t, err)
	require.Len(t, m, 1)
	assert.Equal(t, task.ID, m[tt].ID)
}

func TestListSystemTasks_LimitClamping(t *testing.T) {
	tt := uniqTaskType()
	newPendingSystemTask(t, tt)

	// limit <= 0 -> clamps to 20
	got, err := ListSystemTasks(0)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got), 20)

	// limit > 100 -> clamps to 100
	got, err = ListSystemTasks(500)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got), 100)

	// small limit honored
	got, err = ListSystemTasks(1)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got), 1)
}

func TestListSystemTasksByTypes_FiltersOrdersAndClampsLimit(t *testing.T) {
	includedType := uniqTaskType()
	otherIncludedType := uniqTaskType()
	excludedType := uniqTaskType()
	first := newPendingSystemTask(t, includedType)
	second := newPendingSystemTask(t, otherIncludedType)
	newPendingSystemTask(t, excludedType)

	tasks, err := ListSystemTasksByTypes([]string{includedType, otherIncludedType}, 1000)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, second.ID, tasks[0].ID)
	assert.Equal(t, first.ID, tasks[1].ID)

	tasks, err = ListSystemTasksByTypes([]string{includedType, otherIncludedType}, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, second.ID, tasks[0].ID)

	tasks, err = ListSystemTasksByTypes(nil, 10)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

// ---------------------------------------------------------------------------
// DB: locking / claim / lease lifecycle
// ---------------------------------------------------------------------------

func TestClaimSystemTask(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()

	// claim succeeds: task -> running, lock row created
	claimed, ok, err := ClaimSystemTask(task.ID, tt, "runner-A", now+300)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, claimed)
	assert.Equal(t, SystemTaskStatusRunning, claimed.Status)
	assert.Equal(t, "runner-A", claimed.LockedBy)

	var lock SystemTaskLock
	require.NoError(t, DB.Where("type = ?", tt).First(&lock).Error)
	assert.Equal(t, "runner-A", lock.LockedBy)
	assert.Equal(t, task.TaskID, lock.TaskID)

	// second claim finds no pending row (already running) -> not acquired, no error
	claimed2, ok2, err2 := ClaimSystemTask(task.ID, tt, "runner-B", now+300)
	require.NoError(t, err2)
	assert.False(t, ok2)
	assert.Nil(t, claimed2)

	// claiming a non-existent id -> not acquired, no error
	claimed3, ok3, err3 := ClaimSystemTask(task.ID+123456789, tt, "runner-C", now+300)
	require.NoError(t, err3)
	assert.False(t, ok3)
	assert.Nil(t, claimed3)
}

// A stale lock from a previous (still "running") task is stolen on claim, and
// the previous task's lease is marked expired.
func TestClaimSystemTask_StealsExpiredLeaseAndMarksOldFailed(t *testing.T) {
	requireDB(t)
	tt := uniqTaskType()
	cleanupSystemTaskType(t, tt)
	now := common.GetTimestamp()

	// old task still "running" but with active_key cleared (so a new active task
	// of the same type is allowed), holding an EXPIRED lock.
	oldTaskID := "systask_old_" + strings.ReplaceAll(uniq("o"), "_", "")
	oldTask := &SystemTask{
		TaskID:    oldTaskID,
		Type:      tt,
		Status:    SystemTaskStatusRunning,
		ActiveKey: nil,
		CreatedAt: now - 1000,
		UpdatedAt: now - 1000,
	}
	require.NoError(t, DB.Create(oldTask).Error)
	staleLock := &SystemTaskLock{Type: tt, TaskID: oldTaskID, LockedBy: "old-runner", LockedUntil: now - 100, UpdatedAt: now - 100}
	require.NoError(t, DB.Create(staleLock).Error)

	// new pending task of the same type
	newTask := newPendingSystemTask(t, tt)

	claimed, ok, err := ClaimSystemTask(newTask.ID, tt, "runner-new", now+300)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, SystemTaskStatusRunning, claimed.Status)
	assert.Equal(t, "runner-new", claimed.LockedBy)

	// old task's lease was marked expired -> failed
	reloadOld, err := GetSystemTaskByTaskID(oldTaskID)
	require.NoError(t, err)
	require.NotNil(t, reloadOld)
	assert.Equal(t, SystemTaskStatusFailed, reloadOld.Status)
	assert.Equal(t, "task lease expired", reloadOld.Error)
}

func TestAcquireSystemTaskLock_CreateStealAndBlock(t *testing.T) {
	requireDB(t)
	tt := uniqTaskType()
	cleanupSystemTaskType(t, tt)
	now := common.GetTimestamp()

	// first acquisition creates the lock (already expired for the next step)
	ok, expired, err := acquireSystemTaskLock(tt, "task-1", "runnerA", now, now-1)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "", expired)

	// second acquisition steals the expired lock; returns the previous task id
	ok, expired, err = acquireSystemTaskLock(tt, "task-2", "runnerB", now, now+300)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "task-1", expired)

	// third acquisition while lock is valid -> blocked
	ok, expired, err = acquireSystemTaskLock(tt, "task-3", "runnerC", now, now+300)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "", expired)
}

func TestUpdateSystemTaskState(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()
	_, ok, err := ClaimSystemTask(task.ID, tt, "runner-A", now+300)
	require.NoError(t, err)
	require.True(t, ok)

	// correct runner + valid lock -> updates
	require.NoError(t, UpdateSystemTaskState(task.TaskID, "runner-A", map[string]any{"progress": float64(50)}))
	reload, _ := GetSystemTaskByTaskID(task.TaskID)
	var st map[string]any
	require.NoError(t, reload.DecodeState(&st))
	assert.Equal(t, float64(50), st["progress"])

	// wrong runner -> lock lost sentinel
	err = UpdateSystemTaskState(task.TaskID, "wrong-runner", map[string]any{"x": float64(1)})
	assert.ErrorIs(t, err, ErrSystemTaskLockLost)
}

func TestRenewSystemTaskLock(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()
	_, ok, err := ClaimSystemTask(task.ID, tt, "runner-A", now+300)
	require.NoError(t, err)
	require.True(t, ok)

	// renew by holder
	require.NoError(t, RenewSystemTaskLock(task.TaskID, "runner-A", now+600))
	var lock SystemTaskLock
	require.NoError(t, DB.Where("type = ?", tt).First(&lock).Error)
	assert.Equal(t, now+600, lock.LockedUntil)

	// wrong holder -> lock lost
	err = RenewSystemTaskLock(task.TaskID, "other-runner", now+900)
	assert.ErrorIs(t, err, ErrSystemTaskLockLost)
}

func TestFinishSystemTask(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()
	_, ok, err := ClaimSystemTask(task.ID, tt, "runner-A", now+300)
	require.NoError(t, err)
	require.True(t, ok)

	// finish -> status succeeded, active_key cleared, lock released
	require.NoError(t, FinishSystemTask(task.TaskID, "runner-A", SystemTaskStatusSucceeded, map[string]any{"done": true}, ""))
	reload, _ := GetSystemTaskByTaskID(task.TaskID)
	assert.Equal(t, SystemTaskStatusSucceeded, reload.Status)
	assert.Nil(t, reload.ActiveKey)

	var count int64
	DB.Model(&SystemTaskLock{}).Where("type = ?", tt).Count(&count)
	assert.EqualValues(t, 0, count, "lock released")

	// finishing again -> no longer running -> lock lost sentinel
	err = FinishSystemTask(task.TaskID, "runner-A", SystemTaskStatusSucceeded, nil, "")
	assert.ErrorIs(t, err, ErrSystemTaskLockLost)
}

func TestMarkSystemTaskLeaseExpired(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()
	_, ok, err := ClaimSystemTask(task.ID, tt, "runner-A", now+300)
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, MarkSystemTaskLeaseExpired(task.TaskID))
	reload, _ := GetSystemTaskByTaskID(task.TaskID)
	assert.Equal(t, SystemTaskStatusFailed, reload.Status)
	assert.Nil(t, reload.ActiveKey)
	assert.Equal(t, "task lease expired", reload.Error)
}

func TestReleaseSystemTaskLock(t *testing.T) {
	requireDB(t)
	tt := uniqTaskType()
	cleanupSystemTaskType(t, tt)
	now := common.GetTimestamp()
	ok, _, err := acquireSystemTaskLock(tt, "task-rel", "runnerA", now, now+300)
	require.NoError(t, err)
	require.True(t, ok)

	// wrong holder -> nothing deleted (no error)
	require.NoError(t, ReleaseSystemTaskLock("task-rel", "wrong"))
	var count int64
	DB.Model(&SystemTaskLock{}).Where("type = ?", tt).Count(&count)
	assert.EqualValues(t, 1, count)

	// correct holder -> deleted
	require.NoError(t, ReleaseSystemTaskLock("task-rel", "runnerA"))
	DB.Model(&SystemTaskLock{}).Where("type = ?", tt).Count(&count)
	assert.EqualValues(t, 0, count)
}

func TestExpireStaleSystemTaskLocks(t *testing.T) {
	requireDB(t)
	tt := uniqTaskType()
	cleanupSystemTaskType(t, tt)
	now := common.GetTimestamp()

	// a running task holding an expired lock
	taskID := "systask_stale_" + strings.ReplaceAll(uniq("s"), "_", "")
	task := &SystemTask{TaskID: taskID, Type: tt, Status: SystemTaskStatusRunning, CreatedAt: now - 1000, UpdatedAt: now - 1000}
	require.NoError(t, DB.Create(task).Error)
	lock := &SystemTaskLock{Type: tt, TaskID: taskID, LockedBy: "runnerA", LockedUntil: now - 100, UpdatedAt: now - 100}
	require.NoError(t, DB.Create(lock).Error)

	require.NoError(t, ExpireStaleSystemTaskLocks(now))

	// task marked failed, our lock removed
	reload, _ := GetSystemTaskByTaskID(taskID)
	assert.Equal(t, SystemTaskStatusFailed, reload.Status)
	var count int64
	DB.Model(&SystemTaskLock{}).Where("type = ?", tt).Count(&count)
	assert.EqualValues(t, 0, count)
}

// Concurrency: only one runner claims a single pending system task.
func TestClaimSystemTask_ConcurrentSingleWinner(t *testing.T) {
	tt := uniqTaskType()
	task := newPendingSystemTask(t, tt)
	now := common.GetTimestamp()

	const n = 10
	var wins int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			runner := "runner-" + strconv.Itoa(i)
			claimed, ok, err := ClaimSystemTask(task.ID, tt, runner, now+300)
			if err == nil && ok && claimed != nil {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, atomic.LoadInt32(&wins), "exactly one runner claims the task")
}
