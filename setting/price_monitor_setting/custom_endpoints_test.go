package price_monitor_setting

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantOK  bool
		comment string
	}{
		{name: "pricing path", input: "/api/pricing", want: "/api/pricing", wantOK: true},
		{name: "ratio config path", input: "/api/ratio_config", want: "/api/ratio_config", wantOK: true},
		{name: "path without leading slash", input: "api/pricing", want: "/api/pricing", wantOK: true},
		{name: "surrounding spaces", input: "  /api/pricing  ", want: "/api/pricing", wantOK: true},
		{name: "absolute https", input: "https://example.com/llm/ratio.json", want: "https://example.com/llm/ratio.json", wantOK: true},
		{name: "absolute http with query", input: "http://example.com/p?v=1", want: "http://example.com/p?v=1", wantOK: true},
		{name: "credentials stripped", input: "https://user:secret@example.com/p", want: "https://example.com/p", wantOK: true},
		{name: "fragment stripped", input: "https://example.com/p#frag", want: "https://example.com/p", wantOK: true},
		{name: "empty", input: "", wantOK: false},
		{name: "spaces only", input: "   ", wantOK: false},
		{name: "scheme without host", input: "https://", wantOK: false},
		{name: "unsupported scheme", input: "ftp://example.com/p", wantOK: false},
		{name: "control character", input: "/api/pri\ncing", wantOK: false},
		{name: "inner space", input: "/api/pri cing", wantOK: false},
		{name: "too long", input: "/" + strings.Repeat("a", maxCustomEndpointLength), wantOK: false},
		{name: "at length limit", input: "/" + strings.Repeat("a", maxCustomEndpointLength-1), want: "/" + strings.Repeat("a", maxCustomEndpointLength-1), wantOK: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := SanitizeEndpoint(testCase.input)
			require.Equal(t, testCase.wantOK, ok)
			if testCase.wantOK {
				require.Equal(t, testCase.want, got)
			} else {
				require.Empty(t, got)
			}
		})
	}
}

func TestValidateCustomEndpoints(t *testing.T) {
	t.Run("nil map is accepted", func(t *testing.T) {
		cleaned, err := ValidateCustomEndpoints(nil)
		require.NoError(t, err)
		require.Empty(t, cleaned)
	})

	t.Run("blank values are dropped instead of rejected", func(t *testing.T) {
		cleaned, err := ValidateCustomEndpoints(map[string]string{"7": "  ", "8": "/api/pricing"})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"8": "/api/pricing"}, cleaned)
	})

	t.Run("values are sanitized", func(t *testing.T) {
		cleaned, err := ValidateCustomEndpoints(map[string]string{"7": " https://user:pw@example.com/p "})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"7": "https://example.com/p"}, cleaned)
	})

	t.Run("invalid endpoint is rejected", func(t *testing.T) {
		_, err := ValidateCustomEndpoints(map[string]string{"7": "ftp://example.com"})
		require.Error(t, err)
	})

	t.Run("non numeric channel id is rejected", func(t *testing.T) {
		_, err := ValidateCustomEndpoints(map[string]string{"abc": "/api/pricing"})
		require.Error(t, err)
	})

	t.Run("non positive channel id is rejected", func(t *testing.T) {
		_, err := ValidateCustomEndpoints(map[string]string{"0": "/api/pricing"})
		require.Error(t, err)
		_, err = ValidateCustomEndpoints(map[string]string{"-3": "/api/pricing"})
		require.Error(t, err)
	})

	t.Run("entry count limit", func(t *testing.T) {
		atLimit := make(map[string]string, maxCustomEndpointEntries)
		for index := 1; index <= maxCustomEndpointEntries; index++ {
			atLimit[strconv.Itoa(index)] = "/api/pricing"
		}
		cleaned, err := ValidateCustomEndpoints(atLimit)
		require.NoError(t, err)
		require.Len(t, cleaned, maxCustomEndpointEntries)

		atLimit[strconv.Itoa(maxCustomEndpointEntries+1)] = "/api/pricing"
		_, err = ValidateCustomEndpoints(atLimit)
		require.Error(t, err)
	})
}

func TestNormalizedDropsInvalidCustomEndpointsWithoutMutatingSource(t *testing.T) {
	source := map[string]string{
		"7":   "/api/ratio_config",
		"8":   "ftp://example.com",
		"bad": "/api/pricing",
		"9":   "",
	}
	setting := PriceMonitorSetting{IntervalMinutes: 60, TimeoutSeconds: 10, CustomEndpoints: source}

	normalized := setting.Normalized()

	require.Equal(t, map[string]string{"7": "/api/ratio_config"}, normalized.CustomEndpoints)
	// 发布出去的配置是共享只读快照，Normalized 不能就地改写它的 map。
	require.Len(t, source, 4)
}

func TestCustomEndpointFor(t *testing.T) {
	setting := PriceMonitorSetting{CustomEndpoints: map[string]string{"7": "/api/ratio_config"}}
	require.Equal(t, "/api/ratio_config", setting.CustomEndpointFor(7))
	require.Equal(t, "", setting.CustomEndpointFor(8))
	require.Equal(t, "", PriceMonitorSetting{}.CustomEndpointFor(7))
}
