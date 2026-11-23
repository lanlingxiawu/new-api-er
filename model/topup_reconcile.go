package model

import (
	"context"
	"errors"
)

var ErrTopUpVerificationChanged = errors.New("verified topup changed")

type TopUpCompletion struct {
	UserID           int  `json:"user_id"`
	QuotaAdded       int  `json:"quota_added"`
	AlreadyCompleted bool `json:"already_completed"`
}

func ManualCompleteTopUp(tradeNo string, callerIP string) (int, error) {
	result, err := completeTopUp(context.Background(), tradeNo, callerIP, nil)
	return result.UserID, err
}

// CompleteVerifiedTopUp is called only after a server-side provider check.
// The exact checked order is revalidated under the completion transaction lock.
func CompleteVerifiedTopUp(ctx context.Context, checked TopUp, callerIP string) (TopUpCompletion, error) {
	return completeTopUp(ctx, checked.TradeNo, callerIP, &checked)
}
