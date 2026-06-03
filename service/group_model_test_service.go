package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const groupModelRecentSuccessWindow = time.Minute

// GroupModelActiveProbeFunc performs a real channel/model probe for a group.
type GroupModelActiveProbeFunc func(ctx context.Context, userGroup string, channel *model.Channel, modelName string) error

// GroupModelActiveProbe is registered by controller/channel-test.go to avoid a service -> controller import cycle.
var GroupModelActiveProbe GroupModelActiveProbeFunc

// TestGroupModelAvailability tests model availability for every model in a group.
func TestGroupModelAvailability(ctx context.Context, userGroup string) error {
	channels, err := model.GetChannelsByGroup(userGroup)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to get channels for group %s: %v", userGroup, err))
		return err
	}

	if len(channels) == 0 {
		return updateGroupStatus(ctx, userGroup, 0, 0)
	}

	modelSet := make(map[string]bool)
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		for _, modelName := range ch.GetModels() {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" {
				modelSet[modelName] = true
			}
		}
	}

	totalModels := len(modelSet)
	if totalModels == 0 {
		return updateGroupStatus(ctx, userGroup, 0, 0)
	}

	availableModels := 0
	for modelName := range modelSet {
		available := testModelAvailabilityViaChannel(ctx, userGroup, modelName, channels)
		if available {
			availableModels++
		}
		if err := updateModelStatus(ctx, userGroup, modelName, available); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to update model %s status: %v", modelName, err))
		}
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"[model-test] Group %s model availability: %d/%d available",
		userGroup, availableModels, totalModels,
	))

	return updateGroupStatus(ctx, userGroup, availableModels, totalModels)
}

// testModelAvailabilityViaChannel combines recent real traffic and active model probes.
func testModelAvailabilityViaChannel(ctx context.Context, userGroup, modelName string, channels []*model.Channel) bool {
	supportingChannels := make([]*model.Channel, 0)

	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		for _, m := range channel.GetModels() {
			if strings.TrimSpace(m) == modelName {
				supportingChannels = append(supportingChannels, channel)
				break
			}
		}
	}

	if len(supportingChannels) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"[model-test] model %s in group %s is not supported by any enabled channel",
			modelName, userGroup,
		))
		return false
	}

	for _, channel := range supportingChannels {
		if hasRecentSuccessfulRequest(ctx, userGroup, channel.Id, modelName) {
			logger.LogInfo(ctx, fmt.Sprintf(
				"[model-test] model %s in group %s is available (successful request in channel %d within %s)",
				modelName, userGroup, channel.Id, groupModelRecentSuccessWindow,
			))
			return true
		}
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"[model-test] no recent requests for model %s in group %s, running active probe",
		modelName, userGroup,
	))

	for _, channel := range supportingChannels {
		if probeModelViaChannel(ctx, userGroup, channel, modelName) {
			logger.LogInfo(ctx, fmt.Sprintf(
				"[model-test] model %s in group %s is available (active probe passed on channel %d)",
				modelName, userGroup, channel.Id,
			))
			return true
		}
	}

	logger.LogWarn(ctx, fmt.Sprintf(
		"[model-test] model %s in group %s failed recent history and active probes",
		modelName, userGroup,
	))
	return false
}

func probeModelViaChannel(ctx context.Context, userGroup string, channel *model.Channel, modelName string) bool {
	if channel == nil {
		return false
	}
	if GroupModelActiveProbe == nil {
		logger.LogWarn(ctx, "[model-test] active probe is not registered")
		return false
	}
	if err := GroupModelActiveProbe(ctx, userGroup, channel, modelName); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf(
			"[model-test] active probe failed for group %s channel %d model %s: %v",
			userGroup, channel.Id, modelName, err,
		))
		return false
	}
	return true
}

func hasRecentSuccessfulRequest(ctx context.Context, userGroup string, channelID int, modelName string) bool {
	windowStart := time.Now().Add(-groupModelRecentSuccessWindow).Unix()

	var count int64
	err := model.LOG_DB.Model(&model.Log{}).
		Where(&model.Log{
			ChannelId: channelID,
			ModelName: modelName,
			Group:     userGroup,
			Type:      model.LogTypeConsume,
		}).
		Where("created_at >= ?", windowStart).
		Count(&count).Error

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf(
			"[model-test] failed to check recent requests for group %s channel %d model %s: %v",
			userGroup, channelID, modelName, err,
		))
		return false
	}

	if count == 0 {
		logger.LogDebug(ctx, fmt.Sprintf(
			"[model-test] no recent requests for group %s channel %d model %s within %s",
			userGroup, channelID, modelName, groupModelRecentSuccessWindow,
		))
		return false
	}
	return true
}

func updateGroupStatus(ctx context.Context, userGroup string, availableModels, totalModels int) error {
	now := time.Now().Unix()

	gs := &model.GroupStatus{
		UserGroup:       userGroup,
		AvailableModels: availableModels,
		TotalModels:     totalModels,
		LastTestTime:    now,
		UpdatedAt:       now,
	}

	existing, err := model.GetGroupStatus(userGroup)
	if err == nil && existing.ID != 0 {
		gs.ID = existing.ID
		gs.CreatedAt = existing.CreatedAt
	} else {
		gs.CreatedAt = now
	}

	return model.UpsertGroupStatus(gs)
}

func updateModelStatus(ctx context.Context, userGroup, modelName string, available bool) error {
	now := time.Now().Unix()

	ms := &model.GroupModelStatus{
		UserGroup:    userGroup,
		ModelName:    modelName,
		Available:    available,
		LastTestTime: now,
		UpdatedAt:    now,
	}

	existing, err := model.GetGroupModelStatus(userGroup, modelName)
	if err == nil && existing.ID != 0 {
		ms.ID = existing.ID
		ms.CreatedAt = existing.CreatedAt
	} else {
		ms.CreatedAt = now
	}

	return model.UpsertGroupModelStatus(ms)
}

// RunGroupModelAvailabilityTest runs model availability checks for all known groups.
func RunGroupModelAvailabilityTest(ctx context.Context) error {
	groups, err := model.GetAllGroupStatuses()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to get all groups: %v", err))
		return err
	}

	if len(groups) == 0 {
		groups, err = getGroupsFromChannels()
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to get groups from channels: %v", err))
			return err
		}
	}

	var errors []error
	for _, g := range groups {
		if err := TestGroupModelAvailability(ctx, g.UserGroup); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to test group %s: %v", g.UserGroup, err))
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] %d groups failed testing", len(errors)))
	}

	if err := rebuildGlobalModelStatusesFromGroupAvailability(ctx); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to rebuild global model statuses: %v", err))
		if len(errors) == 0 {
			return err
		}
	}

	return nil
}

func rebuildGlobalModelStatusesFromGroupAvailability(ctx context.Context) error {
	statuses, err := model.GetAllGroupModelStatuses()
	if err != nil {
		return err
	}

	type modelAggregate struct {
		total     int
		available int
		lastTest  int64
	}
	aggregates := make(map[string]modelAggregate)
	for _, status := range statuses {
		modelName := strings.TrimSpace(status.ModelName)
		if modelName == "" {
			continue
		}
		agg := aggregates[modelName]
		agg.total++
		if status.Available {
			agg.available++
		}
		if status.LastTestTime > agg.lastTest {
			agg.lastTest = status.LastTestTime
		}
		aggregates[modelName] = agg
	}

	now := time.Now().Unix()
	for modelName, agg := range aggregates {
		successRate := 0.0
		if agg.total > 0 {
			successRate = float64(agg.available) / float64(agg.total) * 100
		}
		statusCode := 4
		if agg.available > 0 {
			statusCode = 1
		}
		lastTestTime := agg.lastTest
		if lastTestTime == 0 {
			lastTestTime = now
		}

		globalStatus := &model.ModelStatus{
			ModelName:         modelName,
			Status:            statusCode,
			LastTestTime:      lastTestTime,
			SuccessRate:       successRate,
			TotalChannels:     agg.total,
			AvailableChannels: agg.available,
			UpdatedAt:         now,
		}

		var existing model.ModelStatus
		if err := model.DB.Where("model_name = ?", modelName).First(&existing).Error; err == nil && existing.ID != 0 {
			globalStatus.ID = existing.ID
			globalStatus.CreatedAt = existing.CreatedAt
		} else {
			globalStatus.CreatedAt = now
		}

		if err := model.UpsertModelStatus(globalStatus); err != nil {
			return err
		}
	}

	logger.LogInfo(ctx, fmt.Sprintf("[model-test] rebuilt %d global model status rows", len(aggregates)))
	return nil
}

func getGroupsFromChannels() ([]model.GroupStatus, error) {
	channels, err := model.GetAllChannels(0, 10000, false, false)
	if err != nil {
		return nil, err
	}

	groupMap := make(map[string]bool)
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		for _, g := range ch.GetGroups() {
			if g != "" {
				groupMap[g] = true
			}
		}
	}

	result := make([]model.GroupStatus, 0, len(groupMap))
	for g := range groupMap {
		result = append(result, model.GroupStatus{
			UserGroup: g,
		})
	}
	return result, nil
}
