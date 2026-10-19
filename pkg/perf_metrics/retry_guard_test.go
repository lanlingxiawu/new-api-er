package perfmetrics

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRetryGuardLatency(t *testing.T) {
	for _, tc := range []struct {
		stream, hasFirst, success bool
		first, total, want        int64
	}{
		{true, true, true, 100, 90000, 100},
		{true, true, false, 100, 90000, 100},
		{true, false, true, 0, 90000, -1},
		{true, false, false, 0, 10000, 10000},
		{false, false, true, 0, 200, 200},
		{false, false, false, 0, 200, 200},
	} {
		require.Equal(t, tc.want, retryGuardLatency(tc.stream, tc.hasFirst, tc.success, tc.first, tc.total))
	}
}
