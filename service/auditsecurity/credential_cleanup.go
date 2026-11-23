package auditsecurity

import (
	"context"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-redis/redis/v8"
)

func (w *worker) processCredentialCleanup(ctx context.Context, action model.SecurityAction) error {
	if action.Action == "clear_api_keys" && common.RedisEnabled && w.credentialCache == nil && common.RDB != nil {
		options := *common.RDB.Options()
		options.PoolSize, options.MinIdleConns, options.MaxRetries = 1, 0, -1
		options.PoolTimeout, options.DialTimeout = 200*time.Millisecond, 200*time.Millisecond
		options.ReadTimeout, options.WriteTimeout = 200*time.Millisecond, 200*time.Millisecond
		w.credentialCache = redis.NewClient(&options)
	}
	attempted, err := model.ProcessSecurityCredentialCleanup(ctx, w.db, w.credentialCache, action.ID)
	if err == nil {
		return nil
	}
	// A transaction may have committed even when its acknowledgement was lost.
	// Read the atomic receipt before retrying; a completed action never replays.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	defer cancel()
	var receipt model.SecurityAction
	if readErr := w.db.WithContext(readCtx).Select(model.SecurityResponseColumns).Where("id = ?", action.ID).Take(&receipt).Error; readErr != nil {
		return err
	}
	if !slices.Contains([]string{"pending", "clearing", "sync_pending"}, receipt.Outcome) {
		return nil
	}
	// Another worker may already have committed this batch. A late error from
	// its predecessor must not consume the next batch's retry budget.
	if receipt.Result != attempted.Result || receipt.Outcome != attempted.Outcome || receipt.Attempts != attempted.Attempts {
		return nil
	}
	outcome, reason := receipt.Outcome, "sync_pending"
	if receipt.Attempts >= 2 {
		outcome, reason = "failed", "cleanup_failed"
		var result model.SecurityResponseResult
		if common.UnmarshalJsonStr(receipt.Result, &result) == nil && result.StartedAt > 0 {
			outcome = "partial"
		}
	}
	if finishErr := model.FinishSecurityAccountResponse(readCtx, w.db, receipt, outcome, reason); finishErr != nil {
		return finishErr
	}
	common.SysError("audit credential cleanup is awaiting recovery")
	return nil
}
