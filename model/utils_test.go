package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// RecordExist
// ---------------------------------------------------------------------------

func TestRecordExist(t *testing.T) {
	// nil error -> record exists
	ok, err := RecordExist(nil)
	assert.True(t, ok)
	assert.NoError(t, err)

	// ErrRecordNotFound -> does not exist, no propagated error
	ok, err = RecordExist(gorm.ErrRecordNotFound)
	assert.False(t, ok)
	assert.NoError(t, err)

	// wrapped ErrRecordNotFound still matched via errors.Is
	ok, err = RecordExist(errors.Join(gorm.ErrRecordNotFound, errors.New("ctx")))
	assert.False(t, ok)
	assert.NoError(t, err)

	// any other error -> not exist + propagate
	sentinel := errors.New("boom")
	ok, err = RecordExist(sentinel)
	assert.False(t, ok)
	assert.ErrorIs(t, err, sentinel)
}

// ---------------------------------------------------------------------------
// shouldUpdateRedis — pure boolean: RedisEnabled && fromDB && err==nil
// (condition coverage over all three sub-conditions)
// ---------------------------------------------------------------------------

func TestShouldUpdateRedis(t *testing.T) {
	prev := common.RedisEnabled
	defer func() { common.RedisEnabled = prev }()

	common.RedisEnabled = false
	assert.False(t, shouldUpdateRedis(true, nil), "redis disabled -> false")

	common.RedisEnabled = true
	assert.True(t, shouldUpdateRedis(true, nil), "all true -> true")
	assert.False(t, shouldUpdateRedis(false, nil), "not fromDB -> false")
	assert.False(t, shouldUpdateRedis(true, errors.New("x")), "err set -> false")
	assert.False(t, shouldUpdateRedis(false, errors.New("x")), "not fromDB + err -> false")
}

// ---------------------------------------------------------------------------
// addNewRecord — in-memory aggregation into the per-type stores.
// We manipulate the package-private stores directly for isolated setup/teardown
// so we never disturb other tests (tests run serially; no batch worker runs in
// the test binary).
// ---------------------------------------------------------------------------

// resetBatchStores empties all stores under their locks and returns a restore
// func (registered via Cleanup) so the global state is untouched afterwards.
func resetBatchStores(t *testing.T) {
	t.Helper()
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		for i := 0; i < BatchUpdateTypeCount; i++ {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func storeValue(typ, id int) (int, bool) {
	batchUpdateLocks[typ].Lock()
	defer batchUpdateLocks[typ].Unlock()
	v, ok := batchUpdateStores[typ][id]
	return v, ok
}

func TestAddNewRecord_Aggregation(t *testing.T) {
	resetBatchStores(t)

	// first insert seeds the value
	addNewRecord(BatchUpdateTypeTokenQuota, 42, 10)
	v, ok := storeValue(BatchUpdateTypeTokenQuota, 42)
	require.True(t, ok)
	assert.Equal(t, 10, v)

	// subsequent inserts accumulate (including negatives)
	addNewRecord(BatchUpdateTypeTokenQuota, 42, 5)
	addNewRecord(BatchUpdateTypeTokenQuota, 42, -3)
	v, _ = storeValue(BatchUpdateTypeTokenQuota, 42)
	assert.Equal(t, 12, v)

	// distinct ids and distinct types are isolated
	addNewRecord(BatchUpdateTypeTokenQuota, 99, 7)
	v, _ = storeValue(BatchUpdateTypeTokenQuota, 99)
	assert.Equal(t, 7, v)

	_, ok = storeValue(BatchUpdateTypeChannelUsedQuota, 42)
	assert.False(t, ok, "other type store must be independent")
}

// ---------------------------------------------------------------------------
// batchUpdate — drains all stores and flushes to DB. Covers:
//   - no-data early return
//   - token quota path (increaseTokenQuota: +remain, -used)
//   - channel used-quota path
//   - user quota/used/request combined path (deduped across three stores)
// ---------------------------------------------------------------------------

func TestBatchUpdate_NoData_NoOp(t *testing.T) {
	resetBatchStores(t)
	// Should return immediately without touching the DB or panicking.
	batchUpdate()
	// stores remain empty
	for i := 0; i < BatchUpdateTypeCount; i++ {
		v := len(batchUpdateStores[i])
		assert.Equal(t, 0, v)
	}
}

func TestBatchUpdate_FlushesToDB(t *testing.T) {
	requireDB(t)
	resetBatchStores(t)

	user := mkUser(t, func(u *User) {
		u.Quota = 1000
		u.UsedQuota = 0
		u.RequestCount = 0
	})
	token := mkToken(t, user.Id, func(tk *Token) {
		tk.RemainQuota = 500
		tk.UsedQuota = 500
	})
	channel := mkChannel(t, func(ch *Channel) {
		ch.UsedQuota = 0
	})

	// user aggregate spread across three stores for the SAME id (must dedupe)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -100)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 100)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 3)
	// token quota: +50 remain, -50 used
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, 50)
	// channel used quota: +200
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 200)

	batchUpdate()

	// stores drained
	for i := 0; i < BatchUpdateTypeCount; i++ {
		assert.Equalf(t, 0, len(batchUpdateStores[i]), "store %d not drained", i)
	}

	var gotUser User
	require.NoError(t, DB.First(&gotUser, user.Id).Error)
	assert.Equal(t, 900, gotUser.Quota)
	assert.Equal(t, 100, gotUser.UsedQuota)
	assert.Equal(t, 3, gotUser.RequestCount)

	var gotToken Token
	require.NoError(t, DB.First(&gotToken, token.Id).Error)
	assert.Equal(t, 550, gotToken.RemainQuota)
	assert.Equal(t, 450, gotToken.UsedQuota)

	var gotChannel Channel
	require.NoError(t, DB.First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(200), gotChannel.UsedQuota)
}

// Only channel/token stores populated: the user-combine branch must be a no-op
// (updateUserQuotaUsedQuotaAndRequestCount short-circuits on all-zero).
func TestBatchUpdate_TokenOnly(t *testing.T) {
	requireDB(t)
	resetBatchStores(t)

	token := mkToken(t, mkUser(t, nil).Id, func(tk *Token) {
		tk.RemainQuota = 100
		tk.UsedQuota = 100
	})
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, 25)
	batchUpdate()

	var gotToken Token
	require.NoError(t, DB.First(&gotToken, token.Id).Error)
	assert.Equal(t, 125, gotToken.RemainQuota)
	assert.Equal(t, 75, gotToken.UsedQuota)
}
