package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalUnmarshal(t *testing.T) {
	type payload struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	b, err := Marshal(payload{A: 1, B: "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1,"b":"x"}`, string(b))

	var out payload
	require.NoError(t, Unmarshal(b, &out))
	assert.Equal(t, payload{A: 1, B: "x"}, out)

	assert.Error(t, Unmarshal([]byte(`{bad`), &out))
}

func TestUnmarshalJsonStr(t *testing.T) {
	var m map[string]int
	require.NoError(t, UnmarshalJsonStr(`{"a":1}`, &m))
	assert.Equal(t, 1, m["a"])

	assert.Error(t, UnmarshalJsonStr(`nope`, &m))
}

func TestDecodeJson(t *testing.T) {
	var m map[string]string
	require.NoError(t, DecodeJson(strings.NewReader(`{"k":"v"}`), &m))
	assert.Equal(t, "v", m["k"])

	assert.Error(t, DecodeJson(strings.NewReader(`not json`), &m))
}

func TestGetJsonType(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`: "object",
		`  [1,2]`: "array",
		`"str"`:   "string",
		`true`:    "boolean",
		`false`:   "boolean",
		`null`:    "null",
		`123`:     "number",
		`-4.5`:    "number",
		``:        "unknown",
		`   `:     "unknown",
	}
	for in, want := range cases {
		assert.Equal(t, want, GetJsonType(json.RawMessage(in)), "input=%q", in)
	}
}

func TestJsonRawMessageToString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"object passthrough", `{"a":1}`, `{"a":1}`},
		{"array passthrough", `[1,2]`, `[1,2]`},
		{"number passthrough", `42`, `42`},
		{"decoded string", `"hello"`, `hello`},
		{"escaped string decoded", `"a\nb"`, "a\nb"},
		{"empty", ``, ``},
		{"whitespace only", `   `, ``},
		{"null literal", `null`, ``},
		{"malformed string falls back to raw", `"unterminated`, `"unterminated`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, JsonRawMessageToString(json.RawMessage(tc.in)))
		})
	}
}
