package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func buildMaskedTokenResponse(token *model.Token) *model.Token {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	return &maskedToken
}

func buildMaskedTokenResponses(tokens []*model.Token) []*model.Token {
	maskedTokens := make([]*model.Token, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func buildGroupStatusInfo(groupName string) (dto.GroupStatusInfo, bool) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return dto.GroupStatusInfo{}, false
	}

	enabledModels := model.GetGroupEnabledModels(groupName)
	totalModels := len(enabledModels)
	modelStatuses, _ := model.GetModelStatusesByGroup(groupName)
	availableModels := 0
	for _, modelName := range enabledModels {
		if available, ok := modelStatuses[modelName]; !ok || available {
			availableModels++
		}
	}

	lastTestTime := int64(0)
	if groupStatus, err := model.GetGroupStatus(groupName); err == nil && groupStatus != nil {
		if groupStatus.TotalModels > totalModels {
			totalModels = groupStatus.TotalModels
		}
		if groupStatus.AvailableModels > availableModels {
			availableModels = groupStatus.AvailableModels
		}
		lastTestTime = groupStatus.LastTestTime
	}

	availabilityRate := 0.0
	if totalModels > 0 {
		availabilityRate = float64(availableModels) / float64(totalModels) * 100
	}

	return dto.GroupStatusInfo{
		AvailableModels:  availableModels,
		TotalModels:      totalModels,
		AvailabilityRate: availabilityRate,
		LastTestTime:     lastTestTime,
	}, true
}

func buildAggregateGroupStatusInfo(groupNames []string) (dto.GroupStatusInfo, bool) {
	seen := make(map[string]bool)
	result := dto.GroupStatusInfo{}
	found := false
	for _, groupName := range groupNames {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" || seen[groupName] {
			continue
		}
		seen[groupName] = true
		info, ok := buildGroupStatusInfo(groupName)
		if !ok {
			continue
		}
		found = true
		result.AvailableModels += info.AvailableModels
		result.TotalModels += info.TotalModels
		if info.LastTestTime > result.LastTestTime {
			result.LastTestTime = info.LastTestTime
		}
	}
	if result.TotalModels > 0 {
		result.AvailabilityRate = float64(result.AvailableModels) / float64(result.TotalModels) * 100
	}
	return result, found
}

func buildTokenGroupStatusInfo(tokenGroup string, userGroup string) (dto.GroupStatusInfo, bool) {
	if tokenGroup == "auto" {
		return buildAggregateGroupStatusInfo(service.GetUserAutoGroup(userGroup))
	}
	return buildGroupStatusInfo(tokenGroup)
}

func buildTokensWithGroupStatus(tokens []*model.Token, userGroup string) []gin.H {
	if len(tokens) == 0 {
		return []gin.H{}
	}

	groupMap := make(map[string]dto.GroupStatusInfo)
	for _, token := range tokens {
		if token.Group == "" {
			continue
		}
		if _, exists := groupMap[token.Group]; exists {
			continue
		}
		if groupStatus, ok := buildTokenGroupStatusInfo(token.Group, userGroup); ok {
			groupMap[token.Group] = groupStatus
		}
	}

	result := make([]gin.H, 0, len(tokens))
	for _, token := range tokens {
		maskedToken := buildMaskedTokenResponse(token)
		item := gin.H{
			"id":                   maskedToken.Id,
			"user_id":              maskedToken.UserId,
			"key":                  maskedToken.Key,
			"status":               maskedToken.Status,
			"name":                 maskedToken.Name,
			"created_time":         maskedToken.CreatedTime,
			"accessed_time":        maskedToken.AccessedTime,
			"expired_time":         maskedToken.ExpiredTime,
			"remain_quota":         maskedToken.RemainQuota,
			"unlimited_quota":      maskedToken.UnlimitedQuota,
			"used_quota":           maskedToken.UsedQuota,
			"group":                maskedToken.Group,
			"model_limits_enabled": maskedToken.ModelLimitsEnabled,
			"model_limits":         maskedToken.ModelLimits,
			"allow_ips":            maskedToken.AllowIps,
			"cross_group_retry":    maskedToken.CrossGroupRetry,
		}
		if groupStatus, ok := groupMap[token.Group]; ok {
			item["group_status"] = groupStatus
		}
		result = append(result, item)
	}
	return result
}

func GetAllTokens(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserTokens(userId)
	pageInfo.SetTotal(int(total))
	userGroup, _ := model.GetUserGroup(userId, false)
	pageInfo.SetItems(buildTokensWithGroupStatus(tokens, userGroup))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	userId := c.GetInt("id")
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(userId, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	userGroup, _ := model.GetUserGroup(userId, false)
	pageInfo.SetItems(buildTokensWithGroupStatus(tokens, userGroup))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	token, err := model.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), false)
	if err != nil {
		common.SysError("failed to get token by key: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":               "token_usage",
			"name":                 token.Name,
			"total_granted":        token.RemainQuota + token.UsedQuota,
			"total_used":           token.UsedQuota,
			"total_available":      token.RemainQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits":         token.GetModelLimitsMap(),
			"model_limits_enabled": token.ModelLimitsEnabled,
			"expires_at":           expiredAt,
		},
	})
}

func AddToken(c *gin.Context) {
	token := model.Token{}
	err := c.ShouldBindJSON(&token)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	// Validate quota range when quota is limited.
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := int((1000000000 * common.QuotaPerUnit))
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("已达到最大令牌数量限制 (%d)", maxTokens),
		})
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		common.SysLog("failed to generate token key: " + err.Error())
		return
	}
	cleanToken := model.Token{
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
	}
	err = cleanToken.Insert()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	err := model.DeleteTokenById(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	token := model.Token{}
	err := c.ShouldBindJSON(&token)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := int((1000000000 * common.QuotaPerUnit))
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	cleanToken, err := model.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotEnable)
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotEable)
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
	}
	err = cleanToken.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(tokenBatch.Ids) > 100 {
		common.ApiErrorI18n(c, i18n.MsgBatchTooMany, map[string]any{"Max": 100})
		return
	}
	userId := c.GetInt("id")
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
	}
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}

func GetAvailableModelsByGroup(c *gin.Context) {
	userGroup := c.Param("group")
	if userGroup == "" {
		common.ApiError(c, fmt.Errorf("group parameter is required"))
		return
	}
	channels, err := model.GetChannelsByGroup(userGroup)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	modelSet := make(map[string]bool)
	windowStart := time.Now().Unix() - 30*60
	for _, ch := range channels {
		for _, modelName := range ch.GetModels() {
			if modelName == "" {
				continue
			}

			var count int64
			model.LOG_DB.Model(&model.Log{}).
				Where(&model.Log{
					ModelName: modelName,
					Group:     userGroup,
					Type:      model.LogTypeConsume,
				}).
				Where("created_at >= ?", windowStart).
				Count(&count)

			if count > 0 {
				modelSet[modelName] = true
			}
		}
	}

	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}

	common.ApiSuccess(c, dto.AvailableModelsResponse{
		Models: models,
		Count:  len(models),
	})
}

func GetGroupStatuses(c *gin.Context) {
	type GroupStatusResponse struct {
		UserGroup        string  `json:"user_group"`
		AvailableModels  int     `json:"available_models"`
		TotalModels      int     `json:"total_models"`
		AvailabilityRate float64 `json:"availability_rate"`
		LastTestTime     int64   `json:"last_test_time"`
	}

	groupNames := make(map[string]bool)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames[groupName] = true
	}
	groups, err := model.GetAllGroupStatuses()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, g := range groups {
		groupNames[g.UserGroup] = true
	}

	result := make([]GroupStatusResponse, 0, len(groupNames)+1)
	for groupName := range groupNames {
		info, ok := buildGroupStatusInfo(groupName)
		if !ok {
			continue
		}
		result = append(result, GroupStatusResponse{
			UserGroup:        groupName,
			AvailableModels:  info.AvailableModels,
			TotalModels:      info.TotalModels,
			AvailabilityRate: info.AvailabilityRate,
			LastTestTime:     info.LastTestTime,
		})
	}

	allGroups := make([]string, 0, len(groupNames))
	for groupName := range groupNames {
		allGroups = append(allGroups, groupName)
	}
	if autoInfo, ok := buildAggregateGroupStatusInfo(allGroups); ok {
		result = append(result, GroupStatusResponse{
			UserGroup:        "auto",
			AvailableModels:  autoInfo.AvailableModels,
			TotalModels:      autoInfo.TotalModels,
			AvailabilityRate: autoInfo.AvailabilityRate,
			LastTestTime:     autoInfo.LastTestTime,
		})
	}

	common.ApiSuccess(c, gin.H{
		"groups": result,
		"count":  len(result),
	})
}
