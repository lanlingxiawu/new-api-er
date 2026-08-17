package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyChannelVeridropDetectionIndex struct {
	ID        int64 `gorm:"primaryKey"`
	UpdatedAt int64 `gorm:"index:idx_veridrop_updated_id"`
}

func (legacyChannelVeridropDetectionIndex) TableName() string {
	return "channel_veridrop_detections"
}

func TestCleanupChannelVeridropDetectionsBatchPreservesActiveRows(t *testing.T) {
	// The all-records cleanup branch must not run against the shared project DB.
	// This isolated SQLite database exercises the same portable GORM operations.
	testDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&ChannelVeridropDetection{}))
	prefix := fmt.Sprintf("cleanup-%d-", nextTestID())
	cutoff := int64(2_000_000_000)
	tx := testDB.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	rows := []*ChannelVeridropDetection{
		{ChannelID: nextTestID(), ChannelName: prefix + "old-done", Status: ChannelVeridropDetectionDone},
		{ChannelID: nextTestID(), ChannelName: prefix + "old-skipped", Status: ChannelVeridropDetectionSkipped},
		{ChannelID: nextTestID(), ChannelName: prefix + "boundary", Status: ChannelVeridropDetectionError},
		{ChannelID: nextTestID(), ChannelName: prefix + "new-terminal", Status: ChannelVeridropDetectionTimeout},
		{ChannelID: nextTestID(), ChannelName: prefix + "old-queued", Status: ChannelVeridropDetectionQueued},
		{ChannelID: nextTestID(), ChannelName: prefix + "old-running", Status: ChannelVeridropDetectionRunning},
		{ChannelID: nextTestID(), ChannelName: prefix + "active-batch-done", Status: ChannelVeridropDetectionDone, BatchTaskID: "systask_active_cleanup_batch"},
	}
	require.NoError(t, tx.Create(&rows).Error)
	timestamps := []int64{cutoff - 2, cutoff - 1, cutoff, cutoff + 1, cutoff - 2, cutoff - 2, cutoff - 3}
	ids := make([]int64, 0, len(rows))
	for index, row := range rows {
		ids = append(ids, row.ID)
		require.NoError(t, tx.Model(&ChannelVeridropDetection{}).
			Where("id = ?", row.ID).
			UpdateColumn("updated_at", timestamps[index]).Error)
	}

	deleted, err := cleanupChannelVeridropDetectionsBatch(context.Background(), tx, cutoff, 2, []string{"systask_active_cleanup_batch"})
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)

	var remaining []*ChannelVeridropDetection
	require.NoError(t, tx.Where("id IN ?", ids).Order("id asc").Find(&remaining).Error)
	remainingNames := make([]string, 0, len(remaining))
	for _, row := range remaining {
		remainingNames = append(remainingNames, row.ChannelName)
	}
	require.NotContains(t, remainingNames, prefix+"old-done")
	require.NotContains(t, remainingNames, prefix+"old-skipped")
	require.Contains(t, remainingNames, prefix+"boundary")
	require.Contains(t, remainingNames, prefix+"new-terminal")
	require.Contains(t, remainingNames, prefix+"old-queued")
	require.Contains(t, remainingNames, prefix+"old-running")
	require.Contains(t, remainingNames, prefix+"active-batch-done")

	deleted, err = cleanupChannelVeridropDetectionsBatch(context.Background(), tx, cutoff+2, 10000, []string{"systask_active_cleanup_batch"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, int64(2))

	var activeCount int64
	require.NoError(t, tx.Model(&ChannelVeridropDetection{}).
		Where("id IN ?", []int64{rows[4].ID, rows[5].ID}).
		Count(&activeCount).Error)
	require.EqualValues(t, 2, activeCount)
	var protectedCount int64
	require.NoError(t, tx.Model(&ChannelVeridropDetection{}).Where("id = ?", rows[6].ID).Count(&protectedCount).Error)
	require.EqualValues(t, 1, protectedCount)
}

func TestListChannelVeridropDetectionsFiltersAndPages(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ChannelVeridropDetection{}))

	baseChannelID := 880000000 + int(time.Now().UnixNano()%1000000)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id >= ? AND channel_id < ?", baseChannelID, baseChannelID+10).Delete(&ChannelVeridropDetection{}).Error)
	})

	now := common.GetTimestamp()
	outcomeChannelID := baseChannelID + 2
	outcomeChannelName := fmt.Sprintf("vd-outcomes-%d", baseChannelID)
	rows := []*ChannelVeridropDetection{
		{ChannelID: baseChannelID, ChannelName: "vd-a", Protocol: "openai", Model: "gpt-5", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 90, Verdict: "pass", Summary: "healthy upstream", UpdatedAt: now},
		{ChannelID: baseChannelID, ChannelName: "vd-a", Protocol: "openai", Model: "gpt-4o", Mode: "standard", Status: ChannelVeridropDetectionError, Error: "failed", UpdatedAt: now - 86400*3},
		{ChannelID: baseChannelID + 1, ChannelName: "vd-b", Protocol: "anthropic", Model: "claude", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 80, Verdict: "usable", Summary: "slow but usable", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "model-keyword-needle", Mode: "quick", Status: ChannelVeridropDetectionQueued, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "running-model", Mode: "quick", Status: ChannelVeridropDetectionRunning, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "passed-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 88.3, Verdict: "success", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "completed-marginal-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 88.3, Verdict: "marginal", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "marginal-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 63.3, Verdict: "marginal", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "threshold-marginal-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 50, Verdict: "marginal", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "below-threshold-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 49.9, Verdict: "marginal", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "failed-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 0, Verdict: "failed", UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "legacy-unscored-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 0, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "error-model", Mode: "quick", Status: ChannelVeridropDetectionError, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "timeout-model", Mode: "quick", Status: ChannelVeridropDetectionTimeout, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "cancelled-model", Mode: "quick", Status: ChannelVeridropDetectionCancelled, UpdatedAt: now},
		{ChannelID: outcomeChannelID, ChannelName: outcomeChannelName, Protocol: "openai", Model: "skipped-model", Mode: "quick", Status: ChannelVeridropDetectionSkipped, UpdatedAt: now},
		{ChannelID: baseChannelID + 3, ChannelName: "vd-sort-bravo", Protocol: "openai", Model: "zeta-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 40, Verdict: "marginal", UpdatedAt: now - 20},
		{ChannelID: baseChannelID + 4, ChannelName: "vd-sort-alpha", Protocol: "openai", Model: "alpha-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 90, Verdict: "passed", UpdatedAt: now - 10},
		{ChannelID: baseChannelID + 5, ChannelName: "vd-sort-charlie", Protocol: "openai", Model: "mu-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 70, Verdict: "passed", UpdatedAt: now - 30},
		{ChannelID: baseChannelID + 6, ChannelName: "vd-sort-delta", Protocol: "openai", Model: "beta-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 40, Verdict: "marginal", UpdatedAt: now - 20},
		{ChannelID: baseChannelID + 7, ChannelName: "vd-older-batch", Protocol: "openai", Model: "older-batch-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 90, Verdict: "passed", BatchTaskID: "systask_older_batch", UpdatedAt: now - 5},
		{ChannelID: baseChannelID + 8, ChannelName: "vd-latest-batch", Protocol: "openai", Model: "latest-batch-model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 90, Verdict: "passed", BatchTaskID: "systask_latest_batch", UpdatedAt: now - 4},
		{ChannelID: baseChannelID + 8, ChannelName: "vd-latest-batch", Protocol: "openai", Model: "latest-batch-second-model", Mode: "quick", Status: ChannelVeridropDetectionError, BatchTaskID: "systask_latest_batch", UpdatedAt: now - 3},
		{ChannelID: baseChannelID + 9, ChannelName: "vd_literal_target", Protocol: "openai", Model: "literal_model", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 80, Verdict: "usable", UpdatedAt: now},
		{ChannelID: baseChannelID + 9, ChannelName: "vdXliteralXtarget", Protocol: "openai", Model: "literalXmodel", Mode: "quick", Status: ChannelVeridropDetectionDone, Score: 80, Verdict: "usable", UpdatedAt: now},
	}
	for _, row := range rows {
		require.NoError(t, CreateChannelVeridropDetection(row))
	}

	latestBatchID, err := GetLatestChannelVeridropDetectionBatchTaskID()
	require.NoError(t, err)
	require.Equal(t, "systask_latest_batch", latestBatchID)

	latestBatch, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		BatchTaskID: latestBatchID,
		Limit:       10,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"latest-batch-model", "latest-batch-second-model"}, []string{
		latestBatch[0].Model,
		latestBatch[1].Model,
	})

	latestBatchFailed, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		BatchTaskID: latestBatchID,
		Outcome:     "failed",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Len(t, latestBatchFailed, 1)
	require.Equal(t, "latest-batch-second-model", latestBatchFailed[0].Model)

	got, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		Protocol:  "openai",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "gpt-5", got[0].Model)
	require.Equal(t, "gpt-4o", got[1].Model)

	done, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		Status:    string(ChannelVeridropDetectionDone),
		BeforeID:  got[1].ID,
		Limit:     1,
		SortBy:    "score",
		SortOrder: "asc",
	})
	require.NoError(t, err)
	require.Len(t, done, 1)
	require.Less(t, done[0].ID, got[1].ID)

	minScore := 85.0
	maxScore := 95.0
	ranged, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName:  "vd-a",
		Model:        "gpt",
		Mode:         "quick",
		Verdict:      "pass",
		Keyword:      "healthy",
		MinScore:     &minScore,
		MaxScore:     &maxScore,
		UpdatedAfter: now - 3600,
		Limit:        10,
	})
	require.NoError(t, err)
	require.Len(t, ranged, 1)
	require.Equal(t, "gpt-5", ranged[0].Model)

	withError, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: baseChannelID,
		ErrorOnly: true,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, withError, 1)
	require.Equal(t, "gpt-4o", withError[0].Model)

	inProgress, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "in_progress",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, inProgress, 2)
	require.ElementsMatch(t, []ChannelVeridropDetectionStatus{
		ChannelVeridropDetectionQueued,
		ChannelVeridropDetectionRunning,
	}, []ChannelVeridropDetectionStatus{inProgress[0].Status, inProgress[1].Status})

	passed, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "passed",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, passed, 1)
	require.Equal(t, "passed-model", passed[0].Model)

	passedAndCompleted, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcomes: []string{
			string(ChannelVeridropOutcomePassed),
			string(ChannelVeridropOutcomeCompleted),
		},
		Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, passedAndCompleted, 5)
	require.ElementsMatch(t, []string{
		"passed-model",
		"completed-marginal-model",
		"marginal-model",
		"threshold-marginal-model",
		"legacy-unscored-model",
	}, []string{
		passedAndCompleted[0].Model,
		passedAndCompleted[1].Model,
		passedAndCompleted[2].Model,
		passedAndCompleted[3].Model,
		passedAndCompleted[4].Model,
	})

	completed, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "completed",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, completed, 4)
	require.ElementsMatch(t, []string{"completed-marginal-model", "marginal-model", "threshold-marginal-model", "legacy-unscored-model"}, []string{
		completed[0].Model,
		completed[1].Model,
		completed[2].Model,
		completed[3].Model,
	})

	literalChannel, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd_literal_target",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Len(t, literalChannel, 1)
	require.Equal(t, "vd_literal_target", literalChannel[0].ChannelName)

	literalModel, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		Model: "literal_model",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, literalModel, 1)
	require.Equal(t, "literal_model", literalModel[0].Model)

	failed, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "failed",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, failed, 3)
	require.ElementsMatch(t, []string{"failed-model", "error-model", "timeout-model"}, []string{
		failed[0].Model,
		failed[1].Model,
		failed[2].Model,
	})

	lowScore, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "low_score",
		Limit:     20,
	})
	require.NoError(t, err)
	require.Len(t, lowScore, 1)
	require.Equal(t, "below-threshold-model", lowScore[0].Model)

	skipped, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: outcomeChannelID,
		Outcome:   "skipped",
		Limit:     20,
	})
	require.NoError(t, err)
	require.Len(t, skipped, 1)
	require.Equal(t, "skipped-model", skipped[0].Model)

	channelKeyword, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		Keyword: outcomeChannelName,
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, channelKeyword, 10)

	modelKeyword, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		Keyword: "model-keyword-needle",
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, modelKeyword, 1)
	require.Equal(t, outcomeChannelID, modelKeyword[0].ChannelID)

	scoreAsc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "score",
		SortOrder:   "asc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"beta-model", "zeta-model", "mu-model", "alpha-model"}, []string{
		scoreAsc[0].Model, scoreAsc[1].Model, scoreAsc[2].Model, scoreAsc[3].Model,
	})

	scoreDesc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "score",
		SortOrder:   "desc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha-model", "mu-model", "beta-model", "zeta-model"}, []string{
		scoreDesc[0].Model, scoreDesc[1].Model, scoreDesc[2].Model, scoreDesc[3].Model,
	})

	channelAsc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "channel_name",
		SortOrder:   "asc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vd-sort-alpha", "vd-sort-bravo", "vd-sort-charlie", "vd-sort-delta"}, []string{
		channelAsc[0].ChannelName, channelAsc[1].ChannelName, channelAsc[2].ChannelName, channelAsc[3].ChannelName,
	})

	channelDesc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "channel_name",
		SortOrder:   "desc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vd-sort-delta", "vd-sort-charlie", "vd-sort-bravo", "vd-sort-alpha"}, []string{
		channelDesc[0].ChannelName, channelDesc[1].ChannelName, channelDesc[2].ChannelName, channelDesc[3].ChannelName,
	})

	modelAsc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "model",
		SortOrder:   "asc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha-model", "beta-model", "mu-model", "zeta-model"}, []string{
		modelAsc[0].Model, modelAsc[1].Model, modelAsc[2].Model, modelAsc[3].Model,
	})

	modelDesc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "model",
		SortOrder:   "desc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"zeta-model", "mu-model", "beta-model", "alpha-model"}, []string{
		modelDesc[0].Model, modelDesc[1].Model, modelDesc[2].Model, modelDesc[3].Model,
	})

	updatedAsc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "updated_at",
		SortOrder:   "asc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mu-model", "beta-model", "zeta-model", "alpha-model"}, []string{
		updatedAsc[0].Model, updatedAsc[1].Model, updatedAsc[2].Model, updatedAsc[3].Model,
	})

	updatedDesc, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "updated_at",
		SortOrder:   "desc",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha-model", "beta-model", "zeta-model", "mu-model"}, []string{
		updatedDesc[0].Model, updatedDesc[1].Model, updatedDesc[2].Model, updatedDesc[3].Model,
	})

	defaultSort, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelName: "vd-sort-",
		SortBy:      "updated_at; DROP TABLE channel_veridrop_detections",
		SortOrder:   "sideways",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha-model", "beta-model", "zeta-model", "mu-model"}, []string{
		defaultSort[0].Model, defaultSort[1].Model, defaultSort[2].Model, defaultSort[3].Model,
	})

	capped, err := ListChannelVeridropDetections(ChannelVeridropDetectionListOptions{Limit: 500})
	require.NoError(t, err)
	require.LessOrEqual(t, len(capped), 100)
}

func TestSummarizeChannelVeridropDetectionsUsesCompleteFilteredSet(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ChannelVeridropDetection{}))
	channelID := nextTestID()
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id = ?", channelID).Delete(&ChannelVeridropDetection{}).Error)
	})

	now := common.GetTimestamp()
	rows := []*ChannelVeridropDetection{
		{ChannelID: channelID, ChannelName: "summary-target", Model: "queued", Mode: "quick", Status: ChannelVeridropDetectionQueued, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "running", Mode: "quick", Status: ChannelVeridropDetectionRunning, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "passed", Mode: "quick", Status: ChannelVeridropDetectionDone, Verdict: "success", Score: 90, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "boundary", Mode: "quick", Status: ChannelVeridropDetectionDone, Verdict: "marginal", Score: 50, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "legacy", Mode: "quick", Status: ChannelVeridropDetectionDone, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "low", Mode: "quick", Status: ChannelVeridropDetectionDone, Verdict: "marginal", Score: 49.9, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "failed-verdict", Mode: "quick", Status: ChannelVeridropDetectionDone, Verdict: "fail", UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "error", Mode: "quick", Status: ChannelVeridropDetectionError, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "timeout", Mode: "quick", Status: ChannelVeridropDetectionTimeout, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "skipped", Mode: "quick", Status: ChannelVeridropDetectionSkipped, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "cancelled", Mode: "quick", Status: ChannelVeridropDetectionCancelled, UpdatedAt: now},
		{ChannelID: channelID, ChannelName: "summary-target", Model: "other-mode", Mode: "full", Status: ChannelVeridropDetectionDone, Verdict: "passed", Score: 90, UpdatedAt: now},
	}
	require.NoError(t, DB.Create(&rows).Error)

	summary, matchingCount, err := SummarizeChannelVeridropDetections(ChannelVeridropDetectionListOptions{
		ChannelID: channelID,
		Mode:      "quick",
		Outcomes: []string{
			string(ChannelVeridropOutcomeFailed),
			string(ChannelVeridropOutcomeSkipped),
		},
		Limit: 1,
	})
	require.NoError(t, err)
	require.Equal(t, ChannelVeridropDetectionSummary{
		Total:      11,
		InProgress: 2,
		Passed:     1,
		Completed:  2,
		LowScore:   1,
		Failed:     3,
		Skipped:    1,
		Cancelled:  1,
	}, summary)
	require.EqualValues(t, 4, matchingCount)
}

func TestClassifyChannelVeridropDetectionOutcome(t *testing.T) {
	tests := []struct {
		name string
		row  ChannelVeridropDetection
		want ChannelVeridropDetectionOutcome
	}{
		{name: "queued", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionQueued}, want: ChannelVeridropOutcomeInProgress},
		{name: "running", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionRunning}, want: ChannelVeridropOutcomeInProgress},
		{name: "passed alias", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionDone, Verdict: " SUCCESS "}, want: ChannelVeridropOutcomePassed},
		{name: "marginal below threshold", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionDone, Verdict: "marginal", Score: 49.9}, want: ChannelVeridropOutcomeLowScore},
		{name: "marginal boundary", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionDone, Verdict: "marginal", Score: 50}, want: ChannelVeridropOutcomeCompleted},
		{name: "failed verdict", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionDone, Verdict: "fail"}, want: ChannelVeridropOutcomeFailed},
		{name: "error status", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionError}, want: ChannelVeridropOutcomeFailed},
		{name: "timeout status", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionTimeout}, want: ChannelVeridropOutcomeFailed},
		{name: "skipped", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionSkipped}, want: ChannelVeridropOutcomeSkipped},
		{name: "cancelled", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionCancelled}, want: ChannelVeridropOutcomeCancelled},
		{name: "legacy done", row: ChannelVeridropDetection{Status: ChannelVeridropDetectionDone}, want: ChannelVeridropOutcomeCompleted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, ClassifyChannelVeridropDetectionOutcome(&test.row))
		})
	}
}

func TestGetChannelVeridropDetectionBatchTaskIDByID(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ChannelVeridropDetection{}))
	row := &ChannelVeridropDetection{ChannelID: nextTestID(), Status: ChannelVeridropDetectionDone, BatchTaskID: "systask_pinned_batch"}
	require.NoError(t, CreateChannelVeridropDetection(row))
	t.Cleanup(func() { _ = DB.Where("id = ?", row.ID).Delete(&ChannelVeridropDetection{}).Error })
	batchTaskID, err := GetChannelVeridropDetectionBatchTaskIDByID(row.ID)
	require.NoError(t, err)
	require.Equal(t, row.BatchTaskID, batchTaskID)
	_, err = GetChannelVeridropDetectionBatchTaskIDByID(row.ID + 1000000000)
	require.ErrorIs(t, err, ErrInvalidChannelVeridropBatchCursor)
}

func TestMigrateChannelVeridropUpdatedIndexReplacesLegacyIndex(t *testing.T) {
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&legacyChannelVeridropDetectionIndex{}))
	require.True(t, testDB.Migrator().HasIndex(&legacyChannelVeridropDetectionIndex{}, "idx_veridrop_updated_id"))
	require.NoError(t, testDB.AutoMigrate(&ChannelVeridropDetection{}))
	require.NoError(t, migrateChannelVeridropUpdatedIndex(testDB))
	require.NoError(t, migrateChannelVeridropUpdatedIndex(testDB))
	require.False(t, testDB.Migrator().HasIndex(&ChannelVeridropDetection{}, "idx_veridrop_updated_id"))
	require.True(t, testDB.Migrator().HasIndex(&ChannelVeridropDetection{}, "idx_veridrop_updated_id_v2"))
}

func TestFindEnabledChannelsForVeridropAfterID(t *testing.T) {
	baseID := 881000000 + int(time.Now().UnixNano()%1000000)
	ids := []int{baseID, baseID + 1, baseID + 2}
	t.Cleanup(func() {
		require.NoError(t, DB.Where("id IN ?", ids).Delete(&Channel{}).Error)
	})

	channels := []*Channel{
		{Id: ids[0], Name: fmt.Sprintf("veridrop-enabled-a-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-a", Status: common.ChannelStatusEnabled, Models: "gpt-5", Group: "default"},
		{Id: ids[1], Name: fmt.Sprintf("veridrop-disabled-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-b", Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4o", Group: "default"},
		{Id: ids[2], Name: fmt.Sprintf("veridrop-enabled-b-%d", baseID), Type: constant.ChannelTypeAnthropic, Key: "sk-c", Status: common.ChannelStatusEnabled, Models: "claude", Group: "default"},
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
	}

	got, err := FindEnabledChannelsForVeridropAfterID(baseID-1, 10, nil)
	require.NoError(t, err)
	gotIDs := make([]int, 0, len(got))
	for _, channel := range got {
		if channel.Id >= baseID && channel.Id <= baseID+2 {
			gotIDs = append(gotIDs, channel.Id)
		}
	}
	require.Equal(t, []int{ids[0], ids[2]}, gotIDs)

	filtered, err := FindEnabledChannelsForVeridropAfterID(baseID-1, 10, []int{ids[2]})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, ids[2], filtered[0].Id)
}

func TestFindChannelsForVeridropAfterIDCanIncludeDisabledChannels(t *testing.T) {
	baseID := 883000000 + int(time.Now().UnixNano()%1000000)
	ids := []int{baseID, baseID + 1, baseID + 2}
	t.Cleanup(func() {
		require.NoError(t, DB.Where("id IN ?", ids).Delete(&Channel{}).Error)
	})

	channels := []*Channel{
		{Id: ids[0], Name: fmt.Sprintf("veridrop-all-enabled-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-a", Status: common.ChannelStatusEnabled, Models: "gpt-5", Group: "default"},
		{Id: ids[1], Name: fmt.Sprintf("veridrop-all-disabled-%d", baseID), Type: constant.ChannelTypeOpenAI, Key: "sk-b", Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4o", Group: "default"},
		{Id: ids[2], Name: fmt.Sprintf("veridrop-all-auto-disabled-%d", baseID), Type: constant.ChannelTypeAnthropic, Key: "sk-c", Status: common.ChannelStatusAutoDisabled, Models: "claude", Group: "default"},
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
	}

	got, err := FindChannelsForVeridropAfterID(baseID-1, 10, nil, true)
	require.NoError(t, err)
	require.Equal(t, ids, []int{got[0].Id, got[1].Id, got[2].Id})

	filtered, err := FindChannelsForVeridropAfterID(baseID-1, 10, []int{ids[1]}, true)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, common.ChannelStatusManuallyDisabled, filtered[0].Status)

	enabledOnly, err := FindChannelsForVeridropAfterID(baseID-1, 10, ids, false)
	require.NoError(t, err)
	require.Len(t, enabledOnly, 1)
	require.Equal(t, ids[0], enabledOnly[0].Id)
}
