package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
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
	if out.Channel < 0 {
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

// upstreamLogAttempt 是一次带具体凭证的查询尝试。
type upstreamLogAttempt struct {
	baseURL  string
	cred     service.UpstreamLogCredential
	keyIndex int
	isMulti  bool
	// probe 标记「按请求 ID 追溯时逐个试 key」的尝试。只有这类尝试把「查询成功但 0 条」
	// 当作「不是这个 key」继续往下试；其他查询的空结果是合法答案，不回退。
	probe bool
}

// shouldTryNextCredential 判断某个失败是否值得换一套凭证再试。
//
// 只有「这套凭证拿不到日志」才回退：上游拒绝凭证、没有该接口、响应不兼容。
// 超时与并发繁忙是瞬时资源状态，换凭证只会让等待翻倍；而「查询成功但 0 条」是合法答案，
// 不在此列——把空结果当成失败去换凭证，会把「那段时间确实没有日志」变成另一套口径的结果。
func shouldTryNextCredential(err error) bool {
	return errors.Is(err, service.ErrUpstreamLogUnauthorized) ||
		errors.Is(err, service.ErrUpstreamLogUnavailable) ||
		errors.Is(err, service.ErrUpstreamLogEndpointMissing) ||
		errors.Is(err, service.ErrUpstreamLogInvalidResp)
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
	case errors.Is(err, service.ErrUpstreamLogEndpointMissing):
		return i18n.MsgUpstreamLogEndpointMissing
	case errors.Is(err, service.ErrUpstreamLogInvalidResp):
		return i18n.MsgUpstreamLogInvalidResponse
	case errors.Is(err, service.ErrUpstreamLogRateLimited):
		return i18n.MsgUpstreamLogRateLimited
	case errors.Is(err, service.ErrUpstreamLogVersionUnsupported):
		return i18n.MsgUpstreamLogVersionUnsupported
	default:
		return i18n.MsgUpstreamLogUnavailable
	}
}

// upstreamLogErrorMessage 在可执行的提示后面附上上游真实的状态码与正文片段。
//
// 只有一句「上游不可用」时，管理员无法区分是网关拦截、鉴权失败还是上游自己报错，只能
// 去翻上游日志或抓包。正文已在服务层截断并抹掉凭证，本接口又只对管理员开放，因此这里
// 如实展示。连接层失败（超时、拨号失败）没有上游响应，也就不附加——那类错误的原文里
// 带着上游地址与 dial 细节，不能外泄。
func upstreamLogErrorMessage(c *gin.Context, err error) string {
	msg := i18n.T(c, mapUpstreamLogServiceError(err))
	var detail *service.UpstreamLogHTTPError
	if !errors.As(err, &detail) {
		return msg
	}
	if detail.Snippet == "" {
		// 上游只给了状态码（空正文、或正文读失败）：状态码本身仍是有用线索，但不拖一个空冒号。
		return msg + i18n.T(c, i18n.MsgUpstreamLogUpstreamStatus, map[string]any{
			"Status": detail.StatusCode,
		})
	}
	return msg + i18n.T(c, i18n.MsgUpstreamLogUpstreamDetail, map[string]any{
		"Status": detail.StatusCode,
		"Body":   detail.Snippet,
	})
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
	// 渠道类型不参与判定：上游是否讲 new-api 的日志接口，取决于上游实现而不是本站给渠道
	// 贴的类型标签。把 new-api 网关配成 OpenAI / Gemini 兼容类型是常见做法，按类型硬拒会让
	// 本来可用的配置永远查不了。上游不支持时由 404 分支如实报不可用。
	setting := channel.GetSetting()
	channelURL := strings.TrimSpace(channel.GetBaseURL())
	accountToken := strings.TrimSpace(setting.AccountBalanceToken)
	accountUser := strings.TrimSpace(setting.AccountBalanceUserID)
	accountURL := strings.TrimSpace(setting.AccountBalanceURL)
	if accountURL == "" {
		accountURL = channelURL
	}

	// 凭证顺序：账号访问令牌优先，渠道中转密钥兜底。
	// 官方 new-api 上，账号令牌走 /api/log/self，覆盖该账号全部历史且限流宽松；中转密钥只能
	// 走 /api/log/token，只有该令牌最近 1000 条，且同一 IP 20 分钟仅 20 次。
	var attempts []upstreamLogAttempt
	var keyErr string
	if accountToken != "" && accountUser != "" && accountURL != "" {
		attempts = append(attempts, upstreamLogAttempt{
			baseURL: accountURL,
			cred: service.UpstreamLogCredential{
				Token: accountToken, APIUser: accountUser, AccountScope: true,
			},
		})
	}
	if channelURL != "" {
		probeKeys := localRequestId != "" && channel.ChannelInfo.IsMultiKey &&
			!traceKeyIndexFromLog && clientKeyIndex == nil
		if probeKeys {
			// 追溯的本站日志没记录用了哪个 key，管理员也没指定。一条日志只可能属于一个 key，
			// 所以按顺序逐个试、命中即停，而不是让追溯走进「请选择令牌序号」的死路。
			// 浏览场景不在此列：那里「第一个有数据的 key」是任意的，不能冒充整个渠道。
			for i, k := range channel.GetKeys() {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				if status, ok := channel.ChannelInfo.MultiKeyStatusList[i]; ok && status != common.ChannelStatusEnabled {
					continue // 已禁用的 key 不试
				}
				attempts = append(attempts, upstreamLogAttempt{
					baseURL:  channelURL,
					cred:     service.UpstreamLogCredential{Token: k},
					keyIndex: i,
					isMulti:  true,
					probe:    true,
				})
			}
			if len(attempts) == 0 {
				keyErr = i18n.MsgUpstreamLogKeyMissing
			}
		} else {
			// 老日志可能没有记录多令牌序号：此时允许管理员手动指定的序号兜底，
			// 而不是让追溯查询彻底走不下去（同一管理员本来就能直接按渠道+序号查询）。
			if localRequestId != "" && !traceKeyIndexFromLog && channel.ChannelInfo.IsMultiKey {
				req.KeyIndex = clientKeyIndex
			}
			key, idx, multi, errKey := selectChannelKey(channel, req.KeyIndex)
			if errKey == "" {
				attempts = append(attempts, upstreamLogAttempt{
					baseURL:  channelURL,
					cred:     service.UpstreamLogCredential{Token: key},
					keyIndex: idx,
					isMulti:  multi,
				})
			} else {
				// 多令牌渠道没选序号时不再直接报错：还有账号令牌就用它，没有才把错误抛回去。
				if localRequestId != "" && errKey == i18n.MsgUpstreamLogKeyIndexRequired {
					errKey = i18n.MsgUpstreamLogTraceKeyIndexMissing
				}
				keyErr = errKey
			}
		}
	}
	if len(attempts) == 0 {
		if keyErr != "" {
			common.ApiErrorI18n(c, keyErr)
			return
		}
		if channelURL == "" && accountURL == "" {
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogBaseURLMissing)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgUpstreamLogKeyMissing)
		return
	}

	var result *service.UpstreamLogResult
	chosen := attempts[0]
	probeHit := false
	checkedAccount := false
	// 轮询链中已经拿到的「查询成功但 0 条」。后续凭证若失败，如实答复「上游没有这条日志」，
	// 而不是报一个与答案无关的凭证错误。
	var emptyResult *service.UpstreamLogResult
	var emptyAttempt upstreamLogAttempt
	for i, attempt := range attempts {
		// 逐个试 key 可能要很多次往返：客户端已经断开就别再继续打上游。
		if ctxErr := c.Request.Context().Err(); ctxErr != nil {
			logger.LogWarn(c, "upstream log query aborted after the client went away")
			common.ApiErrorI18n(c, i18n.MsgUpstreamLogTimeout)
			return
		}
		result, err = service.QueryUpstreamLogs(c.Request.Context(), attempt.baseURL, attempt.cred, filters, page, pageSize)
		if err == nil {
			chosen = attempt
			if attempt.cred.AccountScope {
				checkedAccount = true
			}
			// 「成功但 0 条」只说明这套凭证看不到该日志：账号令牌与各把密钥的可见范围互不覆盖，
			// 都要继续往下试，不能就此断言上游没有这条日志。
			if len(result.Items) == 0 && i < len(attempts)-1 {
				emptyResult, emptyAttempt = result, attempt
				continue
			}
			probeHit = attempt.probe && len(result.Items) > 0
			break
		}
		logger.LogWarn(c, "upstream log query attempt failed for channel "+
			strings.TrimSpace(channel.Name)+": "+err.Error())
		// 上游限流按出口 IP 计数，继续试后面的密钥只会加深限流且必然同样失败。
		rateLimited := errors.Is(err, service.ErrUpstreamLogRateLimited)
		if rateLimited || i == len(attempts)-1 || !shouldTryNextCredential(err) {
			// 只对凭证类失败与限流兜底：超时、繁忙时这把 key 可能恰好有日志，不能断言「没有」。
			if emptyResult != nil && (rateLimited || shouldTryNextCredential(err)) {
				result, chosen = emptyResult, emptyAttempt
				break
			}
			logger.LogError(c, "upstream log query failed for channel "+
				strings.TrimSpace(channel.Name)+": "+err.Error())
			common.ApiErrorMsg(c, upstreamLogErrorMessage(c, err))
			return
		}
	}
	keyIndex, isMulti := chosen.keyIndex, chosen.isMulti

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
			"filters": req.Filters,
			"scope":   result.Scope,
			// 有没有用账号访问令牌查过上游全部历史。为假时「查不到」只代表该令牌最近 1000 条
			// 日志里没有，页面据此提示配置账号令牌，而不是断言上游已清理日志。
			"checked_account": checkedAccount,
			"page":            page,
			"page_size":       pageSize,
		},
		"total":      result.Total,
		"items":      result.Items,
		"elapsed_ms": result.ElapsedMs,
	}
	if source != nil {
		// 让前端能明确提示序号来自日志还是管理员手选，避免误以为查询结果一定精确对应该次请求。
		// 按请求 ID 轮询命中的 key 同样精确对应该次请求，不该提示「不一定精确」。
		source["key_index_from_log"] = traceKeyIndexFromLog || !isMulti || probeHit
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
// 返回候选渠道的最小信息，绝不返回 Base URL、Key、完整 settings 或成本配置。
//
// 不按渠道类型过滤：上游是否讲 new-api 的日志接口由上游实现决定，而不是本站给渠道贴的
// 类型标签；按 type=61 过滤会让「上游是 new-api 网关、但配成 OpenAI/Gemini 兼容类型」
// 这种常见配置在下拉框里完全消失。唯一的硬性前提是有 Base URL，否则无处可查。
func GetUpstreamLogChannels(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))

	var channels []model.Channel
	tx := model.DB.Model(&model.Channel{}).
		Select("id, name, type, status, channel_info").
		Where("base_url IS NOT NULL AND base_url != ?", "")
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
