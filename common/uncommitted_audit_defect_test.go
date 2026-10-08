//go:build audit_uncommitted

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A twice-encoded Gemini model path is decoded once by net/http before the
// distributor echoes its model name. Stored JSON still contains an encoded
// assignment, and a non-root request-log viewer must not recover its key.
func TestUncommittedAuditEncodedCredentialInBody(t *testing.T) {
	raw := `{"error":{"message":"No available channel for model gemini%3Fkey%3Dsk-AUDIT_SECRET under group default"}}`
	assert.NotContains(t, RedactTextCredentials(raw), "sk-AUDIT_SECRET")
}
