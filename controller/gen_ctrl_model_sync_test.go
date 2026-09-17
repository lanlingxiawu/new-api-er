package controller

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// model_sync.go pure helpers: locale normalization and upstream URL derivation.

func TestNormalizeLocale(t *testing.T) {
	// Empty / zh / zh-cn (case-insensitive) default to the zh metadata; en and ja
	// are the other supported locales; everything else is unsupported.
	cases := []struct {
		in     string
		want   string
		wantOk bool
	}{
		{"", "zh", true},
		{"zh", "zh", true},
		{"zh-CN", "zh", true},
		{" zh-cn ", "zh", true},
		{"en", "en", true},
		{"EN", "en", true},
		{"ja", "ja", true},
		{"JA", "ja", true},
		{"zh-TW", "", false},
		{"fr", "", false},
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

	// Unsupported locale -> non-i18n path (trailing slash trimmed).
	m, v := getUpstreamURLs("de")
	assert.Equal(t, "https://example.test/meta/api/newapi/models.json", m)
	assert.Equal(t, "https://example.test/meta/api/newapi/vendors.json", v)

	// Empty locale defaults to the zh i18n path.
	mz, _ := getUpstreamURLs("")
	assert.Equal(t, "https://example.test/meta/api/i18n/zh/newapi/models.json", mz)

	// Valid locale -> i18n path.
	m2, v2 := getUpstreamURLs("ja")
	assert.Equal(t, "https://example.test/meta/api/i18n/ja/newapi/models.json", m2)
	assert.Equal(t, "https://example.test/meta/api/i18n/ja/newapi/vendors.json", v2)

	// Another unknown locale falls back to the same default (non-i18n) URLs.
	m3, _ := getUpstreamURLs("fr")
	assert.Equal(t, m, m3)
}
