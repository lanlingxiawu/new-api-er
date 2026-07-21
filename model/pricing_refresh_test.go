package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RefreshPricing recomputes the pricing cache immediately (management API path,
// bypassing GetPricing's 1-minute delay). After publishing a new ability model
// and calling RefreshPricing, the model must be present with its pinned ratio.
//
// Note: we deliberately do NOT assert that a pre-refresh GetPricing() omits the
// model. GetPricing recomputes whenever pricingMap is empty, and a freshly
// cleaned test DB can legitimately have zero enabled abilities, so that would be
// environment-dependent. RefreshPricing's forced recompute is what we verify.
func TestRefreshPricing_Recomputes(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("rgrp")
	model := uniq("zzrefreshmodel")
	setModelRatio(t, model, 5.0)
	mkAbilityModel(t, grp, model)

	RefreshPricing()
	pr := findPricing(GetPricing(), model)
	require.NotNil(t, pr)
	assert.Equal(t, 5.0, pr.ModelRatio)
	assert.Contains(t, pr.EnableGroup, grp)
}
