package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreserveChannelProbeMode(t *testing.T) {
	origin := &Channel{OtherSettings: `{"disable_probe_requests":true,"probe_request_mode":"stream"}`}
	for _, tc := range []struct {
		incoming, mode   string
		enabled, invalid bool
	}{
		{"", "stream", true, false},
		{`{}`, "stream", true, false},
		{`{"allow_speed":true}`, "stream", true, false},
		{`{"disable_probe_requests":false}`, "stream", false, false},
		{`{"probe_request_mode":"non_stream"}`, "non_stream", true, false},
		{`{"probe_request_mode":"all"}`, "all", true, false},
		{`{"probe_request_mode":""}`, "", true, false},
		{`{"probe_request_mode":"unknown"}`, "", false, true},
		{`{"probe_request_mode":true}`, "", false, true},
		{`{"probe_request_mode":null}`, "", false, true},
		{`{"disable_probe_requests":null}`, "", false, true},
		{`invalid`, "", false, true},
	} {
		t.Run(tc.incoming, func(t *testing.T) {
			channel := &Channel{OtherSettings: tc.incoming}
			err := channel.PreserveProbeSetting(origin)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.enabled, channel.GetOtherSettings().DisableProbeRequests)
			require.Equal(t, tc.mode, channel.GetOtherSettings().ProbeRequestMode)
		})
	}
	for _, mode := range []string{"all", "non_stream", "stream", ""} {
		channel := &Channel{OtherSettings: `{"probe_request_mode":"` + mode + `"}`}
		require.NoError(t, channel.PreserveProbeSetting(nil))
		require.Equal(t, mode, channel.GetOtherSettings().ProbeRequestMode)
		require.False(t, channel.GetOtherSettings().DisableProbeRequests)
	}
	channel := &Channel{OtherSettings: `{"probe_request_mode":"invalid"}`}
	require.Error(t, channel.PreserveProbeSetting(nil))
}

func TestProbeModePolicyRouting(t *testing.T) {
	// Prefer the configured project DB. SQLite fallback uses only portable
	// GORM CRUD; it does not validate MySQL/PostgreSQL locking or collation.
	if DB == nil {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		DB = db
		InitColumnNames()
		require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
		t.Cleanup(func() { DB = nil; require.NoError(t, sqlDB.Close()) })
	}
	oldPolicy, oldMemory := channelProbePolicy.Load(), common.MemoryCacheEnabled
	oldGroups, oldChannels := group2model2channels, channelsIDM
	t.Cleanup(func() {
		channelProbePolicy.Store(oldPolicy)
		common.MemoryCacheEnabled = oldMemory
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	group := uniq("probe-modes")
	var channels []*Channel
	for i, mode := range []string{"all", "non_stream", "stream", ""} {
		channel := mkChannelWithAbilities(t, func(ch *Channel) {
			ch.Group, ch.Models = group, group
			ch.Priority = common.GetPointer(int64(100 - i))
			ch.OtherSettings = `{"disable_probe_requests":true,"probe_request_mode":"` + mode + `"}`
		})
		channels = append(channels, channel)
	}
	for _, memory := range []bool{false, true} {
		common.MemoryCacheEnabled = memory
		InitChannelCache()
		policy := CurrentChannelProbePolicy()
		require.NotNil(t, policy)
		for _, stream := range []bool{false, true} {
			selectedPolicy := policy.ForStream(stream)
			require.NotNil(t, selectedPolicy)
			require.True(t, selectedPolicy.Blocks(channels[0].Id))
			require.True(t, selectedPolicy.Blocks(channels[3].Id))
			require.Equal(t, !stream, selectedPolicy.Blocks(channels[1].Id))
			require.Equal(t, stream, selectedPolicy.Blocks(channels[2].Id))
			want := channels[2].Id
			if stream {
				want = channels[1].Id
			}
			for _, retry := range []int{0, 1, 9} {
				selected, err := GetRandomSatisfiedChannel(group, group, retry, "", selectedPolicy)
				require.NoError(t, err)
				require.Equal(t, want, selected.Id)
			}
		}
	}
	pinned := CurrentChannelProbePolicy().ForStream(true)
	for _, invalid := range []string{
		`{"disable_probe_requests":true,"probe_request_mode":"unknown"}`,
		`{"disable_probe_requests":true,"probe_request_mode":false}`,
	} {
		previous := CurrentChannelProbePolicy()
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channels[0].Id).Update("settings", invalid).Error)
		RefreshChannelProbePolicy()
		require.Same(t, previous, CurrentChannelProbePolicy(), "invalid saved settings retain the previous snapshot")
	}
	for _, ch := range channels {
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", ch.Id).Update("settings", `{"disable_probe_requests":false,"probe_request_mode":"stream"}`).Error)
	}
	RefreshChannelProbePolicy()
	require.True(t, pinned.Blocks(channels[0].Id))
	policy := CurrentChannelProbePolicy()
	if policy != nil { // Other configured channels may exist in a real DB.
		require.False(t, policy.ForStream(true).Blocks(channels[0].Id))
	}
	require.Nil(t, (&ChannelProbePolicy{}).ForStream(false))
	require.Nil(t, (&ChannelProbePolicy{}).ForStream(true))
	channelProbePolicy.Store(&ChannelProbePolicy{streaming: pinned, refreshedAt: time.Now().Add(-4 * time.Minute)})
	require.Nil(t, CurrentChannelProbePolicy())
}

func TestProbeOnlyStreamingSnapshot(t *testing.T) {
	previous := channelProbePolicy.Load()
	t.Cleanup(func() { channelProbePolicy.Store(previous) })
	streaming := &ChannelProbePolicy{blocked: map[int]bool{1: true}}
	channelProbePolicy.Store(&ChannelProbePolicy{streaming: streaming, refreshedAt: time.Now()})
	policy := CurrentChannelProbePolicy()
	require.NotNil(t, policy)
	require.Nil(t, policy.ForStream(false), "non-stream requests must retain ordinary routing and affinity")
	require.Same(t, streaming, policy.ForStream(true))
	require.True(t, policy.ForStream(true).Blocks(1))
}

func BenchmarkProbeModeSelection(b *testing.B) {
	previous := channelProbePolicy.Load()
	b.Cleanup(func() { channelProbePolicy.Store(previous) })
	channelProbePolicy.Store(&ChannelProbePolicy{
		blocked:     map[int]bool{1: true},
		streaming:   &ChannelProbePolicy{blocked: map[int]bool{1: true}},
		refreshedAt: time.Now(),
	})
	b.ReportAllocs()
	blocked := false
	for i := 0; i < b.N; i++ {
		policy := CurrentChannelProbePolicy().ForStream(i%2 == 0)
		blocked = policy.Blocks(1)
	}
	b.StopTimer()
	require.True(b, blocked)
}
