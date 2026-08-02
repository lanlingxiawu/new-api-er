package model

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Pure getters / setters on Channel
// ---------------------------------------------------------------------------

func TestChannel_GetKeys(t *testing.T) {
	// empty key -> empty slice
	assert.Empty(t, (&Channel{Key: ""}).GetKeys())

	// cached Keys short-circuit
	c := &Channel{Key: "ignored", Keys: []string{"pre1", "pre2"}}
	assert.Equal(t, []string{"pre1", "pre2"}, c.GetKeys())

	// JSON array form (Vertex AI style) -> raw messages (quotes retained)
	c = &Channel{Key: `["a","b"]`}
	assert.Equal(t, []string{`"a"`, `"b"`}, c.GetKeys())

	// newline split fallback
	c = &Channel{Key: "k1\nk2\nk3"}
	assert.Equal(t, []string{"k1", "k2", "k3"}, c.GetKeys())
}

func TestChannel_SimpleGetters(t *testing.T) {
	assert.Equal(t, []string{}, (&Channel{Models: ""}).GetModels())
	assert.Equal(t, []string{"gpt-4o", "claude-3"}, (&Channel{Models: ",gpt-4o,claude-3,"}).GetModels())

	assert.Equal(t, []string{}, (&Channel{Group: ""}).GetGroups())
	assert.Equal(t, []string{"vip", "default"}, (&Channel{Group: " vip , default "}).GetGroups())

	// Tag
	c := &Channel{}
	assert.Equal(t, "", c.GetTag())
	c.SetTag("t1")
	assert.Equal(t, "t1", c.GetTag())

	// AutoBan: nil -> false; 1 -> true; other -> false
	assert.False(t, (&Channel{}).GetAutoBan())
	assert.True(t, (&Channel{AutoBan: common.GetPointer[int](1)}).GetAutoBan())
	assert.False(t, (&Channel{AutoBan: common.GetPointer[int](0)}).GetAutoBan())

	// Priority / Weight nil-safe
	assert.EqualValues(t, 0, (&Channel{}).GetPriority())
	assert.EqualValues(t, 7, (&Channel{Priority: common.GetPointer[int64](7)}).GetPriority())
	assert.Equal(t, 0, (&Channel{}).GetWeight())
	assert.Equal(t, 3, (&Channel{Weight: common.GetPointer[uint](3)}).GetWeight())

	// ModelMapping / StatusCodeMapping nil-safe
	assert.Equal(t, "", (&Channel{}).GetModelMapping())
	assert.Equal(t, "m", (&Channel{ModelMapping: common.GetPointer[string]("m")}).GetModelMapping())
	assert.Equal(t, "", (&Channel{}).GetStatusCodeMapping())
	assert.Equal(t, "s", (&Channel{StatusCodeMapping: common.GetPointer[string]("s")}).GetStatusCodeMapping())
}

func TestChannel_GetBaseURL(t *testing.T) {
	// nil -> ""
	assert.Equal(t, "", (&Channel{}).GetBaseURL())
	// explicit value
	c := &Channel{BaseURL: common.GetPointer[string]("https://x.example")}
	assert.Equal(t, "https://x.example", c.GetBaseURL())
	// empty string -> falls back to the type's default base URL constant
	c = &Channel{Type: 1, BaseURL: common.GetPointer[string]("")}
	assert.Equal(t, constant.ChannelBaseURLs[1], c.GetBaseURL())
	// Corrupt or forward-version channel types must not panic while management
	// APIs enumerate historical rows.
	assert.Equal(t, "", (&Channel{Type: -1, BaseURL: common.GetPointer[string]("")}).GetBaseURL())
	assert.Equal(t, "", (&Channel{Type: len(constant.ChannelBaseURLs), BaseURL: common.GetPointer[string]("")}).GetBaseURL())
}

func TestChannel_OtherInfoRoundTrip(t *testing.T) {
	c := &Channel{}
	assert.Empty(t, c.GetOtherInfo())
	c.SetOtherInfo(map[string]interface{}{"status_reason": "boom", "n": float64(5)})
	info := c.GetOtherInfo()
	assert.Equal(t, "boom", info["status_reason"])
	assert.EqualValues(t, 5, info["n"])

	// malformed JSON tolerated -> empty map, logged
	c.OtherInfo = "{not-json"
	assert.Empty(t, c.GetOtherInfo())
}

func TestChannel_ParamAndHeaderOverride(t *testing.T) {
	c := &Channel{}
	assert.Empty(t, c.GetParamOverride())
	assert.Empty(t, c.GetHeaderOverride())

	c.ParamOverride = common.GetPointer[string](`{"temperature":0.5}`)
	c.HeaderOverride = common.GetPointer[string](`{"X-Test":"v"}`)
	assert.EqualValues(t, 0.5, c.GetParamOverride()["temperature"])
	assert.Equal(t, "v", c.GetHeaderOverride()["X-Test"])

	// malformed -> empty (logged, not fatal)
	c.ParamOverride = common.GetPointer[string](`{bad`)
	assert.Empty(t, c.GetParamOverride())
}

func TestChannelInfo_ValueScan(t *testing.T) {
	ci := ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}
	v, err := ci.Value()
	require.NoError(t, err)

	var out ChannelInfo
	require.NoError(t, out.Scan(v))
	assert.True(t, out.IsMultiKey)
	assert.Equal(t, 2, out.MultiKeySize)
	assert.Equal(t, constant.MultiKeyModePolling, out.MultiKeyMode)
}

// ---------------------------------------------------------------------------
// ChannelSettings / OtherSettings / ValidateSettings
// ---------------------------------------------------------------------------

func TestChannel_ValidateSettings(t *testing.T) {
	// empty everything -> ok
	require.NoError(t, (&Channel{}).ValidateSettings())

	// advanced-custom type but no advanced_custom config -> error
	c := &Channel{Type: constant.ChannelTypeAdvancedCustom}
	assert.Error(t, c.ValidateSettings())

	// advanced-custom with a valid route -> ok
	c = &Channel{Type: constant.ChannelTypeAdvancedCustom}
	c.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
			{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions", Converter: "none"},
		}},
	})
	require.NoError(t, c.ValidateSettings())

	// advanced-custom + upstream model update check but no /v1/models route -> error
	c = &Channel{Type: constant.ChannelTypeAdvancedCustom}
	c.SetOtherSettings(dto.ChannelOtherSettings{
		UpstreamModelUpdateCheckEnabled: true,
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
			{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions", Converter: "none"},
		}},
	})
	assert.Error(t, c.ValidateSettings())

	// malformed Setting JSON -> error surfaced
	bad := "{bad"
	assert.Error(t, (&Channel{Setting: &bad}).ValidateSettings())
}

func TestChannel_GetSetSetting(t *testing.T) {
	c := &Channel{}
	// unset -> zero-value settings
	_ = c.GetSetting()

	c.SetSetting(dto.ChannelSettings{ForceFormat: true})
	require.NotNil(t, c.Setting)
	assert.True(t, c.GetSetting().ForceFormat)

	// OtherSettings round trip
	c.SetOtherSettings(dto.ChannelOtherSettings{UpstreamModelUpdateCheckEnabled: true})
	assert.True(t, c.GetOtherSettings().UpstreamModelUpdateCheckEnabled)
}

// ---------------------------------------------------------------------------
// Sort options + group filter helpers (pure)
// ---------------------------------------------------------------------------

func TestNewChannelSortOptions(t *testing.T) {
	// invalid sortBy -> blanked
	o := NewChannelSortOptions("bogus", "asc", false)
	assert.Equal(t, "", o.SortBy)
	assert.Equal(t, "", o.SortOrder)

	// valid + asc preserved
	o = NewChannelSortOptions("Priority", "ASC", true)
	assert.Equal(t, "priority", o.SortBy)
	assert.Equal(t, "asc", o.SortOrder)
	assert.True(t, o.IDSort)

	// valid + non-asc -> desc
	o = NewChannelSortOptions("balance", "whatever", false)
	assert.Equal(t, "balance", o.SortBy)
	assert.Equal(t, "desc", o.SortOrder)
}

func TestResolveChannelSortOptions(t *testing.T) {
	// no options -> defaults from idSort
	o := resolveChannelSortOptions(true, nil)
	assert.True(t, o.IDSort)
	// provided options -> idSort ORed in
	o = resolveChannelSortOptions(true, []ChannelSortOptions{{SortBy: "id", IDSort: false}})
	assert.True(t, o.IDSort)
	assert.Equal(t, "id", o.SortBy)
}

func TestNormalizeChannelGroupFilter(t *testing.T) {
	assert.Equal(t, "", NormalizeChannelGroupFilter(""))
	assert.Equal(t, "", NormalizeChannelGroupFilter("  "))
	assert.Equal(t, "", NormalizeChannelGroupFilter("all"))
	assert.Equal(t, "", NormalizeChannelGroupFilter("NULL"))
	assert.Equal(t, "vip", NormalizeChannelGroupFilter("  vip "))
}

func TestChannelGroupFilterPattern(t *testing.T) {
	assert.Equal(t, "%,vip,%", channelGroupFilterPattern("vip"))
	// escapes !, %, _
	assert.Equal(t, "%,a!!b!%c!_d,%", channelGroupFilterPattern("a!b%c_d"))
}

func TestApplyChannelGroupFilter_Passthrough(t *testing.T) {
	requireDB(t)
	// empty/all group -> query unchanged and executes cleanly
	q := ApplyChannelGroupFilter(DB.Model(&Channel{}), "all")
	var n int64
	require.NoError(t, q.Count(&n).Error)
}

// ---------------------------------------------------------------------------
// CRUD: Insert / Update / Save / Delete
// ---------------------------------------------------------------------------

func TestChannel_InsertCreatesAbilities(t *testing.T) {
	requireDB(t)
	id := nextTestID()
	c := &Channel{
		Id:     id,
		Type:   1,
		Key:    uniq("ik"),
		Name:   uniq("ins"),
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o,gpt-4o-mini",
		Group:  "default",
	}
	require.NoError(t, c.Insert())
	deleteByID(t, &Channel{}, id)
	cleanupAbilities(t, id)

	got, err := GetChannelById(id, true)
	require.NoError(t, err)
	assert.Equal(t, c.Name, got.Name)
	assert.EqualValues(t, 2, countAbilities(t, id))
}

func TestChannel_Update_RecreatesAbilities(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Models = "a1,a2,a3"
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	require.EqualValues(t, 3, countAbilities(t, ch.Id))

	ch.Models = "a1"
	ch.Name = "updated-name"
	require.NoError(t, ch.Update())

	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "updated-name", got.Name)
	assert.EqualValues(t, 1, countAbilities(t, ch.Id))
}

func TestChannel_Update_MultiKeyRecomputesSize(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Key = "k1\nk2"
		c.Models = "gpt-4o"
		c.ChannelInfo = ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       99, // stale, must be recomputed to 2
			MultiKeyStatusList: map[int]int{5: common.ChannelStatusManuallyDisabled},
		}
	})
	cleanupAbilities(t, ch.Id)

	ch.Key = "k1\nk2\nk3"
	require.NoError(t, ch.Update())
	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 3, got.ChannelInfo.MultiKeySize)
	// stale status index (5 >= new size 3) pruned
	_, exists := got.ChannelInfo.MultiKeyStatusList[5]
	assert.False(t, exists)
}

func TestChannel_SaveWithoutKey(t *testing.T) {
	requireDB(t)
	// id 0 guard
	assert.Error(t, (&Channel{}).SaveWithoutKey())

	ch := mkChannel(t, func(c *Channel) { c.Name = "s0" })
	ch.Name = "s1"
	origKey := ch.Key
	ch.Key = "SHOULD-NOT-PERSIST"
	require.NoError(t, ch.SaveWithoutKey())

	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "s1", got.Name)
	assert.Equal(t, origKey, got.Key, "key column omitted from save")
}

func TestChannel_Delete(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	require.NoError(t, ch.Delete())
	_, err := GetChannelById(ch.Id, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.EqualValues(t, 0, countAbilities(t, ch.Id))
}

// ---------------------------------------------------------------------------
// Batch insert / delete
// ---------------------------------------------------------------------------

func TestBatchInsertChannels(t *testing.T) {
	requireDB(t)
	// empty slice -> nil no-op
	require.NoError(t, BatchInsertChannels(nil))

	id1, id2 := nextTestID(), nextTestID()
	channels := []Channel{
		{Id: id1, Type: 1, Key: uniq("bk"), Name: uniq("b1"), Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default"},
		{Id: id2, Type: 1, Key: uniq("bk"), Name: uniq("b2"), Status: common.ChannelStatusEnabled, Models: "gpt-4o,gpt-4o-mini", Group: "default"},
	}
	require.NoError(t, BatchInsertChannels(channels))
	deleteByID(t, &Channel{}, id1)
	deleteByID(t, &Channel{}, id2)
	cleanupAbilities(t, id1)
	cleanupAbilities(t, id2)

	_, err := GetChannelById(id1, false)
	require.NoError(t, err)
	assert.EqualValues(t, 1, countAbilities(t, id1))
	assert.EqualValues(t, 2, countAbilities(t, id2))
}

func TestBatchDeleteChannels(t *testing.T) {
	requireDB(t)
	_, err := BatchDeleteChannels(nil)
	require.NoError(t, err)

	c1 := mkChannel(t, nil)
	c2 := mkChannel(t, nil)
	require.NoError(t, c1.AddAbilities(nil))
	require.NoError(t, c2.AddAbilities(nil))
	cleanupAbilities(t, c1.Id)
	cleanupAbilities(t, c2.Id)

	_, err = BatchDeleteChannels([]int{c1.Id, c2.Id})
	require.NoError(t, err)
	_, err = GetChannelById(c1.Id, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.EqualValues(t, 0, countAbilities(t, c1.Id))
	assert.EqualValues(t, 0, countAbilities(t, c2.Id))
}

func TestDeleteChannelByStatus_ScopedStatus(t *testing.T) {
	requireDB(t)
	// Use a bespoke status value no other row shares, so we never wipe shared data.
	const uniqueStatus = 987654
	ch := mkChannel(t, func(c *Channel) { c.Status = uniqueStatus })

	n, err := DeleteChannelByStatus(uniqueStatus)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	_, err = GetChannelById(ch.Id, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// ---------------------------------------------------------------------------
// Read / list / count queries
// ---------------------------------------------------------------------------

func TestGetChannelById_SelectAllVsOmitKey(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)

	full, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, ch.Key, full.Key)

	noKey, err := GetChannelById(ch.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "", noKey.Key, "key omitted")

	_, err = GetChannelById(ch.Id+424242, true)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetAllChannelsAndTypeQueries(t *testing.T) {
	requireDB(t)
	const chType = 4242
	ch := mkChannel(t, func(c *Channel) { c.Type = chType })

	all, err := GetAllChannels(0, 10, true, true)
	require.NoError(t, err)
	assert.NotEmpty(t, all)

	byType, err := GetChannelsByType(0, 100, true, chType)
	require.NoError(t, err)
	require.Len(t, byType, 1)
	assert.Equal(t, ch.Id, byType[0].Id)

	cnt, err := CountChannelsByType(chType)
	require.NoError(t, err)
	assert.EqualValues(t, 1, cnt)

	total, err := CountAllChannels()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))

	grouped, err := CountChannelsGroupByType()
	require.NoError(t, err)
	assert.EqualValues(t, 1, grouped[chType])
}

func TestGetEnabledChannelsForBalanceRefresh(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.Status = common.ChannelStatusEnabled })
	channels, err := GetEnabledChannelsForBalanceRefresh()
	require.NoError(t, err)
	found := false
	for _, c := range channels {
		if c.Id == ch.Id {
			found = true
			assert.Equal(t, "", c.Key, "key omitted for balance refresh")
		}
	}
	assert.True(t, found)
}

func TestGetChannelsByIdsAndNames(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.Name = uniq("named") })

	list, err := GetChannelsByIds([]int{ch.Id})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, ch.Id, list[0].Id)

	names, err := GetChannelNamesByIds([]int{ch.Id})
	require.NoError(t, err)
	assert.Equal(t, ch.Name, names[ch.Id])

	// context variant + empty ids
	empty, err := GetChannelNamesByIdsWithContext(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestGetChannelsByGroupAndTag(t *testing.T) {
	requireDB(t)
	grp := uniq("cg")
	tag := uniq("ctag")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Tag = &tag
	})

	// empty group short-circuits to empty
	empty, err := GetChannelsByGroup("  ")
	require.NoError(t, err)
	assert.Empty(t, empty)

	byGroup, err := GetChannelsByGroup(grp)
	require.NoError(t, err)
	require.Len(t, byGroup, 1)
	assert.Equal(t, ch.Id, byGroup[0].Id)

	byTag, err := GetChannelsByTag(tag, true, false)
	require.NoError(t, err)
	require.Len(t, byTag, 1)
	assert.Equal(t, ch.Id, byTag[0].Id)
	assert.Equal(t, "", byTag[0].Key, "selectAll=false omits key")

	byTagFull, err := GetChannelsByTag(tag, false, true)
	require.NoError(t, err)
	require.Len(t, byTagFull, 1)
	assert.Equal(t, ch.Key, byTagFull[0].Key)
}

func TestSearchChannelsAndTags(t *testing.T) {
	requireDB(t)
	grp := uniq("sc")
	tag := uniq("stag")
	name := uniq("scname")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
		c.Name = name
		c.Tag = &tag
	})

	found, err := SearchChannels(name, grp, "gpt-4o", true)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, ch.Id, found[0].Id)
	assert.Equal(t, "", found[0].Key, "search omits key")

	tags, err := SearchTags(name, grp, "gpt-4o", true)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	require.NotNil(t, tags[0])
	assert.Equal(t, tag, *tags[0])
}

func TestTagPaginationAndCount(t *testing.T) {
	requireDB(t)
	tag := uniq("ptag")
	mkChannel(t, func(c *Channel) { c.Tag = &tag })

	total, err := CountAllTags()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))

	// page through all tags; our tag must appear somewhere
	tags, err := GetPaginatedTags(0, 100000)
	require.NoError(t, err)
	found := false
	for _, tg := range tags {
		if tg != nil && *tg == tag {
			found = true
		}
	}
	assert.True(t, found)
}

// ---------------------------------------------------------------------------
// Update helpers: response time / balance / used quota
// ---------------------------------------------------------------------------

func TestChannel_UpdateResponseTimeBalanceQuota(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)

	ch.UpdateResponseTime(321)
	ch.UpdateBalance(12.5)
	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 321, got.ResponseTime)
	assert.InDelta(t, 12.5, got.Balance, 0.0001)
	assert.Greater(t, got.TestTime, int64(0))
	assert.Greater(t, got.BalanceUpdatedTime, int64(0))

	// used-quota accumulation (BatchUpdateEnabled is false in tests)
	require.False(t, common.BatchUpdateEnabled)
	UpdateChannelUsedQuota(ch.Id, 100)
	updateChannelUsedQuota(ch.Id, 50)
	got, err = GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.EqualValues(t, 150, got.UsedQuota)
}

// ---------------------------------------------------------------------------
// Status updates + tag operations
// ---------------------------------------------------------------------------

func TestUpdateChannelStatus_SingleKey(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	ch := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusEnabled
		c.Models = "gpt-4o"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// enabled -> disabled: returns true, persists, disables abilities
	ok := UpdateChannelStatus(ch.Id, "", common.ChannelStatusManuallyDisabled, "manual")
	assert.True(t, ok)
	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status)

	// same status again -> false (no-op)
	assert.False(t, UpdateChannelStatus(ch.Id, "", common.ChannelStatusManuallyDisabled, "manual"))

	// missing channel -> false
	assert.False(t, UpdateChannelStatus(ch.Id+999999, "", common.ChannelStatusEnabled, "x"))
}

func TestChannel_Save(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.Name = "sv0" })
	ch.Name = "sv1"
	require.NoError(t, ch.Save())
	got, err := GetChannelById(ch.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "sv1", got.Name)
}

func TestUpdateChannelStatus_MemoryCachePaths(t *testing.T) {
	requireDB(t)

	// single-key channel through the memory-cache branch
	single := mkChannelWithAbilities(t, func(c *Channel) {
		c.Status = common.ChannelStatusEnabled
		c.Models = "gpt-4o"
	})
	// multi-key channel through the memory-cache branch
	multi := mkChannelWithAbilities(t, func(c *Channel) {
		c.Status = common.ChannelStatusEnabled
		c.Models = "gpt-4o"
		c.Key = "mk0\nmk1"
		c.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom}
	})
	require.NoError(t, multi.SaveChannelInfo())

	enableMemoryCache(t)

	// single-key: enabled -> disabled via cache + DB
	assert.True(t, UpdateChannelStatus(single.Id, "", common.ChannelStatusManuallyDisabled, "manual"))
	got, err := GetChannelById(single.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status)

	// multi-key: disabling the only used key updates the per-key status list
	UpdateChannelStatus(multi.Id, "mk0", common.ChannelStatusManuallyDisabled, "bad key")
	got, err = GetChannelById(multi.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.ChannelInfo.MultiKeyStatusList[0])
}

func TestChannelTagOps_EnableDisableEdit(t *testing.T) {
	requireDB(t)
	tag := uniq("tagops")
	ch := mkChannel(t, func(c *Channel) {
		c.Tag = &tag
		c.Models = "gpt-4o"
		c.Group = "default"
		c.Status = common.ChannelStatusEnabled
		c.Priority = common.GetPointer[int64](1)
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	require.NoError(t, DisableChannelByTag(tag))
	got, _ := GetChannelById(ch.Id, true)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status)

	require.NoError(t, EnableChannelByTag(tag))
	got, _ = GetChannelById(ch.Id, true)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)

	// EditChannelByTag without model/group change -> updates priority via UpdateAbilityByTag
	newPriority := int64(77)
	require.NoError(t, EditChannelByTag(tag, nil, nil, nil, nil, &newPriority, nil, nil, nil))
	got, _ = GetChannelById(ch.Id, true)
	assert.EqualValues(t, 77, got.GetPriority())

	// EditChannelByTag WITH models+group+newTag change -> recreates abilities
	newTag := uniq("tagops2")
	newModels := "gpt-4o,claude-3"
	newGroup := "default,vip"
	require.NoError(t, EditChannelByTag(tag, &newTag, nil, &newModels, &newGroup, nil, nil, nil, nil))
	assert.EqualValues(t, 4, countAbilities(t, ch.Id)) // 2 groups x 2 models
	got, _ = GetChannelById(ch.Id, true)
	assert.Equal(t, newTag, got.GetTag())
}

func TestBatchSetChannelTag(t *testing.T) {
	requireDB(t)
	c1 := mkChannel(t, func(c *Channel) { c.Models = "gpt-4o"; c.Status = common.ChannelStatusEnabled })
	c2 := mkChannel(t, func(c *Channel) { c.Models = "gpt-4o"; c.Status = common.ChannelStatusEnabled })
	require.NoError(t, c1.AddAbilities(nil))
	require.NoError(t, c2.AddAbilities(nil))
	cleanupAbilities(t, c1.Id)
	cleanupAbilities(t, c2.Id)

	tag := uniq("btag")
	require.NoError(t, BatchSetChannelTag([]int{c1.Id, c2.Id}, &tag))

	got, _ := GetChannelById(c1.Id, true)
	assert.Equal(t, tag, got.GetTag())
}

// ---------------------------------------------------------------------------
// Multi-key helpers
// ---------------------------------------------------------------------------

func TestHasEnabledMultiKey(t *testing.T) {
	keys := []string{"k0", "k1"}
	// nil status list -> all enabled
	assert.True(t, hasEnabledMultiKey(keys, nil))
	// one disabled, one implicitly enabled
	assert.True(t, hasEnabledMultiKey(keys, map[int]int{0: common.ChannelStatusManuallyDisabled}))
	// all disabled
	assert.False(t, hasEnabledMultiKey(keys, map[int]int{
		0: common.ChannelStatusManuallyDisabled,
		1: common.ChannelStatusManuallyDisabled,
	}))
}

func TestHandlerMultiKeyUpdate(t *testing.T) {
	// no keys -> whole channel status set
	c := &Channel{Key: ""}
	handlerMultiKeyUpdate(c, "any", common.ChannelStatusManuallyDisabled, "r")
	assert.Equal(t, common.ChannelStatusManuallyDisabled, c.Status)

	// key found -> per-key status list updated, disabling the only key auto-disables channel
	c = &Channel{Key: "k0", Status: common.ChannelStatusEnabled}
	c.ChannelInfo.IsMultiKey = true
	handlerMultiKeyUpdate(c, "k0", common.ChannelStatusManuallyDisabled, "bad key")
	assert.Equal(t, common.ChannelStatusManuallyDisabled, c.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, c.Status, "all keys disabled -> auto disabled")

	// re-enabling the key restores channel enabled
	handlerMultiKeyUpdate(c, "k0", common.ChannelStatusEnabled, "")
	_, exists := c.ChannelInfo.MultiKeyStatusList[0]
	assert.False(t, exists, "enabling deletes the disabled entry")
	assert.Equal(t, common.ChannelStatusEnabled, c.Status)

	// usingKey not found and non-empty -> no change
	c = &Channel{Key: "k0\nk1", Status: common.ChannelStatusEnabled}
	handlerMultiKeyUpdate(c, "missing", common.ChannelStatusManuallyDisabled, "r")
	assert.Equal(t, common.ChannelStatusEnabled, c.Status)

	// usingKey empty and not found -> whole channel status set
	c = &Channel{Key: "k0\nk1", Status: common.ChannelStatusEnabled}
	handlerMultiKeyUpdate(c, "", common.ChannelStatusManuallyDisabled, "reason")
	assert.Equal(t, common.ChannelStatusManuallyDisabled, c.Status)
}

func TestGetNextEnabledKey(t *testing.T) {
	requireDB(t)

	// non multi-key -> returns raw key, index 0
	c := &Channel{Key: "single-key"}
	k, idx, apiErr := c.GetNextEnabledKey()
	assert.Nil(t, apiErr)
	assert.Equal(t, "single-key", k)
	assert.Equal(t, 0, idx)

	// multi-key random mode
	ch := mkChannel(t, func(c *Channel) {
		c.Key = "k0\nk1\nk2"
		c.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModeRandom}
	})
	k, idx, apiErr = ch.GetNextEnabledKey()
	assert.Nil(t, apiErr)
	assert.Contains(t, []string{"k0", "k1", "k2"}, k)
	assert.GreaterOrEqual(t, idx, 0)

	// all keys disabled -> error
	ch2 := mkChannel(t, func(c *Channel) {
		c.Key = "k0\nk1"
		c.ChannelInfo = ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusManuallyDisabled,
				1: common.ChannelStatusManuallyDisabled,
			},
		}
	})
	_, _, apiErr = ch2.GetNextEnabledKey()
	assert.NotNil(t, apiErr)

	// polling mode advances the index and returns an enabled key
	require.False(t, common.MemoryCacheEnabled)
	ch3 := mkChannel(t, func(c *Channel) {
		c.Key = "k0\nk1\nk2"
		c.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModePolling}
	})
	k, idx, apiErr = ch3.GetNextEnabledKey()
	assert.Nil(t, apiErr)
	assert.Equal(t, "k0", k)
	assert.Equal(t, 0, idx)
	assert.Equal(t, 1, ch3.ChannelInfo.MultiKeyPollingIndex, "polling index advanced to next slot")
}

func TestGetChannelPollingLockAndCleanup(t *testing.T) {
	requireDB(t)
	id := nextTestID()
	l1 := GetChannelPollingLock(id)
	l2 := GetChannelPollingLock(id)
	assert.Same(t, l1, l2, "same lock instance for same channel id")

	// cleanup removes locks for channels absent from DB
	CleanupChannelPollingLocks()
	l3 := GetChannelPollingLock(id)
	assert.NotSame(t, l1, l3, "stale lock evicted -> new instance created")
}

// ---------------------------------------------------------------------------
// Name resolution + display options
// ---------------------------------------------------------------------------

func TestCaptureChannelNamesForSnapshot(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.Name = uniq("snap") })
	// empty ids -> empty map
	assert.Empty(t, captureChannelNamesForSnapshot(DB, nil))

	got := captureChannelNamesForSnapshot(DB, []int{ch.Id})
	assert.Equal(t, ch.Name, got[ch.Id])
}

func TestResolveChannelDisplayNames_LiveChannel(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	ch := mkChannel(t, func(c *Channel) { c.Name = uniq("disp") })

	// id 0 and duplicates are de-duplicated; live name resolved from channels table
	res := ResolveChannelDisplayNamesWithoutLogs([]int{0, ch.Id, ch.Id})
	assert.Equal(t, ch.Name, res[ch.Id])

	res = ResolveChannelDisplayNames([]int{ch.Id})
	assert.Equal(t, ch.Name, res[ch.Id])
}

func TestResolveChannelDisplayNames_MemoryCache(t *testing.T) {
	requireDB(t)
	ch := mkChannelWithAbilities(t, func(c *Channel) { c.Name = uniq("dispmc") })
	enableMemoryCache(t)
	res := ResolveChannelDisplayNamesWithoutLogs([]int{ch.Id})
	assert.Equal(t, ch.Name, res[ch.Id])
}

func TestFinalizeChannelDeletion_Synchronous(t *testing.T) {
	requireDB(t)
	// best-effort finalize must not error even when there are no daily-stat rows
	// to backfill and Redis is disabled.
	finalizeChannelDeletion(map[int]string{nextTestID(): "gone"}, []int{nextTestID()})
	// async wrapper with empty inputs returns immediately
	finalizeChannelDeletionAsync(nil, nil)
}

func TestListChannelDisplayOptions(t *testing.T) {
	requireDB(t)
	// smoke: pagination query must succeed regardless of dataset contents
	opts, total, err := ListChannelDisplayOptions(1, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(0))
	assert.NotNil(t, opts)
}

// sanity: ensure key string round-trips through GetKeys splitting stably
func TestChannel_GetKeys_Whitespace(t *testing.T) {
	c := &Channel{Key: "\nk0\nk1\n"}
	assert.Equal(t, []string{"k0", "k1"}, c.GetKeys())
	assert.False(t, strings.HasPrefix(c.Key, "["))
}
