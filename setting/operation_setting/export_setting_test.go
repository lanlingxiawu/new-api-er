package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetExportSetting_ReturnsGlobalPointer(t *testing.T) {
	got := GetExportSetting()
	require.NotNil(t, got)
	assert.Same(t, &exportSetting, got)
}

func TestExportSetting_GetRateLimitCooldownSec_Boundaries(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero-falls-back", 0, DefaultExportRateLimitCooldownSec},
		{"negative-falls-back", -5, DefaultExportRateLimitCooldownSec},
		{"one-lower-valid", 1, 1},
		{"custom-valid", 900, 900},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &ExportSetting{RateLimitCooldownSec: c.in}
			assert.Equal(t, c.want, s.GetRateLimitCooldownSec())
		})
	}
}

func TestExportSetting_GetHardCeilingRows_Boundaries(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero-falls-back", 0, DefaultExportHardCeilingRows},
		{"negative-falls-back", -1, DefaultExportHardCeilingRows},
		{"one-lower-valid", 1, 1},
		{"custom-valid", 500000, 500000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &ExportSetting{HardCeilingRows: c.in}
			assert.Equal(t, c.want, s.GetHardCeilingRows())
		})
	}
}

func TestExportSetting_GetUserExportEnabled(t *testing.T) {
	assert.True(t, (&ExportSetting{UserExportEnabled: true}).GetUserExportEnabled())
	assert.False(t, (&ExportSetting{UserExportEnabled: false}).GetUserExportEnabled())
}

func TestExportSetting_Defaults(t *testing.T) {
	assert.Equal(t, 600, DefaultExportRateLimitCooldownSec)
	assert.Equal(t, 1000000, DefaultExportHardCeilingRows)
	assert.True(t, exportSetting.UserExportEnabled)
}
