package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tfaMake creates an enabled TwoFA row for the given user (with a real TOTP
// secret) and registers hard-delete cleanup for the row and its backup codes.
func tfaMake(t *testing.T, userID int, enabled bool) *TwoFA {
	t.Helper()
	requireDB(t)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: uniq("acct")})
	require.NoError(t, err)
	tf := &TwoFA{
		UserId:    userID,
		Secret:    key.Secret(),
		IsEnabled: enabled,
	}
	require.NoError(t, tf.Create())
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", userID).Delete(&TwoFABackupCode{})
			DB.Unscoped().Delete(&TwoFA{}, tf.Id)
		}
	})
	return tf
}

// ---------------------------------------------------------------------------
// Get / IsEnabled / Create
// ---------------------------------------------------------------------------

func TestTwoFA_GetByUserId(t *testing.T) {
	// userId 0 -> error
	_, err := GetTwoFAByUserId(0)
	assert.Error(t, err)

	u := mkUser(t, nil)

	// not set -> (nil, nil)
	got, err := GetTwoFAByUserId(u.Id)
	require.NoError(t, err)
	assert.Nil(t, got)

	tf := tfaMake(t, u.Id, true)
	got, err = GetTwoFAByUserId(u.Id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, tf.Id, got.Id)
}

func TestTwoFA_IsEnabled(t *testing.T) {
	u := mkUser(t, nil)

	// no record -> false
	ok, err := IsTwoFAEnabled(u.Id)
	require.NoError(t, err)
	assert.False(t, ok)

	// disabled record -> false
	tf := tfaMake(t, u.Id, false)
	ok, err = IsTwoFAEnabled(u.Id)
	require.NoError(t, err)
	assert.False(t, ok)

	// enable -> true
	require.NoError(t, tf.Enable())
	ok, err = IsTwoFAEnabled(u.Id)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestTwoFA_CreateGuards(t *testing.T) {
	u := mkUser(t, nil)
	tfaMake(t, u.Id, false)

	// duplicate for the same user -> error
	dup := &TwoFA{UserId: u.Id, Secret: "x"}
	err := dup.Create()
	assert.Error(t, err)

	// nonexistent user -> "用户不存在"
	ghost := &TwoFA{UserId: 900_000_123, Secret: "x"}
	err = ghost.Create()
	assert.Error(t, err)
}

func TestTwoFA_UpdateGuard(t *testing.T) {
	tf := &TwoFA{} // Id == 0
	assert.Error(t, tf.Update())
}

// ---------------------------------------------------------------------------
// Failed attempts / lockout
// ---------------------------------------------------------------------------

func TestTwoFA_IncrementFailedAttemptsAndLock(t *testing.T) {
	u := mkUser(t, nil)
	tf := tfaMake(t, u.Id, true)

	// increment up to (but not reaching) the lock threshold
	for i := 1; i < common.MaxFailAttempts; i++ {
		require.NoError(t, tf.IncrementFailedAttempts())
		assert.Equal(t, i, tf.FailedAttempts)
		assert.False(t, tf.IsLocked())
	}
	// the increment that reaches MaxFailAttempts sets the lock
	require.NoError(t, tf.IncrementFailedAttempts())
	assert.Equal(t, common.MaxFailAttempts, tf.FailedAttempts)
	require.NotNil(t, tf.LockedUntil)
	assert.True(t, tf.IsLocked())

	// while locked, a further increment is a no-op that preserves state
	prevAttempts := tf.FailedAttempts
	require.NoError(t, tf.IncrementFailedAttempts())
	assert.Equal(t, prevAttempts, tf.FailedAttempts)

	// reset clears everything
	require.NoError(t, tf.ResetFailedAttempts())
	assert.Equal(t, 0, tf.FailedAttempts)
	assert.Nil(t, tf.LockedUntil)
	assert.False(t, tf.IsLocked())

	// id==0 guard
	empty := &TwoFA{}
	assert.Error(t, empty.IncrementFailedAttempts())
}

func TestTwoFA_IsLocked(t *testing.T) {
	tf := &TwoFA{}
	assert.False(t, tf.IsLocked())

	future := time.Now().Add(time.Hour)
	tf.LockedUntil = &future
	assert.True(t, tf.IsLocked())

	past := time.Now().Add(-time.Hour)
	tf.LockedUntil = &past
	assert.False(t, tf.IsLocked())
}

// ---------------------------------------------------------------------------
// Backup codes: creation + single-use consumption
// ---------------------------------------------------------------------------

func TestTwoFA_BackupCodeSingleUse(t *testing.T) {
	u := mkUser(t, nil)
	tfaMake(t, u.Id, true)

	codes := []string{"ABCD-1234", "WXYZ-5678", "QRST-9012"}
	require.NoError(t, CreateBackupCodes(u.Id, codes))
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", u.Id).Delete(&TwoFABackupCode{})
		}
	})

	cnt, err := GetUnusedBackupCodeCount(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 3, cnt)

	// invalid format (too short) -> error, no consumption
	_, err = ValidateBackupCode(u.Id, "abc")
	assert.Error(t, err)

	// wrong (well-formed) code -> false, no error, no consumption
	ok, err := ValidateBackupCode(u.Id, "AAAA-0000")
	require.NoError(t, err)
	assert.False(t, ok)

	// valid code consumed once
	ok, err = ValidateBackupCode(u.Id, "ABCD-1234")
	require.NoError(t, err)
	assert.True(t, ok)

	// SECURITY: the same code must NOT be reusable
	ok, err = ValidateBackupCode(u.Id, "ABCD-1234")
	require.NoError(t, err)
	assert.False(t, ok, "consumed backup code was reusable")

	// normalisation: unformatted input for a still-unused code works once
	ok, err = ValidateBackupCode(u.Id, "wxyz5678")
	require.NoError(t, err)
	assert.True(t, ok)

	cnt, err = GetUnusedBackupCodeCount(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt) // 3 created - 2 consumed

	// regeneration replaces all existing codes (old consumed state is wiped)
	require.NoError(t, CreateBackupCodes(u.Id, []string{"NEW1-1111", "NEW2-2222"}))
	cnt, err = GetUnusedBackupCodeCount(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)
	// the previously-consumed code no longer exists
	ok, err = ValidateBackupCode(u.Id, "ABCD-1234")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// TOTP validation + usage update
// ---------------------------------------------------------------------------

func TestTwoFA_ValidateTOTPAndUpdateUsage(t *testing.T) {
	u := mkUser(t, nil)
	tf := tfaMake(t, u.Id, true)

	// a valid current code succeeds and records usage
	code, err := totp.GenerateCode(tf.Secret, time.Now())
	require.NoError(t, err)
	ok, err := tf.ValidateTOTPAndUpdateUsage(code)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.NotNil(t, tf.LastUsedAt)
	assert.Equal(t, 0, tf.FailedAttempts)

	// a wrong code fails and bumps the failed counter
	ok, err = tf.ValidateTOTPAndUpdateUsage("000000")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 1, tf.FailedAttempts)

	// when locked, validation short-circuits with an error
	future := time.Now().Add(time.Hour)
	tf.LockedUntil = &future
	_, err = tf.ValidateTOTPAndUpdateUsage(code)
	assert.Error(t, err)
}

func TestTwoFA_ValidateBackupCodeAndUpdateUsage(t *testing.T) {
	u := mkUser(t, nil)
	tf := tfaMake(t, u.Id, true)
	require.NoError(t, CreateBackupCodes(u.Id, []string{"BKUP-0001"}))
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", u.Id).Delete(&TwoFABackupCode{})
		}
	})

	// locked -> error path first
	future := time.Now().Add(time.Hour)
	tf.LockedUntil = &future
	_, err := tf.ValidateBackupCodeAndUpdateUsage("BKUP-0001")
	assert.Error(t, err)
	tf.LockedUntil = nil

	// wrong code -> false + failed attempt bump
	ok, err := tf.ValidateBackupCodeAndUpdateUsage("ZZZZ-9999")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 1, tf.FailedAttempts)

	// valid code -> true, resets counter, records usage
	ok, err = tf.ValidateBackupCodeAndUpdateUsage("BKUP-0001")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 0, tf.FailedAttempts)
	assert.NotNil(t, tf.LastUsedAt)

	// consumed -> no longer valid
	ok, err = tf.ValidateBackupCodeAndUpdateUsage("BKUP-0001")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// Disable / delete
// ---------------------------------------------------------------------------

func TestTwoFA_DisableAndDelete(t *testing.T) {
	u := mkUser(t, nil)

	// disabling when not enabled -> ErrTwoFANotEnabled
	err := DisableTwoFA(u.Id)
	assert.ErrorIs(t, err, ErrTwoFANotEnabled)

	tf := tfaMake(t, u.Id, true)
	require.NoError(t, CreateBackupCodes(u.Id, []string{"DELE-0001"}))

	// disable hard-deletes the 2FA record and its backup codes
	require.NoError(t, DisableTwoFA(u.Id))

	got, err := GetTwoFAByUserId(u.Id)
	require.NoError(t, err)
	assert.Nil(t, got)

	var bcCount int64
	require.NoError(t, DB.Model(&TwoFABackupCode{}).Where("user_id = ?", u.Id).Count(&bcCount).Error)
	assert.EqualValues(t, 0, bcCount)

	// Delete guard: id==0
	assert.Error(t, (&TwoFA{}).Delete())

	_ = tf
}

func TestTwoFA_Stats(t *testing.T) {
	u := mkUser(t, nil)
	tfaMake(t, u.Id, true)

	stats, err := GetTwoFAStats()
	require.NoError(t, err)
	require.NotNil(t, stats)
	// keys present with expected types
	assert.Contains(t, stats, "total_users")
	assert.Contains(t, stats, "enabled_users")
	assert.Contains(t, stats, "enabled_rate")
	assert.GreaterOrEqual(t, stats["enabled_users"].(int64), int64(1))
	assert.IsType(t, "", stats["enabled_rate"])
}
