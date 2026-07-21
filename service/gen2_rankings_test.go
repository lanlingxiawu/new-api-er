package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// rankings.go — pure aggregation helpers (no DB)
// ---------------------------------------------------------------------------

func TestRankings_Config(t *testing.T) {
	cases := []struct {
		period      string
		id          string
		duration    time.Duration
		bucket      int64
		hasPrevious bool
		wantErr     bool
	}{
		{"", "week", 7 * 24 * time.Hour, 24 * 3600, true, false},
		{"week", "week", 7 * 24 * time.Hour, 24 * 3600, true, false},
		{"today", "today", 24 * time.Hour, 3600, true, false},
		{"month", "month", 30 * 24 * time.Hour, 24 * 3600, true, false},
		{"year", "year", 365 * 24 * time.Hour, 7 * 24 * 3600, true, false},
		{"decade", "", 0, 0, false, true},
	}
	for _, c := range cases {
		cfg, err := rankingConfig(c.period)
		if c.wantErr {
			require.Error(t, err, c.period)
			continue
		}
		require.NoError(t, err, c.period)
		assert.Equal(t, c.id, cfg.id)
		assert.Equal(t, c.duration, cfg.duration)
		assert.Equal(t, c.bucket, cfg.bucketSize)
		assert.Equal(t, c.hasPrevious, cfg.hasPrevious)
	}
}

func TestRankings_TimeRange(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := rankingPeriodConfig{duration: 24 * time.Hour}
	start, end := rankingTimeRange(cfg, now)
	assert.Equal(t, now.Unix(), end)
	assert.Equal(t, now.Add(-24*time.Hour).Unix(), start)

	// zero duration => start 0
	start0, end0 := rankingTimeRange(rankingPeriodConfig{duration: 0}, now)
	assert.Equal(t, int64(0), start0)
	assert.Equal(t, now.Unix(), end0)
}

func TestRankings_PreviousTimeRange(t *testing.T) {
	cfg := rankingPeriodConfig{duration: 24 * time.Hour}
	currentStart := int64(1_700_000_000)
	ps, pe := previousRankingTimeRange(cfg, currentStart)
	assert.Equal(t, currentStart-1, pe)
	assert.Equal(t, time.Unix(currentStart, 0).Add(-24*time.Hour).Unix(), ps)
}

func TestRankings_Share(t *testing.T) {
	assert.Equal(t, 0.0, rankingShare(0, 100))   // value 0
	assert.Equal(t, 0.0, rankingShare(50, 0))    // total 0
	assert.Equal(t, 0.0, rankingShare(-5, 100))  // negative value
	assert.Equal(t, 0.25, rankingShare(25, 100)) // normal
	// rounding to 4 decimals
	assert.Equal(t, 0.3333, rankingShare(1, 3))
}

func TestRankings_GrowthPct(t *testing.T) {
	assert.Equal(t, 100.0, rankingGrowthPct(10, 0)) // no previous, current>0
	assert.Equal(t, 0.0, rankingGrowthPct(0, 0))    // no previous, current 0
	assert.Equal(t, 0.0, rankingGrowthPct(0, -5))   // previous <=0
	assert.Equal(t, 50.0, rankingGrowthPct(150, 100))
	assert.Equal(t, -50.0, rankingGrowthPct(50, 100))
}

func TestRankings_RoundFloat(t *testing.T) {
	assert.Equal(t, 0.1235, roundRankingFloat(0.12345678))
	assert.Equal(t, 1.0, roundRankingFloat(1.0))
}

func TestRankings_MinInt(t *testing.T) {
	assert.Equal(t, 3, minInt(3, 5))
	assert.Equal(t, 3, minInt(5, 3))
	assert.Equal(t, 4, minInt(4, 4))
}

func TestRankings_MapsAndSum(t *testing.T) {
	totals := []model.RankingQuotaTotal{
		{ModelName: "gpt-4", TotalTokens: 300},
		{ModelName: "claude", TotalTokens: 200},
		{ModelName: "gemini", TotalTokens: 100},
	}
	assert.Equal(t, int64(600), sumRankingTokens(totals))

	ranks := rankingRankMap(totals)
	assert.Equal(t, 1, ranks["gpt-4"])
	assert.Equal(t, 2, ranks["claude"])
	assert.Equal(t, 3, ranks["gemini"])

	toks := rankingTokenMap(totals)
	assert.Equal(t, int64(300), toks["gpt-4"])
	assert.Equal(t, int64(100), toks["gemini"])

	assert.Equal(t, int64(0), sumRankingTokens(nil))
}

func TestRankings_ModelMeta(t *testing.T) {
	meta := map[string]rankingModelMeta{
		"gpt-4": {vendor: "OpenAI", vendorIcon: "icon"},
		"empty": {vendor: ""},
	}
	got := modelMeta("gpt-4", meta)
	assert.Equal(t, "OpenAI", got.vendor)
	// empty vendor falls back to Unknown
	assert.Equal(t, rankingUnknownVendor, modelMeta("empty", meta).vendor)
	// missing model falls back to Unknown
	assert.Equal(t, rankingUnknownVendor, modelMeta("missing", meta).vendor)
}

func TestRankings_EnsureVendorAggregate(t *testing.T) {
	agg := map[string]*vendorAggregate{}
	a := ensureVendorAggregate(agg, rankingModelMeta{vendor: "OpenAI", vendorIcon: ""})
	assert.Equal(t, "OpenAI", a.name)
	// second call returns same aggregate and fills icon
	b := ensureVendorAggregate(agg, rankingModelMeta{vendor: "OpenAI", vendorIcon: "logo"})
	assert.Same(t, a, b)
	assert.Equal(t, "logo", b.icon)
	// empty vendor -> Unknown
	c := ensureVendorAggregate(agg, rankingModelMeta{vendor: ""})
	assert.Equal(t, rankingUnknownVendor, c.name)
}

func TestRankings_BuildRankedModels(t *testing.T) {
	totals := []model.RankingQuotaTotal{
		{ModelName: "gpt-4", TotalTokens: 600},
		{ModelName: "claude", TotalTokens: 400},
	}
	meta := map[string]rankingModelMeta{
		"gpt-4":  {vendor: "OpenAI", vendorIcon: "o"},
		"claude": {vendor: "Anthropic"},
	}
	prevRanks := map[string]int{"gpt-4": 2}
	prevTokens := map[string]int64{"gpt-4": 300, "claude": 400}

	rows := buildRankedModels(totals, 1000, prevRanks, prevTokens, meta, true)
	require.Len(t, rows, 2)
	assert.Equal(t, 1, rows[0].Rank)
	assert.Equal(t, "gpt-4", rows[0].ModelName)
	assert.Equal(t, "OpenAI", rows[0].Vendor)
	require.NotNil(t, rows[0].PreviousRank)
	assert.Equal(t, 2, *rows[0].PreviousRank)
	assert.Equal(t, 0.6, rows[0].Share)
	assert.Equal(t, 100.0, rows[0].GrowthPct) // 600 vs 300
	// claude has no previous rank
	assert.Nil(t, rows[1].PreviousRank)
	assert.Equal(t, 0.0, rows[1].GrowthPct) // 400 vs 400

	// showGrowth=false zeroes growth
	rows2 := buildRankedModels(totals, 1000, prevRanks, prevTokens, meta, false)
	assert.Equal(t, 0.0, rows2[0].GrowthPct)
}

func TestRankings_BuildRankedVendors(t *testing.T) {
	current := []model.RankingQuotaTotal{
		{ModelName: "gpt-4", TotalTokens: 600},
		{ModelName: "gpt-3", TotalTokens: 100},
		{ModelName: "claude", TotalTokens: 300},
	}
	previous := []model.RankingQuotaTotal{
		{ModelName: "gpt-4", TotalTokens: 350},
	}
	meta := map[string]rankingModelMeta{
		"gpt-4":  {vendor: "OpenAI", vendorIcon: "o"},
		"gpt-3":  {vendor: "OpenAI"},
		"claude": {vendor: "Anthropic"},
	}
	rows := buildRankedVendors(current, previous, 1000, meta, true)
	require.Len(t, rows, 2)
	// OpenAI total 700 > Anthropic 300
	assert.Equal(t, "OpenAI", rows[0].Vendor)
	assert.Equal(t, int64(700), rows[0].TotalTokens)
	assert.Equal(t, 1, rows[0].Rank)
	assert.Equal(t, 2, rows[0].ModelsCount)
	assert.Equal(t, "gpt-4", rows[0].TopModel)
	assert.Equal(t, "o", rows[0].VendorIcon)
	assert.Equal(t, "Anthropic", rows[1].Vendor)
	assert.Equal(t, 2, rows[1].Rank)
}

func TestRankings_BuildRankedVendors_SkipsZero(t *testing.T) {
	current := []model.RankingQuotaTotal{{ModelName: "x", TotalTokens: 0}}
	meta := map[string]rankingModelMeta{"x": {vendor: "V"}}
	rows := buildRankedVendors(current, nil, 0, meta, false)
	assert.Empty(t, rows)
}

func TestRankings_BuildModelHistory(t *testing.T) {
	// >rankingHistoryLimit(10) models so the "Others" bucket is exercised
	totals := make([]model.RankingQuotaTotal, 0, 12)
	buckets := make([]model.RankingQuotaBucket, 0)
	meta := map[string]rankingModelMeta{}
	for i := 0; i < 12; i++ {
		name := "m" + string(rune('a'+i))
		totals = append(totals, model.RankingQuotaTotal{ModelName: name, TotalTokens: int64(120 - i*10)})
		meta[name] = rankingModelMeta{vendor: "V"}
		buckets = append(buckets, model.RankingQuotaBucket{ModelName: name, Bucket: 1000, Tokens: int64(120 - i*10)})
	}
	cfg := rankingPeriodConfig{labelLayout: "15:04"}
	hist := buildModelHistory(buckets, totals, meta, cfg)
	// 10 top models + Others
	assert.Equal(t, 11, len(hist.Models))
	assert.Equal(t, rankingOthersLabel, hist.Models[10].Name)
	assert.Equal(t, 1, hist.Buckets)
	assert.NotEmpty(t, hist.Points)
}

func TestRankings_BuildVendorShareHistory(t *testing.T) {
	vendors := make([]RankedVendor, 0, 7)
	buckets := make([]model.RankingQuotaBucket, 0)
	meta := map[string]rankingModelMeta{}
	for i := 0; i < 7; i++ {
		vname := "V" + string(rune('a'+i))
		vendors = append(vendors, RankedVendor{Vendor: vname, TotalTokens: int64(70 - i*5), Share: 0.1})
		mname := "model" + string(rune('a'+i))
		meta[mname] = rankingModelMeta{vendor: vname}
		buckets = append(buckets, model.RankingQuotaBucket{ModelName: mname, Bucket: 2000, Tokens: int64(70 - i*5)})
	}
	cfg := rankingPeriodConfig{labelLayout: "Jan 2"}
	vh := buildVendorShareHistory(buckets, vendors, 1000, meta, cfg)
	// 5 top vendors + Others
	assert.Equal(t, 6, len(vh.Vendors))
	assert.Equal(t, rankingOthersLabel, vh.Vendors[5].Name)
	assert.Equal(t, 1, vh.Buckets)
	assert.NotEmpty(t, vh.Points)
}

func TestRankings_BuildMovers(t *testing.T) {
	pr := func(v int) *int { return &v }
	models := []RankedModel{
		{ModelName: "up", Rank: 1, PreviousRank: pr(5), GrowthPct: 30},   // delta +4 mover
		{ModelName: "down", Rank: 8, PreviousRank: pr(2), GrowthPct: -10}, // delta -6 dropper
		{ModelName: "same", Rank: 3, PreviousRank: pr(3)},                 // delta 0 skip
		{ModelName: "new", Rank: 4, PreviousRank: nil},                    // no prev skip
	}
	movers, droppers := buildRankingMovers(models)
	require.Len(t, movers, 1)
	require.Len(t, droppers, 1)
	assert.Equal(t, "up", movers[0].ModelName)
	assert.Equal(t, 4, movers[0].RankDelta)
	assert.Equal(t, "down", droppers[0].ModelName)
	assert.Equal(t, -6, droppers[0].RankDelta)
}

func TestRankings_Limiters(t *testing.T) {
	models := []RankedModel{{Rank: 1}, {Rank: 2}, {Rank: 3}}
	assert.Len(t, limitRankedModels(models, 2), 2)
	assert.Len(t, limitRankedModels(models, 0), 3) // limit<=0 returns all
	assert.Len(t, limitRankedModels(models, 5), 3) // len<=limit returns all

	movers := []RankingMover{{}, {}, {}}
	assert.Len(t, limitRankingMovers(movers, 1), 1)
	assert.Len(t, limitRankingMovers(movers, 0), 3)
}

func TestRankings_SortedBucketsAndLabels(t *testing.T) {
	set := map[int64]struct{}{300: {}, 100: {}, 200: {}}
	sorted := sortedRankingBuckets(set)
	assert.Equal(t, []int64{100, 200, 300}, sorted)

	ts := rankingBucketTs(0)
	assert.Equal(t, "1970-01-01T00:00:00Z", ts)

	label := rankingBucketLabel(0, rankingPeriodConfig{labelLayout: "2006"})
	assert.Equal(t, "1970", label)
}
