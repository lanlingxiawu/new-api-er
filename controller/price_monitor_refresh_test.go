package controller

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refreshTestSnapshot: one model priced 10 on the platform; channel-a lists it
// at 12 (a measured loss at sell factor 1), official at 8, models.dev missing.
func refreshTestSnapshot() PriceMonitorSnapshot {
	ratio := 1.0
	return PriceMonitorSnapshot{
		CheckedAt:     1_800_000_000,
		MatrixVersion: priceMonitorMatrixVersion,
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "models-dev", Type: priceSourceModelsDev},
			{Key: "channel-a(1)", Type: priceSourceChannel, ChannelId: 1},
		},
		MatrixItems: []PriceMonitorMatrixItem{{
			Model: "refresh-model",
			Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: {Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)},
				"official":              {Mode: priceMonitorModeToken, Input: floatPointer(8), Output: floatPointer(20), Different: true, InputDifferent: true},
				"models-dev":            {Different: true, UnavailableReason: priceMonitorUnavailableMissing},
				"channel-a(1)": {
					Mode: priceMonitorModeToken, Input: floatPointer(12), Output: floatPointer(20), Different: true, InputDifferent: true,
					LossKinds: []string{priceMonitorLossKindMeasured}, SellFactor: floatPointer(1), MeasuredFactor: floatPointer(1.2), ConfiguredFactor: floatPointer(1),
				},
			},
		}},
		ChannelCosts: map[string]PriceMonitorChannelCost{
			"1": {ChannelId: 1, SourceKey: "channel-a(1)", UpstreamRatio: &ratio, Status: priceMonitorCostStatusMatch},
		},
	}
}

func withRefreshSeams(t *testing.T, modelRatio float64, costRatio float64) {
	t.Helper()
	pricing, channels, cost, configured := priceMonitorRefreshPricingData, priceMonitorRefreshChannels, priceMonitorRefreshCostRatio, priceMonitorRefreshConfigured
	t.Cleanup(func() {
		priceMonitorRefreshPricingData, priceMonitorRefreshChannels, priceMonitorRefreshCostRatio, priceMonitorRefreshConfigured = pricing, channels, cost, configured
	})
	priceMonitorRefreshPricingData = func() map[string]any {
		return map[string]any{
			"model_ratio":      map[string]float64{"refresh-model": modelRatio},
			"completion_ratio": map[string]float64{"refresh-model": 2 * 10 / (modelRatio * 2)},
		}
	}
	// No groups: the sell factor falls back to 1, keeping the verdict arithmetic obvious.
	priceMonitorRefreshChannels = func() ([]*model.Channel, error) { return []*model.Channel{{Id: 1, Name: "channel-a"}}, nil }
	priceMonitorRefreshCostRatio = func(int) float64 { return costRatio }
	priceMonitorRefreshConfigured = func() map[int]bool { return map[int]bool{1: true} }
}

func TestRecomputeAfterRepricingClearsDifferenceAndLoss(t *testing.T) {
	withRefreshSeams(t, 6, 1) // platform input becomes 12, matching channel-a
	base := refreshTestSnapshot()
	next, err := recomputePriceMonitorSnapshot(base)
	require.NoError(t, err)

	row := next.MatrixItems[0]
	assert.InDelta(t, 12, *row.Prices[priceMonitorPlatformKey].Input, 1e-9, "the platform cell is rebuilt from current pricing")
	channel := row.Prices["channel-a(1)"]
	assert.False(t, channel.InputDifferent)
	assert.Empty(t, channel.LossKinds, "12 upstream vs 12 sold: no loss any more")
	assert.True(t, row.Prices["official"].Different, "official 8 now differs from 12")
	assert.Equal(t, priceMonitorUnavailableMissing, row.Prices["models-dev"].UnavailableReason, "missing cells are kept as they are")

	require.NotNil(t, row.RepairFloor)
	assert.Equal(t, 6.0, row.RepairFloor.Current["model_ratio"], "Current comes from the live option value, which the snapshot file does not store")
	assert.Zero(t, next.ComparisonModelCounts.LossRisk)

	// base is shared with readers and must not change.
	assert.True(t, base.MatrixItems[0].Prices["channel-a(1)"].InputDifferent)
	assert.Equal(t, []string{priceMonitorLossKindMeasured}, base.MatrixItems[0].Prices["channel-a(1)"].LossKinds)
}

func TestRecomputeResetsModeDifferent(t *testing.T) {
	withRefreshSeams(t, 5, 1)
	base := refreshTestSnapshot()
	cell := base.MatrixItems[0].Prices["channel-a(1)"]
	cell.ModeDifferent = true
	base.MatrixItems[0].Prices["channel-a(1)"] = cell
	next, err := recomputePriceMonitorSnapshot(base)
	require.NoError(t, err)
	assert.False(t, next.MatrixItems[0].Prices["channel-a(1)"].ModeDifferent, "mark only sets it, so it must be cleared first")
}

func TestRecomputeChecksCostRatioAgainstCurrentValue(t *testing.T) {
	withRefreshSeams(t, 5, 0.7) // cost ratio edited to 0.7 against an upstream ratio of 1
	next, err := recomputePriceMonitorSnapshot(refreshTestSnapshot())
	require.NoError(t, err)
	assert.Equal(t, priceMonitorCostStatusLow, next.ChannelCosts["1"].Status)
	assert.Equal(t, 1, next.ComparisonModelCounts.CostRatioMismatch)
	assert.Equal(t, 0.7, *next.MatrixItems[0].Prices["channel-a(1)"].ConfiguredFactor)
}

func TestRefreshInPlaceSavesOnlyOverTheSameRound(t *testing.T) {
	withRefreshSeams(t, 6, 1)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })

	getPriceMonitorStore()
	previousStore := priceMonitorStore
	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	priceMonitorStore = store
	t.Cleanup(func() { priceMonitorStore = previousStore })

	require.NoError(t, store.Save(refreshTestSnapshot()))
	refreshed, err := refreshPriceMonitorSnapshotInPlace()
	require.NoError(t, err)
	require.True(t, refreshed)
	assert.Empty(t, store.Get().MatrixItems[0].Prices["channel-a(1)"].LossKinds)

	// A newer round replaced the snapshot while a refresh was computing: keep the newer one.
	newer := refreshTestSnapshot()
	newer.CheckedAt++
	require.NoError(t, store.Save(newer))
	require.ErrorIs(t, store.SaveIfCheckedAt(newer.CheckedAt-1, refreshTestSnapshot()), errPriceMonitorSnapshotSuperseded)
	assert.Equal(t, newer.CheckedAt, store.Get().CheckedAt)

	common.IsMasterNode = false
	refreshed, err = refreshPriceMonitorSnapshotInPlace()
	require.NoError(t, err)
	assert.False(t, refreshed, "only the master node maintains the snapshot")
}

func TestPriceMonitorChannelCostListOrdersWorkFirst(t *testing.T) {
	withRefreshSeams(t, 5, 1)
	one, half := 1.0, 0.5
	snapshot := PriceMonitorSnapshot{ChannelCosts: map[string]PriceMonitorChannelCost{
		"1": {ChannelId: 1, UpstreamRatio: &one},  // match
		"2": {ChannelId: 2, UpstreamRatio: &half}, // cost 1 vs 0.5: high
		"3": {ChannelId: 3},                       // unknown
		"4": {ChannelId: 4, UpstreamRatio: &one},  // match
	}}
	list := priceMonitorChannelCostList(snapshot)
	ids := make([]int, len(list))
	for i, cost := range list {
		ids[i] = cost.ChannelId
	}
	assert.Equal(t, []int{2, 3, 1, 4}, ids)
}

// The source fingerprint is internal bookkeeping for the next run: the page
// gets none, and the snapshot keeps its own.
func TestPriceMonitorChannelCostListOmitsSourceFingerprint(t *testing.T) {
	withRefreshSeams(t, 5, 1)
	snapshot := PriceMonitorSnapshot{ChannelCosts: map[string]PriceMonitorChannelCost{
		"1": {ChannelId: 1, SourceFingerprint: "0123456789abcdef"},
	}}
	list := priceMonitorChannelCostList(snapshot)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].SourceFingerprint)
	assert.Equal(t, "0123456789abcdef", snapshot.ChannelCosts["1"].SourceFingerprint)
}

// A full run gives a source without a lane the platform has a price-less
// placeholder lane (not different). Recomputing must treat it the same way,
// not as "the source has this lane with no price".
func TestRecomputeKeepsPlaceholderLanesUnchanged(t *testing.T) {
	pricing := map[string]any{
		"model_ratio":      map[string]float64{"refresh-model": 5},
		"completion_ratio": map[string]float64{"refresh-model": 2},
		"cache_ratio":      map[string]float64{"refresh-model": 0.5},
	}
	withRefreshSeams(t, 5, 1)
	priceMonitorRefreshPricingData = func() map[string]any { return pricing }

	platform, ok := priceMonitorCell(pricing, "refresh-model")
	require.True(t, ok)
	official := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	markPriceMonitorDifferences(platform, &official)
	require.False(t, official.Different, "full run: same input/output, missing cache lane is not a difference")
	require.NotEmpty(t, official.Lanes, "full run leaves a placeholder lane")

	base := refreshTestSnapshot()
	base.MatrixVersion = priceMonitorMatrixVersion
	base.MatrixItems[0].Prices["official"] = official
	next, err := recomputePriceMonitorSnapshot(base)
	require.NoError(t, err)
	assert.False(t, next.MatrixItems[0].Prices["official"].Different)
	assert.Zero(t, next.ComparisonModelCounts.PlatformOfficial)
}

// Snapshots from before the upgrade lack the channel ids a recompute needs.
func TestRefreshInPlaceSkipsOldSnapshots(t *testing.T) {
	withRefreshSeams(t, 6, 1)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	getPriceMonitorStore()
	previousStore := priceMonitorStore
	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	priceMonitorStore = store
	t.Cleanup(func() { priceMonitorStore = previousStore })

	old := refreshTestSnapshot()
	old.MatrixVersion = priceMonitorMatrixVersion - 1
	require.NoError(t, store.Save(old))
	refreshed, err := refreshPriceMonitorSnapshotInPlace()
	require.NoError(t, err)
	assert.False(t, refreshed)
	assert.NotEmpty(t, store.Get().MatrixItems[0].Prices["channel-a(1)"].LossKinds, "left untouched")
}

// A change while a check is running is folded in after that check saves.
func TestRefreshInPlaceDuringACheckMarksPending(t *testing.T) {
	withRefreshSeams(t, 6, 1)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	getPriceMonitorStore()
	previousStore := priceMonitorStore
	priceMonitorStore = newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	t.Cleanup(func() { priceMonitorStore = previousStore })
	priceMonitorRunning.Store(true)
	priceMonitorRefreshPending.Store(false)
	t.Cleanup(func() { priceMonitorRunning.Store(false); priceMonitorRefreshPending.Store(false) })

	_, err := refreshPriceMonitorSnapshotInPlace()
	require.NoError(t, err)
	assert.True(t, priceMonitorRefreshPending.Load())
}

// Dynamic expressions compare by source text, which the snapshot file does not
// keep; after a restart the recompute keeps the full run's result for them.
func TestRecomputeKeepsDynamicExpressionComparison(t *testing.T) {
	expr := `p * 2 + (hour(8) >= 20 ? c * 3 : c * 4)`
	pricing := map[string]any{
		"billing_mode": map[string]string{"refresh-model": "tiered_expr"},
		"billing_expr": map[string]string{"refresh-model": expr},
	}
	withRefreshSeams(t, 5, 1)
	priceMonitorRefreshPricingData = func() map[string]any { return pricing }
	platform, ok := priceMonitorCell(pricing, "refresh-model")
	require.True(t, ok)
	require.True(t, platform.Dynamic, "fixture must be a dynamic expression")

	base := refreshTestSnapshot()
	// As loaded from disk: same expression upstream, found equal, Expr not persisted.
	base.MatrixItems[0].Prices["official"] = PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, Tiers: platform.Tiers}
	next, err := recomputePriceMonitorSnapshot(base)
	require.NoError(t, err)
	assert.False(t, next.MatrixItems[0].Prices["official"].Different)

	// Found different at check time: the marks are kept, not cleared.
	base.MatrixItems[0].Prices["official"] = PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, Tiers: platform.Tiers, Different: true, PriceDifferent: true}
	next, err = recomputePriceMonitorSnapshot(base)
	require.NoError(t, err)
	assert.True(t, next.MatrixItems[0].Prices["official"].Different)
	assert.True(t, next.MatrixItems[0].Prices["official"].PriceDifferent)
}
