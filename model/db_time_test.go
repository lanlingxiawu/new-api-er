package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

// GetDBTimestamp reads the current epoch from the DB (dialect-specific SQL),
// falling back to application time on error. With a real DB configured it must
// return a positive value close to wall-clock now.
func TestGetDBTimestamp_CloseToNow(t *testing.T) {
	requireDB(t)
	got := GetDBTimestamp()
	assert.Greater(t, got, int64(0))

	now := common.GetTimestamp()
	diff := got - now
	if diff < 0 {
		diff = -diff
	}
	// DB clock and app clock should agree within a small skew window.
	assert.LessOrEqual(t, diff, int64(120), "db timestamp %d too far from now %d", got, now)
}

// Two consecutive reads must be monotonic (non-decreasing) within the test.
func TestGetDBTimestamp_Monotonic(t *testing.T) {
	requireDB(t)
	a := GetDBTimestamp()
	time.Sleep(1100 * time.Millisecond)
	b := GetDBTimestamp()
	assert.GreaterOrEqual(t, b, a)
}
