package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// usedata.go aggregates per-(user, hour, model) quota usage into the quota_data
// table on the MAIN DB (not LOG_DB). Tests scope every assertion to a unique
// username + unique created_at window so they never read sibling rows, and they
// clean up the shared in-memory CacheQuotaData map + inserted DB rows.
// ---------------------------------------------------------------------------

// usedataResetCache clears the process-global quota cache before and after a
// test so the shared map cannot leak between tests.
func usedataResetCache(t *testing.T) {
	t.Helper()
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	})
}

// usedataUniqTime returns a per-call unique created_at (seconds) so a time-range
// query of [t, t] isolates exactly the rows created by one test.
func usedataUniqTime() int64 {
	return int64(1_900_000_000) + int64(nextTestID())
}

// mkQuotaData inserts one quota_data row on the main DB and registers cleanup.
func mkQuotaData(t *testing.T, mut func(q *QuotaData)) *QuotaData {
	t.Helper()
	requireDB(t)
	q := &QuotaData{
		UserID:    nextTestID(),
		Username:  uniq("qdu"),
		ModelName: uniq("qdm"),
		CreatedAt: usedataUniqTime(),
		UseGroup:  "default",
		Count:     1,
		Quota:     10,
		TokenUsed: 5,
	}
	if mut != nil {
		mut(q)
	}
	require.NoError(t, DB.Table("quota_data").Create(q).Error)
	deleteByID(t, &QuotaData{}, q.Id)
	return q
}

// cleanupQuotaByUsername removes any quota_data rows for a username (used when
// SaveQuotaDataCache inserts rows whose ids the test does not track).
func cleanupQuotaByUsername(t *testing.T, username string) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Table("quota_data").Where("username = ?", username).Delete(&QuotaData{})
		}
	})
}

// ---------------------------------------------------------------------------
// In-memory cache aggregation: logQuotaDataCache / LogQuotaData
// ---------------------------------------------------------------------------

func TestLogQuotaData_CacheAggregation(t *testing.T) {
	usedataResetCache(t)
	uname := uniq("agg")
	model := uniq("aggm")
	uid := nextTestID()

	// two entries with identical key (same hour) aggregate in the cache
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 100, TokenUsed: 10, UseGroup: "g", CreatedAt: 3600})
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 50, TokenUsed: 5, UseGroup: "g", CreatedAt: 3600 + 59})

	CacheQuotaDataLock.Lock()
	var found *QuotaData
	for _, v := range CacheQuotaData {
		if v.Username == uname {
			found = v
		}
	}
	CacheQuotaDataLock.Unlock()
	require.NotNil(t, found)
	assert.Equal(t, 2, found.Count)      // 1 + 1
	assert.Equal(t, 150, found.Quota)    // 100 + 50
	assert.Equal(t, 15, found.TokenUsed) // 10 + 5
	// createdAt truncated to the hour boundary (3600)
	assert.EqualValues(t, 3600, found.CreatedAt)
}

func TestLogQuotaData_HourTruncationSeparatesBuckets(t *testing.T) {
	usedataResetCache(t)
	uname := uniq("hr")
	uid := nextTestID()
	model := uniq("hrm")

	// createdAt in two different hours -> two distinct cache entries
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 1, CreatedAt: 3601})
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 1, CreatedAt: 7201})

	CacheQuotaDataLock.Lock()
	n := 0
	for _, v := range CacheQuotaData {
		if v.Username == uname {
			n++
		}
	}
	CacheQuotaDataLock.Unlock()
	assert.Equal(t, 2, n)
}

// ---------------------------------------------------------------------------
// DB flush: SaveQuotaDataCache (insert) + increaseQuotaData (update path)
// ---------------------------------------------------------------------------

func TestSaveQuotaDataCache_InsertThenIncrease(t *testing.T) {
	requireDB(t)
	usedataResetCache(t)
	uname := uniq("save")
	cleanupQuotaByUsername(t, uname)
	uid := nextTestID()
	model := uniq("savem")
	hour := usedataUniqTime()
	hour = hour - (hour % 3600) // align to hour so query window matches storage

	// first flush -> INSERT branch
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 100, TokenUsed: 10, CreatedAt: hour + 5})
	SaveQuotaDataCache()

	rows, err := GetQuotaDataByUsername(uname, hour, hour+3600)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Count)
	assert.Equal(t, 100, rows[0].Quota)
	assert.Equal(t, 10, rows[0].TokenUsed)

	// second flush with same key -> UPDATE (increaseQuotaData) branch
	LogQuotaData(QuotaDataLogParams{UserID: uid, Username: uname, ModelName: model, Quota: 50, TokenUsed: 5, CreatedAt: hour + 10})
	SaveQuotaDataCache()

	rows, err = GetQuotaDataByUsername(uname, hour, hour+3600)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Count)      // 1 + 1
	assert.Equal(t, 150, rows[0].Quota)    // 100 + 50
	assert.Equal(t, 15, rows[0].TokenUsed) // 10 + 5

	// cache is emptied after every flush
	CacheQuotaDataLock.Lock()
	assert.Empty(t, CacheQuotaData)
	CacheQuotaDataLock.Unlock()
}

// ---------------------------------------------------------------------------
// Read/aggregation getters
// ---------------------------------------------------------------------------

func TestGetQuotaDataByUsernameAndUserId(t *testing.T) {
	requireDB(t)
	uname := uniq("byu")
	uid := nextTestID()
	base := usedataUniqTime()
	model := uniq("byum")

	// two rows, same (user, model, created_at) so GROUP BY collapses to one
	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.ModelName, q.CreatedAt = uname, uid, model, base
		q.Count, q.Quota, q.TokenUsed = 1, 30, 3
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.ModelName, q.CreatedAt = uname, uid, model, base
		q.Count, q.Quota, q.TokenUsed = 2, 70, 7
	})

	byName, err := GetQuotaDataByUsername(uname, base, base)
	require.NoError(t, err)
	require.Len(t, byName, 1)
	assert.Equal(t, 3, byName[0].Count)      // 1 + 2
	assert.Equal(t, 100, byName[0].Quota)    // 30 + 70
	assert.Equal(t, 10, byName[0].TokenUsed) // 3 + 7

	byId, err := GetQuotaDataByUserId(uid, base, base)
	require.NoError(t, err)
	require.Len(t, byId, 1)
	assert.Equal(t, 100, byId[0].Quota)

	// out-of-range time window -> nothing
	empty, err := GetQuotaDataByUsername(uname, base+1, base+2)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestGetQuotaDataGroupByUser(t *testing.T) {
	requireDB(t)
	uname := uniq("grp")
	uid := nextTestID()
	base := usedataUniqTime()

	// two models same user/hour -> grouped by (username, created_at) into one row
	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.CreatedAt, q.Quota, q.Count, q.TokenUsed = uname, uid, base, 40, 1, 4
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.CreatedAt, q.Quota, q.Count, q.TokenUsed = uname, uid, base, 60, 1, 6
	})

	rows, err := GetQuotaDataGroupByUser(base, base)
	require.NoError(t, err)
	// scope to our username since this query spans all users
	var mine *QuotaData
	for _, r := range rows {
		if r.Username == uname {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.Equal(t, 100, mine.Quota)    // 40 + 60
	assert.Equal(t, 2, mine.Count)      // 1 + 1
	assert.Equal(t, 10, mine.TokenUsed) // 4 + 6
}

func TestGetAllQuotaDates(t *testing.T) {
	requireDB(t)
	uname := uniq("all")
	uid := nextTestID()
	base := usedataUniqTime()
	model := uniq("allm")

	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.ModelName, q.CreatedAt, q.Quota, q.Count, q.TokenUsed = uname, uid, model, base, 25, 1, 2
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.Username, q.UserID, q.ModelName, q.CreatedAt, q.Quota, q.Count, q.TokenUsed = uname, uid, model, base, 75, 1, 8
	})

	// with username -> delegates to GetQuotaDataByUsername
	withUser, err := GetAllQuotaDates(base, base, uname)
	require.NoError(t, err)
	require.Len(t, withUser, 1)
	assert.Equal(t, 100, withUser[0].Quota)

	// without username -> groups by (model_name, created_at) across all users;
	// our unique model + time window isolates exactly our two rows.
	all, err := GetAllQuotaDates(base, base, "")
	require.NoError(t, err)
	var mine *QuotaData
	for _, r := range all {
		if r.ModelName == model {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.Equal(t, 100, mine.Quota)    // 25 + 75
	assert.Equal(t, 10, mine.TokenUsed) // 2 + 8
}

// documents that UpdateQuotaData is an infinite polling loop (background
// goroutine) and is intentionally not invoked in tests.
var _ = UpdateQuotaData

// keep common imported for potential toggles / silence unused in trimmed builds
var _ = common.DataExportEnabled
