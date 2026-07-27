package common

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalLogPreview(t *testing.T) {
	origDebug := DebugEnabled
	t.Cleanup(func() { DebugEnabled = origDebug })

	// short content is returned unchanged (boundary: len == limit)
	DebugEnabled = false
	short := strings.Repeat("a", LocalLogContentLimit)
	assert.Equal(t, short, LocalLogPreview(short))

	// over-limit content is truncated with a suffix
	long := strings.Repeat("b", LocalLogContentLimit+10)
	out := LocalLogPreview(long)
	assert.Contains(t, out, "[truncated, original_length=2058, limit=2048]")
	assert.True(t, strings.HasPrefix(out, strings.Repeat("b", LocalLogContentLimit)))

	// debug enabled disables truncation entirely
	DebugEnabled = true
	assert.Equal(t, long, LocalLogPreview(long))
}

func TestGetStringIfEmpty(t *testing.T) {
	assert.Equal(t, "fallback", GetStringIfEmpty("", "fallback"))
	assert.Equal(t, "value", GetStringIfEmpty("value", "fallback"))
}

func TestGetRandomString(t *testing.T) {
	assert.Equal(t, "", GetRandomString(0))
	assert.Equal(t, "", GetRandomString(-3))
	s := GetRandomString(16)
	assert.Len(t, s, 16)
	for _, r := range s {
		assert.True(t, strings.ContainsRune(loAlphanumeric, r), "unexpected char %q", r)
	}
}

const loAlphanumeric = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func TestMapToJsonStr(t *testing.T) {
	assert.Equal(t, `{"a":1}`, MapToJsonStr(map[string]interface{}{"a": 1}))
	// unmarshalable value -> "" (channels cannot be marshaled)
	assert.Equal(t, "", MapToJsonStr(map[string]interface{}{"bad": make(chan int)}))
}

func TestStrToMap(t *testing.T) {
	m, err := StrToMap(`{"a":1,"b":"x"}`)
	require.NoError(t, err)
	assert.Equal(t, float64(1), m["a"])
	assert.Equal(t, "x", m["b"])

	_, err = StrToMap(`not json`)
	assert.Error(t, err)
}

func TestStrToJsonArray(t *testing.T) {
	arr, err := StrToJsonArray(`[1,2,3]`)
	require.NoError(t, err)
	assert.Len(t, arr, 3)

	_, err = StrToJsonArray(`{}`)
	assert.Error(t, err)
}

func TestIsJsonArrayAndObject(t *testing.T) {
	assert.True(t, IsJsonArray(`[1,2]`))
	assert.False(t, IsJsonArray(`{"a":1}`))
	assert.False(t, IsJsonArray(`garbage`))

	assert.True(t, IsJsonObject(`{"a":1}`))
	assert.False(t, IsJsonObject(`[1,2]`))
	assert.False(t, IsJsonObject(`garbage`))
}

func TestString2Int(t *testing.T) {
	assert.Equal(t, 42, String2Int("42"))
	assert.Equal(t, -7, String2Int("-7"))
	assert.Equal(t, 0, String2Int("abc"))
	assert.Equal(t, 0, String2Int(""))
}

func TestStringsContains(t *testing.T) {
	assert.True(t, StringsContains([]string{"a", "b"}, "b"))
	assert.False(t, StringsContains([]string{"a", "b"}, "c"))
	assert.False(t, StringsContains(nil, "c"))
}

func TestStringToByteSlice(t *testing.T) {
	assert.Equal(t, []byte("hello"), StringToByteSlice("hello"))
	assert.Len(t, StringToByteSlice(""), 0)
}

func TestEncodeBase64(t *testing.T) {
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("abc")), EncodeBase64("abc"))
}

func TestGetJsonString(t *testing.T) {
	assert.Equal(t, "", GetJsonString(nil))
	assert.Equal(t, `{"a":1}`, GetJsonString(map[string]int{"a": 1}))
	// unmarshalable -> "" (err path swallowed)
	assert.Equal(t, "", GetJsonString(make(chan int)))
}

func TestNormalizeBillingPreference(t *testing.T) {
	for _, v := range []string{"subscription_first", "wallet_first", "subscription_only", "wallet_only"} {
		assert.Equal(t, v, NormalizeBillingPreference(v))
		assert.Equal(t, v, NormalizeBillingPreference("  "+v+"  "))
	}
	assert.Equal(t, "subscription_first", NormalizeBillingPreference("garbage"))
	assert.Equal(t, "subscription_first", NormalizeBillingPreference(""))
}

func TestMaskEmail(t *testing.T) {
	assert.Equal(t, "***masked***", MaskEmail(""))
	assert.Equal(t, "***masked***", MaskEmail("no-at-symbol"))
	assert.Equal(t, "***@example.com", MaskEmail("user@example.com"))
}

func TestMaskSensitiveInfo(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		check func(t *testing.T, out string)
	}{
		{
			name: "http url host only",
			in:   "http://example.com",
			check: func(t *testing.T, out string) {
				assert.Equal(t, "http://***.com", out)
			},
		},
		{
			name: "https url with path and query",
			in:   "https://api.test.org/v1/users/123?key=secret",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "https://***.org")
				assert.Contains(t, out, "/***/***/***")
				assert.Contains(t, out, "key=***")
				assert.NotContains(t, out, "secret")
			},
		},
		{
			name: "country code tld url",
			in:   "https://sub.domain.co.uk/path/to/resource",
			check: func(t *testing.T, out string) {
				// URL masking collapses the host to ***.co.uk; the plain-domain
				// pass then re-masks the residual co.uk, yielding ***.***.co.uk.
				assert.True(t, strings.HasPrefix(out, "https://"))
				assert.Contains(t, out, "co.uk")
				assert.Contains(t, out, "/***/***/***")
				assert.NotContains(t, out, "domain")
			},
		},
		{
			name: "trailing slash path preserved",
			in:   "https://openai.com/",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "https://***.com/")
			},
		},
		{
			name: "ip address",
			in:   "connect to 192.168.1.1 now",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "***.***.***.***")
			},
		},
		{
			name: "plain domain subdomain depth",
			in:   "api.openai.com",
			check: func(t *testing.T, out string) {
				assert.Equal(t, "***.***.com", out)
			},
		},
		{
			name: "plain domain single sub",
			in:   "openai.com",
			check: func(t *testing.T, out string) {
				assert.Equal(t, "***.com", out)
			},
		},
		{
			name: "api key masked",
			in:   `"api_key:AIzaSyAAAaUooTUni8AdaOkSRMda30n_Q4vrV70"`,
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "api_key:***")
				assert.NotContains(t, out, "AIzaSy")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, MaskSensitiveInfo(tc.in))
		})
	}
}
