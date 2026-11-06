package logger

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type forkConcurrentLogSink struct{ calls atomic.Int64 }

func (s *forkConcurrentLogSink) Write(p []byte) (int, error) { s.calls.Add(1); return len(p), nil }

// Concurrent fault streams share logger counters even when their diagnostic
// bodies are private. The sink is race-safe; a race here belongs to the logger.
func TestForkConcurrentFaultLogging(t *testing.T) {
	sink := &forkConcurrentLogSink{}
	common.LogWriterMu.Lock()
	out, errs := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = sink, sink
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = out, errs
		common.LogWriterMu.Unlock()
	})
	ctx := context.WithValue(context.Background(), common.StreamPrivateContextKey, true)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 64 {
				LogInfo(ctx, "synthetic settlement")
				LogError(ctx, "synthetic transport error")
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(2048), sink.calls.Load())
}
