package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveUserSessionSetting(t *testing.T) {
	t.Helper()
	original := GetUserSessionSetting()
	t.Cleanup(func() { ReplaceUserSessionSetting(original) })
}

func TestValidateUserSessionSettingBoundaries(t *testing.T) {
	valid := UserSessionSetting{
		ActiveLimit: 1, IssuanceLimit: 1, IssuanceWindowSec: 86_400,
		RevokedRetentionDays: 1, HourlyAlertThreshold: 1,
	}
	require.NoError(t, ValidateUserSessionSetting(valid))

	cases := []struct {
		name   string
		mutate func(*UserSessionSetting)
	}{
		{"active below minimum", func(s *UserSessionSetting) { s.ActiveLimit = 0 }},
		{"active above maximum", func(s *UserSessionSetting) { s.ActiveLimit = 10_001 }},
		{"issuance below minimum", func(s *UserSessionSetting) { s.IssuanceLimit = 0 }},
		{"issuance above maximum", func(s *UserSessionSetting) { s.IssuanceLimit = 100_001 }},
		{"window below minimum", func(s *UserSessionSetting) { s.IssuanceWindowSec = 0 }},
		{"window above maximum", func(s *UserSessionSetting) { s.IssuanceWindowSec = 31_536_001 }},
		{"retention below minimum", func(s *UserSessionSetting) { s.RevokedRetentionDays = 0 }},
		{"retention above maximum", func(s *UserSessionSetting) { s.RevokedRetentionDays = 366 }},
		{"alert below minimum", func(s *UserSessionSetting) { s.HourlyAlertThreshold = 0 }},
		{"alert above maximum", func(s *UserSessionSetting) { s.HourlyAlertThreshold = 10_000_001 }},
		{"window exceeds retention", func(s *UserSessionSetting) { s.IssuanceWindowSec = 86_401 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			tc.mutate(&candidate)
			require.Error(t, ValidateUserSessionSetting(candidate))
		})
	}
}

func TestUserSessionSettingPublishesImmutableSnapshot(t *testing.T) {
	preserveUserSessionSetting(t)
	setting := UserSessionSetting{12, 34, 43_200, 2, 56}
	ReplaceUserSessionSetting(setting)

	snapshot := GetUserSessionSnapshot()
	assert.Equal(t, 12, snapshot.ActiveLimit)
	assert.Equal(t, 34, snapshot.IssuanceLimit)
	assert.Equal(t, int64(43_200), snapshot.IssuanceWindowSeconds)
	assert.Equal(t, 2, snapshot.RevokedRetentionDays)
	assert.Equal(t, 56, snapshot.HourlyAlertThreshold)
}

func TestApplyUserSessionEnvDefaults(t *testing.T) {
	preserveUserSessionSetting(t)
	t.Setenv("USER_SESSION_ACTIVE_LIMIT", "12")
	t.Setenv("USER_SESSION_ISSUANCE_LIMIT", "34")
	t.Setenv("USER_SESSION_ISSUANCE_WINDOW_SECONDS", "172800")
	t.Setenv("USER_SESSION_REVOKED_RETENTION_DAYS", "1")
	t.Setenv("USER_SESSION_HOURLY_ALERT_THRESHOLD", "56")

	ApplyUserSessionEnvDefaults()
	snapshot := GetUserSessionSnapshot()
	assert.Equal(t, 12, snapshot.ActiveLimit)
	assert.Equal(t, 34, snapshot.IssuanceLimit)
	assert.Equal(t, int64(86_400), snapshot.IssuanceWindowSeconds, "environment compatibility clamps the window")
	assert.Equal(t, 1, snapshot.RevokedRetentionDays)
	assert.Equal(t, 56, snapshot.HourlyAlertThreshold)
}
