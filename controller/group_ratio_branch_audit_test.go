package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchAuditUserGroupRatioEditPartitions(t *testing.T) {
	for _, raw := range []string{"", "{}", "null"} {
		got, err := parseUserGroupRatiosForEdit(raw)
		require.NoError(t, err)
		assert.Empty(t, got)
	}
	for _, raw := range []string{`[]`, `"vip"`, `{"vip":"1"}`, `{"vip":true}`, `{"vip":1e999}`, `{`} {
		_, err := parseUserGroupRatiosForEdit(raw)
		assert.Error(t, err, raw)
	}
	got, err := parseUserGroupRatiosForEdit(`{"free":0,"discount":0.125,"normal":1,"premium":20}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"free": 0, "discount": 0.125, "normal": 1, "premium": 20}, got)
}
