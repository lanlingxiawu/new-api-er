package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type bodyCacheFunding struct {
	refunds     atomic.Int32
	settlements atomic.Int32
	delta       atomic.Int64
}

func (*bodyCacheFunding) Source() string       { return BillingSourceWallet }
func (*bodyCacheFunding) PreConsume(int) error { return nil }
func (f *bodyCacheFunding) Settle(delta int) error {
	f.delta.Add(int64(delta))
	f.settlements.Add(1)
	return nil
}
func (f *bodyCacheFunding) Refund() error { f.refunds.Add(1); return nil }

func TestBodyCacheSuccessfulSettlementExactlyOnce(t *testing.T) {
	old := common.GetDiskCacheConfig()
	defer common.SetDiskCacheConfig(old)
	for _, mode := range []string{"off", "memory", "disk"} {
		for _, pre := range []int{0, 100, 117, 200} {
			t.Run(fmt.Sprintf("%s/pre=%d", mode, pre), func(t *testing.T) {
				common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: mode != "off", MemoryBudgetMB: 4, Enabled: mode == "disk", ThresholdMB: 0, MaxSizeMB: 4, Path: t.TempDir()})
				storage, err := common.CreateBodyStorage([]byte(`{"model":"fixture","temperature":0}`))
				require.NoError(t, err)
				defer storage.Close()
				funding := &bodyCacheFunding{}
				session := &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding, preConsumedQuota: pre, tokenConsumed: pre}
				var wg sync.WaitGroup
				errs := make(chan error, 32)
				for i := 0; i < 32; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); errs <- session.Settle(117) }()
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/", nil)
				session.Refund(c)
				require.Equal(t, int64(117-pre), funding.delta.Load())
				expectedCalls := int32(1)
				if pre == 117 {
					expectedCalls = 0
				}
				require.Equal(t, expectedCalls, funding.settlements.Load())
				require.Zero(t, funding.refunds.Load())
				require.False(t, session.NeedsRefund())
				require.NoError(t, storage.Close())
				require.Zero(t, common.GetBodyMemoryReserved())
				require.Zero(t, common.GetDiskCacheStats().DiskReservedBytes)
			})
		}
	}
}

func TestBodyCacheCapacityRefundExactlyOnce(t *testing.T) {
	old := common.GetDiskCacheConfig()
	defer common.SetDiskCacheConfig(old)
	common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	held, err := common.CreateBodyStorage(make([]byte, 1<<20))
	require.NoError(t, err)
	defer held.Close()
	funding := &bodyCacheFunding{}
	// Exercise the existing settlement owner after pre-consumption. Playground
	// avoids a real token DB write; FundingSource is the business interface.
	session := &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding, tokenConsumed: 1, preConsumedQuota: 1}
	_, err = common.CreateBodyStorage([]byte("outgoing"))
	require.ErrorIs(t, err, common.ErrBodyCacheCapacity)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	require.True(t, session.NeedsRefund())
	session.Refund(c)
	session.Refund(c)
	require.Eventually(t, func() bool { return funding.refunds.Load() == 1 }, time.Second, time.Millisecond)
	require.False(t, session.NeedsRefund())
	require.Equal(t, int32(1), funding.refunds.Load())
}
