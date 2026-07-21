package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// allScalars exercises every scalar kind handled by the reflection helpers.
type allScalars struct {
	Str  string  `json:"str"`
	Bl   bool    `json:"bl"`
	I    int     `json:"i"`
	I8   int8    `json:"i8"`
	I16  int16   `json:"i16"`
	I32  int32   `json:"i32"`
	I64  int64   `json:"i64"`
	U    uint    `json:"u"`
	U8   uint8   `json:"u8"`
	U16  uint16  `json:"u16"`
	U32  uint32  `json:"u32"`
	U64  uint64  `json:"u64"`
	F32  float32 `json:"f32"`
	F64  float64 `json:"f64"`
}

// complexTypes exercises Ptr / Map / Slice / Struct handling.
type inner struct {
	A int    `json:"a"`
	B string `json:"b"`
}

type complexTypes struct {
	P *int              `json:"p"`
	M map[string]string `json:"m"`
	S []int             `json:"s"`
	T inner             `json:"t"`
}

// tagVariants exercises json-tag resolution rules.
type tagVariants struct {
	Tagged   string `json:"tagged"`
	NoTag    string
	DashTag  string `json:"-"`
	OmitTag  string `json:"omit,omitempty"`
	unexport string //nolint:unused // intentionally unexported to test skip
}

// unsupportedKinds contains kinds hitting the default (skip) branch of configToMap.
type unsupportedKinds struct {
	Keep string   `json:"keep"`
	Ch   chan int `json:"ch"`
	Fn   func()   `json:"fn"`
}

// badStructField forces a json.Marshal error (exported func member is unmarshalable).
type funcHolder struct {
	Fn func() `json:"fn"`
}
type badStructConfig struct {
	Inner funcHolder `json:"inner"`
}
type badPtrConfig struct {
	P *funcHolder `json:"p"`
}
type badMapConfig struct {
	M map[string]interface{} `json:"m"`
}

// ---------------------------------------------------------------------------
// ConfigToMap
// ---------------------------------------------------------------------------

func TestConfigToMap_NonStructReturnsNilNil(t *testing.T) {
	// equivalence: non-struct kinds all take the early return.
	m, err := ConfigToMap(42)
	require.NoError(t, err)
	assert.Nil(t, m)

	x := 7
	m, err = ConfigToMap(&x) // ptr -> Elem is int, still not a struct
	require.NoError(t, err)
	assert.Nil(t, m)

	m, err = ConfigToMap("hello")
	require.NoError(t, err)
	assert.Nil(t, m)
}

func TestConfigToMap_PointerToStructIsDereferenced(t *testing.T) {
	cfg := &allScalars{Str: "x", I: 1}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "x", m["str"])
}

func TestConfigToMap_AllScalarKindsFormatted(t *testing.T) {
	cfg := allScalars{
		Str: "hello",
		Bl:  true,
		I:   -1, I8: -8, I16: -16, I32: -32, I64: -64,
		U: 1, U8: 8, U16: 16, U32: 32, U64: 64,
		F32: 1.5, F64: 2.25,
	}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)

	assert.Equal(t, "hello", m["str"])
	assert.Equal(t, "true", m["bl"])
	assert.Equal(t, "-1", m["i"])
	assert.Equal(t, "-8", m["i8"])
	assert.Equal(t, "-16", m["i16"])
	assert.Equal(t, "-32", m["i32"])
	assert.Equal(t, "-64", m["i64"])
	assert.Equal(t, "1", m["u"])
	assert.Equal(t, "8", m["u8"])
	assert.Equal(t, "16", m["u16"])
	assert.Equal(t, "32", m["u32"])
	assert.Equal(t, "64", m["u64"])
	assert.Equal(t, "1.5", m["f32"])
	assert.Equal(t, "2.25", m["f64"])
}

func TestConfigToMap_BoolFalseFormatted(t *testing.T) {
	// decision coverage: Bool false path.
	m, err := ConfigToMap(allScalars{Bl: false})
	require.NoError(t, err)
	assert.Equal(t, "false", m["bl"])
}

func TestConfigToMap_ComplexTypes(t *testing.T) {
	v := 5
	cfg := complexTypes{
		P: &v,
		M: map[string]string{"k": "val"},
		S: []int{1, 2, 3},
		T: inner{A: 9, B: "bee"},
	}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)

	assert.Equal(t, "5", m["p"])                 // non-nil ptr -> json of pointed value
	assert.JSONEq(t, `{"k":"val"}`, m["m"])      // map -> json
	assert.Equal(t, "[1,2,3]", m["s"])           // slice -> json
	assert.JSONEq(t, `{"a":9,"b":"bee"}`, m["t"]) // struct -> json
}

func TestConfigToMap_NilPointerSerializesToNull(t *testing.T) {
	// boundary: nil pointer branch.
	cfg := complexTypes{P: nil, M: nil, S: nil}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)
	assert.Equal(t, "null", m["p"])
	assert.Equal(t, "null", m["m"]) // nil map marshals to null
	assert.Equal(t, "null", m["s"]) // nil slice marshals to null
}

func TestConfigToMap_TagResolution(t *testing.T) {
	cfg := tagVariants{
		Tagged:   "a",
		NoTag:    "b",
		DashTag:  "c",
		OmitTag:  "d",
		unexport: "hidden",
	}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)

	assert.Equal(t, "a", m["tagged"])
	assert.Equal(t, "b", m["NoTag"], "empty tag falls back to Go field name")
	assert.Equal(t, "c", m["DashTag"], `json:"-" falls back to Go field name (NOT excluded)`)
	assert.Equal(t, "d", m["omit,omitempty"], "raw tag value used verbatim, incl. options")
	_, hasHidden := m["unexport"]
	assert.False(t, hasHidden, "unexported field skipped")
}

func TestConfigToMap_UnsupportedKindsSkipped(t *testing.T) {
	cfg := unsupportedKinds{Keep: "yes"}
	m, err := ConfigToMap(cfg)
	require.NoError(t, err)
	assert.Equal(t, "yes", m["keep"])
	_, hasCh := m["ch"]
	_, hasFn := m["fn"]
	assert.False(t, hasCh, "chan hits default skip branch")
	assert.False(t, hasFn, "func hits default skip branch")
}

func TestConfigToMap_MarshalErrorStructField(t *testing.T) {
	// error path: reflect.Struct branch json.Marshal fails.
	_, err := ConfigToMap(badStructConfig{})
	require.Error(t, err)
}

func TestConfigToMap_MarshalErrorPtrField(t *testing.T) {
	// error path: reflect.Ptr branch (non-nil) json.Marshal fails.
	_, err := ConfigToMap(badPtrConfig{P: &funcHolder{}})
	require.Error(t, err)
}

func TestConfigToMap_MarshalErrorMapField(t *testing.T) {
	// error path: reflect.Map branch json.Marshal fails on unmarshalable value.
	_, err := ConfigToMap(badMapConfig{M: map[string]interface{}{"bad": make(chan int)}})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// UpdateConfigFromMap
// ---------------------------------------------------------------------------

func TestUpdateConfigFromMap_NonPointerIsNoOp(t *testing.T) {
	// value (not pointer) -> early return nil, no mutation possible.
	cfg := allScalars{Str: "orig"}
	err := UpdateConfigFromMap(cfg, map[string]string{"str": "new"})
	require.NoError(t, err)
	assert.Equal(t, "orig", cfg.Str)
}

func TestUpdateConfigFromMap_PointerToNonStructIsNoOp(t *testing.T) {
	x := 5
	err := UpdateConfigFromMap(&x, map[string]string{"anything": "1"})
	require.NoError(t, err)
	assert.Equal(t, 5, x)
}

func TestUpdateConfigFromMap_AllScalarKinds(t *testing.T) {
	cfg := &allScalars{}
	err := UpdateConfigFromMap(cfg, map[string]string{
		"str": "hi",
		"bl":  "true",
		"i":   "-1", "i8": "-8", "i16": "-16", "i32": "-32", "i64": "-64",
		"u": "1", "u8": "8", "u16": "16", "u32": "32", "u64": "64",
		"f32": "1.5", "f64": "2.25",
	})
	require.NoError(t, err)

	assert.Equal(t, "hi", cfg.Str)
	assert.True(t, cfg.Bl)
	assert.Equal(t, -1, cfg.I)
	assert.Equal(t, int8(-8), cfg.I8)
	assert.Equal(t, int16(-16), cfg.I16)
	assert.Equal(t, int32(-32), cfg.I32)
	assert.Equal(t, int64(-64), cfg.I64)
	assert.Equal(t, uint(1), cfg.U)
	assert.Equal(t, uint8(8), cfg.U8)
	assert.Equal(t, uint16(16), cfg.U16)
	assert.Equal(t, uint32(32), cfg.U32)
	assert.Equal(t, uint64(64), cfg.U64)
	assert.Equal(t, float32(1.5), cfg.F32)
	assert.Equal(t, 2.25, cfg.F64)
}

func TestUpdateConfigFromMap_KeyAbsentLeavesFieldUnchanged(t *testing.T) {
	cfg := &allScalars{Str: "keep", I: 99}
	err := UpdateConfigFromMap(cfg, map[string]string{"str": "changed"})
	require.NoError(t, err)
	assert.Equal(t, "changed", cfg.Str)
	assert.Equal(t, 99, cfg.I, "field with no map entry stays unchanged")
}

func TestUpdateConfigFromMap_BoolInvalidSkipped(t *testing.T) {
	cfg := &allScalars{Bl: true}
	err := UpdateConfigFromMap(cfg, map[string]string{"bl": "notabool"})
	require.NoError(t, err)
	assert.True(t, cfg.Bl, "invalid bool string leaves field unchanged")
}

func TestUpdateConfigFromMap_IntInvalidSkipped(t *testing.T) {
	cfg := &allScalars{I: 7}
	err := UpdateConfigFromMap(cfg, map[string]string{"i": "abc"})
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.I, "non-numeric int string leaves field unchanged")
}

func TestUpdateConfigFromMap_IntAcceptsFloatFormattedString(t *testing.T) {
	// compat branch: "2.000000" parses as float then truncates to int.
	cfg := &allScalars{}
	err := UpdateConfigFromMap(cfg, map[string]string{"i": "2.000000", "i64": "3.9"})
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.I)
	assert.Equal(t, int64(3), cfg.I64, "float truncated toward zero")
}

func TestUpdateConfigFromMap_UintInvalidSkipped(t *testing.T) {
	cfg := &allScalars{U: 5}
	err := UpdateConfigFromMap(cfg, map[string]string{"u": "xyz"})
	require.NoError(t, err)
	assert.Equal(t, uint(5), cfg.U)
}

func TestUpdateConfigFromMap_UintAcceptsFloatFormatted(t *testing.T) {
	cfg := &allScalars{}
	err := UpdateConfigFromMap(cfg, map[string]string{"u": "3.0"})
	require.NoError(t, err)
	assert.Equal(t, uint(3), cfg.U)
}

func TestUpdateConfigFromMap_UintNegativeFloatSkipped(t *testing.T) {
	// condition coverage: floatValue < 0 sub-condition true -> skip.
	cfg := &allScalars{U: 42}
	err := UpdateConfigFromMap(cfg, map[string]string{"u": "-1.5"})
	require.NoError(t, err)
	assert.Equal(t, uint(42), cfg.U, "negative float rejected for uint")
}

func TestUpdateConfigFromMap_FloatInvalidSkipped(t *testing.T) {
	cfg := &allScalars{F64: 1.1}
	err := UpdateConfigFromMap(cfg, map[string]string{"f64": "notafloat"})
	require.NoError(t, err)
	assert.Equal(t, 1.1, cfg.F64)
}

func TestUpdateConfigFromMap_PtrFromNilAllocates(t *testing.T) {
	cfg := &complexTypes{P: nil}
	err := UpdateConfigFromMap(cfg, map[string]string{"p": "42"})
	require.NoError(t, err)
	require.NotNil(t, cfg.P)
	assert.Equal(t, 42, *cfg.P)
}

func TestUpdateConfigFromMap_PtrOverwritesExisting(t *testing.T) {
	orig := 1
	cfg := &complexTypes{P: &orig}
	err := UpdateConfigFromMap(cfg, map[string]string{"p": "99"})
	require.NoError(t, err)
	require.NotNil(t, cfg.P)
	assert.Equal(t, 99, *cfg.P)
}

func TestUpdateConfigFromMap_PtrNullClearsPointer(t *testing.T) {
	orig := 7
	cfg := &complexTypes{P: &orig}
	err := UpdateConfigFromMap(cfg, map[string]string{"p": "null"})
	require.NoError(t, err)
	assert.Nil(t, cfg.P, `"null" resets pointer to nil`)
}

func TestUpdateConfigFromMap_PtrInvalidJSONSkipped(t *testing.T) {
	orig := 7
	cfg := &complexTypes{P: &orig}
	err := UpdateConfigFromMap(cfg, map[string]string{"p": "notjson"})
	require.NoError(t, err)
	require.NotNil(t, cfg.P)
	assert.Equal(t, 7, *cfg.P, "invalid json leaves pointer unchanged")
}

func TestUpdateConfigFromMap_MapReplacedFreshly(t *testing.T) {
	cfg := &complexTypes{M: map[string]string{"old": "1"}}
	err := UpdateConfigFromMap(cfg, map[string]string{"m": `{"new":"2"}`})
	require.NoError(t, err)
	_, hasOld := cfg.M["old"]
	assert.False(t, hasOld, "fresh allocation drops keys absent from new JSON")
	assert.Equal(t, "2", cfg.M["new"])
}

func TestUpdateConfigFromMap_MapInvalidJSONSkipped(t *testing.T) {
	cfg := &complexTypes{M: map[string]string{"keep": "1"}}
	err := UpdateConfigFromMap(cfg, map[string]string{"m": "notjson"})
	require.NoError(t, err)
	assert.Equal(t, "1", cfg.M["keep"], "invalid json leaves map unchanged")
}

func TestUpdateConfigFromMap_SliceReplaced(t *testing.T) {
	cfg := &complexTypes{S: []int{1}}
	err := UpdateConfigFromMap(cfg, map[string]string{"s": "[4,5,6]"})
	require.NoError(t, err)
	assert.Equal(t, []int{4, 5, 6}, cfg.S)
}

func TestUpdateConfigFromMap_SliceInvalidJSONSkipped(t *testing.T) {
	cfg := &complexTypes{S: []int{1, 2}}
	err := UpdateConfigFromMap(cfg, map[string]string{"s": "notjson"})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, cfg.S)
}

func TestUpdateConfigFromMap_StructReplaced(t *testing.T) {
	cfg := &complexTypes{T: inner{A: 1, B: "x"}}
	err := UpdateConfigFromMap(cfg, map[string]string{"t": `{"a":9,"b":"y"}`})
	require.NoError(t, err)
	assert.Equal(t, inner{A: 9, B: "y"}, cfg.T)
}

func TestUpdateConfigFromMap_StructInvalidJSONSkipped(t *testing.T) {
	cfg := &complexTypes{T: inner{A: 1, B: "x"}}
	err := UpdateConfigFromMap(cfg, map[string]string{"t": "notjson"})
	require.NoError(t, err)
	assert.Equal(t, inner{A: 1, B: "x"}, cfg.T)
}

func TestUpdateConfigFromMap_UnexportedFieldSkipped(t *testing.T) {
	cfg := &tagVariants{}
	// "unexport" is unexported; there is no reachable map key for it, and even
	// if provided it is filtered by the IsExported guard.
	err := UpdateConfigFromMap(cfg, map[string]string{"unexport": "x", "tagged": "ok"})
	require.NoError(t, err)
	assert.Equal(t, "ok", cfg.Tagged)
	assert.Empty(t, cfg.unexport)
}

func TestUpdateConfigFromMap_TagResolutionRoundTrip(t *testing.T) {
	// The raw-tag quirk is internally consistent: configToMap and
	// updateConfigFromMap use the same key derivation, so a round trip works.
	src := tagVariants{Tagged: "a", NoTag: "b", DashTag: "c", OmitTag: "d"}
	m, err := ConfigToMap(src)
	require.NoError(t, err)

	dst := &tagVariants{}
	require.NoError(t, UpdateConfigFromMap(dst, m))
	assert.Equal(t, "a", dst.Tagged)
	assert.Equal(t, "b", dst.NoTag)
	assert.Equal(t, "c", dst.DashTag)
	assert.Equal(t, "d", dst.OmitTag)
}

func TestConfigRoundTrip_AllScalars(t *testing.T) {
	// Path coverage: full marshal -> unmarshal fidelity for scalar kinds.
	src := allScalars{
		Str: "round", Bl: true,
		I: -5, I8: 1, I16: 2, I32: 3, I64: 4,
		U: 10, U8: 11, U16: 12, U32: 13, U64: 14,
		F32: 3.5, F64: 6.75,
	}
	m, err := ConfigToMap(src)
	require.NoError(t, err)

	dst := &allScalars{}
	require.NoError(t, UpdateConfigFromMap(dst, m))
	assert.Equal(t, src, *dst)
}
