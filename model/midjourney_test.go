package model

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Local fixture: a Midjourney task row. Ids are set explicitly from the test id
// range; UserId is unique per test to isolate rows on the shared table.
// ---------------------------------------------------------------------------

func mkMidjourney(t *testing.T, mut func(mj *Midjourney)) *Midjourney {
	t.Helper()
	requireDB(t)
	now := time.Now().Unix()
	mj := &Midjourney{
		Id:         nextTestID(),
		UserId:     nextTestID(),
		ChannelId:  nextTestID(),
		Action:     "IMAGINE",
		MjId:       uniq("mj"),
		Status:     "SUBMITTED",
		Progress:   "0%",
		SubmitTime: now,
		Quota:      100,
	}
	if mut != nil {
		mut(mj)
	}
	require.NoError(t, mj.Insert())
	deleteByID(t, &Midjourney{}, mj.Id)
	return mj
}

func containsMjId(list []*Midjourney, mjId string) bool {
	for _, m := range list {
		if m.MjId == mjId {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// DB: insert / lookup
// ---------------------------------------------------------------------------

func TestMidjourney_InsertAndGet(t *testing.T) {
	uid := nextTestID()
	mj := mkMidjourney(t, func(mj *Midjourney) {
		mj.UserId = uid
		mj.Quota = 3333
	})

	// GetMjByuId (by primary key)
	got := GetMjByuId(mj.Id)
	require.NotNil(t, got)
	assert.Equal(t, mj.MjId, got.MjId)
	assert.Equal(t, 3333, got.Quota)

	// missing id -> nil
	assert.Nil(t, GetMjByuId(mj.Id+123456789))

	// GetByOnlyMJId
	got = GetByOnlyMJId(mj.MjId)
	require.NotNil(t, got)
	assert.Equal(t, mj.Id, got.Id)
	assert.Nil(t, GetByOnlyMJId("missing-mj-zzz"))

	// GetByMJId scoped to user
	got = GetByMJId(uid, mj.MjId)
	require.NotNil(t, got)
	assert.Equal(t, mj.Id, got.Id)
	// wrong user -> nil
	assert.Nil(t, GetByMJId(uid+999999, mj.MjId))
}

func TestMidjourney_GetByMJIds(t *testing.T) {
	uid := nextTestID()
	a := mkMidjourney(t, func(mj *Midjourney) { mj.UserId = uid })
	b := mkMidjourney(t, func(mj *Midjourney) { mj.UserId = uid })

	got := GetByMJIds(uid, []string{a.MjId, b.MjId})
	assert.Len(t, got, 2)

	// wrong user -> empty
	got = GetByMJIds(uid+999999, []string{a.MjId})
	assert.Empty(t, got)
}

// ---------------------------------------------------------------------------
// DB: listing / filters / pagination / counting
// ---------------------------------------------------------------------------

func TestGetAllUserTask_Midjourney(t *testing.T) {
	uid := nextTestID()
	var created []*Midjourney
	for i := 0; i < 3; i++ {
		i := i
		created = append(created, mkMidjourney(t, func(mj *Midjourney) {
			mj.UserId = uid
			mj.SubmitTime = int64(1000 + i)
		}))
	}

	// pagination ordered id desc
	page1 := GetAllUserTask(uid, 0, 2, TaskQueryParams{})
	assert.Len(t, page1, 2)
	page2 := GetAllUserTask(uid, 2, 2, TaskQueryParams{})
	assert.Len(t, page2, 1)
	assert.Greater(t, page1[0].Id, page2[0].Id)

	// mj id filter
	single := GetAllUserTask(uid, 0, 10, TaskQueryParams{MjID: created[0].MjId})
	require.Len(t, single, 1)
	assert.Equal(t, created[0].Id, single[0].Id)

	// time-range filters (submit_time 1000..1002)
	res := GetAllUserTask(uid, 0, 10, TaskQueryParams{StartTimestamp: "1002"})
	assert.Len(t, res, 1)
	res = GetAllUserTask(uid, 0, 10, TaskQueryParams{EndTimestamp: "1000"})
	assert.Len(t, res, 1)

	// counts
	assert.EqualValues(t, 3, CountAllUserTask(uid, TaskQueryParams{}))
	assert.EqualValues(t, 1, CountAllUserTask(uid, TaskQueryParams{MjID: created[0].MjId}))
	assert.EqualValues(t, 1, CountAllUserTask(uid, TaskQueryParams{StartTimestamp: "1002"}))
	assert.EqualValues(t, 1, CountAllUserTask(uid, TaskQueryParams{EndTimestamp: "1000"}))
}

func TestGetAllTasks_Midjourney(t *testing.T) {
	uid := nextTestID()
	chID := nextTestID()
	for i := 0; i < 2; i++ {
		i := i
		mkMidjourney(t, func(mj *Midjourney) {
			mj.UserId = uid
			mj.ChannelId = chID
			mj.SubmitTime = int64(2000 + i)
		})
	}

	// channel id filter isolates our rows
	res := GetAllTasks(0, 10, TaskQueryParams{ChannelID: strconv.Itoa(chID)})
	assert.Len(t, res, 2)

	// channel + time range
	res = GetAllTasks(0, 10, TaskQueryParams{ChannelID: strconv.Itoa(chID), StartTimestamp: "2001"})
	assert.Len(t, res, 1)
	res = GetAllTasks(0, 10, TaskQueryParams{ChannelID: strconv.Itoa(chID), EndTimestamp: "2000"})
	assert.Len(t, res, 1)

	// counts
	assert.EqualValues(t, 2, CountAllTasks(TaskQueryParams{ChannelID: strconv.Itoa(chID)}))
	assert.EqualValues(t, 1, CountAllTasks(TaskQueryParams{ChannelID: strconv.Itoa(chID), StartTimestamp: "2001"}))
}

func TestMidjourney_UnfinishedQueries(t *testing.T) {
	uid := nextTestID()
	unfinished := mkMidjourney(t, func(mj *Midjourney) {
		mj.UserId = uid
		mj.Progress = "50%"
	})
	mkMidjourney(t, func(mj *Midjourney) {
		mj.UserId = uid
		mj.Progress = "100%"
	})

	// existence check is global -> true because ours exists
	assert.True(t, HasUnfinishedMidjourneyTasks())

	all := GetAllUnFinishTasks()
	assert.True(t, containsMjId(all, unfinished.MjId))
}

// ---------------------------------------------------------------------------
// DB: updates
// ---------------------------------------------------------------------------

func TestMidjourney_UpdateAndProgress(t *testing.T) {
	mj := mkMidjourney(t, nil)

	mj.Status = "SUCCESS"
	mj.ImageUrl = "http://img"
	require.NoError(t, mj.Update())
	got := GetByOnlyMJId(mj.MjId)
	assert.Equal(t, "SUCCESS", got.Status)
	assert.Equal(t, "http://img", got.ImageUrl)

	require.NoError(t, UpdateProgress(mj.Id, "100%"))
	got = GetByOnlyMJId(mj.MjId)
	assert.Equal(t, "100%", got.Progress)
}

func TestMidjourney_BulkUpdate(t *testing.T) {
	uid := nextTestID()
	a := mkMidjourney(t, func(mj *Midjourney) { mj.UserId = uid })
	b := mkMidjourney(t, func(mj *Midjourney) { mj.UserId = uid })

	require.NoError(t, MjBulkUpdate([]string{a.MjId, b.MjId}, map[string]any{"status": "FAILURE"}))
	got := GetByOnlyMJId(a.MjId)
	assert.Equal(t, "FAILURE", got.Status)

	require.NoError(t, MjBulkUpdateByTaskIds([]int{a.Id, b.Id}, map[string]any{"progress": "100%"}))
	got = GetByOnlyMJId(b.MjId)
	assert.Equal(t, "100%", got.Progress)
}

func TestMidjourney_UpdateWithStatus_CAS(t *testing.T) {
	mj := mkMidjourney(t, func(mj *Midjourney) { mj.Status = "SUBMITTED" })

	// wrong fromStatus -> loses
	loser := &Midjourney{Id: mj.Id, Status: "SUCCESS"}
	won, err := loser.UpdateWithStatus("IN_PROGRESS")
	require.NoError(t, err)
	assert.False(t, won)

	// correct fromStatus -> wins
	winner := &Midjourney{Id: mj.Id, Status: "SUCCESS", Progress: "100%"}
	won, err = winner.UpdateWithStatus("SUBMITTED")
	require.NoError(t, err)
	assert.True(t, won)

	// already moved -> second CAS on SUBMITTED loses
	again := &Midjourney{Id: mj.Id, Status: "FAILURE"}
	won, err = again.UpdateWithStatus("SUBMITTED")
	require.NoError(t, err)
	assert.False(t, won)

	// Reload by primary key: Select("*").Updates blanks non-PK columns (incl.
	// mj_id) of the partial candidate struct, so a by-name lookup would miss.
	got := GetMjByuId(mj.Id)
	require.NotNil(t, got)
	assert.Equal(t, "SUCCESS", got.Status)
}

// Concurrency: exactly one goroutine wins the CAS from SUBMITTED -> SUCCESS.
func TestMidjourney_UpdateWithStatus_ConcurrentSingleWinner(t *testing.T) {
	mj := mkMidjourney(t, func(mj *Midjourney) { mj.Status = "SUBMITTED" })

	const n = 12
	var wins int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cand := &Midjourney{Id: mj.Id, Status: "SUCCESS", Progress: "100%"}
			won, err := cand.UpdateWithStatus("SUBMITTED")
			if err == nil && won {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, atomic.LoadInt32(&wins), "exactly one CAS winner")
	got := GetMjByuId(mj.Id)
	require.NotNil(t, got)
	assert.Equal(t, "SUCCESS", got.Status)
}
