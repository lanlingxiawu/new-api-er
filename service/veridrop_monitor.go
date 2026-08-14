package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

var (
	ErrVeridropMonitorDisabled = errors.New("veridrop monitor is disabled")
	ErrVeridropBaseURLEmpty    = errors.New("veridrop monitor base url is not configured")
)

type VeridropDetectionTaskPayload struct {
	Batch                     bool   `json:"batch,omitempty"`
	ChannelID                 int    `json:"channel_id,omitempty"`
	ChannelIDs                []int  `json:"channel_ids,omitempty"`
	Model                     string `json:"model,omitempty"`
	Protocol                  string `json:"protocol,omitempty"`
	Mode                      string `json:"mode,omitempty"`
	MaxChannels               int    `json:"max_channels,omitempty"`
	IncludeLongContext        bool   `json:"include_long_context,omitempty"`
	IncludeLongContextExtreme bool   `json:"include_long_context_extreme,omitempty"`
	OpenAIWireAPI             string `json:"openai_wire_api,omitempty"`
}

type VeridropManualDetectionPayload struct {
	BaseURL                   string `json:"base_url"`
	APIKey                    string `json:"api_key"`
	Model                     string `json:"model"`
	Protocol                  string `json:"protocol"`
	Mode                      string `json:"mode"`
	IncludeLongContext        bool   `json:"include_long_context"`
	IncludeLongContextExtreme bool   `json:"include_long_context_extreme"`
	OpenAIWireAPI             string `json:"openai_wire_api"`
}

type VeridropDetectionSummary struct {
	Created   int `json:"created"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	TimedOut  int `json:"timed_out"`
	Cancelled int `json:"cancelled"`
	Disabled  int `json:"disabled"`
	Channels  int `json:"channels"`
	Models    int `json:"models"`
}

type VeridropDetectionTarget struct {
	ChannelID       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	ChannelType     int      `json:"channel_type"`
	ChannelTypeName string   `json:"channel_type_name"`
	Protocol        string   `json:"protocol"`
	BaseURL         string   `json:"base_url"`
	Models          []string `json:"models"`
	ModelCount      int      `json:"model_count"`
	SkippedReason   string   `json:"skipped_reason,omitempty"`
}

type VeridropDetectionTargets struct {
	Items               []VeridropDetectionTarget `json:"items"`
	ChannelCount        int                       `json:"channel_count"`
	ModelCount          int                       `json:"model_count"`
	SkippedChannelCount int                       `json:"skipped_channel_count"`
}

type veridropDetectionJob struct {
	Channel   *model.Channel
	Protocol  string
	Model     string
	Mode      string
	Detection *model.ChannelVeridropDetection
	BaseURL   string
	APIKey    string
	Manual    bool
}

type veridropSettingsSnapshot struct {
	Enabled                    bool
	BaseURL                    string
	AdminToken                 string
	DefaultMode                string
	DefaultOpenAIWireAPI       string
	IncludeLongContext         bool
	IncludeLongContextExtreme  bool
	MaxConcurrent              int
	SubmitTimeoutSeconds       int
	PollIntervalSeconds        int
	JobTimeoutSeconds          int
	AutoDisableEnabled         bool
	AutoDisableFailedThreshold float64
	BatchSize                  int
}

type veridropSubmitResponse struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

type veridropStatusResponse struct {
	JobID       string `json:"job_id"`
	Protocol    string `json:"protocol"`
	Status      string `json:"status"`
	BaseURL     string `json:"base_url"`
	TargetModel string `json:"target_model"`
	Mode        string `json:"mode"`
	Error       string `json:"error"`
}

type veridropReportSummary struct {
	Protocol string
	Score    float64
	Verdict  string
	Summary  string
	RunError string
}

func StartVeridropDetectionTask(payload VeridropDetectionTaskPayload) (*model.SystemTask, bool, error) {
	settings := snapshotVeridropSettings()
	if !settings.Enabled {
		return nil, false, ErrVeridropMonitorDisabled
	}
	if settings.BaseURL == "" {
		return nil, false, ErrVeridropBaseURLEmpty
	}
	return EnqueueSystemTask(model.SystemTaskTypeVeridrop, payload)
}

func StartManualVeridropDetection(ctx context.Context, payload VeridropManualDetectionPayload) (*model.ChannelVeridropDetection, error) {
	settings := snapshotVeridropSettings()
	if !settings.Enabled {
		return nil, ErrVeridropMonitorDisabled
	}
	if settings.BaseURL == "" {
		return nil, ErrVeridropBaseURLEmpty
	}

	baseURL := strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	apiKey := strings.TrimSpace(payload.APIKey)
	modelName := strings.TrimSpace(payload.Model)
	protocol := normalizeVeridropProtocol(payload.Protocol)
	if baseURL == "" || apiKey == "" || modelName == "" || protocol == "" {
		return nil, errors.New("manual veridrop detection requires base_url, api_key, model and protocol")
	}

	mode := normalizeVeridropMode(payload.Mode, protocol, settings)
	detection := &model.ChannelVeridropDetection{
		ChannelID:   0,
		ChannelName: "Manual Test",
		ChannelType: 0,
		Protocol:    protocol,
		Model:       modelName,
		Mode:        mode,
		Status:      model.ChannelVeridropDetectionQueued,
	}
	if err := model.CreateChannelVeridropDetection(detection); err != nil {
		return nil, err
	}

	runPayload := VeridropDetectionTaskPayload{
		Mode:                      mode,
		IncludeLongContext:        payload.IncludeLongContext,
		IncludeLongContextExtreme: payload.IncludeLongContextExtreme,
		OpenAIWireAPI:             payload.OpenAIWireAPI,
	}
	job := veridropDetectionJob{
		Channel: &model.Channel{
			Id:      0,
			Name:    "Manual Test",
			Type:    0,
			BaseURL: common.GetPointer(baseURL),
		},
		Protocol:  protocol,
		Model:     modelName,
		Mode:      mode,
		Detection: detection,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Manual:    true,
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysLog(fmt.Sprintf("manual veridrop detection panic recovered: %v", r))
			}
		}()
		runCtx := context.Background()
		if ctx != nil {
			runCtx = context.WithoutCancel(ctx)
		}
		runVeridropDetectionJob(runCtx, job, runPayload, settings, &VeridropDetectionSummary{})
	}()

	return detection, nil
}

func ListVeridropDetectionTargets(ctx context.Context, payload VeridropDetectionTaskPayload) (VeridropDetectionTargets, error) {
	settings := snapshotVeridropSettings()
	payload = applyVeridropPayloadDefaults(payload, settings)
	targets := VeridropDetectionTargets{}
	lastID := 0
	channelCount := 0

	for {
		if ctx != nil && ctx.Err() != nil {
			return targets, ctx.Err()
		}
		channels, err := model.FindEnabledChannelsForVeridropAfterID(lastID, settings.BatchSize, payload.ChannelIDs)
		if err != nil {
			return targets, err
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if payload.MaxChannels > 0 && channelCount >= payload.MaxChannels {
				return targets, nil
			}
			channelCount++
			target := buildVeridropDetectionTarget(channel, payload)
			targets.Items = append(targets.Items, target)
			if target.SkippedReason == "" {
				targets.ChannelCount++
				targets.ModelCount += target.ModelCount
			} else {
				targets.SkippedChannelCount++
			}
		}
		if len(channels) < settings.BatchSize {
			break
		}
	}
	return targets, nil
}

func RunVeridropDetectionTask(ctx context.Context, payload VeridropDetectionTaskPayload, report func(processed, total int)) VeridropDetectionSummary {
	settings := snapshotVeridropSettings()
	payload = applyVeridropPayloadDefaults(payload, settings)
	summary := VeridropDetectionSummary{}
	if !settings.Enabled {
		common.SysLog("veridrop detection skipped: monitor disabled")
		return summary
	}
	if settings.BaseURL == "" {
		common.SysLog("veridrop detection skipped: base url empty")
		return summary
	}

	if payload.Batch {
		return runVeridropBatchDetection(ctx, payload, settings, report)
	}

	channel, err := model.GetChannelById(payload.ChannelID, true)
	if err != nil {
		common.SysLog(fmt.Sprintf("veridrop detection channel query failed: %v", err))
		return summary
	}
	if channel == nil {
		return summary
	}
	job, err := buildVeridropDetectionJob(channel, payload.Model, payload.Protocol, payload.Mode, settings)
	if err != nil {
		_ = createSkippedVeridropDetection(channel, payload.Protocol, payload.Model, payload.Mode, sanitizeVeridropText(err.Error(), channel.Key, settings.AdminToken))
		summary.Skipped++
		return summary
	}
	summary.Created++
	summary.Channels = 1
	summary.Models = 1
	runVeridropDetectionJob(ctx, job, payload, settings, &summary)
	if report != nil {
		report(1, 1)
	}
	return summary
}

func applyVeridropPayloadDefaults(payload VeridropDetectionTaskPayload, settings veridropSettingsSnapshot) VeridropDetectionTaskPayload {
	if payload.Mode == "" {
		payload.Mode = settings.DefaultMode
	}
	if payload.OpenAIWireAPI == "" {
		payload.OpenAIWireAPI = settings.DefaultOpenAIWireAPI
	}
	if !payload.IncludeLongContext {
		payload.IncludeLongContext = settings.IncludeLongContext
	}
	if !payload.IncludeLongContextExtreme {
		payload.IncludeLongContextExtreme = settings.IncludeLongContextExtreme
	}
	return payload
}

func runVeridropBatchDetection(ctx context.Context, payload VeridropDetectionTaskPayload, settings veridropSettingsSnapshot, report func(processed, total int)) VeridropDetectionSummary {
	summary := VeridropDetectionSummary{}
	processed := 0
	totalWork := countVeridropBatchWork(ctx, payload, settings)
	lastID := 0
	limitReached := false
	for {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if limitReached {
			break
		}
		channels, err := model.FindEnabledChannelsForVeridropAfterID(lastID, settings.BatchSize, payload.ChannelIDs)
		if err != nil {
			common.SysLog(fmt.Sprintf("veridrop batch channel query failed: %v", err))
			break
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		jobs := make([]veridropDetectionJob, 0, len(channels))
		for _, channel := range channels {
			if ctx != nil && ctx.Err() != nil {
				break
			}
			if channel == nil {
				continue
			}
			if payload.MaxChannels > 0 && summary.Channels >= payload.MaxChannels {
				limitReached = true
				break
			}
			summary.Channels++
			modelNames := normalizeVeridropModelNames(channel.GetModels())
			if payload.Model != "" {
				modelNames = []string{strings.TrimSpace(payload.Model)}
			}
			if len(modelNames) == 0 {
				_ = createSkippedVeridropDetection(channel, payload.Protocol, "", payload.Mode, "channel has no enabled models")
				summary.Skipped++
				processed++
				if report != nil {
					report(processed, totalWork)
				}
				continue
			}
			for _, modelName := range modelNames {
				job, err := buildVeridropDetectionJob(channel, modelName, payload.Protocol, payload.Mode, settings)
				if err != nil {
					_ = createSkippedVeridropDetection(channel, payload.Protocol, modelName, payload.Mode, sanitizeVeridropText(err.Error(), channel.Key, settings.AdminToken))
					summary.Skipped++
					processed++
					if report != nil {
						report(processed, totalWork)
					}
					continue
				}
				summary.Created++
				summary.Models++
				jobs = append(jobs, job)
			}
		}
		batchSummary, batchProcessed := runVeridropDetectionJobs(ctx, jobs, payload, settings, report, processed, totalWork)
		processed = batchProcessed
		mergeVeridropDetectionSummary(&summary, batchSummary)
		if len(channels) < settings.BatchSize {
			break
		}
	}
	if report != nil {
		report(processed, totalWork)
	}
	return summary
}

func countVeridropBatchWork(ctx context.Context, payload VeridropDetectionTaskPayload, settings veridropSettingsSnapshot) int {
	total := 0
	channelCount := 0
	lastID := 0
	for {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		channels, err := model.FindEnabledChannelsForVeridropAfterID(lastID, settings.BatchSize, payload.ChannelIDs)
		if err != nil || len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id
		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if payload.MaxChannels > 0 && channelCount >= payload.MaxChannels {
				return total
			}
			channelCount++
			modelNames := normalizeVeridropModelNames(channel.GetModels())
			if payload.Model != "" {
				modelNames = []string{strings.TrimSpace(payload.Model)}
			}
			if len(modelNames) == 0 {
				total++
				continue
			}
			total += len(modelNames)
		}
		if len(channels) < settings.BatchSize {
			break
		}
	}
	return total
}

func runVeridropDetectionJobs(ctx context.Context, jobs []veridropDetectionJob, payload VeridropDetectionTaskPayload, settings veridropSettingsSnapshot, report func(processed, total int), processed int, total int) (VeridropDetectionSummary, int) {
	summary := VeridropDetectionSummary{}
	if len(jobs) == 0 {
		return summary, processed
	}

	workerCount := settings.MaxConcurrent
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	jobCh := make(chan veridropDetectionJob)
	resultCh := make(chan VeridropDetectionSummary, workerCount)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				jobSummary := VeridropDetectionSummary{}
				runVeridropDetectionJob(ctx, job, payload, settings, &jobSummary)
				resultCh <- jobSummary
			}
		}()
	}

	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			jobCh <- job
		}
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for result := range resultCh {
		mergeVeridropDetectionSummary(&summary, result)
		processed++
		if report != nil {
			report(processed, total)
		}
	}
	return summary, processed
}

func buildVeridropDetectionTarget(channel *model.Channel, payload VeridropDetectionTaskPayload) VeridropDetectionTarget {
	protocol := normalizeVeridropProtocol(payload.Protocol)
	if protocol == "" {
		protocol = inferVeridropProtocol(channel)
	}
	target := VeridropDetectionTarget{
		ChannelID:       channel.Id,
		ChannelName:     channel.Name,
		ChannelType:     channel.Type,
		ChannelTypeName: constant.ChannelTypeNames[channel.Type],
		Protocol:        protocol,
		BaseURL:         strings.TrimRight(channel.GetBaseURL(), "/"),
	}
	if target.ChannelTypeName == "" {
		target.ChannelTypeName = "Unknown"
	}
	if protocol == "" {
		target.SkippedReason = "unsupported channel protocol"
		return target
	}
	modelNames := normalizeVeridropModelNames(channel.GetModels())
	if payload.Model != "" {
		modelNames = []string{strings.TrimSpace(payload.Model)}
	}
	if len(modelNames) == 0 {
		target.SkippedReason = "channel has no enabled models"
		return target
	}
	target.Models = modelNames
	target.ModelCount = len(modelNames)
	return target
}

func mergeVeridropDetectionSummary(target *VeridropDetectionSummary, source VeridropDetectionSummary) {
	target.Succeeded += source.Succeeded
	target.Failed += source.Failed
	target.TimedOut += source.TimedOut
	target.Cancelled += source.Cancelled
	target.Disabled += source.Disabled
}

func buildVeridropDetectionJob(channel *model.Channel, modelName string, protocol string, mode string, settings veridropSettingsSnapshot) (veridropDetectionJob, error) {
	protocol = normalizeVeridropProtocol(protocol)
	if protocol == "" {
		protocol = inferVeridropProtocol(channel)
	}
	if protocol == "" {
		return veridropDetectionJob{}, errors.New("channel is not supported by veridrop monitor")
	}

	modelName = strings.TrimSpace(modelName)
	if modelName == "" && channel.TestModel != nil {
		modelName = strings.TrimSpace(*channel.TestModel)
	}
	if modelName == "" {
		models := normalizeVeridropModelNames(channel.GetModels())
		if len(models) > 0 {
			modelName = models[0]
		}
	}
	if modelName == "" {
		return veridropDetectionJob{}, errors.New("channel model is required for veridrop detection")
	}

	mode = normalizeVeridropMode(mode, protocol, settings)
	detection := &model.ChannelVeridropDetection{
		ChannelID:   channel.Id,
		ChannelName: channel.Name,
		ChannelType: channel.Type,
		Protocol:    protocol,
		Model:       modelName,
		Mode:        mode,
		Status:      model.ChannelVeridropDetectionQueued,
	}
	if err := model.CreateChannelVeridropDetection(detection); err != nil {
		return veridropDetectionJob{}, err
	}

	return veridropDetectionJob{
		Channel:   channel,
		Protocol:  protocol,
		Model:     modelName,
		Mode:      mode,
		Detection: detection,
	}, nil
}

func runVeridropDetectionJob(ctx context.Context, job veridropDetectionJob, payload VeridropDetectionTaskPayload, settings veridropSettingsSnapshot, summary *VeridropDetectionSummary) {
	if ctx != nil && ctx.Err() != nil {
		markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionCancelled, "", nil, "detection cancelled")
		summary.Cancelled++
		return
	}

	now := common.GetTimestamp()
	_ = model.UpdateChannelVeridropDetection(job.Detection.ID, map[string]any{
		"status":     model.ChannelVeridropDetectionRunning,
		"started_at": now,
	})

	key := strings.TrimSpace(job.APIKey)
	if key == "" {
		var apiErr *types.NewAPIError
		key, _, apiErr = job.Channel.GetNextEnabledKey()
		if apiErr != nil {
			message := sanitizeVeridropText(apiErr.Error(), job.Channel.Key, settings.AdminToken)
			markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionError, "", nil, message)
			summary.Failed++
			return
		}
	}
	key = firstVeridropKey(key)
	if key == "" {
		markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionError, "", nil, "channel key is empty")
		summary.Failed++
		return
	}

	submitResp, err := submitVeridropDetection(ctx, settings, job, key, payload)
	if err != nil {
		message := sanitizeVeridropText(err.Error(), key, settings.AdminToken)
		markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionError, "", nil, message)
		summary.Failed++
		return
	}
	_ = model.UpdateChannelVeridropDetection(job.Detection.ID, map[string]any{
		"veridrop_job_id": submitResp.JobID,
	})

	status, err := pollVeridropDetection(ctx, settings, submitResp.JobID)
	if err != nil {
		message := sanitizeVeridropText(err.Error(), key, settings.AdminToken)
		terminal := model.ChannelVeridropDetectionError
		if errors.Is(err, context.DeadlineExceeded) {
			terminal = model.ChannelVeridropDetectionTimeout
			summary.TimedOut++
		} else {
			summary.Failed++
		}
		markVeridropDetectionTerminal(job.Detection.ID, terminal, submitResp.JobID, nil, message)
		return
	}
	if status.Status == "error" {
		message := sanitizeVeridropText(status.Error, key, settings.AdminToken)
		markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionError, submitResp.JobID, nil, message)
		summary.Failed++
		return
	}

	reportBody, reportSummary, err := fetchVeridropResult(ctx, settings, submitResp.JobID)
	if err != nil {
		message := sanitizeVeridropText(err.Error(), key, settings.AdminToken)
		markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionError, submitResp.JobID, nil, message)
		summary.Failed++
		return
	}
	reportBody = sanitizeVeridropText(reportBody, key, settings.AdminToken)
	markVeridropDetectionTerminal(job.Detection.ID, model.ChannelVeridropDetectionDone, submitResp.JobID, &reportSummary, reportBody)
	summary.Succeeded++

	if !job.Manual && shouldAutoDisableByVeridrop(reportSummary, settings) {
		DisableChannel(*types.NewChannelError(job.Channel.Id, job.Channel.Type, job.Channel.Name, job.Channel.ChannelInfo.IsMultiKey, "", true), "Veridrop: "+reportSummary.Summary)
		summary.Disabled++
	}
}

func createSkippedVeridropDetection(channel *model.Channel, protocol string, modelName string, mode string, reason string) error {
	protocol = normalizeVeridropProtocol(protocol)
	if protocol == "" && channel != nil {
		protocol = inferVeridropProtocol(channel)
	}
	if channel == nil {
		return nil
	}
	detection := &model.ChannelVeridropDetection{
		ChannelID:   channel.Id,
		ChannelName: channel.Name,
		ChannelType: channel.Type,
		Protocol:    protocol,
		Model:       modelName,
		Mode:        mode,
		Status:      model.ChannelVeridropDetectionSkipped,
		Error:       reason,
		FinishedAt:  common.GetTimestamp(),
	}
	return model.CreateChannelVeridropDetection(detection)
}

func markVeridropDetectionTerminal(id int64, status model.ChannelVeridropDetectionStatus, jobID string, report *veridropReportSummary, message string) {
	updates := map[string]any{
		"status":      status,
		"finished_at": common.GetTimestamp(),
	}
	if jobID != "" {
		updates["veridrop_job_id"] = jobID
	}
	if report != nil {
		updates["score"] = report.Score
		updates["verdict"] = report.Verdict
		updates["summary"] = report.Summary
		updates["run_error"] = report.RunError
		updates["result_json"] = message
	} else {
		updates["error"] = message
	}
	if err := model.UpdateChannelVeridropDetection(id, updates); err != nil {
		common.SysLog(fmt.Sprintf("veridrop detection update failed: id=%d err=%v", id, err))
	}
}

func submitVeridropDetection(ctx context.Context, settings veridropSettingsSnapshot, job veridropDetectionJob, apiKey string, payload VeridropDetectionTaskPayload) (veridropSubmitResponse, error) {
	endpoint := settings.BaseURL + "/api/detect/" + veridropEndpointProtocol(job.Protocol)
	form := url.Values{}
	baseURL := strings.TrimRight(strings.TrimSpace(job.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(job.Channel.GetBaseURL(), "/")
	}
	form.Set("base_url", baseURL)
	form.Set("api_key", apiKey)
	form.Set("model", job.Model)
	form.Set("mode", job.Mode)
	form.Set("include_long_context", fmt.Sprintf("%t", payload.IncludeLongContext))
	form.Set("include_long_context_extreme", fmt.Sprintf("%t", payload.IncludeLongContextExtreme))
	if job.Protocol == "openai" {
		wire := strings.TrimSpace(payload.OpenAIWireAPI)
		if wire == "" {
			wire = "chat_completions"
		}
		form.Set("wire_api", wire)
	}

	var out veridropSubmitResponse
	err := doVeridropJSON(ctx, settings, http.MethodPost, endpoint, form, &out, settings.SubmitTimeoutSeconds)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out.JobID) == "" {
		return out, errors.New("veridrop detection response missing job_id")
	}
	return out, nil
}

func pollVeridropDetection(ctx context.Context, settings veridropSettingsSnapshot, jobID string) (veridropStatusResponse, error) {
	pollCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.JobTimeoutSeconds)*time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Duration(settings.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		status, err := getVeridropStatus(pollCtx, settings, jobID)
		if err != nil {
			return status, err
		}
		switch status.Status {
		case "done", "error":
			return status, nil
		}
		select {
		case <-pollCtx.Done():
			return status, pollCtx.Err()
		case <-ticker.C:
		}
	}
}

func getVeridropStatus(ctx context.Context, settings veridropSettingsSnapshot, jobID string) (veridropStatusResponse, error) {
	var out veridropStatusResponse
	endpoint := settings.BaseURL + "/api/status/" + url.PathEscape(jobID)
	err := doVeridropJSON(ctx, settings, http.MethodGet, endpoint, nil, &out, settings.SubmitTimeoutSeconds)
	return out, err
}

func fetchVeridropResult(ctx context.Context, settings veridropSettingsSnapshot, jobID string) (string, veridropReportSummary, error) {
	endpoint := settings.BaseURL + "/api/result/" + url.PathEscape(jobID) + ".json"
	body, err := doVeridropRequest(ctx, settings, http.MethodGet, endpoint, nil, settings.SubmitTimeoutSeconds)
	if err != nil {
		return "", veridropReportSummary{}, err
	}
	var report struct {
		Protocol string  `json:"protocol"`
		Score    float64 `json:"total_score"`
		Verdict  string  `json:"verdict"`
		Summary  string  `json:"summary"`
		RunError string  `json:"run_error"`
	}
	if err := common.Unmarshal(body, &report); err != nil {
		return "", veridropReportSummary{}, err
	}
	return string(body), veridropReportSummary{
		Protocol: report.Protocol,
		Score:    report.Score,
		Verdict:  report.Verdict,
		Summary:  report.Summary,
		RunError: report.RunError,
	}, nil
}

func doVeridropJSON(ctx context.Context, settings veridropSettingsSnapshot, method string, endpoint string, form url.Values, out any, timeoutSeconds int) error {
	body, err := doVeridropRequest(ctx, settings, method, endpoint, form, timeoutSeconds)
	if err != nil {
		return err
	}
	return common.Unmarshal(body, out)
}

func doVeridropRequest(ctx context.Context, settings veridropSettingsSnapshot, method string, endpoint string, form url.Values, timeoutSeconds int) ([]byte, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(timeoutCtx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if settings.AdminToken != "" {
		request.Header.Set("Authorization", "Bearer "+settings.AdminToken)
	}

	response, err := veridropHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("veridrop returned HTTP %d: %s", response.StatusCode, string(bytes.TrimSpace(responseBody)))
	}
	return responseBody, nil
}

func veridropHTTPClient() *http.Client {
	return &http.Client{}
}

func snapshotVeridropSettings() veridropSettingsSnapshot {
	setting := operation_setting.GetVeridropMonitorSetting()
	return veridropSettingsSnapshot{
		Enabled:                    setting.Enabled,
		BaseURL:                    strings.TrimRight(strings.TrimSpace(setting.BaseURL), "/"),
		AdminToken:                 strings.TrimSpace(setting.AdminToken),
		DefaultMode:                strings.TrimSpace(setting.DefaultMode),
		DefaultOpenAIWireAPI:       strings.TrimSpace(setting.DefaultOpenAIWireAPI),
		IncludeLongContext:         setting.IncludeLongContext,
		IncludeLongContextExtreme:  setting.IncludeLongContextExtreme,
		MaxConcurrent:              setting.MaxConcurrent,
		SubmitTimeoutSeconds:       setting.SubmitTimeoutSeconds,
		PollIntervalSeconds:        setting.PollIntervalSeconds,
		JobTimeoutSeconds:          setting.JobTimeoutSeconds,
		AutoDisableEnabled:         setting.AutoDisableEnabled,
		AutoDisableFailedThreshold: setting.AutoDisableFailedThreshold,
		BatchSize:                  setting.BatchSize,
	}
}

func inferVeridropProtocol(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	switch channel.Type {
	case constant.ChannelTypeAnthropic:
		return "anthropic"
	case constant.ChannelTypeGemini, constant.ChannelTypeVertexAi:
		return "gemini"
	case constant.ChannelTypeOpenAI,
		constant.ChannelTypeAzure,
		constant.ChannelTypeOpenAIMax,
		constant.ChannelTypeOhMyGPT,
		constant.ChannelTypeCustom,
		constant.ChannelTypeAPI2GPT,
		constant.ChannelTypeOpenRouter,
		constant.ChannelTypeAIProxyLibrary,
		constant.ChannelTypeMoonshot,
		constant.ChannelTypePerplexity,
		constant.ChannelTypeSiliconFlow,
		constant.ChannelTypeDeepSeek,
		constant.ChannelTypeMistral,
		constant.ChannelTypeXai,
		constant.ChannelTypeSubmodel,
		constant.ChannelTypeSora,
		constant.ChannelTypeSub2API,
		constant.ChannelTypeNewAPI:
		return "openai"
	case constant.ChannelTypeAdvancedCustom:
		return inferAdvancedCustomVeridropProtocol(channel.GetOtherSettings().AdvancedCustom)
	default:
		return ""
	}
}

func inferAdvancedCustomVeridropProtocol(config *dto.AdvancedCustomConfig) string {
	if config == nil {
		return ""
	}
	found := ""
	for _, route := range config.Routes {
		converter := strings.TrimSpace(string(route.Converter))
		path := strings.ToLower(strings.TrimSpace(route.IncomingPath + " " + route.UpstreamPath))
		current := ""
		switch {
		case strings.Contains(converter, "anthropic") || strings.Contains(path, "/messages"):
			current = "anthropic"
		case strings.Contains(converter, "gemini") || strings.Contains(path, "generatecontent"):
			current = "gemini"
		case strings.Contains(converter, "openai") || strings.Contains(path, "/chat/completions") || strings.Contains(path, "/responses"):
			current = "openai"
		}
		if current == "" {
			continue
		}
		if found != "" && found != current {
			return ""
		}
		found = current
	}
	return found
}

func normalizeVeridropProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "claude", "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

func veridropEndpointProtocol(protocol string) string {
	if protocol == "anthropic" {
		return "claude"
	}
	return protocol
}

func normalizeVeridropMode(mode string, _ string, settings veridropSettingsSnapshot) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "quick", "standard", "full":
		return mode
	}
	defaultMode := strings.ToLower(strings.TrimSpace(settings.DefaultMode))
	switch defaultMode {
	case "quick", "standard", "full":
		return defaultMode
	}
	return "quick"
}

func normalizeVeridropModelNames(models []string) []string {
	result := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		result = append(result, modelName)
	}
	return result
}

func firstVeridropKey(key string) string {
	keys := strings.Split(strings.TrimSpace(key), "\n")
	for _, item := range keys {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	return ""
}

func sanitizeVeridropText(text string, secrets ...string) string {
	out := text
	for _, secret := range secrets {
		for _, item := range strings.Split(secret, "\n") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = strings.ReplaceAll(out, item, "[redacted]")
		}
	}
	return out
}

func shouldAutoDisableByVeridrop(report veridropReportSummary, settings veridropSettingsSnapshot) bool {
	if !settings.AutoDisableEnabled || settings.AutoDisableFailedThreshold <= 0 {
		return false
	}
	return strings.EqualFold(report.Verdict, "failed") && report.Score <= settings.AutoDisableFailedThreshold
}

func LogVeridropTaskResult(result VeridropDetectionSummary) {
	logger.LogInfo(context.Background(), fmt.Sprintf(
		"veridrop detection task done: created=%d succeeded=%d failed=%d skipped=%d timed_out=%d cancelled=%d disabled=%d",
		result.Created,
		result.Succeeded,
		result.Failed,
		result.Skipped,
		result.TimedOut,
		result.Cancelled,
		result.Disabled,
	))
}
