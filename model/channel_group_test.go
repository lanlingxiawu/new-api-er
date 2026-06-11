package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetChannelsByGroupMatchesCommaSeparatedGroupsOnSQLite(t *testing.T) {
	if allowTestDBCleanup() {
		require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	}
	t.Cleanup(func() {
		if !allowTestDBCleanup() {
			return
		}
		require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	})

	require.NoError(t, DB.Create(&[]Channel{
		{Id: 101, Name: "default-only", Key: "key-101", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 102, Name: "vip-only", Key: "key-102", Group: "vip", Status: common.ChannelStatusEnabled},
		{Id: 103, Name: "default-and-vip", Key: "key-103", Group: "default,vip", Status: common.ChannelStatusEnabled},
		{Id: 104, Name: "vip2-only", Key: "key-104", Group: "vip2", Status: common.ChannelStatusEnabled},
	}).Error)

	channels, err := GetChannelsByGroup("vip")
	require.NoError(t, err)

	names := make([]string, 0, len(channels))
	for _, channel := range channels {
		names = append(names, channel.Name)
	}
	require.ElementsMatch(t, []string{"vip-only", "default-and-vip"}, names)
}
