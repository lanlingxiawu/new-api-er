package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

// FIXED: InitChannelCache (channel_cache.go) previously seeded its group map
// ONLY from the abilities table, then indexed that map with every ENABLED
// channel's group — so any enabled channel whose group had zero ability rows
// caused an "assignment to entry in nil map" panic. The source now lazily
// creates the inner map before the index-write, so orphan groups are safe.
// seedGuardAbilities is retained as a belt-and-suspenders helper (and to keep
// cache contents deterministic), but is no longer required to avoid a panic.
// See TestInitChannelCache_OrphanGroupNoPanic for the direct regression.
func seedGuardAbilities(t *testing.T) {
	t.Helper()
	var enabledGroups []string
	require.NoError(t, DB.Model(&Channel{}).
		Where("status = ?", common.ChannelStatusEnabled).
		Distinct(commonGroupCol).Pluck(commonGroupCol, &enabledGroups).Error)

	need := make(map[string]bool)
	for _, g := range enabledGroups {
		for _, tok := range strings.Split(g, ",") {
			need[tok] = true
		}
	}
	var abGroups []string
	require.NoError(t, DB.Model(&Ability{}).
		Distinct(commonGroupCol).Pluck(commonGroupCol, &abGroups).Error)
	for _, g := range abGroups {
		delete(need, g)
	}
	if len(need) == 0 {
		return
	}
	sentinelCh := nextTestID()
	guards := make([]Ability, 0, len(need))
	for g := range need {
		guards = append(guards, Ability{Group: g, Model: "__mc_guard__", ChannelId: sentinelCh, Enabled: false})
	}
	require.NoError(t, DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&guards).Error)
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("channel_id = ?", sentinelCh).Delete(&Ability{})
		}
	})
}

// enableMemoryCache turns on the in-memory channel cache for the duration of the
// test and rebuilds the global maps (group2model2channels / channelsIDM /
// channel2advancedCustomConfig) from the current DB contents. The flag is
// restored on cleanup. Tests never run in parallel, so the global toggle is safe.
func enableMemoryCache(t *testing.T) {
	t.Helper()
	requireDB(t)
	seedGuardAbilities(t)
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	InitChannelCache()
	t.Cleanup(func() {
		common.MemoryCacheEnabled = prev
	})
}

// mkChannelWithAbilities inserts a channel AND its ability rows, then registers
// cleanup for both. Cache tests create abilities so that group2model2channels is
// populated deterministically. (Historically an enabled channel whose group had
// zero abilities also triggered a nil-map panic in InitChannelCache; that is now
// fixed in the source — see TestInitChannelCache_OrphanGroupNoPanic.)
func mkChannelWithAbilities(t *testing.T, mut func(c *Channel)) *Channel {
	t.Helper()
	ch := mkChannel(t, mut)
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	return ch
}

// advancedCustomChannelSettings builds an OtherSettings JSON string enabling a
// single Advanced Custom route for /v1/chat/completions restricted to `model`.
func advancedCustomChannelSettings(t *testing.T, model string) string {
	t.Helper()
	ch := &Channel{}
	ch.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: []dto.AdvancedCustomRoute{{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
				Converter:    "none",
				Models:       []string{model},
			}},
		},
	})
	return ch.OtherSettings
}

// ---------------------------------------------------------------------------
// InitChannelCache / CacheGetChannel / CacheGetChannelInfo
// ---------------------------------------------------------------------------

func TestCacheGetChannel_DBFallback(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	ch := mkChannel(t, func(c *Channel) { c.Name = uniq("cgc") })

	// MemoryCacheEnabled false -> direct DB read (selectAll true includes key)
	got, err := CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, ch.Id, got.Id)
	assert.Equal(t, ch.Key, got.Key)

	// missing id -> gorm error surfaced
	_, err = CacheGetChannel(ch.Id + 987654)
	assert.Error(t, err)
}

func TestCacheGetChannel_MemoryHitAndMiss(t *testing.T) {
	requireDB(t)
	ch := mkChannelWithAbilities(t, nil)
	enableMemoryCache(t)

	got, err := CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, ch.Id, got.Id)

	// id absent from the cache map -> "已不存在" error
	_, err = CacheGetChannel(999_000_111)
	assert.Error(t, err)
}

// TestInitChannelCache_OrphanGroupNoPanic is the direct regression for the D5
// nil-map panic: an ENABLED channel whose group has ZERO ability rows must not
// crash InitChannelCache. Before the fix the enabled-channel loop indexed
// newGroup2model2channels[group][model] for a group never seeded from the
// abilities table, panicking with "assignment to entry in nil map". The source
// now lazily allocates the inner map, so cache rebuild succeeds and the orphan
// channel is routable. Note: this test deliberately does NOT call
// seedGuardAbilities — it exercises the raw orphan-group path.
func TestInitChannelCache_OrphanGroupNoPanic(t *testing.T) {
	requireDB(t)
	grp := uniq("orphan")
	// enabled channel, unique group, NO abilities inserted
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
		c.Status = common.ChannelStatusEnabled
	})

	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })

	require.NotPanics(t, func() { InitChannelCache() }, "orphan-group enabled channel must not panic InitChannelCache")

	// the orphan channel is present in the id map...
	got, err := CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, ch.Id, got.Id)
	// ...and routable via its group even though it had no ability rows.
	sat, err := GetRandomSatisfiedChannel(grp, "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, sat)
	assert.Equal(t, ch.Id, sat.Id)
}

func TestCacheGetChannelInfo(t *testing.T) {
	requireDB(t)
	ch := mkChannelWithAbilities(t, func(c *Channel) {
		c.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 3}
	})

	// DB fallback path (memory cache disabled)
	require.False(t, common.MemoryCacheEnabled)
	info, err := CacheGetChannelInfo(ch.Id)
	require.NoError(t, err)
	assert.True(t, info.IsMultiKey)
	assert.Equal(t, 3, info.MultiKeySize)

	_, err = CacheGetChannelInfo(ch.Id + 555111)
	assert.Error(t, err)

	// memory cache path
	enableMemoryCache(t)
	info, err = CacheGetChannelInfo(ch.Id)
	require.NoError(t, err)
	assert.True(t, info.IsMultiKey)

	_, err = CacheGetChannelInfo(999_000_222)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetRandomSatisfiedChannel
// ---------------------------------------------------------------------------

func TestGetRandomSatisfiedChannel_DBDelegation(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	grp := uniq("grsc")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// MemoryCacheEnabled false -> delegates to GetChannel (DB path)
	got, err := GetRandomSatisfiedChannel(grp, "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ch.Id, got.Id)
}

func TestGetRandomSatisfiedChannel_MemorySingleAndNone(t *testing.T) {
	requireDB(t)
	grp := uniq("grsc1")
	ch := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o,gpt-4-gizmo-*"
	})
	enableMemoryCache(t)

	// exactly one candidate -> returned directly
	got, err := GetRandomSatisfiedChannel(grp, "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ch.Id, got.Id)

	// unknown model -> nil,nil
	got, err = GetRandomSatisfiedChannel(grp, "totally-absent", 0, "")
	require.NoError(t, err)
	assert.Nil(t, got)

	// normalized-model fallback ("gpt-4-gizmo-xyz" -> "gpt-4-gizmo-*")
	got, err = GetRandomSatisfiedChannel(grp, "gpt-4-gizmo-xyz", 0, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ch.Id, got.Id)
}

func TestGetRandomSatisfiedChannel_PriorityAndRetryClamp(t *testing.T) {
	requireDB(t)
	grp := uniq("grsc2")
	model := "gpt-4o"
	high1 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](10)
	})
	high2 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](10)
	})
	low := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](5)
	})
	enableMemoryCache(t)

	highSet := map[int]bool{high1.Id: true, high2.Id: true}

	// retry 0 -> highest priority bucket {high1, high2}
	got, err := GetRandomSatisfiedChannel(grp, model, 0, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, highSet[got.Id], "retry 0 must pick a priority-10 channel")

	// retry 1 -> next priority bucket {low}
	got, err = GetRandomSatisfiedChannel(grp, model, 1, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, low.Id, got.Id)

	// retry beyond number of distinct priorities -> clamps to smallest priority
	got, err = GetRandomSatisfiedChannel(grp, model, 99, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, low.Id, got.Id)
}

func TestGetRandomSatisfiedChannel_WeightBranches(t *testing.T) {
	requireDB(t)
	model := "gpt-4o"

	// (a) all weights zero -> sumWeight==0 branch (effective weight 100 each)
	grpA := uniq("grw0")
	a1 := mkChannelWithAbilities(t, func(c *Channel) { c.Group, c.Models = grpA, model })
	a2 := mkChannelWithAbilities(t, func(c *Channel) { c.Group, c.Models = grpA, model })
	// (b) small average weight (<10) -> smoothingFactor=100 branch
	grpB := uniq("grws")
	b1 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grpB, model
		c.Weight = common.GetPointer[uint](1)
	})
	b2 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grpB, model
		c.Weight = common.GetPointer[uint](2)
	})
	// (c) large average weight (>=10) -> no smoothing branch
	grpC := uniq("grwl")
	c1 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grpC, model
		c.Weight = common.GetPointer[uint](20)
	})
	c2 := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grpC, model
		c.Weight = common.GetPointer[uint](30)
	})
	enableMemoryCache(t)

	for _, tc := range []struct {
		grp string
		set map[int]bool
	}{
		{grpA, map[int]bool{a1.Id: true, a2.Id: true}},
		{grpB, map[int]bool{b1.Id: true, b2.Id: true}},
		{grpC, map[int]bool{c1.Id: true, c2.Id: true}},
	} {
		// run a few times: the returned channel must always be in the group's set
		for i := 0; i < 8; i++ {
			got, err := GetRandomSatisfiedChannel(tc.grp, model, 0, "")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Truef(t, tc.set[got.Id], "group %s returned unexpected channel %d", tc.grp, got.Id)
		}
	}
}

// ---------------------------------------------------------------------------
// filterChannelsByRequestPathAndModel
// ---------------------------------------------------------------------------

func TestFilterChannelsByRequestPathAndModel(t *testing.T) {
	requireDB(t)

	// empty requestPath -> input returned as-is (skip filtering)
	in := []int{1, 2, 3}
	assert.Equal(t, in, filterChannelsByRequestPathAndModel(in, "", "gpt-4o"))
	// empty candidate list -> returned as-is
	assert.Empty(t, filterChannelsByRequestPathAndModel(nil, "/v1/chat/completions", "gpt-4o"))

	// plain (non advanced-custom) channel always passes path filtering
	plain := mkChannelWithAbilities(t, func(c *Channel) { c.Models = "gpt-4o" })
	// advanced custom channel: kept only when the route matches path+model
	adv := mkChannelWithAbilities(t, func(c *Channel) {
		c.Type = constant.ChannelTypeAdvancedCustom
		c.Models = "gpt-4o"
		c.OtherSettings = advancedCustomChannelSettings(t, "gpt-4o")
	})
	enableMemoryCache(t)

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// matching path+model: both plain and advanced kept
	out := filterChannelsByRequestPathAndModel([]int{plain.Id, adv.Id}, "/v1/chat/completions", "gpt-4o")
	assert.ElementsMatch(t, []int{plain.Id, adv.Id}, out)

	// non-matching path: advanced dropped, plain kept
	out = filterChannelsByRequestPathAndModel([]int{plain.Id, adv.Id}, "/v1/messages", "gpt-4o")
	assert.Equal(t, []int{plain.Id}, out)

	// unknown channel id (not in channelsIDM) is kept so the downstream
	// consistency error is still raised as before
	out = filterChannelsByRequestPathAndModel([]int{424242}, "/v1/chat/completions", "gpt-4o")
	assert.Equal(t, []int{424242}, out)
}

// ---------------------------------------------------------------------------
// CacheUpdateChannelStatus / CacheUpdateChannel
// ---------------------------------------------------------------------------

func TestCacheUpdateChannelStatus(t *testing.T) {
	requireDB(t)

	// memory cache disabled -> no-op (must not panic)
	require.False(t, common.MemoryCacheEnabled)
	CacheUpdateChannelStatus(123, common.ChannelStatusManuallyDisabled)

	grp := uniq("cucs")
	ch := mkChannelWithAbilities(t, func(c *Channel) {
		c.Group, c.Models = grp, "gpt-4o"
	})
	enableMemoryCache(t)

	// precondition: channel is a selectable candidate
	got, err := GetRandomSatisfiedChannel(grp, "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, got)

	// disable -> removed from group2model2channels and status updated in map
	CacheUpdateChannelStatus(ch.Id, common.ChannelStatusManuallyDisabled)
	got, err = GetRandomSatisfiedChannel(grp, "gpt-4o", 0, "")
	require.NoError(t, err)
	assert.Nil(t, got, "disabled channel removed from selection map")
	cached, err := CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, cached.Status)

	// re-enable -> only updates status in the map (not re-added to selection)
	CacheUpdateChannelStatus(ch.Id, common.ChannelStatusEnabled)
	cached, err = CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, cached.Status)
}

func TestCacheUpdateChannel(t *testing.T) {
	requireDB(t)

	// memory cache disabled -> no-op
	require.False(t, common.MemoryCacheEnabled)
	CacheUpdateChannel(&Channel{Id: 1})

	ch := mkChannelWithAbilities(t, func(c *Channel) { c.Name = "before" })
	enableMemoryCache(t)

	// nil guard must not panic
	CacheUpdateChannel(nil)

	// update existing channel -> reflected in the cache map
	ch.Name = "after"
	CacheUpdateChannel(ch)
	cached, err := CacheGetChannel(ch.Id)
	require.NoError(t, err)
	assert.Equal(t, "after", cached.Name)

	// advanced-custom channel -> parsed config stored in channel2advancedCustomConfig
	adv := mkChannel(t, func(c *Channel) {
		c.Type = constant.ChannelTypeAdvancedCustom
		c.Models = "gpt-4o"
		c.OtherSettings = advancedCustomChannelSettings(t, "gpt-4o")
	})
	CacheUpdateChannel(adv)
	channelSyncLock.RLock()
	cfg := channel2advancedCustomConfig[adv.Id]
	channelSyncLock.RUnlock()
	require.NotNil(t, cfg)
	assert.True(t, cfg.SupportsPathForModel("/v1/chat/completions", "gpt-4o"))
}
