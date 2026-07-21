package common

import (
	"testing"

	commonpkg "github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// condition builds a single-condition set operation for exercising compare modes.
func condSet(path, mode string, value interface{}, invert bool) map[string]interface{} {
	return ops(op(map[string]interface{}{
		"path": "matched", "mode": "set", "value": true,
		"conditions": []interface{}{
			map[string]interface{}{"path": path, "mode": mode, "value": value, "invert": invert},
		},
	}))
}

func TestCompareModes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		path    string
		mode    string
		value   interface{}
		invert  bool
		matched bool
	}{
		{"prefix hit", `{"model":"openai/gpt-4"}`, "model", "prefix", "openai/", false, true},
		{"prefix miss", `{"model":"gpt-4"}`, "model", "prefix", "openai/", false, false},
		{"suffix hit", `{"model":"gpt-4-latest"}`, "model", "suffix", "-latest", false, true},
		{"contains hit", `{"model":"a-gpt-b"}`, "model", "contains", "gpt", false, true},
		{"gt hit", `{"n":10}`, "n", "gt", 5, false, true},
		{"gt miss", `{"n":3}`, "n", "gt", 5, false, false},
		{"lt hit", `{"n":3}`, "n", "lt", 5, false, true},
		{"lte boundary", `{"n":5}`, "n", "lte", 5, false, true},
		{"full bool", `{"stream":true}`, "stream", "full", true, false, true},
		{"full number", `{"n":5}`, "n", "full", 5, false, true},
		{"full null both", `{"x":null}`, "x", "full", nil, false, true},
		{"invert prefix", `{"model":"gpt-4"}`, "model", "prefix", "openai/", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := apply(t, tt.input, condSet(tt.path, tt.mode, tt.value, tt.invert), nil)
			require.NoError(t, err)
			if tt.matched {
				assert.Contains(t, string(out), `"matched":true`)
			} else {
				assert.NotContains(t, string(out), `"matched":true`)
			}
		})
	}
}

func TestCompareModes_Errors(t *testing.T) {
	t.Run("unsupported mode", func(t *testing.T) {
		_, err := apply(t, `{"model":"x"}`, condSet("model", "weird", "x", false), nil)
		require.Error(t, err)
	})
	t.Run("type mismatch on full", func(t *testing.T) {
		// jsonValue string vs number target -> compareEqual type error
		_, err := apply(t, `{"model":"x"}`, condSet("model", "full", 5, false), nil)
		require.Error(t, err)
	})
	t.Run("numeric compare on non-number", func(t *testing.T) {
		_, err := apply(t, `{"model":"x"}`, condSet("model", "gt", 5, false), nil)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Header mapping variants
// ---------------------------------------------------------------------------

func TestSetHeaderMap_AppendAndKeepOnlyDeclared(t *testing.T) {
	t.Run("append tokens", func(t *testing.T) {
		ctx := map[string]interface{}{
			paramOverrideContextRequestHeaders: map[string]interface{}{},
			paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a,b"},
		}
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "set_header", "path": "anthropic-beta",
			"value": map[string]interface{}{"$append": "c"},
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "a,b,c", ho["anthropic-beta"])
	})
	t.Run("keep only declared drops undeclared", func(t *testing.T) {
		ctx := map[string]interface{}{
			paramOverrideContextRequestHeaders: map[string]interface{}{},
			paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a,b,c"},
		}
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "set_header", "path": "anthropic-beta",
			"value": map[string]interface{}{"a": "a", "$keep_only_declared": true},
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "a", ho["anthropic-beta"])
	})
	t.Run("wildcard replacement", func(t *testing.T) {
		ctx := map[string]interface{}{
			paramOverrideContextRequestHeaders: map[string]interface{}{},
			paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a,b"},
		}
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "set_header", "path": "anthropic-beta",
			"value": map[string]interface{}{"*": "z"},
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "z", ho["anthropic-beta"])
	})
	t.Run("clearing all tokens deletes header", func(t *testing.T) {
		ctx := map[string]interface{}{
			paramOverrideContextRequestHeaders: map[string]interface{}{},
			paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a"},
		}
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "set_header", "path": "anthropic-beta",
			"value": map[string]interface{}{"a": "", "$keep_only_declared": true},
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		_, exists := ho["anthropic-beta"]
		assert.False(t, exists)
	})
}

func TestPassHeaders_Variants(t *testing.T) {
	base := func() map[string]interface{} {
		return newHeaderContext(map[string]interface{}{"x-a": "1", "x-b": "2"})
	}
	t.Run("comma string", func(t *testing.T) {
		ctx := base()
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "pass_headers", "value": "X-A, X-B",
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "1", ho["x-a"])
		assert.Equal(t, "2", ho["x-b"])
	})
	t.Run("json array string", func(t *testing.T) {
		ctx := base()
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "pass_headers", "value": `["X-A"]`,
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "1", ho["x-a"])
	})
	t.Run("object with names", func(t *testing.T) {
		ctx := base()
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "pass_headers", "value": map[string]interface{}{"names": []interface{}{"X-B"}},
		})), ctx)
		require.NoError(t, err)
		ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
		assert.Equal(t, "2", ho["x-b"])
	})
	t.Run("empty value errors", func(t *testing.T) {
		ctx := base()
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "pass_headers", "value": "",
		})), ctx)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Legacy override escaping
// ---------------------------------------------------------------------------

func TestApplyParamOverride_LegacyEscapesDottedKey(t *testing.T) {
	// A legacy key containing a dot is treated as a literal key, not a path.
	out, err := apply(t, `{"model":"x"}`, map[string]interface{}{
		"a.b": 1,
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"a.b":1`)
}

// ---------------------------------------------------------------------------
// Multi-level wildcard
// ---------------------------------------------------------------------------

func TestApplyParamOverride_MultiLevelWildcard(t *testing.T) {
	input := `{"messages":[{"content":[{"x":1},{"x":2}]},{"content":[{"x":3}]}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "messages.*.content.*.x", "mode": "delete",
	})), nil)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"x":`)
}

// ---------------------------------------------------------------------------
// prune_objects root + recursive false
// ---------------------------------------------------------------------------

func TestPruneObjects_RootPath(t *testing.T) {
	// Empty path prunes from the document root.
	input := `{"type":"keep","child":{"type":"thinking","v":1}}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "", "mode": "prune_objects", "value": "thinking",
	})), nil)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "thinking")
	assert.Contains(t, string(out), `"type":"keep"`)
}

// ---------------------------------------------------------------------------
// return_error value shapes
// ---------------------------------------------------------------------------

func TestReturnError_Shapes(t *testing.T) {
	t.Run("nil value is a parse error", func(t *testing.T) {
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "return_error",
		})), nil)
		require.Error(t, err)
		// nil value fails parsing (not a blockable ParamOverrideReturnError).
		require.Contains(t, err.Error(), "value is required")
	})
	t.Run("string status code out of range errors", func(t *testing.T) {
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "return_error", "value": map[string]interface{}{"message": "x", "status_code": float64(99)},
		})), nil)
		require.Error(t, err)
	})
	t.Run("skip_retry false honored", func(t *testing.T) {
		_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
			"mode": "return_error", "value": map[string]interface{}{"message": "x", "skip_retry": false},
		})), nil)
		require.Error(t, err)
		re, _ := AsParamOverrideReturnError(err)
		assert.False(t, re.SkipRetry)
	})
}

// ---------------------------------------------------------------------------
// Audit lines in debug mode (exercises buildParamOverrideAuditLine branches)
// ---------------------------------------------------------------------------

func TestApplyParamOverrideWithRelayInfo_DebugAuditAllModes(t *testing.T) {
	old := commonpkg.DebugEnabled
	commonpkg.DebugEnabled = true
	t.Cleanup(func() { commonpkg.DebugEnabled = old })

	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		RequestHeaders:  map[string]string{"X-Src": "v"},
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"path": "a", "mode": "set", "value": 1}),
					op(map[string]interface{}{"path": "a", "mode": "delete"}),
					op(map[string]interface{}{"path": "model", "mode": "prepend", "value": "x/"}),
					op(map[string]interface{}{"path": "model", "mode": "append", "value": "-y"}),
					op(map[string]interface{}{"path": "model", "mode": "to_lower"}),
					op(map[string]interface{}{"mode": "copy_header", "from": "X-Src", "to": "X-Dst"}),
				},
			},
		},
	}
	_, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"GPT"}`), info)
	require.NoError(t, err)
	require.NotEmpty(t, info.ParamOverrideAudit)
	// In debug mode even non-sensitive paths are recorded.
	joined := ""
	for _, l := range info.ParamOverrideAudit {
		joined += l + "\n"
	}
	assert.Contains(t, joined, "delete a")
	assert.Contains(t, joined, "copy_header")
}

// ---------------------------------------------------------------------------
// Additional RelayInfo constructors
// ---------------------------------------------------------------------------

func TestGenRelayInfo_AudioImageEmbeddingGeminiWs(t *testing.T) {
	c := newRelayContext(t, "/v1/audio/speech")
	assert.NotNil(t, GenRelayInfoOpenAIAudio(c, nil))
	assert.NotNil(t, GenRelayInfoImage(newRelayContext(t, "/v1/images/generations"), nil))
	assert.NotNil(t, GenRelayInfoEmbedding(newRelayContext(t, "/v1/embeddings"), nil))
	assert.NotNil(t, GenRelayInfoGemini(newRelayContext(t, "/v1beta/models/g:generateContent"), nil))

	ws := GenRelayInfoWs(newRelayContext(t, "/v1/realtime"), nil)
	require.NotNil(t, ws)
	assert.Equal(t, "pcm16", ws.InputAudioFormat)
	assert.True(t, ws.IsFirstRequest)
}

// ---------------------------------------------------------------------------
// validateMultipartTaskRequest / isKnownTaskField
// ---------------------------------------------------------------------------

func TestIsKnownTaskField(t *testing.T) {
	for _, f := range []string{"prompt", "model", "mode", "image", "images", "size", "duration", "input_reference"} {
		assert.True(t, isKnownTaskField(f), f)
	}
	assert.False(t, isKnownTaskField("unknown_field"))
}
