package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/singleflight"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	AutoGroups tokenAutoGroupsInput `json:"auto_groups"`
}

type tokenResponse struct {
	*model.Token
	AutoGroups []string `json:"auto_groups"`
}

func maxTokenQuota() int {
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(1_000_000_000).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return common.MaxWalletQuota
	}
	return quota
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(fmt.Sprintf("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	return &tokenResponse{Token: &maskedToken, AutoGroups: autoGroups}
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func getTokenRequestUserGroup(c *gin.Context) (string, error) {
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func setTokenAutoGroups(c *gin.Context, token *model.Token, groups []string) bool {
	if len(groups) == 0 {
		if err := token.SetAutoGroups(nil); err != nil {
			common.ApiError(c, err)
			return false
		}
		return true
	}

	maxCount := setting.GetMaxTokenAutoGroups()
	if len(groups) > maxCount {
		common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsTooMany, map[string]any{"Max": maxCount})
		return false
	}

	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsDuplicate, map[string]any{"Group": group})
			return false
		}
		seen[group] = struct{}{}
		if !service.IsUserSelectableGroup(userGroup, group) {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsInvalid, map[string]any{"Group": group})
			return false
		}
	}

	if err := token.SetAutoGroups(groups); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
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
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
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
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
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

func GetTokenAutoGroups(c *gin.Context) {
	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":    service.GetUserAutoGroup(userGroup),
		"max_count": setting.GetMaxTokenAutoGroups(),
	})
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
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
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
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	params := tokenAuditParams(c)
	params["name"] = token.Name
	// Validate quota range when quota is limited.
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := maxTokenQuota()
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
	if token.Group == "auto" {
		if !setTokenAutoGroups(c, &token, request.AutoGroups.Groups) {
			return
		}
	} else {
		token.CrossGroupRetry = false
		_ = token.SetAutoGroups(nil)
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
		AutoGroups:         token.AutoGroups,
	}
	err = cleanToken.Insert()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["id"] = cleanToken.Id
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	err = token.Delete()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	params := tokenAuditParams(c)
	if token.Id > 0 {
		params["id"] = token.Id
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
		maxQuotaValue := maxTokenQuota()
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
	params["name"] = cleanToken.Name
	previous := *cleanToken
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
		if token.Group != "auto" {
			cleanToken.CrossGroupRetry = false
			_ = cleanToken.SetAutoGroups(nil)
		} else if request.AutoGroups.Set {
			if !setTokenAutoGroups(c, cleanToken, request.AutoGroups.Groups) {
				return
			}
		}
	}
	err = cleanToken.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	if statusOnly != "" {
		params["from"], params["to"] = previous.Status, cleanToken.Status
	} else {
		changedFields := []string{}
		for _, field := range []struct {
			name    string
			changed bool
		}{
			{"name", previous.Name != cleanToken.Name},
			{"expired_time", previous.ExpiredTime != cleanToken.ExpiredTime},
			{"remain_quota", previous.RemainQuota != cleanToken.RemainQuota},
			{"unlimited_quota", previous.UnlimitedQuota != cleanToken.UnlimitedQuota},
			{"model_limits_enabled", previous.ModelLimitsEnabled != cleanToken.ModelLimitsEnabled},
			{"model_limits", previous.ModelLimits != cleanToken.ModelLimits},
			{"allow_ips", (previous.AllowIps == nil) != (cleanToken.AllowIps == nil) ||
				(previous.AllowIps != nil && cleanToken.AllowIps != nil && *previous.AllowIps != *cleanToken.AllowIps)},
			{"group", previous.Group != cleanToken.Group},
			{"cross_group_retry", previous.CrossGroupRetry != cleanToken.CrossGroupRetry},
			{"auto_groups", previous.AutoGroups != cleanToken.AutoGroups},
		} {
			if field.changed {
				changedFields = append(changedFields, field.name)
			}
		}
		params["changed_fields"] = changedFields
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
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
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["count"] = count
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
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
	returnedIDs := make([]int, 0, len(tokens))
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
		returnedIDs = append(returnedIDs, t.Id)
	}
	params["count"] = len(tokens)
	params["returned_ids"] = returnedIDs
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}

// availableModelsWindowSeconds is how far back a consume log proves a model of
// the group is currently serving traffic.
const availableModelsWindowSeconds = 30 * 60

// The DISTINCT over the last 30 minutes of logs has no fitting index (only
// idx_created_at_type and single-column group / model_name), scans every
// consume row of the window (~900k at 30k RPM) and runs on LOG_DB, which the
// async relay log writer shares. Any logged-in user can call the endpoint, so
// results are cached per group: at most one query per group per TTL on this
// node, and concurrent misses for the same group share that one query.
// A covering index is deliberately not added: building one needs a migration
// on the large logs table, and the query is already bounded to at most once
// per group per node per minute.
const (
	availableModelsCacheTTL = 60 * time.Second
	// availableModelsCacheMaxGroups bounds the map: administrators may ask about
	// arbitrary group names. Real deployments have far fewer groups.
	availableModelsCacheMaxGroups = 1024
)

type availableModelsCacheEntry struct {
	models    []string // sorted, read-only once cached
	expiresAt time.Time
}

var (
	availableModelsCacheMu sync.Mutex
	availableModelsCache   = make(map[string]availableModelsCacheEntry)
	availableModelsFlight  singleflight.Group
	// Swapped by tests.
	availableModelsNow    = time.Now
	loadAvailableModelsFn = loadAvailableModels
)

// GetAvailableModelsByGroup lists the models of a group's channels that had
// consume traffic in that group during the last 30 minutes (cached for up to
// availableModelsCacheTTL). It runs behind UserAuth: administrators may ask
// about any group, other users only about groups they are allowed to use, so
// hidden groups cannot be enumerated. The permission check runs before the
// cache is consulted.
func GetAvailableModelsByGroup(c *gin.Context) {
	group := strings.TrimSpace(c.Param("group"))
	if group == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if c.GetInt("role") < common.RoleAdminUser && !service.GroupInUserUsableGroups(c.GetString("group"), group) {
		common.ApiErrorI18n(c, i18n.MsgDistributorGroupAccessDenied)
		return
	}
	models, err := availableModelsForGroup(group)
	if err != nil {
		logger.LogError(c, "failed to load recent models for group: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	common.ApiSuccess(c, dto.AvailableModelsResponse{
		Models: models,
		Count:  len(models),
	})
}

// availableModelsForGroup serves the group's list from the cache, loading it
// at most once per concurrent miss. Errors are not cached.
func availableModelsForGroup(group string) ([]string, error) {
	if models, ok := cachedAvailableModels(group); ok {
		return models, nil
	}
	v, err, _ := availableModelsFlight.Do(group, func() (any, error) {
		// A flight that finished just before this one started has already filled it.
		if models, ok := cachedAvailableModels(group); ok {
			return models, nil
		}
		models, err := loadAvailableModelsFn(group)
		if err != nil {
			return nil, err
		}
		storeAvailableModels(group, models)
		return models, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]string), nil
}

func cachedAvailableModels(group string) ([]string, bool) {
	availableModelsCacheMu.Lock()
	defer availableModelsCacheMu.Unlock()
	entry, ok := availableModelsCache[group]
	if !ok || !availableModelsNow().Before(entry.expiresAt) {
		return nil, false
	}
	return entry.models, true
}

func storeAvailableModels(group string, models []string) {
	now := availableModelsNow()
	availableModelsCacheMu.Lock()
	defer availableModelsCacheMu.Unlock()
	if _, exists := availableModelsCache[group]; !exists && len(availableModelsCache) >= availableModelsCacheMaxGroups {
		for name, entry := range availableModelsCache {
			if !now.Before(entry.expiresAt) {
				delete(availableModelsCache, name)
			}
		}
		// Still full of live entries: drop an arbitrary one rather than grow.
		for name := range availableModelsCache {
			if len(availableModelsCache) < availableModelsCacheMaxGroups {
				break
			}
			delete(availableModelsCache, name)
		}
	}
	availableModelsCache[group] = availableModelsCacheEntry{models: models, expiresAt: now.Add(availableModelsCacheTTL)}
}

// loadAvailableModels runs the uncached lookup: the group's channel models,
// narrowed by one time-bounded DISTINCT over consume logs. Returns a sorted,
// non-nil slice.
func loadAvailableModels(group string) ([]string, error) {
	channels, err := model.GetChannelsByGroup(group)
	if err != nil {
		return nil, fmt.Errorf("load channels: %w", err)
	}
	candidateSet := make(map[string]struct{})
	for _, ch := range channels {
		for _, modelName := range ch.GetModels() {
			if modelName != "" {
				candidateSet[modelName] = struct{}{}
			}
		}
	}
	models := make([]string, 0)
	if len(candidateSet) > 0 {
		candidates := make([]string, 0, len(candidateSet))
		for modelName := range candidateSet {
			candidates = append(candidates, modelName)
		}
		// One time-bounded DISTINCT query instead of a COUNT per channel x model.
		err = model.LOG_DB.Model(&model.Log{}).
			Where(&model.Log{Group: group, Type: model.LogTypeConsume}).
			Where("created_at >= ?", availableModelsNow().Unix()-availableModelsWindowSeconds).
			Where("model_name IN ?", candidates).
			Distinct("model_name").
			Pluck("model_name", &models).Error
		if err != nil {
			return nil, fmt.Errorf("query recent models: %w", err)
		}
	}
	sort.Strings(models)
	return models, nil
}
