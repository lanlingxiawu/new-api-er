package service

import (
	"reflect"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCostCommissionSnapshotRetainsNoReferenceTypes(t *testing.T) {
	typ := reflect.TypeOf(costCommissionSnapshot{})
	for i := 0; i < typ.NumField(); i++ {
		kind := typ.Field(i).Type.Kind()
		require.NotContains(t, []reflect.Kind{
			reflect.Pointer, reflect.Map, reflect.Interface, reflect.Func,
			reflect.Chan, reflect.Slice, reflect.UnsafePointer,
		}, kind, typ.Field(i).Name)
	}
}

// TestSnapshotCostAndCommissionToleratesMissingChannelMeta is the regression for
// a relay-goroutine panic: ChannelId/ChannelName are promoted from the embedded
// *ChannelMeta, so reading them before InitChannelMeta ran dereferenced nil.
// FinalizeConsumptionSettlement nil-checks ChannelMeta a few lines earlier, so
// this state is reachable, not theoretical.
func TestSnapshotCostAndCommissionToleratesMissingChannelMeta(t *testing.T) {
	info := &relaycommon.RelayInfo{UserId: 42, UsingGroup: "vip", OriginModelName: "gpt-4o"}
	info.PriceData.GroupRatioInfo.GroupRatio = 1.5
	require.Nil(t, info.ChannelMeta)

	var snapshot costCommissionSnapshot
	require.NotPanics(t, func() { snapshot = snapshotCostAndCommission(info, 700, 30) })

	assert.Equal(t, 42, snapshot.UserID)
	assert.Equal(t, "vip", snapshot.UsingGroup)
	assert.Equal(t, "gpt-4o", snapshot.OriginModelName)
	assert.Equal(t, 1.5, snapshot.GroupRatio)
	assert.Equal(t, 700, snapshot.Quota)
	assert.Equal(t, int64(30), snapshot.SurchargeQuota)
	assert.Zero(t, snapshot.ChannelID)
	assert.Empty(t, snapshot.ChannelName)
}

func TestSnapshotCostAndCommissionCopiesChannelMeta(t *testing.T) {
	info := &relaycommon.RelayInfo{
		UserId:      7,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 9, ChannelName: "azure-east"},
	}

	snapshot := snapshotCostAndCommission(info, 100, 0)

	assert.Equal(t, 9, snapshot.ChannelID)
	assert.Equal(t, "azure-east", snapshot.ChannelName)
}

func TestSnapshotCostAndCommissionToleratesNilRelayInfo(t *testing.T) {
	var snapshot costCommissionSnapshot
	require.NotPanics(t, func() { snapshot = snapshotCostAndCommission(nil, 5, 1) })

	assert.Equal(t, 5, snapshot.Quota)
	assert.Equal(t, int64(1), snapshot.SurchargeQuota)
	assert.Zero(t, snapshot.UserID)
}
