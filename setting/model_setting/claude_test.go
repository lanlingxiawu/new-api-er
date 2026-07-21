package model_setting

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withClaudeSettings swaps the package-global claudeSettings for the duration of
// the test and restores it afterwards. All getters read the global, so any test
// that mutates it must save/restore to avoid cross-test leakage.
func withClaudeSettings(t *testing.T, s ClaudeSettings) {
	t.Helper()
	original := claudeSettings
	t.Cleanup(func() { claudeSettings = original })
	claudeSettings = s
}

// ---------------------------------------------------------------------------
// GetClaudeSettings — default-key self-healing branch
// ---------------------------------------------------------------------------

func TestGetClaudeSettings_RestoresMissingDefaultMaxTokens(t *testing.T) {
	// DefaultMaxTokens present but missing the "default" key -> getter injects 8192.
	withClaudeSettings(t, ClaudeSettings{
		DefaultMaxTokens: map[string]int{"claude-x": 4096},
	})

	got := GetClaudeSettings()
	require.Contains(t, got.DefaultMaxTokens, "default")
	assert.Equal(t, 8192, got.DefaultMaxTokens["default"])
	// existing entry untouched
	assert.Equal(t, 4096, got.DefaultMaxTokens["claude-x"])
}

func TestGetClaudeSettings_KeepsExistingDefault(t *testing.T) {
	// "default" already present -> getter must NOT overwrite it.
	withClaudeSettings(t, ClaudeSettings{
		DefaultMaxTokens: map[string]int{"default": 1234},
	})

	got := GetClaudeSettings()
	assert.Equal(t, 1234, got.DefaultMaxTokens["default"])
}

func TestGetClaudeSettings_ReturnsPointerToGlobal(t *testing.T) {
	got := GetClaudeSettings()
	require.Same(t, &claudeSettings, got)
}

// ---------------------------------------------------------------------------
// GetDefaultMaxTokens — per-model lookup with default fallback
// ---------------------------------------------------------------------------

func TestGetDefaultMaxTokens(t *testing.T) {
	c := &ClaudeSettings{DefaultMaxTokens: map[string]int{
		"default":        8192,
		"claude-3-opus":  4096,
		"claude-3-haiku": 2048,
	}}

	// exact model hit
	assert.Equal(t, 4096, c.GetDefaultMaxTokens("claude-3-opus"))
	assert.Equal(t, 2048, c.GetDefaultMaxTokens("claude-3-haiku"))
	// miss -> default
	assert.Equal(t, 8192, c.GetDefaultMaxTokens("claude-unknown"))
	// empty string is a normal miss -> default
	assert.Equal(t, 8192, c.GetDefaultMaxTokens(""))
}

// ---------------------------------------------------------------------------
// WriteHeaders — merge + dedup + Set semantics
// ---------------------------------------------------------------------------

func TestWriteHeaders_ModelNotConfiguredIsNoOp(t *testing.T) {
	c := &ClaudeSettings{HeadersSettings: map[string]map[string][]string{
		"configured-model": {"anthropic-beta": {"x"}},
	}}
	headers := http.Header{}
	headers.Set("anthropic-beta", "existing")

	c.WriteHeaders("other-model", &headers)

	// untouched: still exactly the original single value
	assert.Equal(t, []string{"existing"}, headers.Values("anthropic-beta"))
}

func TestWriteHeaders_MergesConfiguredValuesIntoSingleHeader(t *testing.T) {
	c := &ClaudeSettings{HeadersSettings: map[string]map[string][]string{
		"m": {"anthropic-beta": {"token-efficient-tools-2025-02-19"}},
	}}
	headers := http.Header{}
	headers.Set("anthropic-beta", "output-128k-2025-02-19")

	c.WriteHeaders("m", &headers)

	got := headers.Values("anthropic-beta")
	require.Len(t, got, 1, "merged into a single comma-joined header")
	assert.Equal(t, "output-128k-2025-02-19,token-efficient-tools-2025-02-19", got[0])
}

func TestWriteHeaders_DeduplicatesAcrossCommaSeparatedAndRepeated(t *testing.T) {
	c := &ClaudeSettings{HeadersSettings: map[string]map[string][]string{
		"m": {"anthropic-beta": {
			"token-efficient-tools-2025-02-19",
			"computer-use-2025-01-24",
		}},
	}}
	headers := http.Header{}
	headers.Add("anthropic-beta", "output-128k-2025-02-19, token-efficient-tools-2025-02-19")
	headers.Add("anthropic-beta", "token-efficient-tools-2025-02-19")

	c.WriteHeaders("m", &headers)

	got := headers.Values("anthropic-beta")
	require.Len(t, got, 1)
	assert.Equal(t, "output-128k-2025-02-19,token-efficient-tools-2025-02-19,computer-use-2025-01-24", got[0])
}

func TestWriteHeaders_AllValuesEmptySkipsSet(t *testing.T) {
	// Configured values normalize to nothing AND the incoming header is empty:
	// mergedValues is empty -> the code `continue`s without calling Set, so a
	// header that was never present stays absent.
	c := &ClaudeSettings{HeadersSettings: map[string]map[string][]string{
		"m": {"x-empty": {"", "   ", ",,"}},
	}}
	headers := http.Header{}

	c.WriteHeaders("m", &headers)

	_, present := headers["X-Empty"]
	assert.False(t, present, "empty merge must not create the header")
}

func TestWriteHeaders_MultipleHeaderKeys(t *testing.T) {
	c := &ClaudeSettings{HeadersSettings: map[string]map[string][]string{
		"m": {
			"anthropic-beta": {"a", "b"},
			"x-custom":       {"c"},
		},
	}}
	headers := http.Header{}

	c.WriteHeaders("m", &headers)

	assert.Equal(t, "a,b", headers.Get("anthropic-beta"))
	assert.Equal(t, "c", headers.Get("x-custom"))
}

// ---------------------------------------------------------------------------
// normalizeHeaderListValues — unexported core of the merge
// ---------------------------------------------------------------------------

func TestNormalizeHeaderListValues(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty input", nil, []string{}},
		{"single value", []string{"a"}, []string{"a"}},
		{"comma split", []string{"a,b,c"}, []string{"a", "b", "c"}},
		{"trims whitespace", []string{" a , b "}, []string{"a", "b"}},
		{"skips empty segments", []string{"a,,b", "  ", ""}, []string{"a", "b"}},
		{"dedup across entries", []string{"a,b", "b,c", "a"}, []string{"a", "b", "c"}},
		{"only-empty yields empty slice", []string{"", " ", ",,"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeHeaderListValues(tt.in))
		})
	}
}

func TestNormalizeHeaderListValues_PreservesFirstSeenOrder(t *testing.T) {
	got := normalizeHeaderListValues([]string{"c", "a", "c", "b", "a"})
	assert.Equal(t, []string{"c", "a", "b"}, got)
}
