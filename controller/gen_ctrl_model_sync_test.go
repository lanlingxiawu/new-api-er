package controller

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// model_sync.go pure helpers: locale normalization, upstream URL derivation,
// field-set membership, string coalescing, and status selection.

func TestNormalizeLocale(t *testing.T) {
	// NOTE (source observation, not fixed here): normalizeLocale lowercases its
	// input first (strings.ToLower) but the switch matches the case-sensitive
	// literals "zh-CN" / "zh-TW". A lowercased "zh-cn" can therefore never equal
	// "zh-CN", so the zh-CN / zh-TW locale branches are DEAD CODE — any zh-*
	// locale falls through to the default (non-i18n) upstream URLs. Only "en"
	// and "ja" are actually reachable. These assertions pin the real behavior.
	cases := []struct {
		in     string
		want   string
		wantOk bool
	}{
		{"en", "en", true},
		{"EN", "en", true}, // lowercased then matched
		{"ja", "ja", true},
		{"JA", "ja", true},
		{"zh-CN", "", false}, // unreachable branch -> not ok
		{"zh-cn", "", false},
		{"zh-TW", "", false},
		{"zh-tw", "", false},
		{"fr", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeLocale(c.in)
		assert.Equalf(t, c.wantOk, ok, "in=%q", c.in)
		assert.Equalf(t, c.want, got, "in=%q", c.in)
	}
}

func TestGetUpstreamURLs_DefaultVsLocalized(t *testing.T) {
	// Pin the base so the assertion is deterministic regardless of env.
	prev, had := os.LookupEnv("SYNC_UPSTREAM_BASE")
	os.Setenv("SYNC_UPSTREAM_BASE", "https://example.test/meta/")
	t.Cleanup(func() {
		if had {
			os.Setenv("SYNC_UPSTREAM_BASE", prev)
		} else {
			os.Unsetenv("SYNC_UPSTREAM_BASE")
		}
	})

	// Invalid/empty locale -> non-i18n path (trailing slash trimmed).
	m, v := getUpstreamURLs("")
	assert.Equal(t, "https://example.test/meta/api/newapi/models.json", m)
	assert.Equal(t, "https://example.test/meta/api/newapi/vendors.json", v)

	// Valid locale -> i18n path.
	m2, v2 := getUpstreamURLs("ja")
	assert.Equal(t, "https://example.test/meta/api/i18n/ja/newapi/models.json", m2)
	assert.Equal(t, "https://example.test/meta/api/i18n/ja/newapi/vendors.json", v2)

	// Unknown locale falls back to the default (non-i18n) URLs.
	m3, _ := getUpstreamURLs("de")
	assert.Equal(t, m, m3)
}

func TestContainsField(t *testing.T) {
	fields := []string{"Description", " icon ", "TAGS"}
	assert.True(t, containsField(fields, "description")) // case-insensitive
	assert.True(t, containsField(fields, "icon"))        // trimmed
	assert.True(t, containsField(fields, "tags"))
	assert.False(t, containsField(fields, "status"))
	assert.False(t, containsField(nil, "anything"))
}

func TestCoalesce(t *testing.T) {
	assert.Equal(t, "a", coalesce("a", "b"))
	assert.Equal(t, "b", coalesce("", "b"))
	assert.Equal(t, "b", coalesce("   ", "b")) // whitespace-only treated as empty
	assert.Equal(t, "", coalesce("", ""))
}

func TestChooseStatus(t *testing.T) {
	// primary==0 && fallback!=0 -> fallback
	assert.Equal(t, 2, chooseStatus(0, 2))
	// primary!=0 -> primary
	assert.Equal(t, 5, chooseStatus(5, 2))
	assert.Equal(t, 5, chooseStatus(5, 0))
	// both zero -> default 1
	assert.Equal(t, 1, chooseStatus(0, 0))
}
