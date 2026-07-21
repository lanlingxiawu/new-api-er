package xinference

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This package is DTO/metadata only (no functions), so tests focus on JSON
// shape correctness and the model registry.

func TestXinRerankResponse_Unmarshal(t *testing.T) {
	fixture := `{
		"results": [
			{"index": 0, "relevance_score": 0.98, "document": "first doc"},
			{"index": 1, "relevance_score": 0.42, "document": {"text": "obj doc"}}
		]
	}`
	var resp XinRerankResponse
	require.NoError(t, json.Unmarshal([]byte(fixture), &resp))
	require.Len(t, resp.Results, 2)

	assert.Equal(t, 0, resp.Results[0].Index)
	assert.InDelta(t, 0.98, resp.Results[0].RelevanceScore, 1e-9)
	assert.Equal(t, "first doc", resp.Results[0].Document)

	assert.Equal(t, 1, resp.Results[1].Index)
	assert.InDelta(t, 0.42, resp.Results[1].RelevanceScore, 1e-9)
	// arbitrary JSON object decodes into a map under `any`.
	doc, ok := resp.Results[1].Document.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "obj doc", doc["text"])
}

func TestXinRerankResponse_EmptyResults(t *testing.T) {
	var resp XinRerankResponse
	require.NoError(t, json.Unmarshal([]byte(`{"results": []}`), &resp))
	assert.Len(t, resp.Results, 0)
}

func TestXinRerankResponseDocument_OmitEmptyDocument(t *testing.T) {
	// Document is omitempty: when nil it must not appear in the marshalled JSON.
	doc := XinRerankResponseDocument{Index: 3, RelevanceScore: 0.5}
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "document")
	assert.Contains(t, string(out), `"index":3`)
	assert.Contains(t, string(out), `"relevance_score":0.5`)
}

func TestXinRerankResponseDocument_RoundTrip(t *testing.T) {
	orig := XinRerankResponseDocument{
		Document:       "hello",
		Index:          7,
		RelevanceScore: 0.123,
	}
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var back XinRerankResponseDocument
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, orig, back)
}

func TestModelRegistry(t *testing.T) {
	assert.Equal(t, "xinference", ChannelName)
	assert.Contains(t, ModelList, "bge-reranker-v2-m3")
	assert.Contains(t, ModelList, "jina-reranker-v2")
	assert.Len(t, ModelList, 2)
}
