package ionet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// normalizeTimeString
// ---------------------------------------------------------------------------

func TestNormalizeTimeString_EmptyAndWhitespace(t *testing.T) {
	out, changed := normalizeTimeString("")
	assert.Equal(t, "", out)
	assert.False(t, changed)

	out, changed = normalizeTimeString("   ")
	assert.Equal(t, "   ", out) // returns original input, not trimmed, on empty-after-trim
	assert.False(t, changed)
}

func TestNormalizeTimeString_RFC3339NanoUnchanged(t *testing.T) {
	in := "2023-06-15T12:30:45.123456789Z"
	out, changed := normalizeTimeString(in)
	assert.Equal(t, in, out)
	assert.False(t, changed, "already-normalized value with no surrounding space is unchanged")
}

func TestNormalizeTimeString_RFC3339WithSurroundingSpaceChanged(t *testing.T) {
	// Trims surrounding whitespace → trimmed != input → changed true.
	out, changed := normalizeTimeString("  2023-06-15T12:30:45Z  ")
	assert.Equal(t, "2023-06-15T12:30:45Z", out)
	assert.True(t, changed)
}

func TestNormalizeTimeString_NoTimezoneLayoutNormalized(t *testing.T) {
	// No timezone → fails RFC3339 parsing, matches the layouts loop, becomes UTC RFC3339Nano.
	out, changed := normalizeTimeString("2023-06-15T12:30:45")
	require.True(t, changed)
	parsed, err := time.Parse(time.RFC3339Nano, out)
	require.NoError(t, err)
	assert.Equal(t, 2023, parsed.Year())
	assert.Equal(t, time.UTC, parsed.Location())
}

func TestNormalizeTimeString_NoTimezoneWithFractionNormalized(t *testing.T) {
	out, changed := normalizeTimeString("2023-06-15T12:30:45.123456")
	require.True(t, changed)
	parsed, err := time.Parse(time.RFC3339Nano, out)
	require.NoError(t, err)
	assert.Equal(t, 123456000, parsed.Nanosecond())
}

func TestNormalizeTimeString_NonTimeStringUnchanged(t *testing.T) {
	out, changed := normalizeTimeString("hello world")
	assert.Equal(t, "hello world", out)
	assert.False(t, changed)
}

// ---------------------------------------------------------------------------
// normalizeTimeValues (recursion over maps / slices / scalars)
// ---------------------------------------------------------------------------

func TestNormalizeTimeValues_NestedMapAndSlice(t *testing.T) {
	in := map[string]interface{}{
		"a": "2023-06-15T12:30:45",
		"b": []interface{}{"2023-01-01T00:00:00", "plain"},
		"c": float64(3),
		"d": true,
	}
	out := normalizeTimeValues(in).(map[string]interface{})

	// string with no tz was normalized (contains a 'Z')
	assert.Contains(t, out["a"].(string), "Z")
	arr := out["b"].([]interface{})
	assert.Contains(t, arr[0].(string), "Z")
	assert.Equal(t, "plain", arr[1])
	// non-string scalars pass through untouched
	assert.Equal(t, float64(3), out["c"])
	assert.Equal(t, true, out["d"])
}

func TestNormalizeTimeValues_ScalarPassThrough(t *testing.T) {
	assert.Equal(t, 5, normalizeTimeValues(5))
	assert.Nil(t, normalizeTimeValues(nil))
}

// ---------------------------------------------------------------------------
// decodeWithFlexibleTimes
// ---------------------------------------------------------------------------

func TestDecodeWithFlexibleTimes_NormalizesTimestamp(t *testing.T) {
	type payload struct {
		CreatedAt time.Time `json:"created_at"`
	}
	var p payload
	err := decodeWithFlexibleTimes([]byte(`{"created_at":"2023-06-15T12:30:45"}`), &p)
	require.NoError(t, err)
	assert.Equal(t, 2023, p.CreatedAt.Year())
	assert.Equal(t, time.June, p.CreatedAt.Month())
}

func TestDecodeWithFlexibleTimes_InvalidJSONReturnsError(t *testing.T) {
	var p map[string]interface{}
	err := decodeWithFlexibleTimes([]byte(`{not json`), &p)
	require.Error(t, err)
}

func TestDecodeWithFlexibleTimes_TargetTypeMismatchReturnsError(t *testing.T) {
	// Valid JSON object but target is an int → final Unmarshal fails.
	var target int
	err := decodeWithFlexibleTimes([]byte(`{"a":1}`), &target)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// decodeData / decodeDataWithFlexibleTimes (generic data-wrapper unwrap)
// ---------------------------------------------------------------------------

func TestDecodeData_UnwrapsDataField(t *testing.T) {
	var out []int
	err := decodeData([]byte(`{"data":[1,2,3]}`), &out)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, out)
}

func TestDecodeData_MissingDataYieldsZeroValue(t *testing.T) {
	var out []int
	err := decodeData([]byte(`{"other":1}`), &out)
	require.NoError(t, err)
	assert.Nil(t, out)
}

func TestDecodeData_InvalidJSONReturnsError(t *testing.T) {
	var out []int
	err := decodeData([]byte(`bogus`), &out)
	require.Error(t, err)
}

func TestDecodeDataWithFlexibleTimes_NormalizesInsideData(t *testing.T) {
	type item struct {
		At time.Time `json:"at"`
	}
	var out item
	err := decodeDataWithFlexibleTimes([]byte(`{"data":{"at":"2023-06-15T12:30:45"}}`), &out)
	require.NoError(t, err)
	assert.Equal(t, 2023, out.At.Year())
}

func TestDecodeDataWithFlexibleTimes_InvalidJSONReturnsError(t *testing.T) {
	var out struct{}
	err := decodeDataWithFlexibleTimes([]byte(`{`), &out)
	require.Error(t, err)
}
