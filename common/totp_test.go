package common

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// totp.go: 2FA (TOTP secrets, backup codes, numeric-code validation). Testing
// the app's own second-factor logic — round-trip + format validation.

func withSystemName(t *testing.T, name string) {
	t.Helper()
	orig := SystemName
	SystemName = name
	t.Cleanup(func() { SystemName = orig })
}

func TestGenerateTOTPSecret(t *testing.T) {
	withSystemName(t, "UnitTest")
	key, err := GenerateTOTPSecret("alice@example.com")
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.NotEmpty(t, key.Secret(), "a base32 secret must be produced")
	assert.Equal(t, "UnitTest", key.Issuer(), "issuer comes from SystemName via Get2FAIssuer")
}

func TestValidateTOTPCode(t *testing.T) {
	withSystemName(t, "UnitTest")
	key, err := GenerateTOTPSecret("bob@example.com")
	require.NoError(t, err)

	secret := key.Secret()
	// A freshly generated code for the current window must validate.
	// Signature is ValidateTOTPCode(secret, code).
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	assert.True(t, ValidateTOTPCode(secret, code), "valid current code accepted")

	// Spaces are stripped before validation.
	spaced := code[:3] + " " + code[3:]
	assert.True(t, ValidateTOTPCode(secret, spaced), "internal spaces tolerated")

	// Wrong length is rejected before hitting the validator (the len!=6 branch).
	assert.False(t, ValidateTOTPCode(secret, "12345"), "5-digit code rejected")
	assert.False(t, ValidateTOTPCode(secret, "0000000"), "7-digit code rejected")

	// A well-formed code generated for a window 10 minutes ago no longer validates.
	oldCode, err := totp.GenerateCode(secret, time.Now().Add(-10*time.Minute))
	require.NoError(t, err)
	if oldCode != code { // guard against the astronomically rare collision
		assert.False(t, ValidateTOTPCode(secret, oldCode), "stale code rejected")
	}
}

func TestGenerateBackupCodes(t *testing.T) {
	codes, err := GenerateBackupCodes()
	require.NoError(t, err)
	assert.Len(t, codes, BackupCodeCount)
	for _, c := range codes {
		assert.Regexp(t, `^[A-Z0-9]{4}-[A-Z0-9]{4}$`, c, "backup code is XXXX-XXXX")
		assert.True(t, ValidateBackupCode(c), "generated codes pass their own validator")
	}
}

func TestValidateBackupCode(t *testing.T) {
	assert.True(t, ValidateBackupCode("ABCD-1234"))
	assert.True(t, ValidateBackupCode("abcd1234"), "lowercase + no dash normalized")
	assert.False(t, ValidateBackupCode("ABC-123"), "too short (6)")
	assert.False(t, ValidateBackupCode("ABCD-12!4"), "illegal character")
	assert.False(t, ValidateBackupCode("ABCDEFGHI"), "too long (9)")
}

func TestNormalizeBackupCode(t *testing.T) {
	assert.Equal(t, "ABCD-1234", NormalizeBackupCode("abcd1234"))
	assert.Equal(t, "ABCD-1234", NormalizeBackupCode("ABCD-1234"))
	// Wrong length is returned unchanged.
	assert.Equal(t, "short", NormalizeBackupCode("short"))
}

func TestHashBackupCode(t *testing.T) {
	hash, err := HashBackupCode("abcd1234")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	// Hash is over the NORMALIZED code, so the normalized form validates.
	assert.True(t, ValidatePasswordAndHash("ABCD-1234", hash))
}

func TestGet2FAIssuer(t *testing.T) {
	withSystemName(t, "MyGateway")
	assert.Equal(t, "MyGateway", Get2FAIssuer())
}

func TestTOTP_GetEnvOrDefault(t *testing.T) {
	// The unexported getEnvOrDefault lives in totp.go (distinct from env.go's
	// exported GetEnvOrDefault covered elsewhere).
	t.Setenv("TOTP_TEST_ENV", "present")
	assert.Equal(t, "present", getEnvOrDefault("TOTP_TEST_ENV", "fallback"))
	assert.Equal(t, "fallback", getEnvOrDefault("TOTP_TEST_ENV_UNSET_XYZ", "fallback"))
}

func TestValidateNumericCode(t *testing.T) {
	code, err := ValidateNumericCode("123456")
	require.NoError(t, err)
	assert.Equal(t, "123456", code)

	code, err = ValidateNumericCode("12 34 56")
	require.NoError(t, err)
	assert.Equal(t, "123456", code, "spaces removed")

	_, err = ValidateNumericCode("12345")
	assert.Error(t, err, "5 digits rejected")

	_, err = ValidateNumericCode("12345a")
	assert.Error(t, err, "non-numeric rejected")
}

func TestGenerateQRCodeData(t *testing.T) {
	withSystemName(t, "Gate")
	data := GenerateQRCodeData("SECRET123", "alice")
	assert.True(t, strings.HasPrefix(data, "otpauth://totp/"))
	assert.Contains(t, data, "secret=SECRET123")
	assert.Contains(t, data, "issuer=Gate")
	assert.Contains(t, data, "alice")
}
