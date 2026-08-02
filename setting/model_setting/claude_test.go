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
	t.Cleanup(func() { ReplaceClaudeSettings(original) })
	// 必须走 Replace：直接改包级变量不会重新发布快照，
	// 而 GetClaudeSettings 读的是快照。
	ReplaceClaudeSettings(s)
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

// GetClaudeSettings 返回的是不可变快照，不再是可变全局的指针。
// 原来的契约（返回 &claudeSettings）意味着 relay 热路径上的每个读者都在读一块
// 会被配置更新原地改写的内存；而 getter 自身还会往 DefaultMaxTokens 里写键，
// 并发命中就是 fatal error: concurrent map writes。
func TestGetClaudeSettings_ReturnsImmutableSnapshot(t *testing.T) {
	got := GetClaudeSettings()
	require.NotNil(t, got)
	require.NotSame(t, &claudeSettings, got, "不能再把可变全局的指针交出去")

	// 已经拿到手的快照不会被后续配置变更改写
	before := GetClaudeSettings()
	beforeEnabled := before.ThinkingAdapterEnabled
	withClaudeSettings(t, ClaudeSettings{
		DefaultMaxTokens:       map[string]int{"default": 99},
		ThinkingAdapterEnabled: !beforeEnabled,
	})
	assert.Equal(t, beforeEnabled, before.ThinkingAdapterEnabled, "旧快照必须保持不变")
	assert.Equal(t, !beforeEnabled, GetClaudeSettings().ThinkingAdapterEnabled, "新快照要反映新值")
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

func TestValidateClaudeDefaultMaxTokens(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "positive default", value: `{"default": 8192}`},
		{name: "zero allowed", value: `{"default": 0}`},
		{name: "zero model override allowed", value: `{"default": 8192, "claude-test": 0}`},
		{name: "empty map allowed", value: `{}`},
		{name: "negative default rejected", value: `{"default": -1}`, wantErr: `negative Claude default max_tokens -1 for "default"`},
		{name: "negative model override rejected", value: `{"default": 8192, "claude-test": -5}`, wantErr: `negative Claude default max_tokens -5 for "claude-test"`},
		{name: "non-integer rejected", value: `{"default": "high"}`, wantErr: "JSON map of model to integer"},
		{name: "null rejected", value: `null`, wantErr: "JSON map of model to integer"},
		{name: "malformed rejected", value: `{`, wantErr: "JSON map of model to integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClaudeDefaultMaxTokens(tt.value)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
