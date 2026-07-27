package model

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// LogExportFilter 导出的筛选条件，字段语义与使用日志列表接口完全一致。
type LogExportFilter struct {
	LogType        int    `json:"type"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
	ModelName      string `json:"model_name"`
	Username       string `json:"username"`
	TokenName      string `json:"token_name"`
	ChannelId      int    `json:"channel"`
	Group          string `json:"group"`
	// UserId > 0 时只导出该用户的日志。
	UserId int `json:"user_id"`
}

// logExportCursor 是 keyset 游标。SQL 库用 (created_at, id)，ClickHouse 用
// (created_at, request_id) —— 后者是 CH 上的实际排序键，见 clickHouseLogOrder。
type logExportCursor struct {
	CreatedAt int64
	Id        int
	RequestId string
}

func usingClickHouseLogDB() bool {
	return common.UsingLogDatabase(common.DatabaseTypeClickHouse)
}

// applyLogExportFilter 施加筛选条件。文本类过滤复用列表接口的实现，
// 保证「导出结果 == 页面所见」。
func applyLogExportFilter(tx *gorm.DB, filter LogExportFilter) (*gorm.DB, error) {
	var err error
	if filter.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", filter.LogType)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", filter.Username); err != nil {
		return nil, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("logs.token_name = ?", filter.TokenName)
	}
	if filter.ChannelId != 0 {
		tx = tx.Where("logs.channel_id = ?", filter.ChannelId)
	}
	if filter.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", filter.Group)
	}
	if filter.UserId > 0 {
		tx = tx.Where("logs.user_id = ?", filter.UserId)
	}
	return tx, nil
}

// ensureCursorFields 保证游标字段一定出现在 SELECT 里。
//
// ClickHouse 的排序键是 (created_at, request_id)，游标也用这一对。如果用户选的列
// 里没有 request_id，取出的行该字段为空，游标条件 `created_at = ? AND request_id < ''`
// 永远不成立，翻页会直接跳过该秒剩余的行——静默丢数据。SQL 库用 id 做次键，
// id 本来就无条件选取，不受影响。
func ensureCursorFields(fields []string) []string {
	if !usingClickHouseLogDB() {
		return fields
	}
	for _, f := range fields {
		if f == "request_id" {
			return fields
		}
	}
	out := make([]string, len(fields), len(fields)+1)
	copy(out, fields)
	return append(out, "request_id")
}

// logExportSelectClause 把列集合需要的字段拼成显式 SELECT，避免 SELECT *。
func logExportSelectClause(fields []string) string {
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, "logs."+f)
	}
	return strings.Join(quoted, ", ")
}

// scanLogExportBatch 取一批日志。
//
// 走 (created_at, id) 倒序 keyset，命中现有复合索引 idx_created_at_id；带 type
// 过滤时命中 idx_created_at_type。游标条件用 `a < ? OR (a = ? AND b < ?)` 的 OR
// 形式而非行值比较，SQLite/MySQL/PostgreSQL 三库通用。
//
// 调用方按时间窗口切分区间（见 runLogExport），把单次扫描的索引区间限死，
// 避免跨月的巨型 range scan 长期占用缓冲池。
func scanLogExportBatch(
	ctx context.Context,
	filter LogExportFilter,
	fields []string,
	windowStart, windowEnd int64,
	cursor *logExportCursor,
	limit int,
) ([]*Log, error) {
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).
		Where("logs.created_at >= ? AND logs.created_at <= ?", windowStart, windowEnd)

	var err error
	if tx, err = applyLogExportFilter(tx, filter); err != nil {
		return nil, err
	}

	clickHouse := usingClickHouseLogDB()
	if cursor != nil {
		if clickHouse {
			tx = tx.Where("logs.created_at < ? OR (logs.created_at = ? AND logs.request_id < ?)",
				cursor.CreatedAt, cursor.CreatedAt, cursor.RequestId)
		} else {
			tx = tx.Where("logs.created_at < ? OR (logs.created_at = ? AND logs.id < ?)",
				cursor.CreatedAt, cursor.CreatedAt, cursor.Id)
		}
	}

	order := "logs.created_at desc, logs.id desc"
	if clickHouse {
		order = clickHouseLogOrder("logs.")
	}

	var logs []*Log
	err = tx.Select(logExportSelectClause(ensureCursorFields(fields))).Order(order).Limit(limit).Find(&logs).Error
	if err != nil {
		return nil, err
	}
	return logs, nil
}

// nextLogExportCursor 从一批结果的末行取出下一页游标。
func nextLogExportCursor(logs []*Log) *logExportCursor {
	if len(logs) == 0 {
		return nil
	}
	last := logs[len(logs)-1]
	return &logExportCursor{
		CreatedAt: last.CreatedAt,
		Id:        last.Id,
		RequestId: last.RequestId,
	}
}

// fillLogExportChannelNames 解析渠道名。
//
// 遵循既有约定：删除渠道时不回写 logs 表（避免锁等待），因此渠道名在查询时解析。
// cache 跨整次导出复用并保存负缓存，每个 channel_id 至多解析一次，摊销后导出对
// channels 表的额外查询接近 0。
func fillLogExportChannelNames(ctx context.Context, logs []*Log, cache map[int]string) {
	if cache == nil || len(logs) == 0 {
		return
	}
	var missing []int
	for _, l := range logs {
		if l.ChannelId == 0 {
			continue
		}
		if _, ok := cache[l.ChannelId]; ok {
			continue
		}
		if common.MemoryCacheEnabled {
			if ch, err := CacheGetChannel(l.ChannelId); err == nil && ch != nil {
				cache[l.ChannelId] = ch.Name
				continue
			}
		}
		missing = append(missing, l.ChannelId)
		// 先写负缓存占位，避免同一批里重复进 missing。
		cache[l.ChannelId] = ""
	}
	if len(missing) > 0 {
		var rows []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if err := DB.WithContext(ctx).Table("channels").
			Select("id, name").Where("id IN ?", missing).Find(&rows).Error; err == nil {
			for _, row := range rows {
				cache[row.Id] = row.Name
			}
		}
		// 查询失败时保留负缓存：渠道名留空即可，不该让整次导出失败。
	}
	for _, l := range logs {
		if l.ChannelId != 0 && l.ChannelName == "" {
			l.ChannelName = cache[l.ChannelId]
		}
	}
}

// logExportQueryTimeout 单批查询的超时，热更新即时生效。
func logExportQueryTimeout(sec int) time.Duration {
	return time.Duration(sec) * time.Second
}
