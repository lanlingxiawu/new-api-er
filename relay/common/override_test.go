package common

import (
	"testing"

	commonpkg "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertJSONEqual compares two JSON documents ignoring key order/formatting.
func assertJSONEqual(t *testing.T, expected, actual string) {
	t.Helper()
	assert.JSONEq(t, expected, actual)
}

// op is a small helper to build an operation map.
func op(m map[string]interface{}) map[string]interface{} { return m }

// ops wraps a list of operations in the {"operations": [...]} envelope.
func ops(list ...map[string]interface{}) map[string]interface{} {
	arr := make([]interface{}, len(list))
	for i, o := range list {
		arr[i] = o
	}
	return map[string]interface{}{"operations": arr}
}

func apply(t *testing.T, input string, override map[string]interface{}, ctx map[string]interface{}) ([]byte, error) {
	t.Helper()
	return ApplyParamOverride([]byte(input), override, ctx)
}

// ---------------------------------------------------------------------------
// Empty / passthrough
// ---------------------------------------------------------------------------

func TestApplyParamOverride_Empty(t *testing.T) {
	out, err := apply(t, `{"a":1}`, nil, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"a":1}`, string(out))
}

// ---------------------------------------------------------------------------
// String transforms
// ---------------------------------------------------------------------------

func TestApplyParamOverride_TrimPrefixSuffix(t *testing.T) {
	out, err := apply(t, `{"model":"openai/gpt-4","t":0.7}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "trim_prefix", "value": "openai/",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","t":0.7}`, string(out))

	out, err = apply(t, `{"model":"gpt-4-latest"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "trim_suffix", "value": "-latest",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4"}`, string(out))

	// no-op when prefix absent
	out, err = apply(t, `{"model":"gpt-4"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "trim_prefix", "value": "openai/",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4"}`, string(out))
}

func TestApplyParamOverride_TrimRequiresValue(t *testing.T) {
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "trim_prefix",
	})), nil)
	require.Error(t, err)
}

func TestApplyParamOverride_EnsurePrefixSuffix(t *testing.T) {
	out, err := apply(t, `{"model":"gpt-4"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "ensure_prefix", "value": "openai/",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"openai/gpt-4"}`, string(out))

	// no-op when already prefixed
	out, err = apply(t, `{"model":"openai/gpt-4"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "ensure_prefix", "value": "openai/",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"openai/gpt-4"}`, string(out))

	out, err = apply(t, `{"model":"gpt-4"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "ensure_suffix", "value": "-turbo",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4-turbo"}`, string(out))
}

func TestApplyParamOverride_EnsureRequiresValue(t *testing.T) {
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "ensure_prefix",
	})), nil)
	require.Error(t, err)
}

func TestApplyParamOverride_CaseAndSpace(t *testing.T) {
	out, err := apply(t, `{"model":"  GpT-4  "}`, ops(
		op(map[string]interface{}{"path": "model", "mode": "trim_space"}),
		op(map[string]interface{}{"path": "model", "mode": "to_lower"}),
	), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4"}`, string(out))

	out, err = apply(t, `{"model":"gpt-4"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "to_upper",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"GPT-4"}`, string(out))
}

func TestApplyParamOverride_ReplaceAndRegex(t *testing.T) {
	out, err := apply(t, `{"model":"gpt-4-preview"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "replace", "from": "-preview", "to": "",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4"}`, string(out))

	out, err = apply(t, `{"model":"gpt-4-0613"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "regex_replace", "from": "-\\d+$", "to": "",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4"}`, string(out))
}

func TestApplyParamOverride_ReplaceRequiresFrom(t *testing.T) {
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "replace", "to": "y",
	})), nil)
	require.Error(t, err)
}

func TestApplyParamOverride_RegexInvalidPattern(t *testing.T) {
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "regex_replace", "from": "[", "to": "y",
	})), nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// set / delete
// ---------------------------------------------------------------------------

func TestApplyParamOverride_Set(t *testing.T) {
	out, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "temperature", "mode": "set", "value": 0.2,
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"x","temperature":0.2}`, string(out))
}

func TestApplyParamOverride_SetKeepOrigin(t *testing.T) {
	// keep_origin: existing value not overwritten
	out, err := apply(t, `{"temperature":0.7}`, ops(op(map[string]interface{}{
		"path": "temperature", "mode": "set", "value": 0.2, "keep_origin": true,
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"temperature":0.7}`, string(out))

	// keep_origin sets when absent
	out, err = apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"path": "temperature", "mode": "set", "value": 0.2, "keep_origin": true,
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"x","temperature":0.2}`, string(out))
}

func TestApplyParamOverride_Delete(t *testing.T) {
	out, err := apply(t, `{"model":"x","temperature":0.7}`, ops(op(map[string]interface{}{
		"path": "temperature", "mode": "delete",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"x"}`, string(out))
}

// ---------------------------------------------------------------------------
// move / copy
// ---------------------------------------------------------------------------

func TestApplyParamOverride_Move(t *testing.T) {
	out, err := apply(t, `{"a":1,"b":2}`, ops(op(map[string]interface{}{
		"mode": "move", "from": "a", "to": "c",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"b":2,"c":1}`, string(out))
}

func TestApplyParamOverride_MoveMissingSource(t *testing.T) {
	_, err := apply(t, `{"b":2}`, ops(op(map[string]interface{}{
		"mode": "move", "from": "a", "to": "c",
	})), nil)
	require.Error(t, err)
}

func TestApplyParamOverride_Copy(t *testing.T) {
	out, err := apply(t, `{"a":1}`, ops(op(map[string]interface{}{
		"mode": "copy", "from": "a", "to": "b",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"a":1,"b":1}`, string(out))
}

func TestApplyParamOverride_CopyRequiresFromTo(t *testing.T) {
	_, err := apply(t, `{"a":1}`, ops(op(map[string]interface{}{
		"mode": "copy", "from": "a",
	})), nil)
	require.Error(t, err)
}

func TestApplyParamOverride_CopyMissingSource(t *testing.T) {
	_, err := apply(t, `{"x":1}`, ops(op(map[string]interface{}{
		"mode": "copy", "from": "a", "to": "b",
	})), nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// prepend / append
// ---------------------------------------------------------------------------

func TestApplyParamOverride_PrependAppendString(t *testing.T) {
	out, err := apply(t, `{"model":"gpt"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "prepend", "value": "openai/",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"openai/gpt"}`, string(out))

	out, err = apply(t, `{"model":"gpt"}`, ops(op(map[string]interface{}{
		"path": "model", "mode": "append", "value": "-turbo",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-turbo"}`, string(out))
}

func TestApplyParamOverride_PrependAppendArray(t *testing.T) {
	out, err := apply(t, `{"stop":["b"]}`, ops(op(map[string]interface{}{
		"path": "stop", "mode": "prepend", "value": "a",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"stop":["a","b"]}`, string(out))

	out, err = apply(t, `{"stop":["a"]}`, ops(op(map[string]interface{}{
		"path": "stop", "mode": "append", "value": "b",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"stop":["a","b"]}`, string(out))
}

func TestApplyParamOverride_AppendObjectMerge(t *testing.T) {
	// append onto an object merges keys
	out, err := apply(t, `{"opts":{"a":1}}`, ops(op(map[string]interface{}{
		"path": "opts", "mode": "append", "value": map[string]interface{}{"b": 2},
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"opts":{"a":1,"b":2}}`, string(out))

	// keep_origin preserves existing keys on conflict
	out, err = apply(t, `{"opts":{"a":1}}`, ops(op(map[string]interface{}{
		"path": "opts", "mode": "append", "value": map[string]interface{}{"a": 9, "b": 2}, "keep_origin": true,
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"opts":{"a":1,"b":2}}`, string(out))
}

// ---------------------------------------------------------------------------
// Wildcard paths
// ---------------------------------------------------------------------------

func TestApplyParamOverride_DeleteWildcard(t *testing.T) {
	input := `{"messages":[{"role":"user","x":1},{"role":"assistant","x":2}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "messages.*.x", "mode": "delete",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"messages":[{"role":"user"},{"role":"assistant"}]}`, string(out))
}

func TestApplyParamOverride_SetWildcard(t *testing.T) {
	input := `{"messages":[{"role":"user"},{"role":"assistant"}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "messages.*.seen", "mode": "set", "value": true,
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"messages":[{"role":"user","seen":true},{"role":"assistant","seen":true}]}`, string(out))
}

// ---------------------------------------------------------------------------
// Negative index
// ---------------------------------------------------------------------------

func TestApplyParamOverride_NegativeIndex(t *testing.T) {
	input := `{"messages":[{"role":"user"},{"role":"assistant"}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "messages.-1.role", "mode": "set", "value": "system",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"messages":[{"role":"user"},{"role":"system"}]}`, string(out))
}

// ---------------------------------------------------------------------------
// Conditions
// ---------------------------------------------------------------------------

func TestApplyParamOverride_ConditionOR(t *testing.T) {
	input := `{"model":"gpt-4","stream":true}`
	override := ops(op(map[string]interface{}{
		"path": "temperature", "mode": "set", "value": 0.1,
		"conditions": []interface{}{
			map[string]interface{}{"path": "stream", "mode": "full", "value": true},
			map[string]interface{}{"path": "model", "mode": "full", "value": "nope"},
		},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","stream":true,"temperature":0.1}`, string(out))
}

func TestApplyParamOverride_ConditionAND_NotMet(t *testing.T) {
	input := `{"model":"gpt-4","stream":true}`
	override := ops(op(map[string]interface{}{
		"path": "temperature", "mode": "set", "value": 0.1, "logic": "AND",
		"conditions": []interface{}{
			map[string]interface{}{"path": "stream", "mode": "full", "value": true},
			map[string]interface{}{"path": "model", "mode": "full", "value": "nope"},
		},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, input, string(out)) // condition not met -> unchanged
}

func TestApplyParamOverride_ConditionInvert(t *testing.T) {
	input := `{"model":"gpt-4"}`
	override := ops(op(map[string]interface{}{
		"path": "flag", "mode": "set", "value": true,
		"conditions": []interface{}{
			map[string]interface{}{"path": "model", "mode": "full", "value": "gpt-4", "invert": true},
		},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, input, string(out)) // inverted match -> false -> skip
}

func TestApplyParamOverride_ConditionPassMissingKey(t *testing.T) {
	input := `{"model":"gpt-4"}`
	override := ops(op(map[string]interface{}{
		"path": "flag", "mode": "set", "value": true,
		"conditions": []interface{}{
			map[string]interface{}{"path": "absent", "mode": "full", "value": "x", "pass_missing_key": true},
		},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","flag":true}`, string(out))
}

func TestApplyParamOverride_ConditionNumericComparison(t *testing.T) {
	input := `{"n":5}`
	override := ops(op(map[string]interface{}{
		"path": "big", "mode": "set", "value": true,
		"conditions": []interface{}{
			map[string]interface{}{"path": "n", "mode": "gte", "value": 5},
		},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"n":5,"big":true}`, string(out))
}

func TestApplyParamOverride_ConditionFromContext(t *testing.T) {
	input := `{"model":"gpt-4"}`
	ctx := map[string]interface{}{"is_retry": true}
	override := ops(op(map[string]interface{}{
		"path": "retry_flag", "mode": "set", "value": true,
		"conditions": []interface{}{
			map[string]interface{}{"path": "is_retry", "mode": "full", "value": true},
		},
	}))
	out, err := apply(t, input, override, ctx)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","retry_flag":true}`, string(out))
}

func TestApplyParamOverride_ConditionShorthandObject(t *testing.T) {
	input := `{"model":"gpt-4"}`
	override := ops(op(map[string]interface{}{
		"path": "flag", "mode": "set", "value": true,
		"conditions": map[string]interface{}{"model": "gpt-4"},
	}))
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","flag":true}`, string(out))
}

// ---------------------------------------------------------------------------
// return_error
// ---------------------------------------------------------------------------

func TestApplyParamOverride_ReturnError(t *testing.T) {
	t.Run("string message", func(t *testing.T) {
		_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
			"mode": "return_error", "value": "blocked model",
		})), nil)
		require.Error(t, err)
		re, ok := AsParamOverrideReturnError(err)
		require.True(t, ok)
		assert.Equal(t, "blocked model", re.Message)
	})
	t.Run("object with status and code", func(t *testing.T) {
		_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
			"mode": "return_error", "value": map[string]interface{}{
				"message": "no", "status_code": float64(403), "code": "forbidden",
			},
		})), nil)
		require.Error(t, err)
		re, ok := AsParamOverrideReturnError(err)
		require.True(t, ok)
		assert.Equal(t, 403, re.StatusCode)
		assert.Equal(t, "forbidden", re.Code)
	})
	t.Run("only fires when condition met", func(t *testing.T) {
		out, err := apply(t, `{"model":"ok"}`, ops(op(map[string]interface{}{
			"mode": "return_error", "value": "blocked",
			"conditions": []interface{}{
				map[string]interface{}{"path": "model", "mode": "full", "value": "bad"},
			},
		})), nil)
		require.NoError(t, err)
		assertJSONEqual(t, `{"model":"ok"}`, string(out))
	})
}

// ---------------------------------------------------------------------------
// prune_objects
// ---------------------------------------------------------------------------

func TestApplyParamOverride_PruneObjectsByType(t *testing.T) {
	input := `{"content":[{"type":"text","text":"hi"},{"type":"thinking","text":"secret"}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "content", "mode": "prune_objects", "value": "thinking",
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"content":[{"type":"text","text":"hi"}]}`, string(out))
}

func TestApplyParamOverride_PruneObjectsWhere(t *testing.T) {
	input := `{"items":[{"kind":"a","keep":1},{"kind":"b","keep":2}]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "items", "mode": "prune_objects",
		"value": map[string]interface{}{"where": map[string]interface{}{"kind": "b"}},
	})), nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"items":[{"kind":"a","keep":1}]}`, string(out))
}

func TestApplyParamOverride_PruneObjectsRequiresValue(t *testing.T) {
	_, err := apply(t, `{"a":[]}`, ops(op(map[string]interface{}{
		"path": "a", "mode": "prune_objects",
	})), nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Unknown mode
// ---------------------------------------------------------------------------

func TestApplyParamOverride_UnknownMode(t *testing.T) {
	_, err := apply(t, `{"a":1}`, ops(op(map[string]interface{}{
		"path": "a", "mode": "frobnicate",
	})), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown operation")
}

// ---------------------------------------------------------------------------
// Legacy override (no "operations" key)
// ---------------------------------------------------------------------------

func TestApplyParamOverride_Legacy(t *testing.T) {
	out, err := apply(t, `{"model":"x","temperature":0.7}`, map[string]interface{}{
		"temperature": 0.2,
		"top_p":       0.95,
	}, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"x","temperature":0.2,"top_p":0.95}`, string(out))
}

func TestApplyParamOverride_MixedLegacyAndOperations(t *testing.T) {
	input := `{"model":"openai/gpt-4","temperature":0.7}`
	override := map[string]interface{}{
		"temperature": 0.2,
		"top_p":       0.95,
		"operations": []interface{}{
			op(map[string]interface{}{"path": "model", "mode": "trim_prefix", "value": "openai/"}),
		},
	}
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4","temperature":0.2,"top_p":0.95}`, string(out))
}

func TestApplyParamOverride_MixedConflictPrefersOperations(t *testing.T) {
	input := `{"model":"openai/gpt-4","temperature":0.7}`
	override := map[string]interface{}{
		"model":       "legacy-model",
		"temperature": 0.2,
		"operations": []interface{}{
			op(map[string]interface{}{"path": "model", "mode": "set", "value": "op-model"}),
		},
	}
	out, err := apply(t, input, override, nil)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"op-model","temperature":0.2}`, string(out))
}

// ---------------------------------------------------------------------------
// Header operations (set/delete/copy/move/pass) via context
// ---------------------------------------------------------------------------

func newHeaderContext(reqHeaders map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		paramOverrideContextRequestHeaders: reqHeaders,
		paramOverrideContextHeaderOverride: map[string]interface{}{},
	}
}

func TestApplyParamOverride_SetHeader(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{})
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "set_header", "path": "X-Custom", "value": "v1",
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "v1", ho["x-custom"])
}

func TestApplyParamOverride_SetHeaderKeepOrigin(t *testing.T) {
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"x-custom": "orig"},
	}
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "set_header", "path": "X-Custom", "value": "new", "keep_origin": true,
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "orig", ho["x-custom"])
}

func TestApplyParamOverride_DeleteHeader(t *testing.T) {
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"x-custom": "v"},
	}
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "delete_header", "path": "X-Custom",
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	_, exists := ho["x-custom"]
	assert.False(t, exists)
}

func TestApplyParamOverride_CopyHeaderFromRequest(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{"authorization": "Bearer abc"})
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "copy_header", "from": "Authorization", "to": "X-Auth",
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "Bearer abc", ho["x-auth"])
}

func TestApplyParamOverride_CopyHeaderMissingSourceSkipped(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{})
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "copy_header", "from": "Absent", "to": "X-Auth",
	})), ctx)
	require.NoError(t, err) // missing source is silently skipped
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	_, exists := ho["x-auth"]
	assert.False(t, exists)
}

func TestApplyParamOverride_MoveHeader(t *testing.T) {
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"x-from": "v"},
	}
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "move_header", "from": "X-From", "to": "X-To",
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "v", ho["x-to"])
	_, exists := ho["x-from"]
	assert.False(t, exists)
}

func TestApplyParamOverride_PassHeaders(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{"x-a": "1", "x-b": "2"})
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "pass_headers", "value": []interface{}{"X-A", "X-B", "X-Missing"},
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "1", ho["x-a"])
	assert.Equal(t, "2", ho["x-b"])
}

func TestApplyParamOverride_SetHeaderMapRewritesTokens(t *testing.T) {
	// header token mapping: rewrite one token in a comma-separated header
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "old-beta,keep-beta"},
	}
	_, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "set_header", "path": "anthropic-beta",
		"value": map[string]interface{}{"old-beta": "new-beta"},
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "new-beta,keep-beta", ho["anthropic-beta"])
}

// ---------------------------------------------------------------------------
// sync_fields
// ---------------------------------------------------------------------------

func TestApplyParamOverride_SyncFieldsHeaderToJSON(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{"x-session": "sess-123"})
	out, err := apply(t, `{"model":"x"}`, ops(op(map[string]interface{}{
		"mode": "sync_fields", "from": "header:X-Session", "to": "json:session_id",
	})), ctx)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"x","session_id":"sess-123"}`, string(out))
}

func TestApplyParamOverride_SyncFieldsJSONToHeader(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{})
	_, err := apply(t, `{"session_id":"abc"}`, ops(op(map[string]interface{}{
		"mode": "sync_fields", "from": "header:X-Session", "to": "json:session_id",
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "abc", ho["x-session"])
}

func TestApplyParamOverride_SyncFieldsNoChangeWhenBothExist(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{"x-session": "hdr"})
	out, err := apply(t, `{"session_id":"body"}`, ops(op(map[string]interface{}{
		"mode": "sync_fields", "from": "header:X-Session", "to": "json:session_id",
	})), ctx)
	require.NoError(t, err)
	assertJSONEqual(t, `{"session_id":"body"}`, string(out))
}

func TestApplyParamOverride_SyncFieldsInvalidTarget(t *testing.T) {
	ctx := newHeaderContext(map[string]interface{}{})
	_, err := apply(t, `{"a":1}`, ops(op(map[string]interface{}{
		"mode": "sync_fields", "from": "bogus:X", "to": "json:a",
	})), ctx)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ApplyParamOverrideWithRelayInfo
// ---------------------------------------------------------------------------

func TestApplyParamOverrideWithRelayInfo_Basic(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		RequestURLPath:  "/v1/chat/completions",
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"path": "temperature", "mode": "set", "value": 0.3}),
				},
			},
		},
	}
	out, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4o"}`), info)
	require.NoError(t, err)
	assertJSONEqual(t, `{"model":"gpt-4o","temperature":0.3}`, string(out))
}

func TestApplyParamOverrideWithRelayInfo_NoOverride(t *testing.T) {
	info := &RelayInfo{ChannelMeta: &ChannelMeta{}}
	out, err := ApplyParamOverrideWithRelayInfo([]byte(`{"a":1}`), info)
	require.NoError(t, err)
	assertJSONEqual(t, `{"a":1}`, string(out))
}

func TestApplyParamOverrideWithRelayInfo_SyncRuntimeHeaders(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		RequestHeaders:  map[string]string{"Authorization": "Bearer abc"},
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"mode": "copy_header", "from": "Authorization", "to": "X-Auth"}),
				},
			},
		},
	}
	_, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4o"}`), info)
	require.NoError(t, err)
	assert.True(t, info.UseRuntimeHeadersOverride)
	assert.Equal(t, "Bearer abc", info.RuntimeHeadersOverride["x-auth"])
}

func TestApplyParamOverrideWithRelayInfo_AuditRecordsSensitivePath(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"path": "model", "mode": "set", "value": "gpt-4o-mini"}),
				},
			},
		},
	}
	_, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4o"}`), info)
	require.NoError(t, err)
	require.NotEmpty(t, info.ParamOverrideAudit)
	assert.Contains(t, info.ParamOverrideAudit[0], "set model")
}

func TestApplyParamOverrideWithRelayInfo_NoAuditForNonSensitive(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"path": "temperature", "mode": "set", "value": 0.3}),
				},
			},
		},
	}
	_, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4o"}`), info)
	require.NoError(t, err)
	assert.Empty(t, info.ParamOverrideAudit)
}

// ---------------------------------------------------------------------------
// BuildParamOverrideContext / GetEffectiveHeaderOverride
// ---------------------------------------------------------------------------

func TestBuildParamOverrideContext(t *testing.T) {
	t.Run("nil info", func(t *testing.T) {
		assert.Nil(t, BuildParamOverrideContext(nil))
	})
	t.Run("populates model, path, headers, retry", func(t *testing.T) {
		info := &RelayInfo{
			OriginModelName: "gpt-4o",
			RequestURLPath:  "/v1/chat/completions",
			RequestHeaders:  map[string]string{"X-Test": "v"},
			RetryIndex:      2,
			ChannelMeta:     &ChannelMeta{UpstreamModelName: "gpt-4o-up"},
			IsChannelTest:   true,
		}
		ctx := BuildParamOverrideContext(info)
		assert.Equal(t, "gpt-4o-up", ctx["model"])
		assert.Equal(t, "gpt-4o-up", ctx["upstream_model"])
		assert.Equal(t, "gpt-4o", ctx["original_model"])
		assert.Equal(t, "/v1/chat/completions", ctx["request_path"])
		assert.Equal(t, 2, ctx["retry_index"])
		assert.Equal(t, true, ctx["is_retry"])
		assert.Equal(t, true, ctx["is_channel_test"])
		rh := ctx[paramOverrideContextRequestHeaders].(map[string]interface{})
		assert.Equal(t, "v", rh["x-test"])
	})
	t.Run("last error surfaced", func(t *testing.T) {
		info := &RelayInfo{
			OriginModelName: "gpt-4o",
			LastError:       types.NewError(assertErr("boom"), types.ErrorCodeInvalidRequest),
		}
		ctx := BuildParamOverrideContext(info)
		require.Contains(t, ctx, "last_error")
		le := ctx["last_error"].(map[string]interface{})
		assert.Contains(t, le["message"], "boom")
	})
}

func TestGetEffectiveHeaderOverride(t *testing.T) {
	t.Run("nil info", func(t *testing.T) {
		assert.Equal(t, map[string]interface{}{}, GetEffectiveHeaderOverride(nil))
	})
	t.Run("channel header override sanitized", func(t *testing.T) {
		info := &RelayInfo{ChannelMeta: &ChannelMeta{HeadersOverride: map[string]interface{}{
			"X-Custom": "  value  ", "empty": "",
		}}}
		got := GetEffectiveHeaderOverride(info)
		assert.Equal(t, "value", got["x-custom"])
		_, hasEmpty := got["empty"]
		assert.False(t, hasEmpty)
	})
	t.Run("runtime override takes precedence", func(t *testing.T) {
		info := &RelayInfo{
			UseRuntimeHeadersOverride: true,
			RuntimeHeadersOverride:    map[string]interface{}{"X-Runtime": "rt"},
			ChannelMeta:               &ChannelMeta{HeadersOverride: map[string]interface{}{"X-Channel": "ch"}},
		}
		got := GetEffectiveHeaderOverride(info)
		assert.Equal(t, "rt", got["x-runtime"])
		_, hasChannel := got["x-channel"]
		assert.False(t, hasChannel)
	})
	t.Run("passthrough rule key keeps empty value", func(t *testing.T) {
		info := &RelayInfo{ChannelMeta: &ChannelMeta{HeadersOverride: map[string]interface{}{
			"*": "", "re:x-.*": "",
		}}}
		got := GetEffectiveHeaderOverride(info)
		v, ok := got["*"]
		assert.True(t, ok)
		assert.Equal(t, "", v)
	})
}

// ---------------------------------------------------------------------------
// RemoveDisabledFields / RemoveGeminiDisabledFields
// ---------------------------------------------------------------------------

func TestRemoveDisabledFields(t *testing.T) {
	t.Run("channel passthrough keeps body", func(t *testing.T) {
		body := []byte(`{"service_tier":"flex","model":"x"}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{}, true)
		require.NoError(t, err)
		assert.Equal(t, body, out)
	})
	t.Run("default removes service_tier and safety_identifier", func(t *testing.T) {
		body := []byte(`{"service_tier":"flex","safety_identifier":"user1","model":"x"}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{}, false)
		require.NoError(t, err)
		assert.NotContains(t, string(out), "service_tier")
		assert.NotContains(t, string(out), "safety_identifier")
		assert.Contains(t, string(out), `"model":"x"`)
	})
	t.Run("no controlled fields keeps body", func(t *testing.T) {
		body := []byte(`{"model":"x","temperature":0.5}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{}, false)
		require.NoError(t, err)
		assert.Equal(t, body, out)
	})
	t.Run("allow service_tier keeps it", func(t *testing.T) {
		body := []byte(`{"service_tier":"flex","model":"x"}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{AllowServiceTier: true, AllowSafetyIdentifier: true}, false)
		require.NoError(t, err)
		assert.Contains(t, string(out), "service_tier")
	})
	t.Run("global passthrough keeps body", func(t *testing.T) {
		g := model_setting.GetGlobalSettings()
		old := g.PassThroughRequestEnabled
		g.PassThroughRequestEnabled = true
		t.Cleanup(func() { g.PassThroughRequestEnabled = old })
		body := []byte(`{"service_tier":"flex"}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{}, false)
		require.NoError(t, err)
		assert.Equal(t, body, out)
	})
	t.Run("removes stream_options.include_obfuscation", func(t *testing.T) {
		body := []byte(`{"model":"x","stream_options":{"include_obfuscation":true,"other":1}}`)
		out, err := RemoveDisabledFields(body, dto.ChannelOtherSettings{}, false)
		require.NoError(t, err)
		assert.NotContains(t, string(out), "include_obfuscation")
		assert.Contains(t, string(out), `"other":1`)
	})
}

func TestRemoveGeminiDisabledFields(t *testing.T) {
	t.Run("disabled setting keeps body", func(t *testing.T) {
		g := model_setting.GetGeminiSettings()
		old := g.RemoveFunctionResponseIdEnabled
		g.RemoveFunctionResponseIdEnabled = false
		t.Cleanup(func() { g.RemoveFunctionResponseIdEnabled = old })
		body := []byte(`{"contents":[{"parts":[{"functionResponse":{"id":"x","name":"f"}}]}]}`)
		out, err := RemoveGeminiDisabledFields(body)
		require.NoError(t, err)
		assert.Equal(t, body, out)
	})
	t.Run("enabled removes functionResponse id", func(t *testing.T) {
		g := model_setting.GetGeminiSettings()
		old := g.RemoveFunctionResponseIdEnabled
		g.RemoveFunctionResponseIdEnabled = true
		t.Cleanup(func() { g.RemoveFunctionResponseIdEnabled = old })
		body := []byte(`{"contents":[{"parts":[{"functionResponse":{"id":"x","name":"f"}}]}]}`)
		out, err := RemoveGeminiDisabledFields(body)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"id":"x"`)
		assert.Contains(t, string(out), `"name":"f"`)
	})
}

// keep imports used
var _ = commonpkg.Marshal
