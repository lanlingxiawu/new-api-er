package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetModelEnableGroups / GetModelQuotaTypes read the cached maps populated by
// updatePricing. We publish a model via an ability, force a refresh, then assert
// the cached lookups return the right group + quota type.
func TestGetModelEnableGroupsAndQuotaTypes(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("xgrp")
	model := uniq("zzextramodel")
	setModelRatio(t, model, 2.5) // ratio path -> QuotaType 0
	mkAbilityModel(t, grp, model)
	RefreshPricing()

	groups := GetModelEnableGroups(model)
	assert.Contains(t, groups, grp)

	qt := GetModelQuotaTypes(model)
	assert.Equal(t, []int{0}, qt)
}

func TestGetModelEnableGroups_EdgeCases(t *testing.T) {
	requireDB(t)
	// empty model name -> empty (non-nil) slice
	assert.Equal(t, []string{}, GetModelEnableGroups(""))
	// unknown model -> empty slice
	assert.Empty(t, GetModelEnableGroups(uniq("zz-unknown-model")))
}

func TestGetModelQuotaTypes_Unknown(t *testing.T) {
	requireDB(t)
	// unknown model -> empty int slice
	assert.Equal(t, []int{}, GetModelQuotaTypes(uniq("zz-unknown-model")))
}

// A price-configured model reports QuotaType 1 through the cached map.
func TestGetModelQuotaTypes_PriceModel(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("xgrp")
	model := uniq("zzextraprice")
	setModelPrice(t, model, 9.0)
	mkAbilityModel(t, grp, model)
	RefreshPricing()

	require.Equal(t, []int{1}, GetModelQuotaTypes(model))
}
