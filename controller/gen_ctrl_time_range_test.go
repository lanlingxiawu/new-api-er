package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// time_range_query.go: parseUnixTimeRangeQuery + parseOptionalInt64Query.
// These parse query params defensively (reject negatives / bad ints / inverted ranges).

func queryCtx(t *testing.T, rawQuery string) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest("GET", "/x?"+rawQuery, nil)
	return ctx
}

func TestParseUnixTimeRangeQuery(t *testing.T) {
	// Both empty -> zero range, no error.
	tr, err := parseUnixTimeRangeQuery(queryCtx(t, ""))
	require.NoError(t, err)
	assert.Equal(t, int64(0), tr.StartTime)
	assert.Equal(t, int64(0), tr.EndTime)

	// Valid pair.
	tr, err = parseUnixTimeRangeQuery(queryCtx(t, "start_time=100&end_time=200"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), tr.StartTime)
	assert.Equal(t, int64(200), tr.EndTime)

	// start > end -> error.
	_, err = parseUnixTimeRangeQuery(queryCtx(t, "start_time=300&end_time=200"))
	assert.Error(t, err)

	// Only one bound set skips the ordering check.
	tr, err = parseUnixTimeRangeQuery(queryCtx(t, "start_time=300"))
	require.NoError(t, err)
	assert.Equal(t, int64(300), tr.StartTime)
	assert.Equal(t, int64(0), tr.EndTime)

	// Negative -> error.
	_, err = parseUnixTimeRangeQuery(queryCtx(t, "start_time=-5"))
	assert.Error(t, err)

	// Non-numeric -> error.
	_, err = parseUnixTimeRangeQuery(queryCtx(t, "end_time=abc"))
	assert.Error(t, err)

	// Whitespace-padded value parses (TrimSpace).
	tr, err = parseUnixTimeRangeQuery(queryCtx(t, "start_time=%20100%20"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), tr.StartTime)
}

func TestParseOptionalInt64Query(t *testing.T) {
	v, err := parseOptionalInt64Query(queryCtx(t, ""), "before_id")
	require.NoError(t, err)
	assert.Equal(t, int64(0), v)

	v, err = parseOptionalInt64Query(queryCtx(t, "before_id=42"), "before_id")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)

	_, err = parseOptionalInt64Query(queryCtx(t, "before_id=-1"), "before_id")
	assert.Error(t, err)

	_, err = parseOptionalInt64Query(queryCtx(t, "before_id=x"), "before_id")
	assert.Error(t, err)
}
