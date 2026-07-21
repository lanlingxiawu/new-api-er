package helper

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestStreamResult_Error(t *testing.T) {
	status := relaycommon.NewStreamStatus()
	sr := newStreamResult(status)

	// nil error is a no-op.
	sr.Error(nil)
	require.False(t, status.HasErrors())
	require.False(t, sr.IsStopped())

	sr.Error(errors.New("soft1"))
	sr.Error(errors.New("soft2"))
	require.True(t, status.HasErrors())
	require.Equal(t, 2, status.TotalErrorCount())
	require.False(t, sr.IsStopped(), "soft errors do not stop the stream")
}

func TestStreamResult_Stop(t *testing.T) {
	t.Run("with error records and stops", func(t *testing.T) {
		status := relaycommon.NewStreamStatus()
		sr := newStreamResult(status)
		sr.Stop(errors.New("fatal"))
		require.True(t, sr.IsStopped())
		require.Equal(t, relaycommon.StreamEndReasonHandlerStop, status.EndReason)
		require.True(t, status.HasErrors())
	})
	t.Run("nil error still stops without recording", func(t *testing.T) {
		status := relaycommon.NewStreamStatus()
		sr := newStreamResult(status)
		sr.Stop(nil)
		require.True(t, sr.IsStopped())
		require.Equal(t, relaycommon.StreamEndReasonHandlerStop, status.EndReason)
		require.False(t, status.HasErrors())
	})
}

func TestStreamResult_Done(t *testing.T) {
	status := relaycommon.NewStreamStatus()
	sr := newStreamResult(status)
	sr.Done()
	require.True(t, sr.IsStopped())
	require.Equal(t, relaycommon.StreamEndReasonDone, status.EndReason)
	require.False(t, status.HasErrors())
}

func TestStreamResult_Reset(t *testing.T) {
	status := relaycommon.NewStreamStatus()
	sr := newStreamResult(status)
	sr.Stop(errors.New("fatal"))
	require.True(t, sr.IsStopped())
	sr.reset()
	require.False(t, sr.IsStopped(), "reset clears the per-chunk stopped flag")
}
