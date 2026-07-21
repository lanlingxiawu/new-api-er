package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeModelNames trims, drops empties, and de-duplicates preserving order.
func TestNormalizeModelNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b"},
		normalizeModelNames([]string{" a ", "b", "a", "  ", ""}))
	assert.Empty(t, normalizeModelNames([]string{"", "  "}))
}

// mergeModelNames appends only the models not already present.
func TestMergeModelNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"},
		mergeModelNames([]string{"a", "b"}, []string{"b", "c", "c"}))
}

// subtractModelNames removes the given set.
func TestSubtractModelNames(t *testing.T) {
	assert.Equal(t, []string{"a", "c"},
		subtractModelNames([]string{"a", "b", "c"}, []string{"b", "x"}))
}

// intersectModelNames keeps only allowed models.
func TestIntersectModelNames(t *testing.T) {
	assert.Equal(t, []string{"a", "c"},
		intersectModelNames([]string{"a", "b", "c"}, []string{"a", "c", "z"}))
}

// applySelectedModelChanges: add wins over remove when the same model is in both.
func TestApplySelectedModelChanges_AddWins(t *testing.T) {
	result := applySelectedModelChanges(
		[]string{"a", "b"},
		[]string{"c", "b"}, // add b again
		[]string{"b", "a"}, // remove a and b
	)
	// a removed, b kept (add wins), c added
	assert.ElementsMatch(t, []string{"b", "c"}, result)
}

// normalizeChannelModelMapping: nil / empty / "{}" -> nil; valid mapping trims
// and drops blank source/target pairs.
func TestNormalizeChannelModelMapping(t *testing.T) {
	assert.Nil(t, normalizeChannelModelMapping(nil))
	assert.Nil(t, normalizeChannelModelMapping(&model.Channel{}))

	empty := "{}"
	assert.Nil(t, normalizeChannelModelMapping(&model.Channel{ModelMapping: &empty}))

	mapping := `{" gpt-4o ":" upstream-4o ","blank":""}`
	got := normalizeChannelModelMapping(&model.Channel{ModelMapping: &mapping})
	require.NotNil(t, got)
	assert.Equal(t, map[string]string{"gpt-4o": "upstream-4o"}, got)
}

// collectPendingUpstreamModelChangesFromModels: computes adds (upstream not
// covered locally / by redirect target / by ignore) and removes (local absent
// upstream, but redirect sources are protected).
func TestCollectPendingUpstreamModelChangesFromModels(t *testing.T) {
	local := []string{"gpt-4o", "alias-src"}
	upstream := []string{"gpt-4o", "new-model", "ignored-x", "regex-abc"}
	ignored := []string{"ignored-x", "regex:^regex-"}
	mapping := map[string]string{"alias-src": "new-model"} // redirect target covers new-model

	add, remove := collectPendingUpstreamModelChangesFromModels(local, upstream, ignored, mapping)

	// new-model is covered by redirect target; ignored-x and regex-abc are ignored.
	assert.Empty(t, add)
	// alias-src is a redirect source -> protected from removal even though it is
	// absent upstream.
	assert.Empty(t, remove)
}

func TestCollectPendingUpstreamModelChangesFromModels_AddAndRemove(t *testing.T) {
	local := []string{"keep", "stale"}
	upstream := []string{"keep", "fresh"}
	add, remove := collectPendingUpstreamModelChangesFromModels(local, upstream, nil, nil)
	assert.Equal(t, []string{"fresh"}, add)
	assert.Equal(t, []string{"stale"}, remove)
}

// parseOpenAIModelIDs: valid response, missing data, and no valid ids.
func TestParseOpenAIModelIDs(t *testing.T) {
	ids, err := parseOpenAIModelIDs([]byte(`{"data":[{"id":"gpt-4o"},{"id":" gpt-4o "},{"id":""},{"id":"claude"}]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-4o", "claude"}, ids)

	_, err = parseOpenAIModelIDs([]byte(`{}`))
	assert.Error(t, err, "missing data field must error")

	_, err = parseOpenAIModelIDs([]byte(`{"data":[{"id":""}]}`))
	assert.Error(t, err, "no valid ids must error")

	_, err = parseOpenAIModelIDs([]byte(`not json`))
	assert.Error(t, err)
}
