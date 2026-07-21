package cachex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// prefix() is unexported; exercised through FullKey/MatchPattern which are the
// only callers, plus a direct call here for the trimming edge cases.

func TestNamespace_Prefix(t *testing.T) {
	assert.Equal(t, "", Namespace("").prefix(), "empty namespace has no prefix")
	assert.Equal(t, "", Namespace("   ").prefix(), "whitespace-only trims to empty")
	assert.Equal(t, "chan:", Namespace("chan").prefix())
	assert.Equal(t, "chan:", Namespace("chan:").prefix(), "trailing colon not doubled")
	assert.Equal(t, "chan:", Namespace("chan:::").prefix(), "all trailing colons trimmed")
	assert.Equal(t, "chan:", Namespace("  chan:  ").prefix(), "surrounding spaces trimmed")
}

func TestNamespace_FullKey(t *testing.T) {
	cases := []struct {
		name string
		ns   Namespace
		key  string
		want string
	}{
		{"empty key", "chan", "", ""},
		{"whitespace key", "chan", "   ", ""},
		{"simple", "chan:v1", "abc", "chan:v1:abc"},
		{"already prefixed passes through", "chan:v1", "chan:v1:abc", "chan:v1:abc"},
		{"empty ns returns bare key", "", "abc", "abc"},
		{"empty ns strips leading colons", "", ":abc", "abc"},
		{"leading colons stripped before join", "chan", ":abc", "chan:abc"},
		{"ns normalized before join", "  chan:  ", "abc", "chan:abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.ns.FullKey(tc.key))
		})
	}
}

func TestNamespace_MatchPattern(t *testing.T) {
	assert.Equal(t, "*", Namespace("").MatchPattern(), "no namespace matches everything")
	assert.Equal(t, "chan:*", Namespace("chan").MatchPattern())
	assert.Equal(t, "chan:*", Namespace("chan:").MatchPattern())
}
