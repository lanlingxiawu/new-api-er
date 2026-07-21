package ionet

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// requestPath returns the endpoint path (BaseURL stripped) of the last request
// recorded by the stub, so tests can assert routing without the base prefix.
func requestPath(t *testing.T, stub *stubHTTPClient) string {
	t.Helper()
	require.NotNil(t, stub.last, "no request was recorded")
	const base = "https://base.example/v1"
	require.True(t, strings.HasPrefix(stub.last.URL, base), "unexpected base in %q", stub.last.URL)
	return strings.TrimPrefix(stub.last.URL, base)
}

// mustTime parses an RFC3339 timestamp or fails the test.
func mustTime(s string) time.Time {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return tm
}
