package thirdpartysd2

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadUsesDurationFallback(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "dreamina-seedance-2-0-260128",
		Prompt:   "test",
		Duration: 6,
	})
	require.NoError(t, err)
	require.NotNil(t, payload.Duration)
	require.Equal(t, 6, int(*payload.Duration))
}

func TestConvertToRequestPayloadPrefersSecondsOverDuration(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "dreamina-seedance-2-0-fast-260128",
		Prompt:   "test",
		Duration: 4,
		Seconds:  "8",
	})
	require.NoError(t, err)
	require.NotNil(t, payload.Duration)
	require.Equal(t, 8, int(*payload.Duration))
}
