package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// channel_account_balance.go — Redis-backed per-channel upstream balance cache.
// Keyspace "channel_account_balance:<id>" is distinct from the relay hot path.
// ---------------------------------------------------------------------------

// With Redis disabled every function must be a safe no-op (Rule 0 graceful
// degradation): writes/deletes return nil, reads return (nil,nil), batch read
// returns an empty map.
func TestChannelAccountBalance_RedisDisabled(t *testing.T) {
	require.False(t, common.RedisEnabled)
	id := nextTestID()

	require.NoError(t, SetChannelAccountBalance(id, ChannelAccountBalance{Quota: 1}))

	got, err := GetChannelAccountBalance(id)
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, DeleteChannelAccountBalance(id))

	res := BatchGetChannelAccountBalance([]int{id, id + 1})
	assert.Empty(t, res)
}

func TestChannelAccountBalance_RoundTrip(t *testing.T) {
	enableRedis(t)
	id := nextTestID()
	t.Cleanup(func() { _ = DeleteChannelAccountBalance(id) })

	// miss before set
	got, err := GetChannelAccountBalance(id)
	require.NoError(t, err)
	assert.Nil(t, got, "unset key -> redis.Nil -> (nil,nil)")

	data := ChannelAccountBalance{
		Group:       "default",
		Quota:       123456,
		UsedQuota:   789,
		UpdatedTime: 1700000000,
	}
	require.NoError(t, SetChannelAccountBalance(id, data))

	got, err = GetChannelAccountBalance(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, data.Group, got.Group)
	assert.EqualValues(t, 123456, got.Quota)
	assert.EqualValues(t, 789, got.UsedQuota)
	assert.EqualValues(t, 1700000000, got.UpdatedTime)

	// delete -> subsequent get misses (nil,nil)
	require.NoError(t, DeleteChannelAccountBalance(id))
	got, err = GetChannelAccountBalance(id)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestChannelAccountBalance_BatchGet(t *testing.T) {
	enableRedis(t)

	// empty ids short-circuits to an empty (non-nil) map even with Redis on.
	assert.Empty(t, BatchGetChannelAccountBalance(nil))

	id1 := nextTestID()
	id2 := nextTestID()
	id3 := nextTestID() // never set -> must be absent from result
	t.Cleanup(func() {
		_ = DeleteChannelAccountBalance(id1)
		_ = DeleteChannelAccountBalance(id2)
	})

	require.NoError(t, SetChannelAccountBalance(id1, ChannelAccountBalance{Quota: 11}))
	require.NoError(t, SetChannelAccountBalance(id2, ChannelAccountBalance{Quota: 22}))

	res := BatchGetChannelAccountBalance([]int{id1, id2, id3})
	require.Len(t, res, 2)
	require.Contains(t, res, id1)
	require.Contains(t, res, id2)
	assert.NotContains(t, res, id3)
	assert.EqualValues(t, 11, res[id1].Quota)
	assert.EqualValues(t, 22, res[id2].Quota)
}

func TestChannelAccountBalanceKey(t *testing.T) {
	assert.Equal(t, "channel_account_balance:42", channelAccountBalanceKey(42))
}
