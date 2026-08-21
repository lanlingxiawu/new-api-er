package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type veridropDetectRequest struct {
	ChannelID                 int    `json:"channel_id"`
	ChannelIDs                []int  `json:"channel_ids"`
	BaseURL                   string `json:"base_url"`
	APIKey                    string `json:"api_key"`
	Model                     string `json:"model"`
	Protocol                  string `json:"protocol"`
	Mode                      string `json:"mode"`
	MaxChannels               int    `json:"max_channels"`
	IncludeLongContext        *bool  `json:"include_long_context"`
	IncludeLongContextExtreme *bool  `json:"include_long_context_extreme"`
	OpenAIWireAPI             string `json:"openai_wire_api"`
	Force                     bool   `json:"force"`
}

type veridropCleanupRequest struct {
	RetentionDays int `json:"retention_days"`
}

type veridropManualModelsRequest struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Protocol string `json:"protocol"`
}

func parseOptionalNonNegativeInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

func parseOptionalNonNegativeInt64(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value >= 0
}

func parseOptionalScore(raw string) (*float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 100 {
		return nil, false
	}
	return &value, true
}

func queryValueAllowed(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func parseVeridropOutcomes(raw string) ([]string, bool) {
	parts := strings.Split(raw, ",")
	outcomes := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		outcome := strings.TrimSpace(part)
		if outcome == "" || !queryValueAllowed(outcome, "in_progress", "passed", "completed", "failed", "low_score", "skipped", "cancelled") {
			return nil, false
		}
		if _, exists := seen[outcome]; exists {
			return nil, false
		}
		seen[outcome] = struct{}{}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, true
}

func startVeridropSystemTask(c *gin.Context, payload service.VeridropDetectionTaskPayload, single bool, auditAction string, auditFields map[string]interface{}, logMessage string) {
	var task *model.SystemTask
	var created bool
	var err error
	if single {
		task, created, err = service.StartSingleVeridropDetectionTask(payload)
	} else {
		task, created, err = service.StartVeridropDetectionTask(payload)
	}
	if err != nil {
		respondVeridropStartError(c, logMessage, err, i18n.MsgRetryLater)
		return
	}
	auditFields["created"] = created
	recordManageAudit(c, auditAction, auditFields)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"task": task.ToResponse(), "created": created},
	})
}

func respondVeridropStartError(c *gin.Context, logMessage string, err error, fallbackKey string) {
	logger.LogError(c, logMessage+": "+err.Error())
	switch {
	case errors.Is(err, service.ErrVeridropMonitorDisabled):
		common.ApiErrorI18n(c, i18n.MsgVeridropDisabled)
	case errors.Is(err, service.ErrVeridropBaseURLEmpty):
		common.ApiErrorI18n(c, i18n.MsgVeridropBaseURLMissing)
	case errors.Is(err, service.ErrInvalidVeridropWireAPI):
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
	default:
		common.ApiErrorI18n(c, fallbackKey)
	}
}

func StartChannelVeridropDetection(c *gin.Context) {
	var req veridropDetectRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode veridrop detection request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if req.ChannelID <= 0 {
		common.ApiErrorI18n(c, i18n.MsgIdEmpty)
		return
	}

	startVeridropSystemTask(c, service.VeridropDetectionTaskPayload{
		ChannelID:                 req.ChannelID,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
		Force:                     req.Force,
	}, true, "channel.veridrop_detect", map[string]interface{}{
		"channel_id": req.ChannelID,
	}, "start veridrop detection failed")
}

func StartEnabledChannelsVeridropDetection(c *gin.Context) {
	var req veridropDetectRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode veridrop batch detection request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	startVeridropSystemTask(c, service.VeridropDetectionTaskPayload{
		Batch:                     true,
		ChannelIDs:                req.ChannelIDs,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		MaxChannels:               req.MaxChannels,
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
		Force:                     req.Force,
	}, false, "channel.veridrop_detect_enabled", map[string]interface{}{}, "start veridrop batch detection failed")
}

func StartChannelsVeridropDetection(c *gin.Context) {
	var req veridropDetectRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode selected veridrop batch detection request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(req.ChannelIDs) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	seen := make(map[int]struct{}, len(req.ChannelIDs))
	for _, channelID := range req.ChannelIDs {
		if channelID <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		if _, exists := seen[channelID]; exists {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		seen[channelID] = struct{}{}
	}

	startVeridropSystemTask(c, service.VeridropDetectionTaskPayload{
		Batch:                     true,
		ChannelIDs:                req.ChannelIDs,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		MaxChannels:               len(req.ChannelIDs),
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
		Force:                     req.Force,
		IncludeDisabled:           true,
	}, false, "channel.veridrop_detect_batch", map[string]interface{}{
		"channel_ids": req.ChannelIDs,
	}, "start selected veridrop batch detection failed")
}

func StartManualChannelVeridropDetection(c *gin.Context) {
	var req veridropDetectRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode manual veridrop detection request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	detection, err := service.StartManualVeridropDetection(c.Request.Context(), service.VeridropManualDetectionPayload{
		BaseURL:                   req.BaseURL,
		APIKey:                    req.APIKey,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		IncludeLongContext:        req.IncludeLongContext != nil && *req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme != nil && *req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
		Force:                     req.Force,
	})
	if err != nil {
		respondVeridropStartError(c, "start manual veridrop detection failed", err, i18n.MsgInvalidParams)
		return
	}

	recordManageAudit(c, "channel.veridrop_detect_manual", map[string]interface{}{
		"detection_id": detection.ID,
		"protocol":     detection.Protocol,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    detection,
	})
}

func FetchVeridropManualModels(c *gin.Context) {
	var req veridropManualModelsRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if baseURL == "" || apiKey == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	channelType := 0
	switch strings.ToLower(strings.TrimSpace(req.Protocol)) {
	case "openai":
		channelType = constant.ChannelTypeOpenAI
	case "anthropic":
		channelType = constant.ChannelTypeAnthropic
	case "gemini":
		channelType = constant.ChannelTypeGemini
	default:
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	models, err := fetchChannelUpstreamModelIDs(&model.Channel{
		Type:    channelType,
		Key:     strings.Split(apiKey, "\n")[0],
		BaseURL: &baseURL,
	})
	if err != nil {
		logger.LogError(c, "discover veridrop manual models failed: "+sanitizeFetchModelsError(err, apiKey).Error())
		common.ApiErrorI18n(c, i18n.MsgModelGetListFailed)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": models})
}

func ListVeridropSystemTasks(c *gin.Context) {
	limit := 30
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > 100 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		limit = parsedLimit
	}

	tasks, err := model.ListSystemTasksByTypes([]string{
		model.SystemTaskTypeVeridrop,
		model.SystemTaskTypeVeridropSingle,
		model.SystemTaskTypeVeridropCleanup,
	}, limit)
	if err != nil {
		logger.LogError(c, "list veridrop system tasks failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	responses := make([]model.SystemTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToResponse())
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": responses})
}

func ListChannelVeridropDetectionTargets(c *gin.Context) {
	scope := strings.TrimSpace(c.Query("scope"))
	if scope != "" && scope != "enabled" && scope != "all" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	maxChannels, valid := parseOptionalNonNegativeInt(c.Query("max_channels"))
	if !valid {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	targets, err := service.ListVeridropDetectionTargets(c.Request.Context(), service.VeridropDetectionTaskPayload{
		Mode:            c.Query("mode"),
		Protocol:        c.Query("protocol"),
		Model:           c.Query("model"),
		MaxChannels:     maxChannels,
		OpenAIWireAPI:   c.Query("openai_wire_api"),
		IncludeDisabled: scope == "all",
	})
	if err != nil {
		logger.LogError(c, "list veridrop detection targets failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    targets,
	})
}

func ListChannelVeridropDetectionResults(c *gin.Context) {
	batch := strings.TrimSpace(c.Query("batch"))
	if batch != "" && batch != "latest" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	channelID, validChannelID := parseOptionalNonNegativeInt(c.Query("channel_id"))
	limit, validLimit := parseOptionalNonNegativeInt(c.Query("limit"))
	beforeID, validBeforeID := parseOptionalNonNegativeInt64(c.Query("before_id"))
	updatedAfter, validUpdatedAfter := parseOptionalNonNegativeInt64(c.Query("updated_after"))
	minScore, validMinScore := parseOptionalScore(c.Query("min_score"))
	maxScore, validMaxScore := parseOptionalScore(c.Query("max_score"))
	outcome := strings.TrimSpace(c.Query("outcome"))
	outcomesRaw, outcomesProvided := c.GetQuery("outcomes")
	var outcomes []string
	validOutcomes := true
	if outcomesProvided {
		outcomes, validOutcomes = parseVeridropOutcomes(outcomesRaw)
	}
	status := strings.TrimSpace(c.Query("status"))
	protocol := strings.TrimSpace(c.Query("protocol"))
	mode := strings.TrimSpace(c.Query("mode"))
	sortBy := strings.TrimSpace(c.Query("sort_by"))
	sortOrder := strings.TrimSpace(c.Query("sort_order"))
	errorOnly := strings.TrimSpace(c.Query("error_only"))
	validEnums := queryValueAllowed(outcome, "in_progress", "passed", "completed", "failed", "low_score", "skipped", "cancelled") &&
		queryValueAllowed(status, "queued", "running", "done", "error", "timeout", "cancelled", "skipped") &&
		queryValueAllowed(protocol, "openai", "anthropic", "gemini") &&
		queryValueAllowed(mode, "quick", "standard", "full") &&
		queryValueAllowed(sortBy, "updated_at", "score", "channel_name", "model") &&
		queryValueAllowed(sortOrder, "asc", "desc") &&
		queryValueAllowed(errorOnly, "true", "false", "1", "0")
	if !validChannelID || !validLimit || limit > 100 || !validBeforeID || !validUpdatedAfter ||
		!validMinScore || !validMaxScore || (minScore != nil && maxScore != nil && *minScore > *maxScore) || !validEnums {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if !validOutcomes || (outcomesProvided && outcome != "") {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	latestBatchID := ""
	if batch == "latest" {
		var err error
		if beforeID > 0 {
			latestBatchID, err = model.GetChannelVeridropDetectionBatchTaskIDByID(beforeID)
		} else {
			latestBatchID, err = model.GetLatestChannelVeridropDetectionBatchTaskID()
		}
		if err != nil {
			logger.LogError(c, "resolve veridrop detection batch failed: "+err.Error())
			if errors.Is(err, model.ErrInvalidChannelVeridropBatchCursor) {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
			common.ApiErrorI18n(c, i18n.MsgRetryLater)
			return
		}
		if latestBatchID == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "",
				"data": gin.H{
					"items":           make([]*model.ChannelVeridropDetection, 0),
					"next_before_id":  0,
					"latest_batch_id": "",
					"matching_count":  0,
					"summary":         model.ChannelVeridropDetectionSummary{},
				},
			})
			return
		}
	}
	listOptions := model.ChannelVeridropDetectionListOptions{
		BatchTaskID:  latestBatchID,
		ChannelID:    channelID,
		ChannelName:  strings.TrimSpace(c.Query("channel_name")),
		Model:        strings.TrimSpace(c.Query("model")),
		Mode:         mode,
		Verdict:      strings.TrimSpace(c.Query("verdict")),
		Outcome:      outcome,
		Outcomes:     outcomes,
		Keyword:      strings.TrimSpace(c.Query("keyword")),
		Status:       status,
		Protocol:     protocol,
		MinScore:     minScore,
		MaxScore:     maxScore,
		UpdatedAfter: updatedAfter,
		ErrorOnly:    errorOnly == "true" || errorOnly == "1",
		BeforeID:     beforeID,
		Limit:        limit,
		SortBy:       sortBy,
		SortOrder:    sortOrder,
	}
	items, err := model.ListChannelVeridropDetections(listOptions)
	if err != nil {
		logger.LogError(c, "list veridrop detection results failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	summary, matchingCount, err := model.SummarizeChannelVeridropDetections(listOptions)
	if err != nil {
		logger.LogError(c, "summarize veridrop detection results failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	var nextBeforeID int64
	if len(items) > 0 {
		nextBeforeID = items[len(items)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":           items,
			"next_before_id":  nextBeforeID,
			"latest_batch_id": latestBatchID,
			"matching_count":  matchingCount,
			"summary":         summary,
		},
	})
}

func GetChannelVeridropDetectionResult(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}

	item, err := model.GetChannelVeridropDetectionByID(id)
	if err != nil {
		logger.LogError(c, "get veridrop detection result failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	if item == nil {
		common.ApiErrorI18n(c, i18n.MsgNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    item,
	})
}

func StartChannelVeridropDetectionCleanup(c *gin.Context) {
	var req veridropCleanupRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode veridrop cleanup request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	task, created, err := service.StartVeridropDetectionCleanupTask(req.RetentionDays)
	if err != nil {
		if errors.Is(err, service.ErrInvalidVeridropCleanupRetention) {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		logger.LogError(c, "start veridrop cleanup failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	recordManageAudit(c, "channel.veridrop_cleanup", map[string]interface{}{
		"retention_days": req.RetentionDays,
		"created":        created,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task":    task.ToResponse(),
			"created": created,
		},
	})
}
