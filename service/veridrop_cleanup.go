package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const veridropCleanupBatchSize = 500

var ErrInvalidVeridropCleanupRetention = errors.New("invalid veridrop cleanup retention days")

type VeridropDetectionCleanupPayload struct {
	RetentionDays   int   `json:"retention_days"`
	TargetTimestamp int64 `json:"target_timestamp"`
	BatchSize       int   `json:"batch_size"`
}

type VeridropDetectionCleanupResult struct {
	DeletedCount int64 `json:"deleted_count"`
}

func StartVeridropDetectionCleanupTask(retentionDays int) (*model.SystemTask, bool, error) {
	payload, err := buildVeridropDetectionCleanupPayload(retentionDays, common.GetTimestamp())
	if err != nil {
		return nil, false, err
	}
	return EnqueueSystemTask(model.SystemTaskTypeVeridropCleanup, payload)
}

func buildVeridropDetectionCleanupPayload(retentionDays int, now int64) (VeridropDetectionCleanupPayload, error) {
	if retentionDays < 0 || retentionDays > 3650 {
		return VeridropDetectionCleanupPayload{}, ErrInvalidVeridropCleanupRetention
	}
	targetTimestamp := now
	if retentionDays > 0 {
		targetTimestamp = now - int64(retentionDays)*int64((24*time.Hour)/time.Second)
	}
	return VeridropDetectionCleanupPayload{
		RetentionDays:   retentionDays,
		TargetTimestamp: targetTimestamp,
		BatchSize:       veridropCleanupBatchSize,
	}, nil
}

func RunVeridropDetectionCleanupTask(
	ctx context.Context,
	payload VeridropDetectionCleanupPayload,
	report func(processed, total int),
) (VeridropDetectionCleanupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if payload.RetentionDays < 0 || payload.RetentionDays > 3650 || payload.TargetTimestamp < 0 {
		return VeridropDetectionCleanupResult{}, ErrInvalidVeridropCleanupRetention
	}
	if payload.BatchSize <= 0 || payload.BatchSize > 1000 {
		payload.BatchSize = veridropCleanupBatchSize
	}
	if payload.TargetTimestamp == 0 {
		payload.TargetTimestamp = common.GetTimestamp()
	}
	excludedBatchTaskIDs := make([]string, 0, 1)
	activeBatch, err := model.GetActiveSystemTask(model.SystemTaskTypeVeridrop)
	if err != nil {
		return VeridropDetectionCleanupResult{}, err
	}
	if activeBatch != nil {
		excludedBatchTaskIDs = append(excludedBatchTaskIDs, activeBatch.TaskID)
	}

	total, err := model.CountChannelVeridropDetectionsForCleanup(ctx, payload.TargetTimestamp, excludedBatchTaskIDs)
	if err != nil {
		return VeridropDetectionCleanupResult{}, err
	}
	if report != nil {
		report(0, int(total))
	}

	result := VeridropDetectionCleanupResult{}
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		deleted, err := model.CleanupChannelVeridropDetectionsBatch(ctx, payload.TargetTimestamp, payload.BatchSize, excludedBatchTaskIDs)
		if err != nil {
			return result, err
		}
		if deleted == 0 {
			return result, nil
		}
		result.DeletedCount += deleted
		if report != nil {
			report(int(result.DeletedCount), int(total))
		}
	}
}
