package controller

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Only public administration metadata is selected; no channel credentials or
// routing configuration can accidentally enter the user-edit option response.
func UserChannelExclusionOptions(c *gin.Context) {
	after, err := strconv.Atoi(c.DefaultQuery("after_id", "0"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	keyword := strings.TrimSpace(c.Query("keyword"))
	if err != nil || limitErr != nil || after < 0 || limit < 1 || limit > 100 || utf8.RuneCountInString(keyword) > 128 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	query := model.DB.WithContext(ctx).Model(&model.Channel{}).Select("id", "name", "type", "status").Order("id")
	if raw := c.Query("ids"); raw != "" {
		if c.Query("after_id") != "" || keyword != "" {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		ids, err := model.ParseUserChannelExclusions("[" + strings.TrimSpace(raw) + "]")
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		query = query.Where("id IN ?", ids)
		limit = model.MaxUserChannelExclusions
	} else {
		query = query.Where("id > ?", after)
		if keyword != "" {
			pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(keyword)) + "%"
			if id, err := strconv.Atoi(strings.TrimPrefix(keyword, "#")); err == nil && id > 0 {
				query = query.Where("(id = ? OR LOWER(name) LIKE ? ESCAPE '!')", id, pattern)
			} else {
				query = query.Where("LOWER(name) LIKE ? ESCAPE '!'", pattern)
			}
		}
	}
	items := make([]struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Type   int    `json:"type"`
		Status int    `json:"status"`
	}, 0)
	if err := query.Limit(limit + 1).Scan(&items).Error; err != nil {
		logger.LogError(c, common.LogText("channel exclusion options failed: %v", err))
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	common.ApiSuccess(c, gin.H{"items": items, "next_after_id": next, "has_more": hasMore})
}
