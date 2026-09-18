package controller

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func isPaymentComplianceOptionKey(key string) bool {
	return strings.HasPrefix(key, "payment_setting.compliance_")
}

func isPositiveOptionValue(value string) bool {
	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil {
		return intValue > 0
	}
	floatValue, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && floatValue > 0
}

func isIntInRange(value string, min, max int) bool {
	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return intValue >= min && intValue <= max
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	scope := strings.TrimSpace(c.Query("scope"))
	var allowedKeys map[string]struct{}
	if scope != "" {
		var ok bool
		allowedKeys, ok = settingsaccess.OptionKeys(scope)
		if !ok {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
	}

	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.RLock()
	for k, v := range common.OptionMap {
		if k == "theme.frontend" || k == "billing_setting.billing_mode" || k == "billing_setting.billing_expr" {
			continue
		}
		if allowedKeys != nil {
			if _, allowed := allowedKeys[k]; !allowed {
				continue
			}
		}
		value := common.Interface2String(v)
		isSensitiveKey := strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key")
		if isSensitiveKey {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		if slices.Contains(completionRatioMetaOptionKeys, k) {
			optionValues[k] = value
		}
	}
	common.OptionMapRWMutex.RUnlock()
	// Display the same effective expressions used by pricing and settlement,
	// including built-in defaults absent from persisted administrator options.
	for key, values := range map[string]map[string]string{
		"billing_setting.billing_mode": billing_setting.GetBillingModeCopy(),
		"billing_setting.billing_expr": billing_setting.GetBillingExprCopy(),
	} {
		if allowedKeys != nil {
			if _, allowed := allowedKeys[key]; !allowed {
				continue
			}
		}
		encoded, err := common.Marshal(values)
		if err != nil {
			logger.LogError(c, "failed to encode billing expression options: "+err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": i18n.T(c, i18n.MsgDatabaseError)})
			return
		}
		options = append(options, &model.Option{Key: key, Value: string(encoded)})
	}
	if allowedKeys == nil {
		options = append(options, &model.Option{Key: "CompletionRatioMeta", Value: buildCompletionRatioMetaValue(optionValues)})
	} else if _, allowed := allowedKeys["CompletionRatioMeta"]; allowed {
		options = append(options, &model.Option{Key: "CompletionRatioMeta", Value: buildCompletionRatioMetaValue(optionValues)})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

func GetBusinessStatsCircuitBreakerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    model.GetBusinessStatsCircuitBreakerStatus(),
	})
}

type OptionUpdateRequest struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type OptionGroupUpdateRequest struct {
	Scope  string            `json:"scope"`
	Module string            `json:"module"`
	Values map[string]string `json:"values"`
}

func UpdateOptionGroup(c *gin.Context) {
	var request OptionGroupUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	request.Scope = strings.TrimSpace(request.Scope)
	if request.Scope != "" && !settingsaccess.AllowsGroup(request.Scope, request.Module, request.Values) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	applied, err := model.SaveConfigGroup(request.Module, request.Values)
	if err != nil {
		logger.LogError(c, "failed to update configuration group: "+err.Error())
		if !applied {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"applied": false, "apply_error": "config.apply_failed_restart_required"}})
		return
	}
	recordManageAudit(c, "option.group.update", map[string]any{"scope": request.Scope, "module": request.Module})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"applied": true}})
}

func UpdatePasskeyDomains(c *gin.Context) {
	var request struct {
		RPID                *string `json:"rp_id"`
		LegacyRPIDs         *string `json:"legacy_rp_ids"`
		Origins             *string `json:"origins"`
		Preview             bool    `json:"preview"`
		RemovalConfirmation string  `json:"removal_confirmation"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.RPID == nil || request.LegacyRPIDs == nil || request.Origins == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	change, err := model.UpdatePasskeyDomainOptions(map[string]string{
		"passkey.rp_id": *request.RPID, "passkey.legacy_rp_ids": *request.LegacyRPIDs, "passkey.origins": *request.Origins,
	}, request.Preview, request.RemovalConfirmation)
	if err != nil {
		writePasskeyDomainSettingsError(c, err)
		if !request.Preview {
			recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", err)
		}
		return
	}
	if !request.Preview {
		recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", nil)
	}
	common.ApiSuccess(c, change)
}

func writePasskeyDomainSettingsError(c *gin.Context, err error) {
	var removal *model.PasskeyDomainRemovalError
	if errors.As(err, &removal) {
		c.JSON(http.StatusConflict, gin.H{
			"success": false, "code": "PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED",
			"message": i18n.T(c, i18n.MsgPasskeyRPIDRemovalConfirmation), "data": removal.Change,
		})
		return
	}
	if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiError(c, err)
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	option.Scope = strings.TrimSpace(option.Scope)
	if option.Scope != "" && !settingsaccess.AllowsOption(option.Scope, option.Key) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "commission_tier_reset_setting.last_reset_at":
		common.ApiErrorMsg(c, "last_reset_at is maintained by the reset task and cannot be modified manually")
		return
	case "QuotaForInviter", "QuotaForInvitee":
		if isPositiveOptionValue(option.Value.(string)) && !operation_setting.IsPaymentComplianceConfirmed() {
			common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
			return
		}
	default:
		if isPaymentComplianceOptionKey(option.Key) {
			common.ApiErrorMsg(c, "合规确认字段不允许通过通用设置接口修改")
			return
		}
	}
	if option.Key == "TaskPublicAddress" && option.Value.(string) != "" {
		if err := service.ValidateTaskArtifactBaseURL(option.Value.(string)); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	// Group names disappearing from GroupRatio (or reappearing under a name that
	// was deleted before) invalidate every per-user exclusive ratio keyed by that
	// name; the list is collected here and cleaned up after the save succeeds.
	var groupRatioCleanupTargets []string
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！",
			})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})

			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && !system_setting.GetTelegramSettings().IsConfigured() {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"code":    "TELEGRAM_OAUTH_NOT_CONFIGURED",
				"message": "Telegram OAuth is not configured or enabled. Please contact your administrator.",
			})
			return
		}
	case "theme.frontend":
		if option.Value != "default" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Classic 前端已移除，主题只能设置为 default",
			})
			return
		}
	case "GroupRatio":
		// 清理计划以本节点内存里的分组表为基准，判断这次「新增 / 删除」了哪些分组；只有主节点
		// 的内存保证是最新的。从节点上保存可能把已存在的分组误判为新增，进而清掉用户专属倍率。
		if !common.IsMasterNode {
			common.ApiErrorI18n(c, i18n.MsgGroupRatioMasterRequired)
			return
		}
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		var aborted bool
		groupRatioCleanupTargets, aborted = planGroupRatioCleanup(c, option.Value.(string))
		if aborted {
			return
		}
	case "gemini.safety_settings":
		err = model_setting.ValidateGeminiSafetySettings(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "claude.default_max_tokens":
		err = model_setting.ValidateClaudeDefaultMaxTokens(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case operation_setting.ToolPriceOptionKey:
		err = operation_setting.ValidateToolPricesJSON(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "图片倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频补全倍率设置失败: " + err.Error(),
			})
			return
		}
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "缓存创建倍率设置失败: " + err.Error(),
			})
			return
		}
	case "thirdpartysd2_pricing.matrix":
		err = model_setting.ValidateThirdPartySD2PricingMatrixJSON(option.Value.(string))
		if err != nil {
			logger.LogError(c, "thirdpartysd2 pricing matrix rejected: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgThirdPartySD2PricingInvalid, map[string]any{"Detail": err.Error()})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "billing_setting.billing_expr":
		expressions := make(map[string]string)
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil {
			common.ApiErrorMsg(c, "计费表达式配置必须是模型到表达式的 JSON 对象: "+err.Error())
			return
		}
		models := make([]string, 0, len(expressions))
		for modelName := range expressions {
			models = append(models, modelName)
		}
		sort.Strings(models)
		storedVariants := billing_setting.GetPluginBillingExprCopy()
		for _, modelName := range models {
			variants := make(map[string]any)
			for key, expression := range storedVariants {
				if plugin, name, ok := billing_setting.SplitPluginBillingExprKey(key); ok && name == modelName {
					variants[plugin] = expression
				}
			}
			err = model.ValidateModelPricing(modelName, model.PricingValues{
				"billing_setting.billing_expr":          expressions[modelName],
				billing_setting.PluginBillingExprOption: variants,
			})
			if err != nil {
				common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 的计费表达式无效: %v", modelName, err))
				return
			}
		}
	case billing_setting.PluginBillingExprOption:
		var expressions map[string]string
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil || expressions == nil {
			common.ApiErrorMsg(c, "plugin billing expressions must be a JSON object")
			return
		}
		for key, expression := range expressions {
			plugin, name, valid := billing_setting.SplitPluginBillingExprKey(key)
			if !valid {
				common.ApiErrorMsg(c, "invalid plugin billing expression key: "+key)
				return
			}
			if err = model.ValidateModelPricing(name, model.PricingValues{
				billing_setting.PluginBillingExprOption: map[string]any{plugin: expression},
			}); err != nil {
				common.ApiErrorMsg(c, err.Error())
				return
			}
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "commission_tier_reset_setting.period_mode":
		mode := strings.TrimSpace(option.Value.(string))
		if mode != operation_setting.CommissionPeriodModeResetDay && mode != operation_setting.CommissionPeriodModeNaturalMonth {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "统计方式仅支持按重置日或按自然月",
			})
			return
		}
	case "commission_tier_reset_setting.reset_day":
		if !isIntInRange(option.Value.(string), 1, 31) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "重置日期必须为 1-31 之间的整数",
			})
			return
		}
	case "commission_tier_reset_setting.reset_hour":
		if !isIntInRange(option.Value.(string), 0, 23) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "重置时刻（时）必须为 0-23 之间的整数",
			})
			return
		}
	case "commission_tier_reset_setting.reset_minute", "commission_tier_reset_setting.reset_second":
		if !isIntInRange(option.Value.(string), 0, 59) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "重置时刻（分/秒）必须为 0-59 之间的整数",
			})
			return
		}
	case "commission_tier_reset_setting.timezone":
		timezone := strings.TrimSpace(option.Value.(string))
		if timezone != "Local" && timezone != "Asia/Shanghai" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "重置时区仅支持中国时区或服务器时区",
			})
			return
		}
	case "business_stats_circuit_breaker_setting.failure_threshold":
		if !isIntInRange(option.Value.(string), 1, 1000) {
			common.ApiErrorMsg(c, "failure_threshold must be between 1 and 1000")
			return
		}
	case "business_stats_circuit_breaker_setting.initial_cooldown_seconds":
		if !isIntInRange(option.Value.(string), 1, 86400) {
			common.ApiErrorMsg(c, "initial_cooldown_seconds must be between 1 and 86400")
			return
		}
	case "business_stats_circuit_breaker_setting.max_cooldown_seconds":
		if !isIntInRange(option.Value.(string), 1, 604800) {
			common.ApiErrorMsg(c, "max_cooldown_seconds must be between 1 and 604800")
			return
		}
	case "business_stats_circuit_breaker_setting.side_effect_db_timeout_ms":
		if !isIntInRange(option.Value.(string), 50, 30000) {
			common.ApiErrorMsg(c, "side_effect_db_timeout_ms must be between 50 and 30000")
			return
		}
	// 每日金额上限：单键 PUT 不会调用 setting 包里的整份校验函数，因此这里逐键校验，
	// 取值范围与 ValidateChannelDailyLimitSetting 保持一致。
	case "channel_daily_limit_setting.retention_days":
		if !isIntInRange(option.Value.(string), operation_setting.MinChannelDailyLimitRetentionDays, operation_setting.MaxChannelDailyLimitRetentionDays) {
			common.ApiErrorMsg(c, fmt.Sprintf("retention_days must be between %d and %d",
				operation_setting.MinChannelDailyLimitRetentionDays, operation_setting.MaxChannelDailyLimitRetentionDays))
			return
		}
	case "channel_daily_limit_setting.timezone":
		if err := operation_setting.ValidateChannelDailyLimitTimezone(option.Value.(string)); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	if model.IsPasskeyDomainOption(option.Key) {
		change, updateErr := model.UpdatePasskeyDomainOptions(map[string]string{option.Key: option.Value.(string)}, false, "")
		if updateErr != nil {
			writePasskeyDomainSettingsError(c, updateErr)
			recordPasskeyDomainAudit(c, change, false, updateErr)
			return
		}
		recordPasskeyDomainAudit(c, change, false, nil)
		common.ApiSuccess(c, change)
		return
	}
	if parts := strings.SplitN(option.Key, ".", 2); len(parts) == 2 && (parts[0] == "rate_limit_setting" || parts[0] == "db_pool_setting" || parts[0] == "user_session_setting" || parts[0] == "relay_timeout_setting" || parts[0] == "veridrop_monitor_setting" || parts[0] == "channel_daily_limit_setting") {
		_, err = model.SaveConfigGroup(parts[0], map[string]string{parts[1]: option.Value.(string)})
	} else {
		err = model.UpdateOption(option.Key, option.Value.(string))
	}
	if err != nil {
		if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
			writeSecurityOperationError(c, err)
		} else {
			common.ApiError(c, err)
		}
		return
	}
	// Off the request goroutine: the cleanup paces itself across the whole users
	// table and must not hold up the admin response. Repeated group edits are
	// merged into a single pass — see
	// docs/design/user-exclusive-group-ratio-deletion.md.
	model.ScheduleUserGroupRatioCleanup(groupRatioCleanupTargets)
	// 调度参数（统计方式/重置日/重置时刻/时区）变更后，把 last_reset_at 前移到新调度的
	// 最近边界，避免仅因边界被重新定义而在下一分钟补跑一次意料之外的重置。
	if isCommissionTierResetScheduleKey(option.Key) {
		service.RearmCommissionTierResetScheduleIfNeeded()
	}
	// 出于安全考虑只记录被修改的配置项名称，不记录配置值（可能含密钥等敏感信息）。
	recordManageAudit(c, "option.update", map[string]any{
		"scope": option.Scope,
		"key":   option.Key,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// isCommissionTierResetScheduleKey 判断该选项键是否属于"会改变重置调度边界"的参数。
// 注意：enabled 与 last_reset_at 不在其列——首次启用的 arm 由重置任务自身处理，
// last_reset_at 本就是被 arm 逻辑维护的目标。
func isCommissionTierResetScheduleKey(key string) bool {
	switch key {
	case "commission_tier_reset_setting.period_mode",
		"commission_tier_reset_setting.reset_day",
		"commission_tier_reset_setting.reset_hour",
		"commission_tier_reset_setting.reset_minute",
		"commission_tier_reset_setting.reset_second",
		"commission_tier_reset_setting.timezone":
		return true
	default:
		return false
	}
}

// planGroupRatioCleanup diffs the submitted GroupRatio against the live registry
// and returns the group names whose per-user exclusive ratios are no longer
// valid: names being removed, plus names being added.
//
// Added names matter because group ratios change over time — a recreated "vip"
// is not the same pricing object as the deleted one, so ratios negotiated
// against the old pricing must not come back with the name.
//
// Deleting a group that still has enabled channels is rejected outright: the
// group would keep serving traffic with no price attached. Reports whether the
// request has already been answered.
func planGroupRatioCleanup(c *gin.Context, value string) ([]string, bool) {
	submitted := make(map[string]float64)
	if err := common.UnmarshalJsonStr(value, &submitted); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return nil, true
	}
	current := ratio_setting.GetGroupRatioCopy()

	var removed, added []string
	for name := range current {
		if _, ok := submitted[name]; !ok {
			removed = append(removed, name)
		}
	}
	for name := range submitted {
		if _, ok := current[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)

	// Re-creating a name whose cleanup is still running would let the old
	// exclusive ratios — still in users' rows until the scan reaches them — bill
	// the new group. Refuse until the cleanup has finished (usually seconds).
	if busy := activeGroupRatioCleanups(added); len(busy) > 0 {
		common.ApiErrorI18n(c, i18n.MsgGroupRecreateCleanupInProgress, map[string]any{
			"Groups": strings.Join(busy, ", "),
		})
		return nil, true
	}

	if len(removed) > 0 {
		blocked, err := model.GroupsWithEnabledChannels(removed)
		if err != nil {
			logger.LogError(c, "failed to check enabled channels before group deletion: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgGroupDeleteChannelCheckFailed)
			return nil, true
		}
		if len(blocked) > 0 {
			common.ApiErrorI18n(c, i18n.MsgGroupDeleteHasEnabledChannels, map[string]any{
				"Groups": strings.Join(blocked, ", "),
			})
			return nil, true
		}
	}
	return append(removed, added...), false
}
