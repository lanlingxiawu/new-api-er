package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setup.go: GetSetup returns the first Setup row, or nil when the query errors
// (e.g. no rows). We insert a row and confirm GetSetup returns a non-nil value.
//
// The nil branch (empty table -> ErrRecordNotFound) is NOT deterministically
// testable here: the Setup table is a shared singleton and clearing it would
// require a global truncate, which the harness forbids. Documented, not tested.
func TestGetSetup_ReturnsRowWhenPresent(t *testing.T) {
	requireDB(t)

	s := &Setup{
		Version:       uniq("v"),
		InitializedAt: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(s).Error)
	require.NotZero(t, s.ID)
	t.Cleanup(func() { DB.Unscoped().Delete(&Setup{}, s.ID) })

	got := GetSetup()
	// First() orders by primary key; either our row or a pre-existing one, but
	// with at least our row present it must be non-nil.
	require.NotNil(t, got)
	assert.NotEmpty(t, got.Version)
	assert.Greater(t, got.InitializedAt, int64(0))
}
