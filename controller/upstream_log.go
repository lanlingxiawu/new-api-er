package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// upstreamLogFiltersDTO 复用现有日志查询语义的筛选字段。
type upstreamLogFiltersDTO struct {
	Type              int    `json:"type"`
	Username          string `json:"username"`
	TokenName         string `json:"token_name"`
	ModelName         string `json:"model_name"`
	StartTimestamp    int64  `json:"start_timestamp"`
	EndTimestamp      int64  `json:"end_timestamp"`
	Channel           int    `json:"channel"`
	LogId             int    `json:"log_id"`
	Group             string `json:"group"`
	RequestId         string `json:"request_id"`
	UpstreamRequestId string `json:"upstream_request_id"`
}

// upstreamLogQueryRequest 是 POST /api/log/upstream/query 的请求体。
type upstreamLogQueryRequest struct {
	LocalRequestId string                `json:"local_request_id"`
	ChannelId      int                   `json:"channel_id"`
	KeyIndex       *int                  `json:"key_index"`
	Page           int                   `json:"page"`
	PageSize       int                   `json:"page_size"`
	Filters        upstreamLogFiltersDTO `json:"filters"`
}

const (
	upstreamLogMaxShort = 64
	upstreamLogMaxModel = 128
	upstreamLogMaxReqId = 128
)

// hasControlChars 检测字符串是否包含控制字符，防止把非法输入透传到上游。
func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func normalizeLocalRequestId(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || hasControlChars(value) {
		return "", false
	}
	return value, true
}

// normalizeAndValidateFilters 复制 allowlist 字段、trim、限长并拒绝控制字符。
func normalizeAndValidateFilters(in upstreamLogFiltersDTO) (service.UpstreamLogFilters, bool) {
	trimLimit := func(s string, max int) (string, bool) {
		s = strings.TrimSpace(s)
		if len(s) > max || hasControlChars(s) {
			return "", false
		}
		return s, true
	}
	out := service.UpstreamLogFilters{
		Type:           in.Type,
		StartTimestamp: in.StartTimestamp,
		EndTimestamp:   in.EndTimestamp,
		Channel:        in.Channel,
		LogId:          in.LogId,
	}
	ok := true
	var good bool
	if out.Username, good = trimLimit(in.Username, upstreamLogMaxShort); !good {
		ok = false
	}
	if out.TokenName, good = trimLimit(in.TokenName, upstreamLogMaxShort); !good {
		ok = false
	}
	if out.ModelName, good = trimLimit(in.ModelName, upstreamLogMaxModel); !good {
		ok = false
	}
	if out.Group, good = trimLimit(in.Group, upstreamLogMaxShort); !good {
		ok = false
	}
	if out.RequestId, good = trimLimit(in.RequestId, upstreamLogMaxReqId); !good {
		ok = false
	}
	if out.UpstreamRequestId, good = trimLimit(in.UpstreamRequestId, upstreamLogMaxReqId); !good {
		ok = false
	}
	if out.LogId < 0 || out.Channel < 0 {
		ok = false
	}
	if out.StartTimestamp != 0 && out.EndTimestamp != 0 && out.StartTimestamp > out.EndTimestamp {
		ok = false
	}
	return out, ok
}

// selectChannelKey 按 key_index 选择渠道令牌；多令牌必须提供明确序号，不触发轮询。
// 返回选中的 key、实际序号、是否多令牌、以及可能的 i18n 错误键。
func selectChannelKey(channel *model.Channel, keyIndex *int) (key string, index int, isMulti bool, errKey string) {
	if channel.ChannelInfo.IsMultiKey {
		keys := channel.GetKeys()
		if len(keys) == 0 {
			return "", 0, true, i18n.MsgUpstreamLogKeyMissing
		}
		if keyIndex == nil {
			return "", 0, true, i18n.MsgUpstreamLogKeyIndexRequired
		}
		idx := *keyIndex
		if idx < 0 || idx >= len(keys) {
			return "", 0, true, i18n.MsgUpstreamLogKeyIndexInvalid
		}
		selected := strings.TrimSpace(keys[idx])
		if selected == "" {
			return "", 0, true, i18n.MsgUpstreamLogKeyMissing
		}
		return selected, idx, true, ""
	}
	// 单令牌：key_index 必须省略或为 0。
	if keyIndex != nil && *keyIndex != 0 {
		return "", 0, false, i18n.MsgUpstreamLogKeyIndexInvalid
	}
	if strings.TrimSpace(channel.Key) == "" {
		return "", 0, false, i18n.MsgUpstreamLogKeyMissing
	}
	return channel.Key, 0, false, ""
}

// mapUpstreamLogServiceError 把服务层错误映射为用户可执行的 i18n 键。
func mapUpstreamLogServiceError(err error) string {
	switch {
	case errors.Is(err, service.ErrUpstreamLogBaseURLMissing):
		return i18n.MsgUpstreamLogBaseURLMissing
	case errors.Is(err, service.ErrUpstreamLogBaseURLInvalid):
		return i18n.MsgUpstreamLogBaseURLMissing
	case errors.Is(err, service.ErrUpstreamLogBusy):
		return i18n.MsgUpstreamLogBusy
	case errors.Is(err, service.ErrUpstreamLogTimeout):
		return i18n.MsgUpstreamLogTimeout
	case errors.Is(err, service.ErrUpstreamLogUnauthorized):
		return i18n.MsgUpstreamLogUnauthorized
	case errors.Is(err, service.ErrUpstreamLogUnavailable):
		return i18n.MsgUpstreamLogUnavailable
	case errors.Is(err, service.ErrUpstreamLogInvalidResp):
		return i18n.MsgUpstreamLogInvalidResponse
	default:
		return i18n.MsgUpstreamLogUnavailable
	}
}

// QueryUpstreamLog 处理 POST /api/log/upstream/query（AdminAuth）。
func QueryUpstreamLog(c *gin.Context) {
	var req upstreamLogQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogInvalidParams)
		return
	}
	localRequestId := strings.TrimSpace(req.LocalRequestId)
	// 追溯模式下客户端提供的令牌序号只在本站日志没有记录时兜底，绝不覆盖日志记录值。
	clientKeyIndex := req.KeyIndex
	traceKeyIndexFromLog := false
	var source gin.H
	if localRequestId != "" {
		var valid bool
		if localRequestId, valid = normalizeLocalRequestId(req.LocalRequestId); !valid {
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogInvalidParams)
			return
		}
		trace, err := model.GetLogTraceByRequestId(localRequestId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				common.ApiErrorI18n(c, i18n.MsgUpstreamLogLocalRequestNotFound)
				return
			}
			logger.LogError(c, "failed to resolve local request for upstream log query: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogUnavailable)
			return
		}
		upstreamRequestId := strings.TrimSpace(trace.UpstreamRequestId)
		if upstreamRequestId == "" {
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogRequestIdMissing)
			return
		}
		if len(upstreamRequestId) > upstreamLogMaxReqId || hasControlChars(upstreamRequestId) {
			logger.LogWarn(c, "local log contains an invalid upstream request id")
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogRequestIdMissing)
			return
		}

		req.ChannelId = trace.ChannelId
		req.KeyIndex = nil
		req.Filters = upstreamLogFiltersDTO{RequestId: upstreamRequestId}
		if trace.Other != "" {
			var other struct {
				AdminInfo struct {
					MultiKeyIndex *int `json:"multi_key_index"`
				} `json:"admin_info"`
			}
			if err := common.UnmarshalJsonStr(trace.Other, &other); err != nil {
				logger.LogWarn(c, "local log metadata is invalid while resolving upstream log query")
			} else if other.AdminInfo.MultiKeyIndex != nil {
				req.KeyIndex = other.AdminInfo.MultiKeyIndex
				traceKeyIndexFromLog = true
			}
		}
		source = gin.H{
			"request_id":          localRequestId,
			"upstream_request_id": upstreamRequestId,
		}
	}
	if req.ChannelId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogInvalidParams)
		return
	}
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	filters, ok := normalizeAndValidateFilters(req.Filters)
	if !ok {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogInvalidParams)
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogChannelNotFound)
		return
	}
	if channel.Type != constant.ChannelTypeNewAPI {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogUnsupportedChannelType)
		return
	}

	baseURL := strings.TrimSpace(channel.GetBaseURL())
	if baseURL == "" {
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogBaseURLMissing)
		return
	}

	// 老日志可能没有记录多令牌序号：此时允许管理员手动指定的序号兜底，
	// 而不是让追溯查询彻底走不下去（同一管理员本来就能直接按渠道+序号查询）。
	if localRequestId != "" && !traceKeyIndexFromLog && channel.ChannelInfo.IsMultiKey {
		req.KeyIndex = clientKeyIndex
	}

	key, keyIndex, isMulti, errKey := selectChannelKey(channel, req.KeyIndex)
	if errKey != "" {
		if localRequestId != "" && errKey == i18n.MsgUpstreamLogKeyIndexRequired {
			errKey = i18n.MsgUpstreamLogTraceKeyIndexMissing
		}
		common.ApiErrorI18n(c, errKey)
		return
	}

	result, err := service.QueryUpstreamLogs(c.Request.Context(), baseURL, key, filters, page, pageSize)
	if err != nil {
		logger.LogError(c, "upstream log query failed for channel "+
			strings.TrimSpace(channel.Name)+": "+err.Error())
		common.ApiErrorI18n(c, mapUpstreamLogServiceError(err))
		return
	}

	// 审计：只记录管理员 ID、渠道 ID、令牌序号、是否精确、结果状态；绝不记录令牌。
	logger.LogInfo(c, "upstream log query ok: channel="+
		strings.TrimSpace(channel.Name)+" scope="+result.Scope)

	response := gin.H{
		"channel": gin.H{
			"id":           channel.Id,
			"name":         channel.Name,
			"type":         channel.Type,
			"key_index":    keyIndex,
			"is_multi_key": isMulti,
		},
		"query": gin.H{
			"filters":                 req.Filters,
			"scope":                   result.Scope,
			"upstream_supports_exact": result.SupportsExact,
			"page":                    page,
			"page_size":               pageSize,
		},
		"total":      result.Total,
		"items":      result.Items,
		"elapsed_ms": result.ElapsedMs,
	}
	if source != nil {
		// 让前端能明确提示序号来自日志还是管理员手选，避免误以为查询结果一定精确对应该次请求。
		source["key_index_from_log"] = traceKeyIndexFromLog || !isMulti
		response["source"] = source
	}
	common.ApiSuccess(c, response)
}

// upstreamLogChannelOption 是目标渠道选择器所需的最小渠道信息。
type upstreamLogChannelOption struct {
	Id         int    `json:"id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	IsMultiKey bool   `json:"is_multi_key"`
	KeyCount   int    `json:"key_count"`
	Status     int    `json:"status"`
}

// GetUpstreamLogChannels 处理 GET /api/log/upstream/channels（AdminAuth）。
// 只返回 New API 渠道的最小信息，绝不返回 Base URL、Key、完整 settings 或成本配置。
func GetUpstreamLogChannels(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))

	var channels []model.Channel
	tx := model.DB.Model(&model.Channel{}).
		Select("id, name, type, status, channel_info").
		Where("type = ?", constant.ChannelTypeNewAPI)
	if keyword != "" {
		if id := common.String2Int(keyword); id > 0 {
			tx = tx.Where("id = ? OR name LIKE ?", id, "%"+keyword+"%")
		} else {
			tx = tx.Where("name LIKE ?", "%"+keyword+"%")
		}
	}
	if err := tx.Order("id desc").Limit(50).Find(&channels).Error; err != nil {
		logger.LogError(c, "failed to list upstream log channels: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogUnavailable)
		return
	}

	options := make([]upstreamLogChannelOption, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		keyCount := 1
		if ch.ChannelInfo.IsMultiKey && ch.ChannelInfo.MultiKeySize > 0 {
			keyCount = ch.ChannelInfo.MultiKeySize
		}
		options = append(options, upstreamLogChannelOption{
			Id:         ch.Id,
			Name:       ch.Name,
			Type:       ch.Type,
			IsMultiKey: ch.ChannelInfo.IsMultiKey,
			KeyCount:   keyCount,
			Status:     ch.Status,
		})
	}
	common.ApiSuccess(c, options)
}
