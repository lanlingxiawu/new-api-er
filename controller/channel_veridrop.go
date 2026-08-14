package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
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
	IncludeLongContext        bool   `json:"include_long_context"`
	IncludeLongContextExtreme bool   `json:"include_long_context_extreme"`
	OpenAIWireAPI             string `json:"openai_wire_api"`
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

	task, created, err := service.StartVeridropDetectionTask(service.VeridropDetectionTaskPayload{
		ChannelID:                 req.ChannelID,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
	})
	if err != nil {
		logger.LogError(c, "start veridrop detection failed: "+err.Error())
		if errors.Is(err, service.ErrVeridropMonitorDisabled) {
			common.ApiErrorI18n(c, i18n.MsgVeridropDisabled)
			return
		}
		if errors.Is(err, service.ErrVeridropBaseURLEmpty) {
			common.ApiErrorI18n(c, i18n.MsgVeridropBaseURLMissing)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	recordManageAudit(c, "channel.veridrop_detect", map[string]interface{}{
		"channel_id": req.ChannelID,
		"created":    created,
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

func StartEnabledChannelsVeridropDetection(c *gin.Context) {
	var req veridropDetectRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		logger.LogError(c, "decode veridrop batch detection request failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	task, created, err := service.StartVeridropDetectionTask(service.VeridropDetectionTaskPayload{
		Batch:                     true,
		ChannelIDs:                req.ChannelIDs,
		Model:                     req.Model,
		Protocol:                  req.Protocol,
		Mode:                      req.Mode,
		MaxChannels:               req.MaxChannels,
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
	})
	if err != nil {
		logger.LogError(c, "start veridrop batch detection failed: "+err.Error())
		if errors.Is(err, service.ErrVeridropMonitorDisabled) {
			common.ApiErrorI18n(c, i18n.MsgVeridropDisabled)
			return
		}
		if errors.Is(err, service.ErrVeridropBaseURLEmpty) {
			common.ApiErrorI18n(c, i18n.MsgVeridropBaseURLMissing)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	recordManageAudit(c, "channel.veridrop_detect_enabled", map[string]interface{}{
		"created": created,
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
		IncludeLongContext:        req.IncludeLongContext,
		IncludeLongContextExtreme: req.IncludeLongContextExtreme,
		OpenAIWireAPI:             req.OpenAIWireAPI,
	})
	if err != nil {
		logger.LogError(c, "start manual veridrop detection failed: "+err.Error())
		if errors.Is(err, service.ErrVeridropMonitorDisabled) {
			common.ApiErrorI18n(c, i18n.MsgVeridropDisabled)
			return
		}
		if errors.Is(err, service.ErrVeridropBaseURLEmpty) {
			common.ApiErrorI18n(c, i18n.MsgVeridropBaseURLMissing)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
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

func ListChannelVeridropDetectionTargets(c *gin.Context) {
	maxChannels, _ := strconv.Atoi(c.Query("max_channels"))
	targets, err := service.ListVeridropDetectionTargets(c.Request.Context(), service.VeridropDetectionTaskPayload{
		Mode:                      c.Query("mode"),
		Protocol:                  c.Query("protocol"),
		Model:                     c.Query("model"),
		MaxChannels:               maxChannels,
		IncludeLongContext:        c.Query("include_long_context") == "true",
		IncludeLongContextExtreme: c.Query("include_long_context_extreme") == "true",
		OpenAIWireAPI:             c.Query("openai_wire_api"),
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
	channelID, _ := strconv.Atoi(c.Query("channel_id"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	beforeID, _ := strconv.ParseInt(c.Query("before_id"), 10, 64)
	updatedAfter, _ := strconv.ParseInt(c.Query("updated_after"), 10, 64)
	var minScore *float64
	if raw := strings.TrimSpace(c.Query("min_score")); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			minScore = &value
		}
	}
	var maxScore *float64
	if raw := strings.TrimSpace(c.Query("max_score")); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			maxScore = &value
		}
	}
	items, err := model.ListChannelVeridropDetections(model.ChannelVeridropDetectionListOptions{
		ChannelID:    channelID,
		ChannelName:  strings.TrimSpace(c.Query("channel_name")),
		Model:        strings.TrimSpace(c.Query("model")),
		Mode:         strings.TrimSpace(c.Query("mode")),
		Verdict:      strings.TrimSpace(c.Query("verdict")),
		Keyword:      strings.TrimSpace(c.Query("keyword")),
		Status:       c.Query("status"),
		Protocol:     c.Query("protocol"),
		MinScore:     minScore,
		MaxScore:     maxScore,
		UpdatedAfter: updatedAfter,
		ErrorOnly:    c.Query("error_only") == "true" || c.Query("error_only") == "1",
		BeforeID:     beforeID,
		Limit:        limit,
	})
	if err != nil {
		logger.LogError(c, "list veridrop detection results failed: "+err.Error())
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
			"items":          items,
			"next_before_id": nextBeforeID,
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
