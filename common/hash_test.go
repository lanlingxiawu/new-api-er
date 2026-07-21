package common

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSha256Raw(t *testing.T) {
	got := Sha256Raw([]byte("abc"))
	want := sha256.Sum256([]byte("abc"))
	assert.Equal(t, want[:], got)
	assert.Len(t, got, 32)
}

func TestSha1RawAndSha1(t *testing.T) {
	raw := Sha1Raw([]byte("abc"))
	want := sha1.Sum([]byte("abc"))
	assert.Equal(t, want[:], raw)

	assert.Equal(t, hex.EncodeToString(want[:]), Sha1([]byte("abc")))
	// known vector for "abc"
	assert.Equal(t, "a9993e364706816aba3e25717850c26c9cd0d89d", Sha1([]byte("abc")))
}

func TestHmacSha256(t *testing.T) {
	msg, key := "message", "secret"
	raw := HmacSha256Raw([]byte(msg), []byte(key))

	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(msg))
	assert.Equal(t, h.Sum(nil), raw)

	assert.Equal(t, hex.EncodeToString(raw), HmacSha256(msg, key))
	assert.Len(t, HmacSha256(msg, key), 64)
}
