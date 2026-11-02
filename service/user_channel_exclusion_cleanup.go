package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type userChannelExclusionCleanupHandler struct{}

func init() { RegisterSystemTaskHandler(userChannelExclusionCleanupHandler{}) }

func (userChannelExclusionCleanupHandler) Type() string {
	return model.SystemTaskTypeUserChannelExclusionCleanup
}
func (userChannelExclusionCleanupHandler) Interval() time.Duration { return 15 * time.Second }
func (userChannelExclusionCleanupHandler) NewPayload() any         { return nil }
func (userChannelExclusionCleanupHandler) Enabled() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := model.DueUserChannelExclusionCleanups(ctx)
	if err != nil {
		common.SysError(common.LogText("user channel exclusion cleanup scan failed: %v", err))
		return false
	}
	return len(rows) > 0
}

func (userChannelExclusionCleanupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	completed := 0
	deferred := 0
	lastFailure := ""
	var runErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(common.LogText("user channel exclusion cleanup panic: %v", recovered))
			deferred++
			lastFailure = "panic"
		}
		status := model.SystemTaskStatusSucceeded
		if runErr != nil || deferred > 0 {
			status = model.SystemTaskStatusFailed
		}
		err := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]any{"processed_users": completed, "deferred_channels": deferred, "last_failure_phase": lastFailure}, lastFailure)
		if err != nil {
			common.SysError(common.LogText("user channel exclusion cleanup finish failed: %v", err))
		}
	}()
	step, cancel := context.WithTimeout(ctx, time.Second)
	rows, err := model.DueUserChannelExclusionCleanups(step)
	cancel()
	if err != nil {
		runErr = err
		lastFailure = "scan"
		common.SysError(common.LogText("user channel exclusion cleanup scan failed: %v", err))
		return
	}
	for _, row := range rows {
		// Bound each run; pending state survives cancellation and lease expiry.
		for range 100 {
			if ctx.Err() != nil {
				runErr = ctx.Err()
				lastFailure = "cancelled"
				return
			}
			step, cancel := context.WithTimeout(ctx, time.Second)
			userID, err := model.PrepareUserChannelExclusionCleanup(step, row.ChannelID, task.TaskID, runnerID)
			cancel()
			reason := "database"
			if err == nil && userID == 0 {
				break
			}
			if err == nil {
				step, cancel = context.WithTimeout(ctx, time.Second)
				err = model.PublishUserChannelExclusionCleanup(step, userID)
				cancel()
				reason = "cache_publish"
			}
			if err == nil {
				step, cancel = context.WithTimeout(ctx, time.Second)
				err = model.AcknowledgeUserChannelExclusionCleanup(step, row.ChannelID, userID, task.TaskID, runnerID)
				cancel()
				reason = "acknowledge"
			}
			if err != nil {
				lastFailure = reason
				common.SysError(common.LogText("user channel exclusion cleanup failed: channel=%d phase=%s error=%v", row.ChannelID, reason, err))
				step, cancel = context.WithTimeout(ctx, time.Second)
				deferErr := model.DeferUserChannelExclusionCleanup(step, row.ChannelID, reason, task.TaskID, runnerID)
				cancel()
				if deferErr != nil {
					runErr = deferErr
					return
				}
				deferred++
				break
			}
			completed++
			// Deliberately slower than relay: at most 20 users/s from its pool.
			select {
			case <-ctx.Done():
				runErr = ctx.Err()
				lastFailure = "cancelled"
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}
