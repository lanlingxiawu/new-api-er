package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// pricing.go :: filterPricingByUsableGroups — pure. Rows whose EnableGroup
// contains "all" are always visible; otherwise a row is kept only if it shares
// at least one group with the caller's usable groups. Empty usable set hides
// everything; empty pricing set is returned as-is.
// ---------------------------------------------------------------------------

func TestFilterPricingByUsableGroups(t *testing.T) {
	pricing := []model.Pricing{
		{ModelName: "m-all", EnableGroup: []string{"all"}},
		{ModelName: "m-vip", EnableGroup: []string{"vip"}},
		{ModelName: "m-default", EnableGroup: []string{"default"}},
		{ModelName: "m-multi", EnableGroup: []string{"vip", "default"}},
		{ModelName: "m-none", EnableGroup: []string{"secret"}},
	}

	names := func(ps []model.Pricing) map[string]bool {
		m := map[string]bool{}
		for _, p := range ps {
			m[p.ModelName] = true
		}
		return m
	}

	t.Run("usable=default keeps all + default + multi", func(t *testing.T) {
		got := filterPricingByUsableGroups(pricing, map[string]string{"default": "default"})
		n := names(got)
		require.True(t, n["m-all"])
		require.True(t, n["m-default"])
		require.True(t, n["m-multi"])
		require.False(t, n["m-vip"])
		require.False(t, n["m-none"])
	})

	t.Run("empty usable set hides everything", func(t *testing.T) {
		got := filterPricingByUsableGroups(pricing, map[string]string{})
		require.Empty(t, got)
	})

	t.Run("empty pricing returned as-is", func(t *testing.T) {
		got := filterPricingByUsableGroups([]model.Pricing{}, map[string]string{"default": "default"})
		require.Empty(t, got)
	})

	t.Run("all-tag row visible even to a narrow group", func(t *testing.T) {
		got := filterPricingByUsableGroups(pricing, map[string]string{"vip": "vip"})
		n := names(got)
		require.True(t, n["m-all"])
		require.True(t, n["m-vip"])
		require.True(t, n["m-multi"])
		require.False(t, n["m-default"])
	})
}
