package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetVerificationState isolates each test from the shared package-level map.
func resetVerificationState(t *testing.T) {
	t.Helper()
	verificationMutex.Lock()
	origMap := verificationMap
	origValid := VerificationValidMinutes
	verificationMap = make(map[string]verificationValue)
	verificationMutex.Unlock()
	t.Cleanup(func() {
		verificationMutex.Lock()
		verificationMap = origMap
		VerificationValidMinutes = origValid
		verificationMutex.Unlock()
	})
}

func TestGenerateVerificationCode(t *testing.T) {
	full := GenerateVerificationCode(0)
	assert.Len(t, full, 32) // 0 => full uuid without dashes

	short := GenerateVerificationCode(6)
	assert.Len(t, short, 6)

	assert.NotEqual(t, GenerateVerificationCode(0), GenerateVerificationCode(0))
}

func TestVerifyCodeWithKey(t *testing.T) {
	resetVerificationState(t)

	RegisterVerificationCodeWithKey("alice", "123456", EmailVerificationPurpose)

	// correct code + purpose
	assert.True(t, VerifyCodeWithKey("alice", "123456", EmailVerificationPurpose))
	// wrong code
	assert.False(t, VerifyCodeWithKey("alice", "000000", EmailVerificationPurpose))
	// wrong purpose (namespacing) -> not found
	assert.False(t, VerifyCodeWithKey("alice", "123456", PasswordResetPurpose))
	// unknown key
	assert.False(t, VerifyCodeWithKey("bob", "123456", EmailVerificationPurpose))
}

func TestVerifyCodeWithKey_Expired(t *testing.T) {
	resetVerificationState(t)

	RegisterVerificationCodeWithKey("carol", "abcdef", PasswordResetPurpose)
	// Force expiry by rewinding the stored timestamp beyond the validity window.
	verificationMutex.Lock()
	v := verificationMap[PasswordResetPurpose+"carol"]
	v.time = time.Now().Add(-time.Duration(VerificationValidMinutes+1) * time.Minute)
	verificationMap[PasswordResetPurpose+"carol"] = v
	verificationMutex.Unlock()

	assert.False(t, VerifyCodeWithKey("carol", "abcdef", PasswordResetPurpose))
}

func TestDeleteKey(t *testing.T) {
	resetVerificationState(t)

	RegisterVerificationCodeWithKey("dave", "code", EmailVerificationPurpose)
	assert.True(t, VerifyCodeWithKey("dave", "code", EmailVerificationPurpose))

	DeleteKey("dave", EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey("dave", "code", EmailVerificationPurpose))
}

func TestRemoveExpiredPairsTriggeredOnOverflow(t *testing.T) {
	resetVerificationState(t)

	// Seed more than verificationMapMaxSize expired entries, then register one
	// fresh entry to trip the size-based cleanup which prunes expired pairs.
	past := time.Now().Add(-time.Duration(VerificationValidMinutes+1) * time.Minute)
	verificationMutex.Lock()
	for i := 0; i < verificationMapMaxSize+2; i++ {
		verificationMap[EmailVerificationPurpose+string(rune('a'+i))] = verificationValue{
			code: "x",
			time: past,
		}
	}
	verificationMutex.Unlock()

	RegisterVerificationCodeWithKey("fresh", "new", EmailVerificationPurpose)

	verificationMutex.Lock()
	// All expired entries must have been pruned; only the fresh one remains.
	remaining := len(verificationMap)
	fresh := verificationMap[EmailVerificationPurpose+"fresh"]
	verificationMutex.Unlock()

	assert.Equal(t, 1, remaining)
	assert.Equal(t, "new", fresh.code)
}

func TestVerificationPurposesDistinct(t *testing.T) {
	require.NotEqual(t, EmailVerificationPurpose, PasswordResetPurpose)
}
