package common

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crypto.go: HMAC-SHA256 signing + bcrypt password hashing. Testing the app's
// own auth primitives (round-trip correctness, tamper rejection) — defensive.

func TestGenerateHMACWithKey_DeterministicAndHex(t *testing.T) {
	key := []byte("secret-key")
	a := GenerateHMACWithKey(key, "payload")
	b := GenerateHMACWithKey(key, "payload")
	assert.Equal(t, a, b, "same key+data => same MAC")

	_, err := hex.DecodeString(a)
	require.NoError(t, err, "output must be valid hex")
	assert.Len(t, a, 64, "SHA-256 HMAC is 32 bytes => 64 hex chars")

	assert.NotEqual(t, a, GenerateHMACWithKey(key, "payload2"), "different data => different MAC")
	assert.NotEqual(t, a, GenerateHMACWithKey([]byte("other-key"), "payload"), "different key => different MAC")
}

func TestGenerateHMAC_UsesCryptoSecret(t *testing.T) {
	orig := CryptoSecret
	CryptoSecret = "unit-test-secret"
	defer func() { CryptoSecret = orig }()

	got := GenerateHMAC("data")
	// Must equal signing with the CryptoSecret as key.
	assert.Equal(t, GenerateHMACWithKey([]byte("unit-test-secret"), "data"), got)
}

func TestPassword2Hash_And_Validate(t *testing.T) {
	hash, err := Password2Hash("hunter2")
	require.NoError(t, err)
	assert.NotEqual(t, "hunter2", hash, "hash must not equal the plaintext")
	assert.NotEmpty(t, hash)

	assert.True(t, ValidatePasswordAndHash("hunter2", hash), "correct password validates")
	assert.False(t, ValidatePasswordAndHash("wrong", hash), "wrong password rejected")
}

func TestValidatePasswordAndHash_InvalidHash(t *testing.T) {
	assert.False(t, ValidatePasswordAndHash("any", "not-a-bcrypt-hash"),
		"a malformed hash must reject, not panic")
}

func TestPassword2Hash_SaltedDistinctHashes(t *testing.T) {
	h1, err := Password2Hash("same")
	require.NoError(t, err)
	h2, err := Password2Hash("same")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "bcrypt salts => identical passwords hash differently")
	assert.True(t, ValidatePasswordAndHash("same", h1))
	assert.True(t, ValidatePasswordAndHash("same", h2))
}
