package model

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestProbeRoutingCandidateFiltering(t *testing.T) {
	oldEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	oldGroups, oldChannels := group2model2channels, channelsIDM
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldEnabled
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	group2model2channels = map[string]map[string][]int{"probe-test": {"model": {1, 2, 3}}}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Priority: common.GetPointer(int64(100)), Status: common.ChannelStatusEnabled},
		2: {Id: 2, Priority: common.GetPointer(int64(50)), Status: common.ChannelStatusEnabled},
		3: {Id: 3, Priority: common.GetPointer(int64(0)), Status: common.ChannelStatusEnabled},
	}
	policy := &ChannelProbePolicy{blocked: map[int]bool{1: true}, refreshedAt: time.Now()}
	for _, tt := range []struct{ retry, want int }{{0, 2}, {1, 3}, {9, 3}} {
		ch, err := GetRandomSatisfiedChannel("probe-test", "model", tt.retry, "", policy)
		require.NoError(t, err)
		require.Equal(t, tt.want, ch.Id)
	}
	ch, err := GetRandomSatisfiedChannel("probe-test", "model", 0, "")
	require.NoError(t, err)
	require.Equal(t, 1, ch.Id)
	policy = &ChannelProbePolicy{blocked: map[int]bool{1: true, 2: true, 3: true}}
	_, err = GetRandomSatisfiedChannel("probe-test", "model", 0, "", policy)
	require.ErrorIs(t, err, ErrProbeChannelUnavailable)
	ch, err = GetRandomSatisfiedChannel("probe-test", "absent", 0, "", policy)
	require.NoError(t, err)
	require.Nil(t, ch)
}

func BenchmarkProbeChannelSelection(b *testing.B) {
	oldEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	oldGroups, oldChannels := group2model2channels, channelsIDM
	b.Cleanup(func() {
		common.MemoryCacheEnabled = oldEnabled
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	for _, count := range []int{1, 10, 100, 1000} {
		channelsIDM = make(map[int]*Channel, count)
		ids := make([]int, count)
		policy := &ChannelProbePolicy{blocked: make(map[int]bool), refreshedAt: time.Now()}
		for i := range ids {
			ids[i] = i + 1
			channelsIDM[i+1] = &Channel{Id: i + 1, Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(0))}
			if i%2 == 1 {
				policy.blocked[i+1] = true
			}
		}
		group2model2channels = map[string]map[string][]int{"probe-bench": {"model": ids}}
		for _, mode := range []string{"baseline", "half_blocked", "all_allowed"} {
			b.Run(fmt.Sprintf("candidates_%d/%s", count, mode), func(b *testing.B) {
				var p *ChannelProbePolicy
				if mode == "half_blocked" {
					p = policy
				} else if mode == "all_allowed" {
					p = &ChannelProbePolicy{blocked: map[int]bool{-1: true}}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = GetRandomSatisfiedChannel("probe-bench", "model", 0, "/v1/responses", p)
				}
			})
		}
		if count == 1000 {
			for _, filtered := range []bool{false, true} {
				b.Run(fmt.Sprintf("parallel_1000/filtered_%v", filtered), func(b *testing.B) {
					var p *ChannelProbePolicy
					if filtered {
						p = &ChannelProbePolicy{blocked: map[int]bool{-1: true}}
					}
					b.ReportAllocs()
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							_, _ = GetRandomSatisfiedChannel("probe-bench", "model", 0, "/v1/responses", p)
						}
					})
				})
			}
		}
	}
}

func TestProbePolicySnapshotLifecycle(t *testing.T) {
	previous := channelProbePolicy.Load()
	t.Cleanup(func() { channelProbePolicy.Store(previous) })
	channelProbePolicy.Store(nil)
	require.Nil(t, CurrentChannelProbePolicy())
	channelProbePolicy.Store(&ChannelProbePolicy{blocked: map[int]bool{1: true}, refreshedAt: time.Now().Add(-4 * time.Minute)})
	require.Nil(t, CurrentChannelProbePolicy())
	channelProbePolicy.Store(&ChannelProbePolicy{blocked: map[int]bool{1: true}, refreshedAt: time.Now()})
	pinned := CurrentChannelProbePolicy()
	require.True(t, pinned.Blocks(1))
	channelProbePolicy.Store(&ChannelProbePolicy{blocked: map[int]bool{}, refreshedAt: time.Now()})
	require.Nil(t, CurrentChannelProbePolicy())
	require.True(t, pinned.Blocks(1), "in-flight requests keep their original policy")
}

func TestProbePolicyConcurrentRead(t *testing.T) {
	previous := channelProbePolicy.Load()
	t.Cleanup(func() { channelProbePolicy.Store(previous) })
	a := &ChannelProbePolicy{blocked: map[int]bool{1: true}, refreshedAt: time.Now()}
	b := &ChannelProbePolicy{blocked: map[int]bool{2: true}, refreshedAt: time.Now()}
	channelProbePolicy.Store(a)
	var invalid atomic.Bool
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 10000; j++ {
				p := CurrentChannelProbePolicy()
				if p == nil || p.Blocks(1) == p.Blocks(2) {
					invalid.Store(true)
				}
			}
		}()
	}
	for i := 0; i < 10000; i++ {
		channelProbePolicy.Store(a)
		channelProbePolicy.Store(b)
	}
	readers.Wait()
	require.False(t, invalid.Load())
}

func TestProbeRoutingDBParity(t *testing.T) {
	requireDB(t)
	name := uniq("probe-parity")
	first := mkChannelWithAbilities(t, func(ch *Channel) {
		ch.Models = name
		ch.Group = name
		ch.Priority = common.GetPointer(int64(100))
		ch.OtherSettings = `{"disable_probe_requests":true}`
	})
	second := mkChannelWithAbilities(t, func(ch *Channel) {
		ch.Models = name
		ch.Group = name
		ch.Priority = common.GetPointer(int64(50))
	})
	oldMemory := common.MemoryCacheEnabled
	oldPolicy := channelProbePolicy.Load()
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory; channelProbePolicy.Store(oldPolicy) })
	for _, memory := range []bool{false, true} {
		common.MemoryCacheEnabled = memory
		InitChannelCache()
		policy := CurrentChannelProbePolicy()
		require.NotNil(t, policy)
		require.True(t, policy.Blocks(first.Id))
		selected, err := GetRandomSatisfiedChannel(name, name, 0, "", policy)
		require.NoError(t, err)
		require.Equal(t, second.Id, selected.Id)
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", second.Id).Update("settings", `{"disable_probe_requests":true}`).Error)
		RefreshChannelProbePolicy()
		_, err = GetRandomSatisfiedChannel(name, name, 0, "", CurrentChannelProbePolicy())
		require.ErrorIs(t, err, ErrProbeChannelUnavailable)
		// The pinned policy remains usable while new requests see the edit.
		selected, err = GetRandomSatisfiedChannel(name, name, 0, "", policy)
		require.NoError(t, err)
		require.Equal(t, second.Id, selected.Id)
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", second.Id).Update("settings", `{}`).Error)
	}
}

func TestPreserveChannelProbeSetting(t *testing.T) {
	for _, tt := range []struct {
		incoming string
		want     bool
	}{
		{`{"allow_speed":true}`, true},
		{`{"disable_probe_requests":false}`, false},
		{`{"disable_probe_requests":true}`, true},
		{`{}`, true},
	} {
		ch := &Channel{OtherSettings: tt.incoming}
		require.NoError(t, ch.PreserveProbeSetting(&Channel{OtherSettings: `{"disable_probe_requests":true}`}))
		require.Equal(t, tt.want, ch.GetOtherSettings().DisableProbeRequests)
	}
	ch := &Channel{OtherSettings: `{"disable_probe_requests":"false"}`}
	require.Error(t, ch.PreserveProbeSetting(&Channel{}))
}
