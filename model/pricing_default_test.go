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
// getOrCreateVendor — found-in-map short-circuit vs create-new (DB insert).
// ---------------------------------------------------------------------------

func TestGetOrCreateVendor_FoundInMap(t *testing.T) {
	vendorMap := map[int]*Vendor{
		77: {Id: 77, Name: "Anthropic"},
	}
	// Existing vendor returned without any DB insert.
	got := getOrCreateVendor("Anthropic", vendorMap)
	assert.Equal(t, 77, got)
	assert.Len(t, vendorMap, 1)
}

func TestGetOrCreateVendor_CreatesNew(t *testing.T) {
	requireDB(t)
	name := uniq("zzVendorNew") // guaranteed-unique -> no unique-index conflict
	vendorMap := map[int]*Vendor{}

	id := getOrCreateVendor(name, vendorMap)
	require.NotZero(t, id, "new vendor should be inserted and its id returned")
	deleteByID(t, &Vendor{}, id)

	// The new vendor is registered in the map.
	require.Contains(t, vendorMap, id)
	assert.Equal(t, name, vendorMap[id].Name)

	// Persisted in DB.
	got, err := GetVendorByID(id)
	require.NoError(t, err)
	assert.Equal(t, name, got.Name)
}

// ---------------------------------------------------------------------------
// initDefaultVendorMapping — skip existing meta, rule match, no-rule fallback.
// ---------------------------------------------------------------------------

func TestInitDefaultVendorMapping(t *testing.T) {
	existing := uniq("zzexisting")
	ruleMatch := "claude-" + uniq("zzc") // matches the "claude" -> Anthropic rule
	noRule := uniq("zznomatch")           // matches no vendor rule

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
