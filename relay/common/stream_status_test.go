package common

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamStatus_SetEndReason_FirstWins(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("ctx"))
	assert.Equal(t, StreamEndReasonDone, s.EndReason)
	assert.Nil(t, s.EndError)
}

func TestStreamStatus_SetEndReason_WithError(t *testing.T) {
	s := NewStreamStatus()
	e := fmt.Errorf("read: connection reset")
	s.SetEndReason(StreamEndReasonScannerErr, e)
	assert.Equal(t, StreamEndReasonScannerErr, s.EndReason)
	assert.Equal(t, e, s.EndError)
}

func TestStreamStatus_NilReceiverSafe(t *testing.T) {
	var s *StreamStatus
	assert.NotPanics(t, func() {
		s.SetEndReason(StreamEndReasonDone, nil)
		s.RecordError("x")
		assert.False(t, s.HasErrors())
		assert.Equal(t, 0, s.TotalErrorCount())
		assert.True(t, s.IsNormalEnd())
		assert.Equal(t, "StreamStatus<nil>", s.Summary())
	})
}

func TestStreamStatus_SetEndReason_Concurrent(t *testing.T) {
	s := NewStreamStatus()
	reasons := []StreamEndReason{
		StreamEndReasonDone, StreamEndReasonTimeout, StreamEndReasonClientGone,
		StreamEndReasonScannerErr, StreamEndReasonHandlerStop, StreamEndReasonEOF,
		StreamEndReasonPanic, StreamEndReasonPingFail,
	}
	var wg sync.WaitGroup
	for _, r := range reasons {
		wg.Add(1)
		go func(reason StreamEndReason) { defer wg.Done(); s.SetEndReason(reason, nil) }(r)
	}
	wg.Wait()
	assert.NotEqual(t, StreamEndReasonNone, s.EndReason)
}

func TestStreamStatus_RecordError_Basic(t *testing.T) {
	s := NewStreamStatus()
	s.RecordError("a")
	s.RecordError("b")
	s.RecordError("c")
	assert.True(t, s.HasErrors())
	assert.Equal(t, 3, s.TotalErrorCount())
	assert.Len(t, s.Errors, 3)
}

func TestStreamStatus_RecordError_CapAtMax(t *testing.T) {
	s := NewStreamStatus()
	for i := 0; i < 30; i++ {
		s.RecordError(fmt.Sprintf("e_%d", i))
	}
	assert.Equal(t, maxStreamErrorEntries, len(s.Errors))
	assert.Equal(t, 30, s.TotalErrorCount())
}

func TestStreamStatus_RecordError_Concurrent(t *testing.T) {
	s := NewStreamStatus()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) { defer wg.Done(); s.RecordError(fmt.Sprintf("e_%d", idx)) }(i)
	}
	wg.Wait()
	assert.Equal(t, 100, s.TotalErrorCount())
	assert.LessOrEqual(t, len(s.Errors), maxStreamErrorEntries)
}

func TestStreamStatus_HasErrors_Empty(t *testing.T) {
	s := NewStreamStatus()
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_IsNormalEnd(t *testing.T) {
	tests := []struct {
		reason StreamEndReason
		normal bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
		{StreamEndReasonNone, false},
	}
	for _, tt := range tests {
		s := NewStreamStatus()
		s.SetEndReason(tt.reason, nil)
		assert.Equal(t, tt.normal, s.IsNormalEnd(), "reason=%s", tt.reason)
	}
}

func TestStreamStatus_Summary(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	require.Contains(t, s.Summary(), "reason=done")
	require.NotContains(t, s.Summary(), "soft_errors")

	s2 := NewStreamStatus()
	s2.SetEndReason(StreamEndReasonScannerErr, fmt.Errorf("boom"))
	s2.RecordError("bad json")
	s2.RecordError("write failed")
	sum := s2.Summary()
	require.Contains(t, sum, "reason=scanner_error")
	require.Contains(t, sum, `end_error="boom"`)
	require.Contains(t, sum, "soft_errors=2")
}
