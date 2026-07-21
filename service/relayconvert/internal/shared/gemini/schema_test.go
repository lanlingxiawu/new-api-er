package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- CleanFunctionParameters ------------------------------------------------

func TestCleanFunctionParameters_Nil(t *testing.T) {
	assert.Nil(t, CleanFunctionParameters(nil))
}

func TestCleanFunctionParameters_DropsDisallowedFields(t *testing.T) {
	in := map[string]interface{}{
		"type":                 "object",
		"description":          "d",
		"additionalProperties": false, // not in allow-list -> dropped
		"$schema":              "http://x",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "foo": "bar"},
		},
	}
	out := CleanFunctionParameters(in).(map[string]interface{})
	assert.Equal(t, "OBJECT", out["type"])
	assert.Equal(t, "d", out["description"])
	_, hasAP := out["additionalProperties"]
	assert.False(t, hasAP)
	_, hasSchema := out["$schema"]
	assert.False(t, hasSchema)
	props := out["properties"].(map[string]interface{})
	name := props["name"].(map[string]interface{})
	assert.Equal(t, "STRING", name["type"])
	_, hasFoo := name["foo"]
	assert.False(t, hasFoo)
}

func TestCleanFunctionParameters_TypeNormalization(t *testing.T) {
	cases := map[string]string{
		"object":  "OBJECT",
		"array":   "ARRAY",
		"string":  "STRING",
		"integer": "INTEGER",
		"number":  "NUMBER",
		"boolean": "BOOLEAN",
	}
	for in, want := range cases {
		out := CleanFunctionParameters(map[string]interface{}{"type": in}).(map[string]interface{})
		assert.Equal(t, want, out["type"], "type %s", in)
	}
}

func TestCleanFunctionParameters_NullTypeBecomesNullable(t *testing.T) {
	out := CleanFunctionParameters(map[string]interface{}{"type": "null"}).(map[string]interface{})
	_, hasType := out["type"]
	assert.False(t, hasType)
	assert.Equal(t, true, out["nullable"])
}

func TestCleanFunctionParameters_UnknownTypePassthrough(t *testing.T) {
	out := CleanFunctionParameters(map[string]interface{}{"type": "widget"}).(map[string]interface{})
	assert.Equal(t, "widget", out["type"])
}

func TestCleanFunctionParameters_TypeArrayWithNull(t *testing.T) {
	out := CleanFunctionParameters(map[string]interface{}{"type": []interface{}{"string", "null"}}).(map[string]interface{})
	assert.Equal(t, "STRING", out["type"])
	assert.Equal(t, true, out["nullable"])
}

func TestCleanFunctionParameters_TypeArrayOnlyNull(t *testing.T) {
	out := CleanFunctionParameters(map[string]interface{}{"type": []interface{}{"null"}}).(map[string]interface{})
	_, hasType := out["type"]
	assert.False(t, hasType)
	assert.Equal(t, true, out["nullable"])
}

func TestCleanFunctionParameters_TypeArrayNonString(t *testing.T) {
	// non-string entries are ignored; no valid type -> type removed.
	out := CleanFunctionParameters(map[string]interface{}{"type": []interface{}{1, true}}).(map[string]interface{})
	_, hasType := out["type"]
	assert.False(t, hasType)
}

func TestCleanFunctionParameters_ItemsAsMap(t *testing.T) {
	in := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "string", "drop": 1},
	}
	out := CleanFunctionParameters(in).(map[string]interface{})
	items := out["items"].(map[string]interface{})
	assert.Equal(t, "STRING", items["type"])
	_, dropped := items["drop"]
	assert.False(t, dropped)
}

func TestCleanFunctionParameters_ItemsAsArrayTakesFirst(t *testing.T) {
	in := map[string]interface{}{
		"type":  "array",
		"items": []interface{}{map[string]interface{}{"type": "integer"}},
	}
	out := CleanFunctionParameters(in).(map[string]interface{})
	items := out["items"].(map[string]interface{})
	assert.Equal(t, "INTEGER", items["type"])
}

func TestCleanFunctionParameters_AnyOfRecursion(t *testing.T) {
	in := map[string]interface{}{
		"anyOf": []interface{}{
			map[string]interface{}{"type": "string", "junk": 1},
			map[string]interface{}{"type": "integer"},
		},
	}
	out := CleanFunctionParameters(in).(map[string]interface{})
	anyOf := out["anyOf"].([]interface{})
	first := anyOf[0].(map[string]interface{})
	assert.Equal(t, "STRING", first["type"])
	_, junk := first["junk"]
	assert.False(t, junk)
}

func TestCleanFunctionParameters_TopLevelArray(t *testing.T) {
	in := []interface{}{
		map[string]interface{}{"type": "string", "x": 1},
	}
	out := CleanFunctionParameters(in).([]interface{})
	first := out[0].(map[string]interface{})
	assert.Equal(t, "STRING", first["type"])
}

func TestCleanFunctionParameters_ScalarPassthrough(t *testing.T) {
	assert.Equal(t, "raw", CleanFunctionParameters("raw"))
	assert.Equal(t, 7, CleanFunctionParameters(7))
}

// depth guard: at/over max depth the shallow cleaner runs.
func TestCleanShallow_Map(t *testing.T) {
	in := map[string]interface{}{
		"type":       "object",
		"drop":       1,
		"properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}},
		"items":      map[string]interface{}{"type": "string"},
		"anyOf":      []interface{}{map[string]interface{}{}},
	}
	out := cleanGeminiFunctionParametersWithDepth(in, geminiFunctionSchemaMaxDepth).(map[string]interface{})
	assert.Equal(t, "OBJECT", out["type"])
	_, hasDrop := out["drop"]
	assert.False(t, hasDrop)
	_, hasProps := out["properties"]
	assert.False(t, hasProps)
	_, hasItems := out["items"]
	assert.False(t, hasItems)
	_, hasAnyOf := out["anyOf"]
	assert.False(t, hasAnyOf)
}

func TestCleanShallow_ArrayAndScalar(t *testing.T) {
	out := cleanGeminiFunctionParametersWithDepth([]interface{}{1, 2}, geminiFunctionSchemaMaxDepth)
	assert.Equal(t, []interface{}{}, out)
	assert.Equal(t, "s", cleanGeminiFunctionParametersWithDepth("s", geminiFunctionSchemaMaxDepth))
}

// normalize with no/nil type is a no-op.
func TestNormalize_NoType(t *testing.T) {
	m := map[string]interface{}{"description": "d"}
	normalizeGeminiSchemaTypeAndNullable(m)
	_, hasType := m["type"]
	assert.False(t, hasType)

	m2 := map[string]interface{}{"type": nil}
	normalizeGeminiSchemaTypeAndNullable(m2)
	assert.Nil(t, m2["type"])
}

// ---- RemoveAdditionalProperties --------------------------------------------

func TestRemoveAdditionalProperties_DepthGuard(t *testing.T) {
	in := map[string]interface{}{"type": "object", "additionalProperties": true}
	out := RemoveAdditionalProperties(in, 5)
	// depth>=5 returns as-is (unmodified).
	assert.Equal(t, in, out)
}

func TestRemoveAdditionalProperties_NonMap(t *testing.T) {
	assert.Equal(t, "x", RemoveAdditionalProperties("x", 0))
}

func TestRemoveAdditionalProperties_EmptyMap(t *testing.T) {
	m := map[string]interface{}{}
	assert.Equal(t, m, RemoveAdditionalProperties(m, 0))
}

func TestRemoveAdditionalProperties_NonObjectArrayType(t *testing.T) {
	in := map[string]interface{}{"type": "string", "title": "t", "$schema": "s"}
	out := RemoveAdditionalProperties(in, 0).(map[string]interface{})
	_, hasTitle := out["title"]
	assert.False(t, hasTitle)
	_, hasSchema := out["$schema"]
	assert.False(t, hasSchema)
	assert.Equal(t, "string", out["type"])
}

func TestRemoveAdditionalProperties_Object(t *testing.T) {
	in := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"title":                "T",
		"properties": map[string]interface{}{
			"nested": map[string]interface{}{"type": "object", "additionalProperties": true},
		},
		"anyOf": []interface{}{
			map[string]interface{}{"type": "object", "additionalProperties": true},
		},
	}
	out := RemoveAdditionalProperties(in, 0).(map[string]interface{})
	_, hasAP := out["additionalProperties"]
	assert.False(t, hasAP)
	_, hasTitle := out["title"]
	assert.False(t, hasTitle)
	nested := out["properties"].(map[string]interface{})["nested"].(map[string]interface{})
	_, nestedAP := nested["additionalProperties"]
	assert.False(t, nestedAP)
	anyOf := out["anyOf"].([]interface{})[0].(map[string]interface{})
	_, anyOfAP := anyOf["additionalProperties"]
	assert.False(t, anyOfAP)
}

func TestRemoveAdditionalProperties_Array(t *testing.T) {
	in := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "object", "additionalProperties": true},
	}
	out := RemoveAdditionalProperties(in, 0).(map[string]interface{})
	items := out["items"].(map[string]interface{})
	_, hasAP := items["additionalProperties"]
	assert.False(t, hasAP)
}

// ---- OpenAIToolChoiceToConfig ----------------------------------------------

func TestOpenAIToolChoiceToConfig_Nil(t *testing.T) {
	assert.Nil(t, OpenAIToolChoiceToConfig(nil))
}

func TestOpenAIToolChoiceToConfig_Strings(t *testing.T) {
	cases := map[string]string{
		"auto":     "AUTO",
		"none":     "NONE",
		"required": "ANY",
		"other":    "AUTO", // default
	}
	for in, want := range cases {
		cfg := OpenAIToolChoiceToConfig(in)
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.FunctionCallingConfig)
		assert.Equal(t, want, string(cfg.FunctionCallingConfig.Mode), "input %s", in)
	}
}

func TestOpenAIToolChoiceToConfig_FunctionMapWithName(t *testing.T) {
	tc := map[string]interface{}{
		"type":     "function",
		"function": map[string]interface{}{"name": "get_weather"},
	}
	cfg := OpenAIToolChoiceToConfig(tc)
	require.NotNil(t, cfg)
	assert.Equal(t, "ANY", string(cfg.FunctionCallingConfig.Mode))
	assert.Equal(t, []string{"get_weather"}, cfg.FunctionCallingConfig.AllowedFunctionNames)
}

func TestOpenAIToolChoiceToConfig_FunctionMapNoName(t *testing.T) {
	tc := map[string]interface{}{"type": "function"}
	cfg := OpenAIToolChoiceToConfig(tc)
	require.NotNil(t, cfg)
	assert.Equal(t, "ANY", string(cfg.FunctionCallingConfig.Mode))
	assert.Nil(t, cfg.FunctionCallingConfig.AllowedFunctionNames)
}

func TestOpenAIToolChoiceToConfig_NonFunctionMap(t *testing.T) {
	assert.Nil(t, OpenAIToolChoiceToConfig(map[string]interface{}{"type": "auto"}))
}

func TestOpenAIToolChoiceToConfig_UnsupportedType(t *testing.T) {
	assert.Nil(t, OpenAIToolChoiceToConfig(42))
}
