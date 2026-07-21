package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FormatMatchingModelName + handleThinkingBudgetModel (name normalization)
// ---------------------------------------------------------------------------

func TestFormatMatchingModelName_GizmoWildcards(t *testing.T) {
	assert.Equal(t, "gpt-4-gizmo-*", FormatMatchingModelName("gpt-4-gizmo-g-abc123"))
	assert.Equal(t, "gpt-4-gizmo-*", FormatMatchingModelName("gpt-4-gizmo"))
	assert.Equal(t, "gpt-4o-gizmo-*", FormatMatchingModelName("gpt-4o-gizmo-xyz"))
	assert.Equal(t, "gpt-4o-gizmo-*", FormatMatchingModelName("gpt-4o-gizmo"))
}

func TestFormatMatchingModelName_PassthroughUnknown(t *testing.T) {
	// No rule matches -> returned unchanged.
	assert.Equal(t, "gpt-4o", FormatMatchingModelName("gpt-4o"))
	assert.Equal(t, "", FormatMatchingModelName(""))
	assert.Equal(t, "claude-opus-4-8", FormatMatchingModelName("claude-opus-4-8"))
}

func TestFormatMatchingModelName_GeminiThinkingBudget(t *testing.T) {
	// pro thinking -> pro wildcard
	assert.Equal(t, "gemini-2.5-pro-thinking-*",
		FormatMatchingModelName("gemini-2.5-pro-thinking-8192"))
	// flash thinking -> flash wildcard
	assert.Equal(t, "gemini-2.5-flash-thinking-*",
		FormatMatchingModelName("gemini-2.5-flash-thinking-1024"))
	// non-thinking flash stays unchanged (prefix matches but "-thinking-" absent)
	assert.Equal(t, "gemini-2.5-flash-preview-05-20",
		FormatMatchingModelName("gemini-2.5-flash-preview-05-20"))
	// pro without thinking stays unchanged
	assert.Equal(t, "gemini-2.5-pro", FormatMatchingModelName("gemini-2.5-pro"))
}

// FINDING (documented, not fixed): flash-lite thinking names normalize to the
// wildcard key "gemini-2.5-flash-lite-thinking-*", but defaultModelRatio only
// defines "gemini-2.5-flash-lite-preview-thinking-*". The produced key therefore
// has NO ratio entry -> such models fall through to the 37.5 default. This test
// pins the ACTUAL (buggy) behavior so a future fix is a deliberate change.
func TestFormatMatchingModelName_FlashLiteThinking_WildcardMismatch(t *testing.T) {
	got := FormatMatchingModelName("gemini-2.5-flash-lite-preview-thinking-4096")
	assert.Equal(t, "gemini-2.5-flash-lite-thinking-*", got,
		"flash-lite thinking normalizes to a key not present in defaultModelRatio")

	// Confirm the produced wildcard is NOT in the seeded ratio map.
	_, ok := modelRatioMap.Get("gemini-2.5-flash-lite-thinking-*")
	assert.False(t, ok, "the normalized flash-lite wildcard is undefined -> dead lookup")

	// And the map DOES contain the un-reachable preview variant.
	_, ok = modelRatioMap.Get("gemini-2.5-flash-lite-preview-thinking-*")
	assert.True(t, ok, "the -preview- wildcard exists but normalization never targets it")
}

func TestHandleThinkingBudgetModel_Conditions(t *testing.T) {
	// prefix matches AND contains -thinking- -> wildcard
	assert.Equal(t, "W", handleThinkingBudgetModel("pre-x-thinking-1", "pre", "W"))
	// prefix matches but no -thinking- -> unchanged
	assert.Equal(t, "pre-x", handleThinkingBudgetModel("pre-x", "pre", "W"))
	// -thinking- present but prefix does not match -> unchanged
	assert.Equal(t, "other-thinking-1", handleThinkingBudgetModel("other-thinking-1", "pre", "W"))
}

// ---------------------------------------------------------------------------
// GetModelRatio
// ---------------------------------------------------------------------------

func TestGetModelRatio_ExactMatch(t *testing.T) {
	ratio, ok, name := GetModelRatio("gpt-4o")
	assert.True(t, ok)
	assert.Equal(t, 1.25, ratio)
	assert.Equal(t, "gpt-4o", name)
}

func TestGetModelRatio_GizmoNormalizedMatch(t *testing.T) {
	ratio, ok, name := GetModelRatio("gpt-4-gizmo-g-xyz")
	assert.True(t, ok)
	assert.Equal(t, 15.0, ratio)
	assert.Equal(t, "gpt-4-gizmo-*", name, "returned name is the normalized key")
}

func TestGetModelRatio_UnknownDefaultSelfUseOff(t *testing.T) {
	withSelfUseMode(t, false)
	ratio, ok, name := GetModelRatio("totally-unknown-model")
	assert.Equal(t, 37.5, ratio)
	assert.False(t, ok, "self-use off -> ok is false so callers treat as not-found")
	assert.Equal(t, "totally-unknown-model", name)
}

func TestGetModelRatio_UnknownDefaultSelfUseOn(t *testing.T) {
	withSelfUseMode(t, true)
	ratio, ok, _ := GetModelRatio("totally-unknown-model")
	assert.Equal(t, 37.5, ratio)
	assert.True(t, ok, "self-use on -> ok reports true for unknown models")
}

func TestGetModelRatio_CompactSuffix_WildcardHit(t *testing.T) {
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })

	require.NoError(t, UpdateModelRatioByJSONString(`{"`+CompactWildcardModelKey+`":3.5}`))
	ratio, ok, name := GetModelRatio("some-model" + CompactModelSuffix)
	assert.True(t, ok)
	assert.Equal(t, 3.5, ratio)
	assert.Equal(t, "some-model"+CompactModelSuffix, name)
}

func TestGetModelRatio_CompactSuffix_NoWildcardFallsToDefault(t *testing.T) {
	withSelfUseMode(t, false)
	// wildcard key is absent by default -> compact miss falls to 37.5/ok=false
	ratio, ok, _ := GetModelRatio("orphan-model" + CompactModelSuffix)
	assert.Equal(t, 37.5, ratio)
	assert.False(t, ok)
}

func TestGetModelRatio_ExplicitCompactKeyExactMatch(t *testing.T) {
	// An exact entry for a compact-suffixed model wins before the wildcard branch.
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })
	require.NoError(t, UpdateModelRatioByJSONString(`{"foo`+CompactModelSuffix+`":9}`))
	ratio, ok, _ := GetModelRatio("foo" + CompactModelSuffix)
	assert.True(t, ok)
	assert.Equal(t, 9.0, ratio)
}

// ---------------------------------------------------------------------------
// GetModelPrice
// ---------------------------------------------------------------------------

func TestGetModelPrice_ExactMatch(t *testing.T) {
	price, ok := GetModelPrice("dall-e-3", false)
	assert.True(t, ok)
	assert.Equal(t, 0.04, price)
}

func TestGetModelPrice_GizmoNormalized(t *testing.T) {
	price, ok := GetModelPrice("gpt-4-gizmo-anything", false)
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
}

func TestGetModelPrice_Unknown(t *testing.T) {
	price, ok := GetModelPrice("no-such-price-model", false)
	assert.False(t, ok)
	assert.Equal(t, -1.0, price)
}

func TestGetModelPrice_UnknownWithPrintErr(t *testing.T) {
	// printErr=true exercises the SysError branch (no panic, still -1/false).
	price, ok := GetModelPrice("no-such-price-model", true)
	assert.False(t, ok)
	assert.Equal(t, -1.0, price)
}

func TestGetModelPrice_CompactWildcard(t *testing.T) {
	snap := snapshotFloatMap(modelPriceMap)
	t.Cleanup(func() { restoreFloatMap(modelPriceMap, snap) })
	require.NoError(t, UpdateModelPriceByJSONString(`{"`+CompactWildcardModelKey+`":0.2}`))
	price, ok := GetModelPrice("x"+CompactModelSuffix, false)
	assert.True(t, ok)
	assert.Equal(t, 0.2, price)
}

func TestGetModelPrice_CompactSuffixNoWildcard(t *testing.T) {
	// suffix present, wildcard absent, printErr true -> -1/false via SysError path
	price, ok := GetModelPrice("y"+CompactModelSuffix, true)
	assert.False(t, ok)
	assert.Equal(t, -1.0, price)
}

// ---------------------------------------------------------------------------
// GetModelRatioOrPrice
// ---------------------------------------------------------------------------

func TestGetModelRatioOrPrice_UsesPrice(t *testing.T) {
	v, usePrice, exist := GetModelRatioOrPrice("dall-e-3")
	assert.Equal(t, 0.04, v)
	assert.True(t, usePrice)
	assert.True(t, exist)
}

func TestGetModelRatioOrPrice_UsesRatio(t *testing.T) {
	v, usePrice, exist := GetModelRatioOrPrice("gpt-4o")
	assert.Equal(t, 1.25, v)
	assert.False(t, usePrice)
	assert.True(t, exist)
}

func TestGetModelRatioOrPrice_UnknownSelfUseOff(t *testing.T) {
	withSelfUseMode(t, false)
	v, usePrice, exist := GetModelRatioOrPrice("unknown-xyz")
	assert.Equal(t, 37.5, v)
	assert.False(t, usePrice)
	assert.False(t, exist, "unknown model with self-use off -> not existing")
}

func TestGetModelRatioOrPrice_UnknownSelfUseOn(t *testing.T) {
	withSelfUseMode(t, true)
	// GetModelRatio reports success=true under self-use, so exist becomes true.
	v, usePrice, exist := GetModelRatioOrPrice("unknown-xyz")
	assert.Equal(t, 37.5, v)
	assert.False(t, usePrice)
	assert.True(t, exist)
}

// ---------------------------------------------------------------------------
// getHardcodedCompletionModelRatio — decision/condition matrix (the core of
// derived completion pricing). Each row pins (ratio, locked).
// ---------------------------------------------------------------------------

func TestGetHardcodedCompletionModelRatio_Matrix(t *testing.T) {
	cases := []struct {
		name   string
		ratio  float64
		locked bool
	}{
		// reserved models (checked before everything)
		{"gpt-4-all", 2, false},
		{"gpt-4o-gizmo-*", 2, false},
		{"anything-all", 2, false},
		// gpt-4o family
		{"gpt-4o-2024-05-13", 3, true},
		{"gpt-4o-mini-tts", 20, false},
		{"gpt-4o-mini-tts-2024", 20, false},
		{"gpt-4o", 4, false},
		{"gpt-4o-mini", 4, false},
		// gpt-5 family
		{"gpt-5", 8, true},
		{"gpt-5-mini", 8, true},
		{"gpt-5-chat-latest", 8, true},
		{"gpt-5.4", 6, true},
		{"gpt-5.4-nano", 6.25, true},
		{"gpt-5.5", 6, false},
		{"gpt-5.6-sol", 6, false},
		// gpt-4.5 / gpt-4-turbo / gpt-4 default
		{"gpt-4.5-preview", 2, true},
		{"gpt-4.5-preview-2025-02-27", 2, true},
		{"gpt-4-turbo", 3, true},
		{"gpt-4-turbo-2024-04-09", 3, true},
		// condition coverage on the OR sub-conditions of the turbo branch:
		// name must start "gpt-" AND end with the suffix for these to fire.
		{"gpt-4-1106", 3, true},
		{"gpt-4-1105", 3, true},
		{"gpt-4", 2, false},
		{"gpt-4-0613", 2, false},
		// o1 / o3
		{"o1", 4, true},
		{"o1-preview", 4, true},
		{"o3-mini", 4, true},
		// chatgpt-4o-latest
		{"chatgpt-4o-latest", 3, true},
		// claude families
		{"claude-3-haiku-20240307", 5, true},
		{"claude-3-5-sonnet-20240620", 5, true},
		{"claude-sonnet-4-20250514", 5, true},
		{"claude-opus-4-8", 5, true},
		{"claude-haiku-4-5-20251001", 5, true},
		// gpt-3.5 family — FIXED: the dedicated gpt-3.5 block was moved BEFORE the
		// generic HasPrefix(name,"gpt-") block, so gpt-3.5* names now resolve to
		// their intended ratios (turbo & *-0125 => 3 locked; *-1106 => 2 locked;
		// others => 4/3 locked) instead of the generic (2,false).
		{"gpt-3.5-turbo", 3, true},
		{"gpt-3.5-turbo-0125", 3, true},
		{"gpt-3.5-turbo-1106", 2, true},
		{"gpt-3.5-turbo-16k", 4.0 / 3.0, true},
		// mistral
		{"mistral-large", 3, true},
		// gemini families
		{"gemini-1.5-pro", 4, true},
		{"gemini-2.0-flash", 4, true},
		{"gemini-2.5-pro", 8, false},
		{"gemini-2.5-flash-preview-05-20", 3.5 / 0.15, false},
		{"gemini-2.5-flash-preview-05-20-nothinking", 4, false},
		{"gemini-2.5-flash-lite-preview-06-17", 4, false},
		{"gemini-2.5-flash", 2.5 / 0.3, false},
		{"gemini-robotics-er-1.5-preview", 2.5 / 0.3, false},
		{"gemini-3-pro", 6, false},
		{"gemini-3-pro-image", 60, false},
		{"gemini-unknown-x", 4, false},
		// command family
		{"command-r", 3, true},
		{"command-r-plus", 5, true},
		{"command-r-08-2024", 4, true},
		{"command-r-plus-08-2024", 4, true},
		{"command-light", 4, false},
		// ERNIE families
		{"ERNIE-Speed-8K", 2, true},
		{"ERNIE-Lite-8K", 2, true},
		{"ERNIE-Character-8K", 2, true},
		{"ERNIE-Functions-8K", 2, true},
		// llama switch
		{"llama2-70b-4096", 0.8 / 0.64, true},
		{"llama3-8b-8192", 2, true},
		{"llama3-70b-8192", 0.79 / 0.59, true},
		// fully unknown
		{"some-random-model", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ratio, locked := getHardcodedCompletionModelRatio(c.name)
			assert.Equal(t, c.ratio, ratio, "ratio for %s", c.name)
			assert.Equal(t, c.locked, locked, "locked for %s", c.name)
		})
	}
}

// ---------------------------------------------------------------------------
// GetCompletionRatio — layered resolution (slash override / locked hardcoded /
// map override / hardcoded default)
// ---------------------------------------------------------------------------

func TestGetCompletionRatio_LockedHardcodedWinsOverMap(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })

	// claude-3 is locked at 5; even a user override in the map must be ignored.
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"claude-3-5-sonnet-20240620":999}`))
	assert.Equal(t, 5.0, GetCompletionRatio("claude-3-5-sonnet-20240620"))
}

func TestGetCompletionRatio_MapOverrideBeatsUnlockedHardcoded(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })

	// gpt-4o hardcoded is 4 but UNLOCKED -> a map override takes effect.
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"gpt-4o":2.7}`))
	assert.Equal(t, 2.7, GetCompletionRatio("gpt-4o"))
}

func TestGetCompletionRatio_HardcodedDefaultWhenNoMapEntry(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })
	require.NoError(t, UpdateCompletionRatioByJSONString(`{}`))
	// gpt-4o unlocked default 4, no override present
	assert.Equal(t, 4.0, GetCompletionRatio("gpt-4o"))
	// fully unknown -> hardcoded default 1
	assert.Equal(t, 1.0, GetCompletionRatio("mystery-model"))
}

func TestGetCompletionRatio_SlashOverride(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })

	require.NoError(t, UpdateCompletionRatioByJSONString(`{"vendor/model-x":6.5}`))
	assert.Equal(t, 6.5, GetCompletionRatio("vendor/model-x"))
}

func TestGetCompletionRatio_SlashModelNotInMapFallsToHardcoded(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })
	require.NoError(t, UpdateCompletionRatioByJSONString(`{}`))
	// contains "/" but no map entry -> falls through to hardcoded (=1 default)
	assert.Equal(t, 1.0, GetCompletionRatio("vendor/unknown"))
}

func TestGetCompletionRatio_GizmoMapOverride(t *testing.T) {
	// default completionRatioMap seeds gpt-4o-gizmo-* = 3 (unlocked hardcoded=2).
	assert.Equal(t, 3.0, GetCompletionRatio("gpt-4o-gizmo-xyz"))
}

// ---------------------------------------------------------------------------
// GetCompletionRatioInfo — mirrors GetCompletionRatio but surfaces Locked
// ---------------------------------------------------------------------------

func TestGetCompletionRatioInfo_Locked(t *testing.T) {
	info := GetCompletionRatioInfo("claude-3-5-sonnet-20240620")
	assert.Equal(t, 5.0, info.Ratio)
	assert.True(t, info.Locked)
}

func TestGetCompletionRatioInfo_UnlockedFromHardcoded(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })
	require.NoError(t, UpdateCompletionRatioByJSONString(`{}`))
	info := GetCompletionRatioInfo("gpt-4o")
	assert.Equal(t, 4.0, info.Ratio)
	assert.False(t, info.Locked)
}

func TestGetCompletionRatioInfo_UnlockedFromMap(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"gpt-4o":2.1}`))
	info := GetCompletionRatioInfo("gpt-4o")
	assert.Equal(t, 2.1, info.Ratio)
	assert.False(t, info.Locked)
}

func TestGetCompletionRatioInfo_SlashOverride(t *testing.T) {
	snap := snapshotFloatMap(completionRatioMap)
	t.Cleanup(func() { restoreFloatMap(completionRatioMap, snap) })
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"acme/foo":3.3}`))
	info := GetCompletionRatioInfo("acme/foo")
	assert.Equal(t, 3.3, info.Ratio)
	assert.False(t, info.Locked)
}

func TestGetCompletionRatioInfo_DefaultWhenUnknown(t *testing.T) {
	info := GetCompletionRatioInfo("no-such-model-xyz")
	assert.Equal(t, 1.0, info.Ratio)
	assert.False(t, info.Locked)
}

// TestGetHardcodedCompletionModelRatio_GPT35DeadCode documents the FIX for the
// former dead-code branch: the gpt-3.5 block was moved before the generic "gpt-"
// block, so gpt-3.5-turbo now resolves to its intended (3, locked) ratio instead
// of the generic (2, false) that previously shadowed it.
func TestGetHardcodedCompletionModelRatio_GPT35DeadCode(t *testing.T) {
	ratio, locked := getHardcodedCompletionModelRatio("gpt-3.5-turbo")
	assert.Equal(t, 3.0, ratio, "fixed behavior: dedicated gpt-3.5 block returns the intended 3")
	assert.True(t, locked, "fixed behavior: gpt-3.5-turbo ratio is now locked")
}

// ---------------------------------------------------------------------------
// Audio / image ratios
// ---------------------------------------------------------------------------

func TestGetAudioRatio(t *testing.T) {
	assert.Equal(t, 16.0, GetAudioRatio("gpt-4o-audio-preview"))
	assert.Equal(t, 1.0, GetAudioRatio("model-without-audio-ratio"))
}

func TestGetAudioCompletionRatio(t *testing.T) {
	assert.Equal(t, 2.0, GetAudioCompletionRatio("gpt-4o-realtime"))
	assert.Equal(t, 0.0, GetAudioCompletionRatio("tts-1"))
	assert.Equal(t, 1.0, GetAudioCompletionRatio("unknown-audio-model"))
}

func TestContainsAudioRatio(t *testing.T) {
	assert.True(t, ContainsAudioRatio("gpt-4o-audio-preview"))
	assert.False(t, ContainsAudioRatio("nope"))
}

func TestContainsAudioCompletionRatio(t *testing.T) {
	assert.True(t, ContainsAudioCompletionRatio("tts-1"))
	assert.False(t, ContainsAudioCompletionRatio("nope"))
}

func TestGetImageRatio(t *testing.T) {
	ratio, ok := GetImageRatio("gpt-image-1")
	assert.True(t, ok)
	assert.Equal(t, 2.0, ratio)

	ratio, ok = GetImageRatio("no-image-ratio")
	assert.False(t, ok)
	assert.Equal(t, 1.0, ratio)
}

// ---------------------------------------------------------------------------
// JSON update + marshal round-trips (Update*ByJSONString / *2JSONString)
// ---------------------------------------------------------------------------

func TestUpdateModelRatioByJSONString_ReplacesMap(t *testing.T) {
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })

	require.NoError(t, UpdateModelRatioByJSONString(`{"only-model":42}`))
	// Update REPLACES the whole map, so previously-seeded keys vanish.
	ratio, ok, _ := GetModelRatio("only-model")
	assert.True(t, ok)
	assert.Equal(t, 42.0, ratio)
	_, ok, _ = GetModelRatio("gpt-4o")
	assert.False(t, ok, "prior keys are wiped by a full replace")
}

func TestUpdateModelRatioByJSONString_MalformedReturnsErrorAndPreserves(t *testing.T) {
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })

	before := modelRatioMap.Len()
	err := UpdateModelRatioByJSONString(`{not valid json`)
	require.Error(t, err)
	// FIXED: LoadFromJsonString now unmarshals into a temp map and only swaps it
	// in on success, so a malformed payload returns an error WITHOUT clearing the
	// existing map. Pin the corrected preserve-on-error behavior.
	assert.Equal(t, before, modelRatioMap.Len(), "malformed input must preserve the existing map")
}

func TestUpdateModelRatioByJSONString_EmptyObject(t *testing.T) {
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })
	require.NoError(t, UpdateModelRatioByJSONString(`{}`))
	assert.Equal(t, 0, modelRatioMap.Len())
}

func TestModelRatio2JSONString_RoundTrip(t *testing.T) {
	snap := snapshotFloatMap(modelRatioMap)
	t.Cleanup(func() { restoreFloatMap(modelRatioMap, snap) })
	require.NoError(t, UpdateModelRatioByJSONString(`{"m":1.5}`))
	assert.JSONEq(t, `{"m":1.5}`, ModelRatio2JSONString())
}

func TestUpdateAndMarshal_AllRatioMaps(t *testing.T) {
	type tc struct {
		name    string
		update  func(string) error
		marshal func() string
		mapRef  func() map[string]float64
		raw     *types.RWMap[string, float64]
	}
	cases := []tc{
		{"modelPrice", UpdateModelPriceByJSONString, ModelPrice2JSONString, GetModelPriceCopy, modelPriceMap},
		{"completion", UpdateCompletionRatioByJSONString, CompletionRatio2JSONString, GetCompletionRatioCopy, completionRatioMap},
		{"image", UpdateImageRatioByJSONString, ImageRatio2JSONString, GetImageRatioCopy, imageRatioMap},
		{"audio", UpdateAudioRatioByJSONString, AudioRatio2JSONString, GetAudioRatioCopy, audioRatioMap},
		{"audioCompletion", UpdateAudioCompletionRatioByJSONString, AudioCompletionRatio2JSONString, GetAudioCompletionRatioCopy, audioCompletionRatioMap},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := snapshotFloatMap(c.raw)
			t.Cleanup(func() { restoreFloatMap(c.raw, snap) })

			require.NoError(t, c.update(`{"k":2.5}`))
			assert.JSONEq(t, `{"k":2.5}`, c.marshal())
			assert.Equal(t, map[string]float64{"k": 2.5}, c.mapRef())

			require.Error(t, c.update(`{bad`))
		})
	}
}

// ---------------------------------------------------------------------------
// Copy getters return independent snapshots
// ---------------------------------------------------------------------------

func TestGetModelRatioCopy_IsIndependent(t *testing.T) {
	cp := GetModelRatioCopy()
	require.NotEmpty(t, cp)
	cp["gpt-4o"] = -1 // mutate copy
	ratio, _, _ := GetModelRatio("gpt-4o")
	assert.Equal(t, 1.25, ratio, "mutating the copy must not touch the live map")
}

func TestGetModelPriceMapAndCopy(t *testing.T) {
	m := GetModelPriceMap()
	assert.Equal(t, 0.04, m["dall-e-3"])
	cp := GetModelPriceCopy()
	assert.Equal(t, 0.04, cp["dall-e-3"])
}

// ---------------------------------------------------------------------------
// Default tables — sample of well-known billing anchors + precision (RMB const)
// ---------------------------------------------------------------------------

func TestDefaultModelRatio_WellKnownAnchors(t *testing.T) {
	d := GetDefaultModelRatioMap()
	assert.Equal(t, 15.0, d["gpt-4"])
	assert.Equal(t, 1.25, d["gpt-4o"])
	assert.Equal(t, 7.5, d["o1"])
	assert.Equal(t, 0.625, d["gpt-5"])
	assert.Equal(t, 2.5, d["claude-opus-4-8"])
	assert.Equal(t, 1.5, d["claude-sonnet-4-5-20250929"])
	assert.Equal(t, 0.0, d["glm-4-flash"], "explicit free model must stay exactly zero")
	// deepseek uses a division literal -> pin exact
	assert.Equal(t, 0.27/2, d["deepseek-chat"])
	assert.Equal(t, 0.55/2, d["deepseek-reasoner"])
}

func TestDefaultModelRatio_RMBPrecision(t *testing.T) {
	d := GetDefaultModelRatioMap()
	// RMB = USD/USD2RMB = 500/7.3. Reference the SAME expression to pin precision.
	assert.Equal(t, 0.120*RMB, d["ERNIE-4.0-8K"])
	assert.Equal(t, 0.001*RMB, d["ERNIE-Tiny-8K"])
	assert.Equal(t, 20.0/1000*RMB, d["yi-large"])
	// sanity on the constant itself
	assert.InDelta(t, 68.4931506, RMB, 1e-6)
	assert.Equal(t, 500.0, float64(USD))
}

func TestDefaultModelPrice_WellKnownAnchors(t *testing.T) {
	d := GetDefaultModelPriceMap()
	assert.Equal(t, 0.04, d["dall-e-3"])
	assert.Equal(t, 0.1, d["mj_imagine"])
	assert.Equal(t, 0.0, d["mj_inpaint"])
	assert.Equal(t, 0.5, d["sora-2-pro"])
}

func TestDefaultModelRatio2JSONString(t *testing.T) {
	s := DefaultModelRatio2JSONString()
	assert.Contains(t, s, `"gpt-4o"`)
	assert.NotEqual(t, "{}", s)
}
