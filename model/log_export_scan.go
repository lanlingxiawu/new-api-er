package model

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// LogExportFilter 导出的筛选条件，字段语义与使用日志列表接口完全一致。
//
// 新增字段一律 omitempty：任务状态整体序列化进 Redis，升级前创建的任务反序列化后
// 新字段为零值/nil，行为与升级前完全一致。
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

	// ── 数值条件：logs 表真实列，直接下推 SQL ──────────────────
	// 一律用指针：0 是有意义的取值（quota_max=0 就是「只看零费用的行」），
	// 非指针 + omitempty 会把它静默丢掉（Rule 5）。

	// Charged 是否产生了费用。quota 是整数额度列，「计费了」在系统里就是 quota > 0。
	//
	// 用 QuotaMin=1 也能表达同一件事，但那要求使用者先知道 quota 存的是整数额度、
	// 而不是界面上那一栏美元（1 额度 = 1/QuotaPerUnit 美元，本站默认 $0.000002）。
	// 拿美元数字去填额度阈值会差出五个数量级，所以这里给出不需要换算的写法。
	Charged             *bool `json:"charged,omitempty"`
	QuotaMin            *int  `json:"quota_min,omitempty"`
	QuotaMax            *int  `json:"quota_max,omitempty"`
	PromptTokensMin     *int  `json:"prompt_tokens_min,omitempty"`
	PromptTokensMax     *int  `json:"prompt_tokens_max,omitempty"`
	CompletionTokensMin *int  `json:"completion_tokens_min,omitempty"`
	// CompletionTokensMax 配合 QuotaMin 才能表达「输出为 0 却计费」这类问题：
	// 只有下限就写不出「等于 0」。
	CompletionTokensMax *int  `json:"completion_tokens_max,omitempty"`
	UseTimeMin          *int  `json:"use_time_min,omitempty"`
	UseTimeMax          *int  `json:"use_time_max,omitempty"`
	IsStream            *bool `json:"is_stream,omitempty"`

	// ── other 条件：无索引可走，在扫描管线里逐行判定 ────────────
	// AnomalyKinds 命中任一即保留；为空但 AnomalyOnly=true 时表示「任意异常」。
	AnomalyKinds    []string `json:"anomaly_kinds,omitempty"`
	AnomalyOnly     bool     `json:"anomaly_only,omitempty"`
	UsageSource     []string `json:"usage_source,omitempty"`
	StreamEndReason []string `json:"stream_end_reason,omitempty"`
	SettlementState []string `json:"settlement_state,omitempty"`
	MinRetryCount   *int     `json:"min_retry_count,omitempty"`
}

// HasRowFilter 是否存在需要逐行判定的条件。为真时扫描必须取 other 并解析 JSON，
// 即使用户选的列一个都不依赖它。
func (f LogExportFilter) HasRowFilter() bool {
	return f.AnomalyOnly || len(f.AnomalyKinds) > 0 || len(f.UsageSource) > 0 ||
		len(f.StreamEndReason) > 0 || len(f.SettlementState) > 0 || f.MinRetryCount != nil
}

// matchesRow 判定一行是否满足全部 other 条件。多个条件之间是 AND；
// 单个条件内部的多个取值之间是 OR。
func (f LogExportFilter) matchesRow(l *Log, ctx *rowCtx) bool {
	if f.AnomalyOnly || len(f.AnomalyKinds) > 0 {
		if !logExportRowMatchesAnomaly(l, ctx, f.AnomalyKinds) {
			return false
		}
	}
	if len(f.UsageSource) > 0 {
		if !slices.Contains(f.UsageSource, otherValue(ctx.streamResult(l), "usage_source")) {
			return false
		}
	}
	if len(f.SettlementState) > 0 {
		if !slices.Contains(f.SettlementState, otherValue(ctx.streamResult(l), "settlement_state")) {
			return false
		}
	}
	if len(f.StreamEndReason) > 0 {
		if !slices.Contains(f.StreamEndReason, otherValue(ctx.streamStatus(l), "end_reason")) {
			return false
		}
	}
	if f.MinRetryCount != nil {
		chain, _ := ctx.adminInfo(l)["use_channel"].([]any)
		if len(chain) < *f.MinRetryCount {
			return false
		}
	}
	return true
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

	// 数值条件下推 SQL：它们是 logs 表真实列，加在已有的时间窗口 range scan 之上
	// 只是多一个过滤谓词，不增加扫描量，还能减少返回的行数。
	// 刻意不为它们建索引——永远与时间窗口同时出现，而 logs 是关系链路的写入目标，
	// 多一个索引就是多一份写入放大。
	if filter.Charged != nil {
		if *filter.Charged {
			tx = tx.Where("logs.quota > 0")
		} else {
			tx = tx.Where("logs.quota <= 0")
		}
	}
	if filter.QuotaMin != nil {
		tx = tx.Where("logs.quota >= ?", *filter.QuotaMin)
	}
	if filter.QuotaMax != nil {
		tx = tx.Where("logs.quota <= ?", *filter.QuotaMax)
	}
	if filter.PromptTokensMin != nil {
		tx = tx.Where("logs.prompt_tokens >= ?", *filter.PromptTokensMin)
	}
	if filter.CompletionTokensMin != nil {
		tx = tx.Where("logs.completion_tokens >= ?", *filter.CompletionTokensMin)
	}
	if filter.CompletionTokensMax != nil {
		tx = tx.Where("logs.completion_tokens <= ?", *filter.CompletionTokensMax)
	}
	if filter.PromptTokensMax != nil {
		tx = tx.Where("logs.prompt_tokens <= ?", *filter.PromptTokensMax)
	}
	if filter.UseTimeMin != nil {
		tx = tx.Where("logs.use_time >= ?", *filter.UseTimeMin)
	}
	if filter.UseTimeMax != nil {
		tx = tx.Where("logs.use_time <= ?", *filter.UseTimeMax)
	}
	if filter.IsStream != nil {
		tx = tx.Where("logs.is_stream = ?", *filter.IsStream)
	}
	return tx, nil
}

// ensureCursorFields 保证游标字段一定出现在 SELECT 里。
//
// ClickHouse 的排序键是 (created_at, request_id)，游标也用这一对。如果用户选的列
// 里没有 request_id，取出的行该字段为空，游标条件 `created_at = ? AND request_id < ”`
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

// EstimateLogExportRows 估算某组筛选条件命中的行数，用于导出前的可行性判断
// （xlsx 是否可用、预计多少个分片）。
//
// 关键点：**不做全表 COUNT**。COUNT 的代价随命中行数线性增长，而导出弹窗里
// 用户每改一次筛选就要估一次，直接 COUNT 会把日志大表反复扫穿。这里改成
// 「数到 limit 就停」的有界计数：
//
//	SELECT count(*) FROM (SELECT 1 FROM logs WHERE ... LIMIT n) t
//
// 代价被 limit 钉死，与实际数据量无关。返回 capped=true 表示真实行数 ≥ rows，
// 这对「是否超过 xlsx 行数上限」这类判断已经足够。
//
// **LIMIT 限住的是结果不是工作量**：当过滤条件命中很少或为零时（用户敲了个
// 不存在的用户名，或用了 `%xx%` 通配），数据库要扫完整段时间范围才能确定凑不
// 满 limit。EXPLAIN ANALYZE 实测：50 万行的表、过滤条件无匹配时
// `Rows Removed by Filter: 500000`，整段被扫穿。因此调用方必须给一个**短**超时，
// 让最坏情况退化成「几秒后放弃估算」而不是「扫穿日志表」——估算只是提示，
// 失败不影响导出。
func EstimateLogExportRows(ctx context.Context, filter LogExportFilter, limit int) (rows int64, capped bool, err error) {
	if limit <= 0 {
		return 0, false, nil
	}
	sub := LOG_DB.WithContext(ctx).Model(&Log{}).
		Select("1").
		Where("logs.created_at >= ? AND logs.created_at <= ?", filter.StartTimestamp, filter.EndTimestamp)
	if sub, err = applyLogExportFilter(sub, filter); err != nil {
		return 0, false, err
	}
	sub = sub.Limit(limit)

	// 子查询必须带别名，PostgreSQL 才接受。
	if err = LOG_DB.WithContext(ctx).Table("(?) as t", sub).Count(&rows).Error; err != nil {
		return 0, false, err
	}
	return rows, rows >= int64(limit), nil
}
