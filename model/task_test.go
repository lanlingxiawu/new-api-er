package model

import (
	"database/sql/driver"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	commonRelay "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Local fixture: an async Task row scoped by a unique UserId per test.
// Tasks have no FK constraints, so we do not need a real user row.
// ---------------------------------------------------------------------------

func mkTask(t *testing.T, mut func(tk *Task)) *Task {
	t.Helper()
	requireDB(t)
	now := time.Now().Unix()
	tk := &Task{
		ID:         int64(nextTestID()),
		CreatedAt:  now,
		UpdatedAt:  now,
		TaskID:     uniq("task"),
		Platform:   constant.TaskPlatformSuno,
		UserId:     nextTestID(),
		ChannelId:  nextTestID(),
		Action:     "generate",
		Status:     TaskStatusSubmitted,
		Progress:   "0%",
		SubmitTime: now,
		Quota:      100,
	}
	if mut != nil {
		mut(tk)
	}
	require.NoError(t, tk.Insert())
	deleteByID(t, &Task{}, int(tk.ID))
	return tk
}

// ---------------------------------------------------------------------------
// Pure logic
// ---------------------------------------------------------------------------

func TestTaskStatus_ToVideoStatus(t *testing.T) {
	cases := []struct {
		in   TaskStatus
		want string
	}{
		{TaskStatusQueued, dto.VideoStatusQueued},
		{TaskStatusSubmitted, dto.VideoStatusQueued},
		{TaskStatusInProgress, dto.VideoStatusInProgress},
		{TaskStatusSuccess, dto.VideoStatusCompleted},
		{TaskStatusFailure, dto.VideoStatusFailed},
		{TaskStatusNotStart, dto.VideoStatusUnknown}, // NOT_START hits default
		{TaskStatus("whatever"), dto.VideoStatusUnknown},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, c.in.ToVideoStatus(), "ToVideoStatus(%q)", string(c.in))
	}
}

func TestTask_SetGetData(t *testing.T) {
	tk := &Task{}
	tk.SetData(map[string]any{"k": "v", "n": float64(3)})
	require.NotEmpty(t, tk.Data)

	var out map[string]any
	require.NoError(t, tk.GetData(&out))
	assert.Equal(t, "v", out["k"])
	assert.Equal(t, float64(3), out["n"])
}

func TestProperties_ScanValue(t *testing.T) {
	// Value() on the zero value yields nil (stored as NULL)
	v, err := (Properties{}).Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	// Value() on a populated struct yields JSON bytes
	p := Properties{Input: "hi", UpstreamModelName: "up", OriginModelName: "orig"}
	v, err = p.Value()
	require.NoError(t, err)
	b, ok := v.([]byte)
	require.True(t, ok)

	// Scan round-trips
	var got Properties
	require.NoError(t, got.Scan(b))
	assert.Equal(t, p, got)

	// Scan on empty bytes resets to zero
	got = Properties{Input: "dirty"}
	require.NoError(t, got.Scan([]byte{}))
	assert.Equal(t, Properties{}, got)

	// Scan on nil (non-[]byte) also resets to zero
	got = Properties{Input: "dirty"}
	require.NoError(t, got.Scan(nil))
	assert.Equal(t, Properties{}, got)
}

func TestTaskPrivateData_ScanValue(t *testing.T) {
	// zero -> nil
	v, err := (TaskPrivateData{}).Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	pd := TaskPrivateData{Key: "sk", UpstreamTaskID: "up-1", ResultURL: "http://x", TokenId: 7}
	v, err = pd.Value()
	require.NoError(t, err)
	b, ok := v.([]byte)
	require.True(t, ok)

	var got TaskPrivateData
	require.NoError(t, got.Scan(b))
	assert.Equal(t, "sk", got.Key)
	assert.Equal(t, "up-1", got.UpstreamTaskID)
	assert.Equal(t, "http://x", got.ResultURL)
	assert.Equal(t, 7, got.TokenId)

	// empty bytes -> Scan leaves the receiver untouched (no reset in source)
	var untouched TaskPrivateData
	require.NoError(t, untouched.Scan([]byte{}))
	assert.Equal(t, TaskPrivateData{}, untouched)
}

func TestTask_GetUpstreamTaskID(t *testing.T) {
	tk := &Task{TaskID: "public-id"}
	// no UpstreamTaskID -> falls back to TaskID
	assert.Equal(t, "public-id", tk.GetUpstreamTaskID())
	// with UpstreamTaskID -> returns it
	tk.PrivateData.UpstreamTaskID = "upstream-id"
	assert.Equal(t, "upstream-id", tk.GetUpstreamTaskID())
}

func TestTask_GetResultURL(t *testing.T) {
	tk := &Task{FailReason: "http://legacy"}
	// no ResultURL -> legacy fallback to FailReason
	assert.Equal(t, "http://legacy", tk.GetResultURL())
	tk.PrivateData.ResultURL = "http://new"
	assert.Equal(t, "http://new", tk.GetResultURL())
}

func TestGenerateTaskID(t *testing.T) {
	id := GenerateTaskID()
	assert.True(t, strings.HasPrefix(id, "task_"))
	assert.Greater(t, len(id), len("task_"))
	// distinct across calls
	assert.NotEqual(t, id, GenerateTaskID())
}

func TestTask_SnapshotEqual(t *testing.T) {
	tk := &Task{
		Status:     TaskStatusInProgress,
		Progress:   "40%",
		StartTime:  10,
		FinishTime: 0,
		FailReason: "",
		Data:       json.RawMessage(`{"a":1}`),
	}
	tk.PrivateData.ResultURL = "http://r"

	s1 := tk.Snapshot()
	assert.True(t, s1.Equal(tk.Snapshot()), "snapshot equal to itself")

	// change progress -> not equal
	tk.Progress = "50%"
	assert.False(t, s1.Equal(tk.Snapshot()))

	// each field independently breaks equality (condition coverage)
	base := &Task{Status: TaskStatusSuccess, Progress: "100%", StartTime: 1, FinishTime: 2, FailReason: "f", Data: json.RawMessage(`{}`)}
	base.PrivateData.ResultURL = "u"
	b := base.Snapshot()
	mutators := []func(*Task){
		func(x *Task) { x.Status = TaskStatusFailure },
		func(x *Task) { x.Progress = "99%" },
		func(x *Task) { x.StartTime = 999 },
		func(x *Task) { x.FinishTime = 999 },
		func(x *Task) { x.FailReason = "other" },
		func(x *Task) { x.PrivateData.ResultURL = "other" },
		func(x *Task) { x.Data = json.RawMessage(`{"z":9}`) },
	}
	for i, m := range mutators {
		cp := *base
		cp.PrivateData = base.PrivateData
		m(&cp)
		assert.Falsef(t, b.Equal(cp.Snapshot()), "mutator %d should break equality", i)
	}
}

func TestTask_ToOpenAIVideo(t *testing.T) {
	tk := &Task{
		TaskID:    "vid-1",
		Status:    TaskStatusSuccess,
		Progress:  "75%",
		CreatedAt: 111,
		UpdatedAt: 222,
	}
	tk.Properties.OriginModelName = "sora"
	tk.PrivateData.ResultURL = "http://video"

	v := tk.ToOpenAIVideo()
	assert.Equal(t, "vid-1", v.ID)
	assert.Equal(t, dto.VideoStatusCompleted, v.Status)
	assert.Equal(t, "sora", v.Model)
	assert.Equal(t, 75, v.Progress)
	assert.EqualValues(t, 111, v.CreatedAt)
	assert.EqualValues(t, 222, v.CompletedAt)
	assert.Equal(t, "http://video", v.Metadata["url"])
}

func TestInitTask(t *testing.T) {
	t.Run("gemini captures api key, generates task id", func(t *testing.T) {
		info := &commonRelay.RelayInfo{
			UserId:          42,
			UsingGroup:      "grp-x",
			OriginModelName: "origin-model",
		}
		info.ChannelMeta = &commonRelay.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			ChannelId:         7,
			ApiKey:            "secret-key",
			UpstreamModelName: "upstream-model",
		}
		tk := InitTask(constant.TaskPlatformSuno, info)
		assert.Equal(t, 42, tk.UserId)
		assert.Equal(t, "grp-x", tk.Group)
		assert.Equal(t, 7, tk.ChannelId)
		assert.Equal(t, constant.TaskPlatformSuno, tk.Platform)
		assert.Equal(t, TaskStatus(TaskStatusNotStart), tk.Status)
		assert.Equal(t, "0%", tk.Progress)
		assert.True(t, strings.HasPrefix(tk.TaskID, "task_"))
		assert.Equal(t, "secret-key", tk.PrivateData.Key)
		assert.Equal(t, "upstream-model", tk.Properties.UpstreamModelName)
		assert.Equal(t, "origin-model", tk.Properties.OriginModelName)
	})

	t.Run("non-gemini channel does not capture key, honors pre-generated public id", func(t *testing.T) {
		info := &commonRelay.RelayInfo{UserId: 1}
		info.ChannelMeta = &commonRelay.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "nope"}
		info.TaskRelayInfo = &commonRelay.TaskRelayInfo{PublicTaskID: "task_pre_generated"}
		tk := InitTask(constant.TaskPlatformMidjourney, info)
		assert.Empty(t, tk.PrivateData.Key)
		assert.Equal(t, "task_pre_generated", tk.TaskID)
	})

	t.Run("channel meta present but empty model names", func(t *testing.T) {
		// NOTE: InitTask REQUIRES ChannelMeta != nil. Although task.go:177 guards
		// `relayInfo.ChannelMeta != nil` before capturing the key, task.go:205
		// unconditionally reads the promoted `relayInfo.ChannelId` (a *ChannelMeta
		// field), so a nil ChannelMeta panics regardless. See report BUG note.
		info := &commonRelay.RelayInfo{UserId: 2}
		info.ChannelMeta = &commonRelay.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}
		tk := InitTask(constant.TaskPlatformSuno, info)
		assert.Empty(t, tk.PrivateData.Key)
		assert.Empty(t, tk.Properties.UpstreamModelName)
		assert.Empty(t, tk.Properties.OriginModelName)
		assert.True(t, strings.HasPrefix(tk.TaskID, "task_"))
	})
}

// sanity: TaskPrivateData.Value implements driver.Valuer
var _ driver.Valuer = TaskPrivateData{}

// ---------------------------------------------------------------------------
// DB: insert / get
// ---------------------------------------------------------------------------

func TestTask_InsertAndGet(t *testing.T) {
	uid := nextTestID()
	tk := mkTask(t, func(tk *Task) {
		tk.UserId = uid
		tk.Quota = 4242
	})

	// GetByOnlyTaskId
	got, exist, err := GetByOnlyTaskId(tk.TaskID)
	require.NoError(t, err)
	require.True(t, exist)
	assert.Equal(t, tk.ID, got.ID)
	assert.Equal(t, 4242, got.Quota)

	// empty task id short-circuits (no error, not exist)
	got, exist, err = GetByOnlyTaskId("")
	require.NoError(t, err)
	assert.False(t, exist)
	assert.Nil(t, got)

	// missing task id -> not exist, no error
	_, exist, err = GetByOnlyTaskId("missing-task-zzz")
	require.NoError(t, err)
	assert.False(t, exist)

	// GetByTaskId scoped to user
	got, exist, err = GetByTaskId(uid, tk.TaskID)
	require.NoError(t, err)
	require.True(t, exist)
	assert.Equal(t, tk.ID, got.ID)

	// wrong user -> not exist
	_, exist, err = GetByTaskId(uid+999999, tk.TaskID)
	require.NoError(t, err)
	assert.False(t, exist)

	// empty task id guard
	_, exist, err = GetByTaskId(uid, "")
	require.NoError(t, err)
	assert.False(t, exist)
}

func TestTask_GetByTaskIds(t *testing.T) {
	uid := nextTestID()
	a := mkTask(t, func(tk *Task) { tk.UserId = uid })
	b := mkTask(t, func(tk *Task) { tk.UserId = uid })

	got, err := GetByTaskIds(uid, []any{a.TaskID, b.TaskID})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// empty ids -> nil, nil
	got, err = GetByTaskIds(uid, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ---------------------------------------------------------------------------
// DB: listing / filters / pagination / counting
// ---------------------------------------------------------------------------

func TestTaskGetAllUserTask_PaginationAndFilters(t *testing.T) {
	uid := nextTestID()
	var created []*Task
	for i := 0; i < 3; i++ {
		i := i
		created = append(created, mkTask(t, func(tk *Task) {
			tk.UserId = uid
			tk.SubmitTime = int64(1000 + i)
			tk.Action = "generate"
			tk.Status = TaskStatusSubmitted
		}))
	}

	// pagination: page size 2, ordered id desc
	page1 := TaskGetAllUserTask(uid, 0, 2, SyncTaskQueryParams{})
	assert.Len(t, page1, 2)
	page2 := TaskGetAllUserTask(uid, 2, 2, SyncTaskQueryParams{})
	assert.Len(t, page2, 1)
	assert.Greater(t, page1[0].ID, page2[0].ID)
	// channel_id is omitted from this query
	assert.EqualValues(t, 0, page1[0].ChannelId)

	// task id filter
	single := TaskGetAllUserTask(uid, 0, 10, SyncTaskQueryParams{TaskID: created[0].TaskID})
	require.Len(t, single, 1)
	assert.Equal(t, created[0].ID, single[0].ID)

	// action + status + platform filters
	res := TaskGetAllUserTask(uid, 0, 10, SyncTaskQueryParams{Action: "generate", Status: string(TaskStatusSubmitted), Platform: constant.TaskPlatformSuno})
	assert.Len(t, res, 3)

	// time-range filters (submit_time 1000..1002)
	res = TaskGetAllUserTask(uid, 0, 10, SyncTaskQueryParams{StartTimestamp: 1002})
	assert.Len(t, res, 1)
	res = TaskGetAllUserTask(uid, 0, 10, SyncTaskQueryParams{EndTimestamp: 1000})
	assert.Len(t, res, 1)
	res = TaskGetAllUserTask(uid, 0, 10, SyncTaskQueryParams{StartTimestamp: 1000, EndTimestamp: 1002})
	assert.Len(t, res, 3)

	// counts
	assert.EqualValues(t, 3, TaskCountAllUserTask(uid, SyncTaskQueryParams{}))
	assert.EqualValues(t, 1, TaskCountAllUserTask(uid, SyncTaskQueryParams{TaskID: created[0].TaskID}))
	assert.EqualValues(t, 3, TaskCountAllUserTask(uid, SyncTaskQueryParams{Action: "generate", Status: string(TaskStatusSubmitted), Platform: constant.TaskPlatformSuno}))
	assert.EqualValues(t, 1, TaskCountAllUserTask(uid, SyncTaskQueryParams{StartTimestamp: 1002}))
	assert.EqualValues(t, 1, TaskCountAllUserTask(uid, SyncTaskQueryParams{EndTimestamp: 1000}))
}

func TestTaskGetAllTasks_AdminFilters(t *testing.T) {
	uid := nextTestID()
	chID := nextTestID()
	for i := 0; i < 2; i++ {
		i := i
		mkTask(t, func(tk *Task) {
			tk.UserId = uid
			tk.ChannelId = chID
			tk.SubmitTime = int64(2000 + i)
			tk.Action = "song"
			tk.Status = TaskStatusInProgress
		})
	}

	// scope by our unique user id
	res := TaskGetAllTasks(0, 10, SyncTaskQueryParams{UserID: itoa(uid)})
	assert.Len(t, res, 2)

	// UserIDs slice
	res = TaskGetAllTasks(0, 10, SyncTaskQueryParams{UserIDs: []int{uid}})
	assert.Len(t, res, 2)

	// channel id filter + action + status
	res = TaskGetAllTasks(0, 10, SyncTaskQueryParams{ChannelID: itoa(chID), Action: "song", Status: string(TaskStatusInProgress), Platform: constant.TaskPlatformSuno})
	assert.Len(t, res, 2)

	// time-range + count
	res = TaskGetAllTasks(0, 10, SyncTaskQueryParams{UserID: itoa(uid), StartTimestamp: 2001})
	assert.Len(t, res, 1)
	assert.EqualValues(t, 2, TaskCountAllTasks(SyncTaskQueryParams{UserID: itoa(uid)}))
	assert.EqualValues(t, 2, TaskCountAllTasks(SyncTaskQueryParams{UserIDs: []int{uid}}))
	assert.EqualValues(t, 2, TaskCountAllTasks(SyncTaskQueryParams{ChannelID: itoa(chID)}))
	assert.EqualValues(t, 1, TaskCountAllTasks(SyncTaskQueryParams{UserID: itoa(uid), EndTimestamp: 2000}))
}

func TestTask_UnfinishedQueries(t *testing.T) {
	uid := nextTestID()
	// an unfinished task (progress != 100%, status submitted, old submit_time)
	unfinished := mkTask(t, func(tk *Task) {
		tk.UserId = uid
		tk.Progress = "30%"
		tk.Status = TaskStatusSubmitted
		tk.SubmitTime = 500
	})
	// a finished task
	mkTask(t, func(tk *Task) {
		tk.UserId = uid
		tk.Progress = "100%"
		tk.Status = TaskStatusSuccess
		tk.SubmitTime = 500
	})

	// HasUnfinishedSyncTasks is a global existence check -> true because ours exists
	assert.True(t, HasUnfinishedSyncTasks())

	// GetAllUnFinishSyncTasks contains our unfinished, not the finished one
	all := GetAllUnFinishSyncTasks(10000)
	assert.True(t, containsTaskID(all, unfinished.TaskID))

	// GetTimedOutUnfinishedTasks: cutoff after submit_time returns it
	timedOut := GetTimedOutUnfinishedTasks(1000, 10000)
	assert.True(t, containsTaskID(timedOut, unfinished.TaskID))
	// cutoff before submit_time excludes it
	none := GetTimedOutUnfinishedTasks(400, 10000)
	assert.False(t, containsTaskID(none, unfinished.TaskID))
}

// ---------------------------------------------------------------------------
// DB: updates
// ---------------------------------------------------------------------------

func TestTask_UpdateAndUpdateQuota(t *testing.T) {
	tk := mkTask(t, nil)

	tk.Progress = "100%"
	tk.Status = TaskStatusSuccess
	require.NoError(t, tk.Update())

	got, _, _ := GetByOnlyTaskId(tk.TaskID)
	assert.Equal(t, "100%", got.Progress)
	assert.Equal(t, TaskStatus(TaskStatusSuccess), got.Status)

	tk.Quota = 9999
	require.NoError(t, tk.UpdateQuota())
	got, _, _ = GetByOnlyTaskId(tk.TaskID)
	assert.Equal(t, 9999, got.Quota)
}

func TestTask_BulkUpdate(t *testing.T) {
	uid := nextTestID()
	a := mkTask(t, func(tk *Task) { tk.UserId = uid })
	b := mkTask(t, func(tk *Task) { tk.UserId = uid })

	// empty inputs are no-ops
	require.NoError(t, TaskBulkUpdate(nil, map[string]any{"status": "x"}))
	require.NoError(t, TaskBulkUpdateByID(nil, map[string]any{"status": "x"}))

	// bulk update by task_id string
	require.NoError(t, TaskBulkUpdate([]string{a.TaskID, b.TaskID}, map[string]any{"status": string(TaskStatusFailure)}))
	got, _, _ := GetByOnlyTaskId(a.TaskID)
	assert.Equal(t, TaskStatus(TaskStatusFailure), got.Status)

	// bulk update by primary key id
	require.NoError(t, TaskBulkUpdateByID([]int64{a.ID, b.ID}, map[string]any{"progress": "100%"}))
	got, _, _ = GetByOnlyTaskId(b.TaskID)
	assert.Equal(t, "100%", got.Progress)
}

func TestTask_UpdateWithStatus_CAS(t *testing.T) {
	tk := mkTask(t, func(tk *Task) { tk.Status = TaskStatusSubmitted })

	// wrong fromStatus -> no rows changed, returns false
	loser := &Task{ID: tk.ID, Status: TaskStatusSuccess}
	won, err := loser.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	assert.False(t, won)

	// correct fromStatus -> wins
	winner := &Task{ID: tk.ID, Status: TaskStatusSuccess, Progress: "100%"}
	won, err = winner.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	assert.True(t, won)

	// status already moved; a second CAS on SUBMITTED must lose
	again := &Task{ID: tk.ID, Status: TaskStatusFailure}
	won, err = again.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	assert.False(t, won)

	// Reload by primary key: Select("*").Updates blanks non-PK columns (incl.
	// task_id) of the partial candidate struct, so a by-task_id lookup would miss.
	var got Task
	require.NoError(t, DB.First(&got, tk.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusSuccess), got.Status)
}

// Concurrency: exactly one goroutine wins the CAS from SUBMITTED -> SUCCESS.
func TestTask_UpdateWithStatus_ConcurrentSingleWinner(t *testing.T) {
	tk := mkTask(t, func(tk *Task) { tk.Status = TaskStatusSubmitted })

	const n = 12
	var wins int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cand := &Task{ID: tk.ID, Status: TaskStatusSuccess, Progress: "100%"}
			won, err := cand.UpdateWithStatus(TaskStatusSubmitted)
			if err == nil && won {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, atomic.LoadInt32(&wins), "exactly one CAS winner")
	var got Task
	require.NoError(t, DB.First(&got, tk.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusSuccess), got.Status)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsTaskID(tasks []*Task, id string) bool {
	for _, tk := range tasks {
		if tk.TaskID == id {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
