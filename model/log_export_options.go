package model

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type LogExportFilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// LogExportChannelOptions searches channel metadata rather than sampled logs.
// A numeric keyword also remains usable for historical logs after channel deletion.
func LogExportChannelOptions(ctx context.Context, keyword, cursor string) ([]LogExportFilterOption, string, error) {
	after := 0
	if cursor != "" {
		var err error
		after, err = strconv.Atoi(cursor)
		if err != nil || after < 0 {
			return nil, "", ErrEmployeeExportInvalid
		}
	}
	keyword = strings.TrimSpace(keyword)
	query := DB.WithContext(ctx).Model(&Channel{}).Select("id, name").Where("id > ?", after).Order("id")
	items := make([]LogExportFilterOption, 0, 50)
	if keyword != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(keyword)) + "%"
		query = query.Where("LOWER(name) LIKE ? ESCAPE '!'", pattern)
	}
	id, idErr := strconv.Atoi(strings.TrimPrefix(keyword, "#"))
	if idErr == nil && id > 0 {
		query = query.Where("id <> ?", id)
	}
	if idErr == nil && id > 0 && after == 0 {
		// Resolve the exact ID first so name matches cannot push it off page one.
		var channel Channel
		if err := DB.WithContext(ctx).Select("id, name").Where("id = ?", id).Find(&channel).Error; err != nil {
			return nil, "", err
		}
		value := strconv.Itoa(id)
		label := channel.Name
		if label == "" {
			label = value
		}
		items = append(items, LogExportFilterOption{Value: value, Label: label})
	}
	limit := 50 - len(items)
	var channels []struct {
		Id   int
		Name string
	}
	if err := query.Limit(limit + 1).Find(&channels).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(channels) > limit {
		channels = channels[:limit]
		next = strconv.Itoa(channels[limit-1].Id)
	}
	for _, channel := range channels {
		value := strconv.Itoa(channel.Id)
		label := channel.Name
		if label == "" {
			label = value
		}
		items = append(items, LogExportFilterOption{Value: value, Label: label})
	}
	return items, next, nil
}

// LogExportFilterOptions samples a bounded keyset page, never a full-table DISTINCT.
func LogExportFilterOptions(ctx context.Context, filter LogExportFilter, field, keyword, cursor string, customerIDs []int) ([]LogExportFilterOption, string, error) {
	fields := map[string]string{"model_name": "model_name", "token_name": "token_name", "group": logGroupCol, "channel": "channel_id"}
	column, ok := fields[field]
	if !ok {
		return nil, "", ErrEmployeeExportInvalid
	}
	var after *logExportCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", ErrEmployeeExportInvalid
		}
		after = &logExportCursor{}
		if err := common.Unmarshal(raw, after); err != nil {
			return nil, "", ErrEmployeeExportInvalid
		}
	}
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).Where("logs.created_at >= ? AND logs.created_at <= ?", filter.StartTimestamp, filter.EndTimestamp)
	if filter.LogType != 0 {
		tx = tx.Where("logs.type = ?", filter.LogType)
	}
	if customerIDs != nil {
		if len(customerIDs) == 0 {
			return []LogExportFilterOption{}, "", nil
		}
		tx = tx.Where("logs.user_id IN ?", customerIDs)
	}
	// Search within the candidate page below. Applying LIKE before LIMIT makes
	// a missing keyword read the entire history window without a next cursor.
	order := "logs.created_at, logs.id"
	selected := "logs.id, logs.created_at, logs." + column
	if usingClickHouseLogDB() {
		order = "logs.created_at, logs.request_id"
		selected += ", logs.request_id"
	}
	if after != nil {
		if usingClickHouseLogDB() {
			tx = tx.Where("logs.created_at > ? OR (logs.created_at = ? AND logs.request_id > ?)", after.CreatedAt, after.CreatedAt, after.RequestId)
		} else {
			tx = tx.Where("logs.created_at > ? OR (logs.created_at = ? AND logs.id > ?)", after.CreatedAt, after.CreatedAt, after.Id)
		}
	}
	var logs []*Log
	if err := tx.Select(selected).Order(order).Limit(500).Find(&logs).Error; err != nil {
		return nil, "", err
	}
	items := []LogExportFilterOption{}
	seen := map[string]bool{}
	for _, log := range logs {
		value := log.ModelName
		switch field {
		case "token_name":
			value = log.TokenName
		case "group":
			value = log.Group
		case "channel":
			value = strconv.Itoa(log.ChannelId)
		}
		if value != "" && !seen[value] && strings.Contains(strings.ToLower(value), strings.ToLower(keyword)) {
			items = append(items, LogExportFilterOption{Value: value, Label: value})
			seen[value] = true
		}
	}
	next := ""
	if len(logs) == 500 {
		raw, err := common.Marshal(nextLogExportCursor(logs))
		if err != nil {
			return nil, "", err
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return items, next, nil
}
