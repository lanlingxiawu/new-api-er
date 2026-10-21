package common_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client disconnect may accompany bytes in the same Read call.
type auditDisconnectedBody struct{ sent bool }

func (r *auditDisconnectedBody) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, "partial body"), io.ErrUnexpectedEOF
}

func TestIndependentAuditBodyLimitsAndDisconnect(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	for _, disk := range []bool{false, true} {
		common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 16, Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
		for _, declared := range []int64{-1, 1, 1 << 20} {
			for _, length := range []int{0, 1, 32767, 32768, 32769} {
				t.Run(fmt.Sprintf("disk=%t/declared=%d/actual=%d", disk, declared, length), func(t *testing.T) {
					before := common.GetDiskCacheStats()
					payload := bytes.Repeat([]byte("x"), length)
					storage, err := common.CreateBodyStorageFromReader(bytes.NewReader(payload), declared, 32768)
					if length > 32768 {
						require.ErrorIs(t, err, common.ErrRequestBodyTooLarge)
						require.Nil(t, storage)
					} else {
						require.NoError(t, err)
						t.Cleanup(func() { _ = storage.Close() })
						reader, err := storage.NewReader()
						require.NoError(t, err)
						got, err := io.ReadAll(reader)
						require.NoError(t, err)
						require.Equal(t, payload, got)
						require.NoError(t, reader.Close())
						require.NoError(t, storage.Close())
						require.NoError(t, storage.Close())
					}
					after := common.GetDiskCacheStats()
					assert.Equal(t, before.BodyMemoryReservedBytes, after.BodyMemoryReservedBytes)
					assert.Equal(t, before.DiskReservedBytes, after.DiskReservedBytes)
					assert.Equal(t, before.ActiveDiskFiles, after.ActiveDiskFiles)
				})
			}
		}
		t.Run(fmt.Sprintf("disk=%t/disconnect", disk), func(t *testing.T) {
			before := common.GetDiskCacheStats()
			storage, err := common.CreateBodyStorageFromReader(&auditDisconnectedBody{}, 100, 32768)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.Nil(t, storage)
			after := common.GetDiskCacheStats()
			assert.Equal(t, before.BodyMemoryReservedBytes, after.BodyMemoryReservedBytes)
			assert.Equal(t, before.DiskReservedBytes, after.DiskReservedBytes)
			assert.Equal(t, before.ActiveDiskFiles, after.ActiveDiskFiles)
		})
	}
}

func TestIndependentAuditConcurrentBodyReplay(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	const payload = "independent replay payload"
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk=%t", disk), func(t *testing.T) {
			common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 4, Enabled: disk, ThresholdMB: 0, MaxSizeMB: 4, Path: t.TempDir()})
			before := common.GetDiskCacheStats()
			results := make(chan error, 16)
			var workers sync.WaitGroup
			for range 16 {
				workers.Go(func() {
					for range 100 {
						storage, err := common.CreateBodyStorageFromReader(strings.NewReader(payload), -1, 32768)
						if err != nil {
							results <- err
							return
						}
						reader, err := storage.NewReader()
						if err != nil {
							_ = storage.Close()
							results <- err
							return
						}
						got, readErr := io.ReadAll(reader)
						closeReaderErr := reader.Close()
						closeErr := storage.Close()
						if readErr != nil || closeReaderErr != nil || closeErr != nil || string(got) != payload {
							results <- fmt.Errorf("replay mismatch: read=%v readerClose=%v close=%v payload=%q", readErr, closeReaderErr, closeErr, got)
							return
						}
					}
				})
			}
			workers.Wait()
			close(results)
			for err := range results {
				assert.NoError(t, err)
			}
			after := common.GetDiskCacheStats()
			assert.Equal(t, before.BodyMemoryReservedBytes, after.BodyMemoryReservedBytes)
			assert.Equal(t, before.DiskReservedBytes, after.DiskReservedBytes)
			assert.Equal(t, before.ActiveDiskFiles, after.ActiveDiskFiles)
		})
	}
}
