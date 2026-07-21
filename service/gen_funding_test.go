package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// funding_source.go — WalletFunding lifecycle + refundWithRetry. DB-backed.
// ===========================================================================

func TestFunding_WalletSource(t *testing.T) {
	w := &WalletFunding{userId: 1}
	assert.Equal(t, BillingSourceWallet, w.Source())
}

func TestFunding_SubscriptionSource(t *testing.T) {
	s := &SubscriptionFunding{}
	assert.Equal(t, BillingSourceSubscription, s.Source())
}

func TestFunding_WalletPreConsume_NonPositiveNoop(t *testing.T) {
	w := &WalletFunding{userId: 200}
	require.NoError(t, w.PreConsume(0))
	require.NoError(t, w.PreConsume(-10))
	assert.Equal(t, 0, w.consumed)
}

func TestFunding_WalletPreConsume_Deducts(t *testing.T) {
	truncate(t)
	const uid = 201
	seedUser(t, uid, 10000)
	w := &WalletFunding{userId: uid}
	require.NoError(t, w.PreConsume(1500))
	assert.Equal(t, 1500, w.consumed)
	assert.Equal(t, 8500, getUserQuota(t, uid))
}

func TestFunding_WalletSettle_Directions(t *testing.T) {
	truncate(t)
	const uid = 202
	seedUser(t, uid, 10000)
	w := &WalletFunding{userId: uid}

	require.NoError(t, w.Settle(0)) // noop
	assert.Equal(t, 10000, getUserQuota(t, uid))

	require.NoError(t, w.Settle(1000)) // positive => further deduction
	assert.Equal(t, 9000, getUserQuota(t, uid))

	require.NoError(t, w.Settle(-500)) // negative => refund
	assert.Equal(t, 9500, getUserQuota(t, uid))
}

func TestFunding_WalletRefund_NoopWhenNothingConsumed(t *testing.T) {
	truncate(t)
	const uid = 203
	seedUser(t, uid, 10000)
	w := &WalletFunding{userId: uid} // consumed == 0
	require.NoError(t, w.Refund())
	assert.Equal(t, 10000, getUserQuota(t, uid))
}

func TestFunding_WalletRefund_ReturnsConsumed(t *testing.T) {
	truncate(t)
	const uid = 204
	seedUser(t, uid, 10000)
	w := &WalletFunding{userId: uid}
	require.NoError(t, w.PreConsume(2000))
	require.NoError(t, w.Refund())
	assert.Equal(t, 10000, getUserQuota(t, uid), "consumed fully returned")
}

// --- refundWithRetry --------------------------------------------------------

func TestFunding_RefundWithRetry_NilFn(t *testing.T) {
	require.NoError(t, refundWithRetry(nil))
}

func TestFunding_RefundWithRetry_ImmediateSuccess(t *testing.T) {
	calls := 0
	err := refundWithRetry(func() error { calls++; return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "no retries on first success")
}

func TestFunding_RefundWithRetry_EventualSuccess(t *testing.T) {
	calls := 0
	err := refundWithRetry(func() error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestFunding_RefundWithRetry_ExhaustsAndReturnsLastErr(t *testing.T) {
	calls := 0
	want := errors.New("permanent")
	err := refundWithRetry(func() error { calls++; return want })
	require.ErrorIs(t, err, want)
	assert.Equal(t, 3, calls, "max 3 attempts")
}
