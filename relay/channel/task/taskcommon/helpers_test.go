package taskcommon

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// UnmarshalMetadata
// ---------------------------------------------------------------------------

type sampleTarget struct {
	Model      string `json:"model"`
	Resolution string `json:"resolution"`
	Duration   int    `json:"duration"`
}

func TestUnmarshalMetadata_NilReturnsNilNoMutation(t *testing.T) {
	var target sampleTarget
	err := UnmarshalMetadata(nil, &target)
	require.NoError(t, err)
	assert.Equal(t, sampleTarget{}, target)
}

func TestUnmarshalMetadata_PopulatesTarget(t *testing.T) {
	meta := map[string]any{
		"resolution": "1080p",
		"duration":   10,
	}
	var target sampleTarget
	err := UnmarshalMetadata(meta, &target)
	require.NoError(t, err)
	assert.Equal(t, "1080p", target.Resolution)
	assert.Equal(t, 10, target.Duration)
}

func TestUnmarshalMetadata_StripsModelKeyToPreventBillingBypass(t *testing.T) {
	meta := map[string]any{
		"model":      "cheaper-model",
		"resolution": "720p",
	}
	var target sampleTarget
	err := UnmarshalMetadata(meta, &target)
	require.NoError(t, err)
	// model must be dropped from both the source map and the target.
	assert.Empty(t, target.Model)
	_, ok := meta["model"]
	assert.False(t, ok, "model key must be deleted from metadata map")
	assert.Equal(t, "720p", target.Resolution)
}

func TestUnmarshalMetadata_UnmarshalErrorOnTypeMismatch(t *testing.T) {
	// duration declared as int in target but provided as a non-numeric string.
	meta := map[string]any{
		"duration": "not-a-number",
	}
	var target sampleTarget
	err := UnmarshalMetadata(meta, &target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal metadata failed")
}

// ---------------------------------------------------------------------------
// DefaultString / DefaultInt (boundary + equivalence)
// ---------------------------------------------------------------------------

func TestDefaultString(t *testing.T) {
	assert.Equal(t, "fallback", DefaultString("", "fallback"))
	assert.Equal(t, "value", DefaultString("value", "fallback"))
}

func TestDefaultInt(t *testing.T) {
	assert.Equal(t, 42, DefaultInt(0, 42))  // zero -> fallback
	assert.Equal(t, 7, DefaultInt(7, 42))   // non-zero -> value
	assert.Equal(t, -1, DefaultInt(-1, 42)) // negative is non-zero -> value
}

// ---------------------------------------------------------------------------
// EncodeLocalTaskID / DecodeLocalTaskID (round-trip + error path)
// ---------------------------------------------------------------------------

func TestLocalTaskID_RoundTrip(t *testing.T) {
	names := []string{
		"operations/abc-123",
		"",
		"projects/p/locations/us/operations/xyz==weird//chars",
	}
	for _, name := range names {
		encoded := EncodeLocalTaskID(name)
		decoded, err := DecodeLocalTaskID(encoded)
		require.NoError(t, err)
		assert.Equal(t, name, decoded)
	}
}

func TestDecodeLocalTaskID_InvalidBase64ReturnsError(t *testing.T) {
	// '@' is not part of the base64url alphabet.
	_, err := DecodeLocalTaskID("not@valid@base64")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// BuildProxyURL
// ---------------------------------------------------------------------------

func TestBuildProxyURL(t *testing.T) {
	orig := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = orig })

	system_setting.ServerAddress = "https://gw.example.com"
	assert.Equal(t,
		"https://gw.example.com/v1/videos/task_abc/content",
		BuildProxyURL("task_abc"))

	system_setting.ServerAddress = ""
	assert.Equal(t, "/v1/videos/task_abc/content", BuildProxyURL("task_abc"))
}

// ---------------------------------------------------------------------------
// BaseBilling — no-op embeddable defaults
// ---------------------------------------------------------------------------

func TestBaseBilling_NoOps(t *testing.T) {
	b := BaseBilling{}
	assert.Nil(t, b.EstimateBilling(nil, nil))
	assert.Nil(t, b.AdjustBillingOnSubmit(nil, nil))
	assert.Equal(t, 0, b.AdjustBillingOnComplete(nil, nil))
}

// Guard: progress constants are the documented values consumed by pollers.
func TestProgressConstants(t *testing.T) {
	assert.Equal(t, "10%", ProgressSubmitted)
	assert.Equal(t, "20%", ProgressQueued)
	assert.Equal(t, "30%", ProgressInProgress)
	assert.Equal(t, "100%", ProgressComplete)
}
