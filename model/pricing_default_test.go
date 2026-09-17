package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// getDefaultVendorIcon — pure lookup.
// ---------------------------------------------------------------------------

func TestGetDefaultVendorIcon(t *testing.T) {
	assert.Equal(t, "OpenAI", getDefaultVendorIcon("OpenAI"))
	assert.Equal(t, "Claude.Color", getDefaultVendorIcon("Anthropic"))
	assert.Equal(t, "Gemini.Color", getDefaultVendorIcon("Google"))
	assert.Equal(t, "AzureAI", getDefaultVendorIcon("Microsoft"))
	// unknown vendor -> empty string
	assert.Equal(t, "", getDefaultVendorIcon("zz-unknown-vendor"))
}

// ---------------------------------------------------------------------------
// initDefaultVendorMapping — skip existing meta, rule match, no-rule fallback.
// ---------------------------------------------------------------------------

func TestInitDefaultVendorMapping(t *testing.T) {
	// Model names must be digit-free: defaultVendorRules contains numeric/short
	// patterns like "360", "o1", "o3". uniq() embeds a numeric suffix, so a
	// random "360" inside it would make noRule/ruleMatch accidentally match the
	// 360 rule (map iteration order is randomized), flaking the assertions. The
	// metaMap is local to this test, so fixed alpha-only names are unique enough.
	existing := "zzexisting-fixed"
	ruleMatch := "claude-zzruleonly" // matches only the "claude" -> Anthropic rule
	noRule := "zznomatchtoken"       // contains no vendor rule pattern

	metaMap := map[string]*Model{
		existing: {ModelName: existing, VendorID: 999}, // pre-populated -> must be untouched
	}
	// Pre-seed Anthropic in the vendor map so the rule path takes the
	// found-in-map branch (deterministic, no DB insert / unique conflict).
	vendorMap := map[int]*Vendor{
		88: {Id: 88, Name: "Anthropic"},
	}
	abilities := []AbilityWithChannel{
		{Ability: Ability{Model: existing}},
		{Ability: Ability{Model: ruleMatch}},
		{Ability: Ability{Model: noRule}},
	}

	initDefaultVendorMapping(metaMap, vendorMap, abilities)

	// existing entry left as-is
	require.Contains(t, metaMap, existing)
	assert.Equal(t, 999, metaMap[existing].VendorID)

	// rule match got Anthropic (id 88), Status 1, exact name rule
	require.Contains(t, metaMap, ruleMatch)
	assert.Equal(t, 88, metaMap[ruleMatch].VendorID)
	assert.Equal(t, 1, metaMap[ruleMatch].Status)
	assert.Equal(t, NameRuleExact, metaMap[ruleMatch].NameRule)

	// no-rule match got vendor 0
	require.Contains(t, metaMap, noRule)
	assert.Equal(t, 0, metaMap[noRule].VendorID)
	assert.Equal(t, 1, metaMap[noRule].Status)
}
