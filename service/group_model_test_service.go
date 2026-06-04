package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type probeWork struct {
	userGroup string
	modelName string
	channels  []*model.Channel
}

type probeResult struct {
	userGroup string
	modelName string
	available bool
}

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

// testModelAvailabilityViaChannel runs an active probe against each supporting channel.
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
		if probeModelViaChannel(ctx, userGroup, channel, modelName) {
			logger.LogInfo(ctx, fmt.Sprintf(
				"[model-test] model %s in group %s is available (active probe passed on channel %d)",
				modelName, userGroup, channel.Id,
			))
			return true
		}
	}

	logger.LogWarn(ctx, fmt.Sprintf(
		"[model-test] model %s in group %s failed all active probes",
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

// RunGroupModelAvailabilityTest runs model availability checks for all known groups
// using a bounded worker pool so probes are batched rather than fired all at once.
// The behaviour is controlled by MonitorSetting.ModelHealthCheck* fields.
func RunGroupModelAvailabilityTest(ctx context.Context) error {
	cfg := operation_setting.GetMonitorSetting()
	if !cfg.ModelHealthCheckEnabled {
		logger.LogInfo(ctx, "[model-test] model health check is disabled, skipping")
		return nil
	}
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

	// ── Step 1: collect all (group, model, channels) work items ──────────────
	works, err := collectProbeWorks(ctx, groups)
	if err != nil {
		return err
	}
	workers := cfg.ModelHealthCheckWorkers
	logger.LogInfo(ctx, fmt.Sprintf("[model-test] starting batched probe: %d items, %d workers, %dms inter-probe delay",
		len(works), workers, cfg.ModelHealthCheckIntervalMs))

	// ── Step 2: run probes via worker pool ───────────────────────────────────
	results := runProbesWithPool(ctx, works, workers, time.Duration(cfg.ModelHealthCheckIntervalMs)*time.Millisecond)

	// ── Step 3: persist per-model results and aggregate per-group status ─────
	if err := persistProbeResults(ctx, results); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to persist probe results: %v", err))
	}

	// ── Step 4: rebuild global model_statuses table ──────────────────────────
	if err := rebuildGlobalModelStatusesFromGroupAvailability(ctx); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to rebuild global model statuses: %v", err))
		return err
	}

	return nil
}

// collectProbeWorks builds the full list of probes to run across all groups.
func collectProbeWorks(ctx context.Context, groups []model.GroupStatus) ([]probeWork, error) {
	var works []probeWork
	for _, g := range groups {
		channels, err := model.GetChannelsByGroup(g.UserGroup)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to get channels for group %s: %v", g.UserGroup, err))
			continue
		}

		modelSet := make(map[string][]*model.Channel) // model → channels that support it
		for _, ch := range channels {
			if ch == nil || ch.Status != common.ChannelStatusEnabled {
				continue
			}
			for _, m := range ch.GetModels() {
				m = strings.TrimSpace(m)
				if m != "" {
					modelSet[m] = append(modelSet[m], ch)
				}
			}
		}

		for modelName, chans := range modelSet {
			works = append(works, probeWork{
				userGroup: g.UserGroup,
				modelName: modelName,
				channels:  chans,
			})
		}
	}
	return works, nil
}

// runProbesWithPool dispatches works to a bounded goroutine pool and collects results.
func runProbesWithPool(ctx context.Context, works []probeWork, workers int, delay time.Duration) []probeResult {
	results := make([]probeResult, len(works))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, w := range works {
		// 修复：用 select + return 真正终止循环，而非 break 出 select
		select {
		case <-ctx.Done():
			logger.LogWarn(ctx, "[model-test] context cancelled, stopping probe pool early")
			wg.Wait()
			return results
		default:
		}

		// 修复：sem 写入也要监听 ctx，防止 context 取消时在此永久阻塞
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			logger.LogWarn(ctx, "[model-test] context cancelled while waiting for worker slot")
			wg.Wait()
			return results
		}

		wg.Add(1)
		go func(idx int, work probeWork) {
			defer wg.Done()
			defer func() {
				if delay > 0 {
					time.Sleep(delay)
				}
				<-sem
			}()
			available := probeModelViaChannel(ctx, work.userGroup, work.channels[0], work.modelName)
			if !available {
				for _, ch := range work.channels[1:] {
					if probeModelViaChannel(ctx, work.userGroup, ch, work.modelName) {
						available = true
						break
					}
				}
			}
			results[idx] = probeResult{
				userGroup: work.userGroup,
				modelName: work.modelName,
				available: available,
			}
		}(i, w)
	}

	wg.Wait()
	return results
}

// persistProbeResults writes per-model statuses and updates per-group aggregate counters.
func persistProbeResults(ctx context.Context, results []probeResult) error {
	// group → (available, total)
	type groupAgg struct{ avail, total int }
	agg := make(map[string]*groupAgg)

	for _, r := range results {
		if r.userGroup == "" {
			continue
		}
		if err := updateModelStatus(ctx, r.userGroup, r.modelName, r.available); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to update model status %s/%s: %v", r.userGroup, r.modelName, err))
		}
		if agg[r.userGroup] == nil {
			agg[r.userGroup] = &groupAgg{}
		}
		agg[r.userGroup].total++
		if r.available {
			agg[r.userGroup].avail++
		}
	}

	for groupName, a := range agg {
		if err := updateGroupStatus(ctx, groupName, a.avail, a.total); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to update group status %s: %v", groupName, err))
		}
		logger.LogInfo(ctx, fmt.Sprintf("[model-test] group %s: %d/%d models available", groupName, a.avail, a.total))
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
	var firstErr error
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

		// 修复：upsert 失败记录错误但继续，避免部分写入后中断导致全局状态表数据不一致
		if err := model.UpsertModelStatus(globalStatus); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[model-test] failed to upsert global status for model %s: %v", modelName, err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	logger.LogInfo(ctx, fmt.Sprintf("[model-test] rebuilt %d global model status rows", len(aggregates)))
	return firstErr
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
