package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// request_log.go stores relay request logs in Redis (primary) or an in-process
// memory slice (fallback when Redis is disabled). No DB is involved. Tests must
// isolate the shared global memory slice / Redis keys; both are cleaned up.
// ---------------------------------------------------------------------------

// requestLogResetMemory clears the shared in-memory slice + seq so a test starts
// from a known-empty state and does not leak rows into sibling tests.
func requestLogResetMemory(t *testing.T) {
	t.Helper()
	memRequestLogMu.Lock()
	memRequestLogs = nil
	memRequestSeq = 0
	memRequestLogMu.Unlock()
	t.Cleanup(func() {
		memRequestLogMu.Lock()
		memRequestLogs = nil
		memRequestSeq = 0
		memRequestLogMu.Unlock()
	})
}

// requestLogWithLimits temporarily overrides the global min/max cleanup limits.
func requestLogWithLimits(t *testing.T, minC, maxC int) {
	t.Helper()
	prevMin, prevMax := common.RequestLogMinCount, common.RequestLogMaxCount
	common.RequestLogMinCount = minC
	common.RequestLogMaxCount = maxC
	t.Cleanup(func() {
		common.RequestLogMinCount = prevMin
		common.RequestLogMaxCount = prevMax
	})
}

// ---------------------------------------------------------------------------
// Pure logic: effectiveRequestLogLimits normalisation
// ---------------------------------------------------------------------------

func TestEffectiveRequestLogLimits(t *testing.T) {
	cases := []struct {
		name             string
		minC, maxC       int
		wantMax, wantMin int
	}{
		// normal, well-ordered config passes through unchanged
		{"normal", 1000, 5000, 5000, 1000},
		// maxCount <= 0 -> default max, min(0) stays 0 (< default max)
		{"max zero -> default", 0, 0, defaultRequestLogMaxCount, 0},
		{"max negative -> default", 10, -5, defaultRequestLogMaxCount, 10},
		// minCount < 0 -> clamped to 0
		{"min negative -> 0", -100, 200, 200, 0},
		// minCount >= maxCount -> retain half of max
		{"min == max", 200, 200, 200, 100},
		{"min > max", 400, 200, 200, 100},
		// boundary: min just below max stays
		{"min just below max", 199, 200, 200, 199},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requestLogWithLimits(t, c.minC, c.maxC)
			gotMax, gotMin := effectiveRequestLogLimits()
			assert.Equal(t, c.wantMax, gotMax, "maxCount")
			assert.Equal(t, c.wantMin, gotMin, "minCount")
			// invariant the function guarantees: 0 <= min < max
			assert.GreaterOrEqual(t, gotMin, 0)
			assert.Less(t, gotMin, gotMax)
		})
	}
}

// ---------------------------------------------------------------------------
// Pure logic: matchRequestLog filter matrix
// ---------------------------------------------------------------------------

func TestMatchRequestLog(t *testing.T) {
	base := &RequestLog{
		Username:   "alice",
		ModelName:  "gpt-4o",
		ChannelId:  7,
		RequestId:  "req-1",
		StatusCode: 200,
		CreatedAt:  1000,
	}
	// no filters -> match
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 0, 0))
	// each filter matches its own value
	assert.True(t, matchRequestLog(base, "alice", "gpt-4o", 7, "req-1", 200, 500, 1500))
	// each filter independently rejects a mismatch (condition coverage)
	assert.False(t, matchRequestLog(base, "bob", "", 0, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "gpt-3", 0, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 8, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 0, "req-2", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 0, "", 500, 0, 0))
	// time range boundaries: created_at=1000
	assert.False(t, matchRequestLog(base, "", "", 0, "", 0, 1001, 0)) // start after
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 1000, 0))  // start == createdAt
	assert.False(t, matchRequestLog(base, "", "", 0, "", 0, 0, 999))  // end before
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 0, 1000))  // end == createdAt
}

func TestCloneRequestLogMeta(t *testing.T) {
	log := &RequestLog{
		Id:              5,
		Username:        "u",
		RequestHeaders:  "h",
		RequestBody:     "b",
		ResponseHeaders: "rh",
		ResponseBody:    "rb",
	}
	m := cloneRequestLogMeta(log)
	assert.Equal(t, 5, m.Id)
	assert.Equal(t, "u", m.Username)
	// big fields stripped in the meta copy
	assert.Empty(t, m.RequestHeaders)
	assert.Empty(t, m.RequestBody)
	assert.Empty(t, m.ResponseHeaders)
	assert.Empty(t, m.ResponseBody)
	// original untouched
	assert.Equal(t, "h", log.RequestHeaders)
}

// ---------------------------------------------------------------------------
// Memory-backed path (Redis disabled)
// ---------------------------------------------------------------------------

func TestRecordRequestLog_Memory(t *testing.T) {
	require.False(t, common.RedisEnabled)
	requestLogResetMemory(t)

	// nil guard is a no-op
	RecordRequestLog(nil)
	memRequestLogMu.Lock()
	assert.Empty(t, memRequestLogs)
	memRequestLogMu.Unlock()

	// CreatedAt=0 gets defaulted; Id assigned; newest at head
	l1 := &RequestLog{Username: "a"}
	RecordRequestLog(l1)
	assert.Equal(t, 1, l1.Id)
	assert.NotZero(t, l1.CreatedAt)

	l2 := &RequestLog{Username: "b", CreatedAt: 42}
	RecordRequestLog(l2)
	assert.Equal(t, 2, l2.Id)
	assert.EqualValues(t, 42, l2.CreatedAt)

	got, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[0].Username) // newest first
}

func TestRecordRequestLogMemory_Trim(t *testing.T) {
	requestLogResetMemory(t)
	requestLogWithLimits(t, 1, 3) // min=1, max=3

	for i := 0; i < 3; i++ {
		RecordRequestLog(&RequestLog{Username: "u"})
	}
	memRequestLogMu.Lock()
	assert.Len(t, memRequestLogs, 3) // exactly at max, no trim yet
	memRequestLogMu.Unlock()

	// 4th record exceeds max -> trim to min (1)
	RecordRequestLog(&RequestLog{Username: "u"})
	memRequestLogMu.Lock()
	assert.Len(t, memRequestLogs, 1)
	memRequestLogMu.Unlock()
}

func TestGetAllRequestLogs_Memory_FilterAndPaging(t *testing.T) {
	requestLogResetMemory(t)
	requestLogWithLimits(t, 1000, 5000)

	RecordRequestLog(&RequestLog{Username: "alice", ModelName: "m1", CreatedAt: 100})
	RecordRequestLog(&RequestLog{Username: "bob", ModelName: "m2", CreatedAt: 200})
	RecordRequestLog(&RequestLog{Username: "alice", ModelName: "m1", CreatedAt: 300})

	// filter by username -> 2 rows, big fields stripped (clone path)
	got, total, err := GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, got, 2)

	// pagination on filtered set: page size 1
	page1, total, err := GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, page1, 1)
	page2, _, err := GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 1, 1)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].CreatedAt, page2[0].CreatedAt)

	// startIdx beyond range -> empty, total still counted
	got, total, err = GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 99, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Empty(t, got)

	// num <= 0 -> empty slice
	got, _, err = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGetRequestLogById_Memory(t *testing.T) {
	requestLogResetMemory(t)
	l := &RequestLog{Username: "z", RequestBody: "body"}
	RecordRequestLog(l)

	got, err := GetRequestLogById(l.Id)
	require.NoError(t, err)
	assert.Equal(t, "z", got.Username)
	assert.Equal(t, "body", got.RequestBody) // full record includes big fields

	_, err = GetRequestLogById(999999)
	assert.ErrorIs(t, err, errRequestLogNotFound)
}

func TestDeleteOldRequestLog_Memory(t *testing.T) {
	requestLogResetMemory(t)
	RecordRequestLog(&RequestLog{Username: "old", CreatedAt: 100})
	RecordRequestLog(&RequestLog{Username: "new", CreatedAt: 500})

	deleted, err := DeleteOldRequestLog(300)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	_, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestClearAllRequestLogs_Memory(t *testing.T) {
	requestLogResetMemory(t)
	RecordRequestLog(&RequestLog{Username: "a"})
	RecordRequestLog(&RequestLog{Username: "b"})

	cleared, err := ClearAllRequestLogs()
	require.NoError(t, err)
	assert.EqualValues(t, 2, cleared)

	_, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

// ---------------------------------------------------------------------------
// Redis-backed path. Uses a DISTINCT key namespace (request_log:*) from the
// relay token cache, so it cannot collide with the main relay chain.
// Each test clears the namespace before and after.
// ---------------------------------------------------------------------------

func requestLogClearRedis(t *testing.T) {
	t.Helper()
	_, _ = ClearAllRequestLogs()
	t.Cleanup(func() { _, _ = ClearAllRequestLogs() })
}

func TestRequestLog_RedisRoundTrip(t *testing.T) {
	enableRedis(t)
	require.True(t, useRedisForRequestLog())
	requestLogClearRedis(t)
	requestLogWithLimits(t, 1000, 5000)

	l := &RequestLog{
		Username:     "ralice",
		ModelName:    "gpt-4o",
		ChannelId:    3,
		RequestId:    "rr-1",
		StatusCode:   200,
		RequestBody:  "reqbody",
		ResponseBody: "respbody",
		CreatedAt:    1000,
	}
	RecordRequestLog(l)
	assert.NotZero(t, l.Id)

	// list (no filter) returns meta only (big fields stripped)
	got, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, "ralice", got[0].Username)
	assert.Empty(t, got[0].RequestBody)

	// detail returns full body
	detail, err := GetRequestLogById(l.Id)
	require.NoError(t, err)
	assert.Equal(t, "reqbody", detail.RequestBody)
	assert.Equal(t, "respbody", detail.ResponseBody)

	// filtered path (username set) exercises the load-all-then-filter branch
	got, total, err = GetAllRequestLogs("ralice", "gpt-4o", 3, "rr-1", 200, 500, 1500, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, got, 1)

	// filter that matches nothing
	_, total, err = GetAllRequestLogs("nobody", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	// missing id -> redis error propagated
	_, err = GetRequestLogById(987654321)
	assert.Error(t, err)
}

func TestRequestLog_RedisPagingNoFilter(t *testing.T) {
	enableRedis(t)
	requestLogClearRedis(t)
	requestLogWithLimits(t, 1000, 5000)

	for i := 0; i < 3; i++ {
		RecordRequestLog(&RequestLog{Username: "rp", CreatedAt: int64(100 + i)})
	}
	// page size 2 then 1
	page1, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, page1, 2)
	page2, _, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 1)

	// startIdx beyond total -> empty
	got, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 99, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Empty(t, got)
}

func TestRequestLog_RedisTrim(t *testing.T) {
	enableRedis(t)
	requestLogClearRedis(t)
	requestLogWithLimits(t, 2, 3) // min=2, max=3

	for i := 0; i < 5; i++ {
		RecordRequestLog(&RequestLog{Username: "rt", CreatedAt: int64(i)})
	}
	// Trim fires only when count EXCEEDS max(3): the 4th insert (count 4>3)
	// trims to min(2); the 5th brings it back to 3 (==max, not over). So the
	// steady-state count sits at max, never unbounded.
	_, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
}

func TestRequestLog_RedisTrimMinZero(t *testing.T) {
	enableRedis(t)
	requestLogClearRedis(t)
	// min=0 forces the "delete index key entirely" branch in trimRequestLogsRedis
	requestLogWithLimits(t, 0, 2)

	for i := 0; i < 4; i++ {
		RecordRequestLog(&RequestLog{Username: "rz", CreatedAt: int64(i)})
	}
	_, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 100)
	require.NoError(t, err)
	// index deleted on the record that crossed max -> only records added after
	// the last trim remain. Assert it never exceeds max (no unbounded growth).
	assert.LessOrEqual(t, total, int64(2))
}

func TestRequestLog_RedisDeleteOldAndClear(t *testing.T) {
	enableRedis(t)
	requestLogClearRedis(t)
	requestLogWithLimits(t, 1000, 5000)

	RecordRequestLog(&RequestLog{Username: "rold", CreatedAt: 100})
	RecordRequestLog(&RequestLog{Username: "rnew", CreatedAt: 500})

	deleted, err := DeleteOldRequestLog(300)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	_, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// DeleteOldRequestLog with nothing matching -> 0 deleted
	deleted, err = DeleteOldRequestLog(1)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)

	cleared, err := ClearAllRequestLogs()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cleared, int64(1))
	_, total, err = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	// clearing an already-empty namespace succeeds (RENAME miss -> SCAN fallback)
	_, err = ClearAllRequestLogs()
	require.NoError(t, err)
}
