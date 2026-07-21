package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// usedata_rankings.go ranks model token usage from quota_data. Queries have no
// user filter, so tests isolate by unique model names + a unique created_at
// window. Cross-dialect note: rankingBucketExpr uses FLOOR(created_at/N)*N on
// MySQL (integer-preserving) and (created_at/N)*N elsewhere (PG/SQLite already
// do integer division). Main DB here is MySQL, so the FLOOR form runs live.
// ---------------------------------------------------------------------------

func TestRankingBucketExpr_Dialects(t *testing.T) {
	prev := common.MainDatabaseType()
	t.Cleanup(func() { common.SetMainDatabaseType(prev) })

	common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	assert.Equal(t, "FLOOR(created_at / 3600) * 3600", rankingBucketExpr(3600))

	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	assert.Equal(t, "(created_at / 3600) * 3600", rankingBucketExpr(3600))
}

func TestGetRankingQuotaTotals(t *testing.T) {
	requireDB(t)
	base := usedataUniqTime()
	mHi, mLo, mZero := uniq("rkhi"), uniq("rklo"), uniq("rkzero")

	// higher token_used should rank first (ORDER BY total_tokens DESC)
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed, q.Quota = mHi, base, 900, 1
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed, q.Quota = mLo, base, 100, 1
	})
	// token_used == 0 excluded by HAVING sum(token_used) > 0
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed, q.Quota = mZero, base, 0, 1
	})
	// empty model_name excluded by WHERE model_name <> ''
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed, q.Quota = "", base, 500, 1
	})

	rows, err := GetRankingQuotaTotals(base, base)
	require.NoError(t, err)

	// collect only our unique models, preserving returned order
	var ordered []RankingQuotaTotal
	for _, r := range rows {
		if r.ModelName == mHi || r.ModelName == mLo || r.ModelName == mZero {
			ordered = append(ordered, r)
		}
	}
	require.Len(t, ordered, 2) // zero-token model absent
	assert.Equal(t, mHi, ordered[0].ModelName)
	assert.EqualValues(t, 900, ordered[0].TotalTokens)
	assert.Equal(t, mLo, ordered[1].ModelName)
	assert.EqualValues(t, 100, ordered[1].TotalTokens)
}

func TestGetRankingQuotaBuckets(t *testing.T) {
	requireDB(t)
	base := usedataUniqTime()
	base -= base % 3600 // align to an hour bucket
	model := uniq("rkb")

	// two rows in bucket #1 (aggregate), one row in bucket #2
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed = model, base + 10, 30
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed = model, base + 20, 20
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed = model, base + 3610, 5
	})

	rows, err := GetRankingQuotaBuckets(base, base+7200, 3600)
	require.NoError(t, err)

	var mine []RankingQuotaBucket
	for _, r := range rows {
		if r.ModelName == model {
			mine = append(mine, r)
		}
	}
	require.Len(t, mine, 2)
	// ORDER BY bucket ASC
	assert.EqualValues(t, base, mine[0].Bucket)
	assert.EqualValues(t, 50, mine[0].Tokens) // 30 + 20 aggregated in bucket 1
	assert.EqualValues(t, base+3600, mine[1].Bucket)
	assert.EqualValues(t, 5, mine[1].Tokens)
}

func TestGetRankingQuotaBuckets_DefaultBucketSize(t *testing.T) {
	requireDB(t)
	base := usedataUniqTime()
	base -= base % 3600
	model := uniq("rkbd")
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed = model, base + 5, 12
	})

	// bucketSize <= 0 defaults to 3600
	rows, err := GetRankingQuotaBuckets(base, base+3600, 0)
	require.NoError(t, err)
	var mine *RankingQuotaBucket
	for i := range rows {
		if rows[i].ModelName == model {
			mine = &rows[i]
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, base, mine.Bucket)
	assert.EqualValues(t, 12, mine.Tokens)
}

func TestApplyRankingQuotaTimeRange(t *testing.T) {
	requireDB(t)
	base := usedataUniqTime()
	model := uniq("rktr")
	mkQuotaData(t, func(q *QuotaData) {
		q.ModelName, q.CreatedAt, q.TokenUsed = model, base, 42
	})

	// no bounds (0,0) -> scans everything; ensure our row is present
	rows, err := GetRankingQuotaTotals(0, 0)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.ModelName == model {
			found = true
		}
	}
	assert.True(t, found)

	// window entirely after our row -> excluded
	rows, err = GetRankingQuotaTotals(base+1, base+2)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, model, r.ModelName)
	}
}
