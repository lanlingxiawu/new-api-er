package operation_setting

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProbeRoutingSettingRange(t *testing.T) {
	for _, n := range []int{0, 1, 128, 1024, 1025} {
		err := ValidateProbeRoutingSetting(ProbeRoutingSetting{MaxInputChars: n})
		require.Equal(t, n < 1 || n > 1024, err != nil)
	}
}
