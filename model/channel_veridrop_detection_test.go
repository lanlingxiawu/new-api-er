package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestListChannelVeridropDetectionsFiltersAndPages(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ChannelVeridropDetection{}))

	baseChannelID := 880000000 + int(time.Now().UnixNano()%1000000)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id >= ? AND channel_id < ?", baseChannelID, baseChannelID+10).Delete(&ChannelVeridropDetection{}).Error)
	})

	now := common.GetTimestamp()
	rows := []*ChannelVeridropDetection{
		{ChannelID: baseChannelID, ChannelName: "vd-a", Protocol: "openai", Model: "gpt-5", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 90, Verdict: "pass", Summary: "healthy upstream", UpdatedAt: now},
		{ChannelID: baseChannelID, ChannelName: "vd-a", Protocol: "openai", Model: "gpt-4o", Mode: "standard", Status: ChannelVeridropDetectionError, Error: "failed", UpdatedAt: now - 86400*3},
		{ChannelID: baseChannelID + 1, ChannelName: "vd-b", Protocol: "anthropic", Model: "claude", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 80, Verdict: "usable", Summary: "slow but usable", UpdatedAt: now},
	}
	for _, row := range rows {
		require.NoError(t, CreateChannelVeridropDetection(row))
	}

	got, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		Protocol:  "openai",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Greater(t, got[0].ID, got[1].ID)

	done, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		Status:    string(ChannelVeridropDetectionDone),
		BeforeID:  got[0].ID,
		Limit:     1,
	})
	require.NoError(t, err)
	require.Len(t, done, 1)
	require.Less(t, done[0].ID, got[0].ID)

	minScore := 85.0
	maxScore := 95.0
	ranged, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName:  "vd-a",
		Model:        "gpt",
		Mode:         "quick",
		Verdict:      "pass",
		Keyword:      "healthy",
		MinScore:     &minScore,
		MaxScore:     &maxScore,
		UpdatedAfter: now - 3600,
		Limit:        10,
	})
	require.NoError(t, err)
	require.Len(t, ranged, 1)
	require.Equal(t, "gpt-5", ranged[0].Model)

	withError, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		ErrorOnly: true,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, withError, 1)
	require.Equal(t, "gpt-4o", withError[0].Model)

	capped, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{Limit: 500})
	require.NoError(t, err)
	require.LessOrEqual(t, len(capped), 100)
}

func TestFindEnabledChannelsForVeridropAfterID(t *testing.T) {
	baseID := 881000000 + int(time.Now().UnixNano()%1000000)
	ids := []int{baseID, baseID + 1, baseID + 2}
	t.Cleanup(func() {
		require.NoError(t, DB.Where("id IN ?", ids).Delete(&Channel{}).Error)
	})

	channels := []*Channel{
		{Id: ids[0], Name: fmt.Sprintf("veridrop-enabled-a-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-a", Status: common.ChannelStatusEnabled, Models: "gpt-5", Group: "default"},
		{Id: ids[1], Name: fmt.Sprintf("veridrop-disabled-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-b", Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4o", Group: "default"},
		{Id: ids[2], Name: fmt.Sprintf("veridrop-enabled-b-%d", baseID), Type: constant.ChannelTypeAnthropic, Key: "sk-c", Status: common.ChannelStatusEnabled, Models: "claude", Group: "default"},
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
	}

	got, err := FindEnabledChannelsForVeridropAfterID(baseID-1, 10, nil)
	require.NoError(t, err)
	gotIDs := make([]int, 0, len(got))
	for _, channel := range got {
		if channel.Id >= baseID && channel.Id <= baseID+2 {
			gotIDs = append(gotIDs, channel.Id)
		}
	}
	require.Equal(t, []int{ids[0], ids[2]}, gotIDs)

	filtered, err := FindEnabledChannelsForVeridropAfterID(baseID-1, 10, []int{ids[2]})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, ids[2], filtered[0].Id)
}
