package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactTextCredentials(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"error body echoing a model name built with ?key=",
			`{"error":{"message":"No available channel for model gemini-2.5-flash?key=sk-abc123 under group default"}}`,
			`{"error":{"message":"No available channel for model gemini-2.5-flash?key=*** under group default"}}`},
		{"value stops at quote", `"url":"https://x/y?api_key=SECRET","n":1`, `"url":"https://x/y?api_key=***","n":1`},
		{"several credentials and a harmless pair", `a?key=K1&token=T2&alt=sse`, `a?key=***&token=***&alt=sse`},
		{"dotted name", `models/gemini.key=SECRET more`, `models/gemini.key=*** more`},
		{"percent-encoded name", `k%65y=SECRET`, `k%65y=***`},
		{"non-credential names untouched", `alt=sse model=gpt-4o temperature=0`, `alt=sse model=gpt-4o temperature=0`},
		{"already masked is idempotent", `key=***`, `key=***`},
		{"empty", ``, ``},
		{"prompt text without assignments", `Please summarise: the key idea is simple.`, `Please summarise: the key idea is simple.`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RedactTextCredentials(tc.in))
			assert.Equal(t, tc.want, RedactTextCredentials(RedactTextCredentials(tc.in)), "idempotent")
		})
	}
}
