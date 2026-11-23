package model

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// QuotaExportFilter is the equality filter for one quota_data export.
// Nil pointers mean the column is not filtered. Zero is a real stored value.
type QuotaExportFilter struct {
	UserID    *int   `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	UseGroup  string `json:"use_group,omitempty"`
	ChannelID *int   `json:"channel_id,omitempty"`
	TokenID   *int   `json:"token_id,omitempty"`
	NodeName  string `json:"node_name,omitempty"`
}

var errQuotaExportRate = errors.New("quota per unit is zero")

var quotaExportHeaders = map[string]string{
	"id":                    "ID",
	"user_id":               "用户 ID",
	"username":              "用户名",
	"model_name":            "模型",
	"hour":                  "小时",
	"created_at":            "统计时间",
	"use_group":             "分组",
	"channel_id":            "渠道 ID",
	"count":                 "请求次数",
	"quota":                 "消耗额度（美元）",
	"token_used":            "消耗 Token",
	"cache_read_tokens":     "缓存读取 Token",
	"cache_creation_tokens": "缓存创建 Token",
	"token_id":              "令牌 ID",
	"node_name":             "节点",
}

func QuotaExportColumns(role int) []string {
	columns := []string{
		"id", "user_id", "username", "model_name", "hour", "created_at", "use_group", "channel_id",
		"count", "quota", "token_used", "cache_read_tokens", "cache_creation_tokens",
	}
	if role >= common.RoleRootUser {
		columns = append(columns, "token_id", "node_name")
	}
	return columns
}

func quotaExportHasRootColumn(columns []string) bool {
	return slices.Contains(columns, "token_id") || slices.Contains(columns, "node_name")
}

type quotaExportCursor struct {
	At int64
	ID int
}

type quotaExportReader struct {
	job       *LogExportJob
	perUnit   float64
	loc       *time.Location
	validated time.Time
}

func newQuotaExportReader(job *LogExportJob) (*quotaExportReader, error) {
	if common.QuotaPerUnit == 0 {
		return nil, errQuotaExportRate
	}
	loc := time.Local
	if job.Options.Timezone != "" {
		parsed, err := time.LoadLocation(job.Options.Timezone)
		if err != nil {
			return nil, err
		}
		loc = parsed
	}
	return &quotaExportReader{job: job, perUnit: common.QuotaPerUnit, loc: loc}, nil
}

func (r *quotaExportReader) Reverse() bool { return false }

func (r *quotaExportReader) BatchSize(configured int) int {
	if configured < 1 {
		return 1
	}
	return configured
}

func (r *quotaExportReader) Header() []string {
	header := make([]string, len(r.job.Columns))
	for i, key := range r.job.Columns {
		header[i] = quotaExportHeaders[key]
	}
	return header
}

func (r *quotaExportReader) Validate(ctx context.Context, force bool) error {
	if !force && time.Since(r.validated) < time.Second {
		return ctx.Err()
	}
	var user User
	err := DB.WithContext(ctx).Select("id", "role", "status").Where("id = ?", r.job.UserID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (user.Status != common.UserStatusEnabled || user.Role < common.RoleAdminUser)) {
		return ErrLogExportAccessChanged
	}
	if err != nil {
		return err
	}
	if quotaExportHasRootColumn(r.job.Columns) && user.Role < common.RoleRootUser {
		return ErrLogExportAccessChanged
	}
	r.validated = time.Now()
	return ctx.Err()
}

func (r *quotaExportReader) Read(ctx context.Context, start, end int64, cursor any, limit int) (LogExportBatch, error) {
	batch := LogExportBatch{Position: end}
	if limit < 1 {
		limit = 1
	}
	query := DB.WithContext(ctx).Table("quota_data").
		Select("id, user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name, token_used, count, quota, cache_read_tokens, cache_creation_tokens").
		Where("created_at >= ? AND created_at <= ?", start, end)
	if cursor != nil {
		position, ok := cursor.(quotaExportCursor)
		if !ok {
			return batch, ErrLogExportDataSource
		}
		query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", position.At, position.At, position.ID)
	}
	filter := r.job.QuotaFilters
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.Username != "" {
		query = query.Where("username = ?", filter.Username)
	}
	if filter.ModelName != "" {
		query = query.Where("model_name = ?", filter.ModelName)
	}
	if filter.UseGroup != "" {
		query = query.Where("use_group = ?", filter.UseGroup)
	}
	if filter.ChannelID != nil {
		query = query.Where("channel_id = ?", *filter.ChannelID)
	}
	if filter.TokenID != nil {
		query = query.Where("token_id = ?", *filter.TokenID)
	}
	if filter.NodeName != "" {
		query = query.Where("node_name = ?", filter.NodeName)
	}
	var rows []QuotaData
	if err := query.Order("created_at").Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return batch, err
	}
	batch.Scanned = len(rows)
	batch.Done = len(rows) < limit
	if len(rows) == 0 {
		return batch, nil
	}
	last := rows[len(rows)-1]
	batch.Cursor = quotaExportCursor{At: last.CreatedAt, ID: last.Id}
	batch.Position = last.CreatedAt
	batch.Rows = make([]LogExportRow, 0, len(rows))
	for i := range rows {
		values, err := r.render(&rows[i])
		if err != nil {
			return batch, err
		}
		batch.Rows = append(batch.Rows, LogExportRow{Values: values, At: rows[i].CreatedAt})
	}
	return batch, nil
}

func (r *quotaExportReader) render(row *QuotaData) ([]string, error) {
	values := make([]string, 0, len(r.job.Columns))
	for _, key := range r.job.Columns {
		switch key {
		case "id":
			values = append(values, strconv.Itoa(row.Id))
		case "user_id":
			values = append(values, strconv.Itoa(row.UserID))
		case "username":
			values = append(values, row.Username)
		case "model_name":
			values = append(values, row.ModelName)
		case "hour":
			values = append(values, time.Unix(row.CreatedAt, 0).In(r.loc).Format("2006-01-02 15:04"))
		case "created_at":
			values = append(values, strconv.FormatInt(row.CreatedAt, 10))
		case "use_group":
			values = append(values, row.UseGroup)
		case "channel_id":
			values = append(values, strconv.Itoa(row.ChannelID))
		case "count":
			values = append(values, strconv.Itoa(row.Count))
		case "quota":
			usd := math.Round(float64(row.Quota)/r.perUnit*1e6) / 1e6
			values = append(values, strconv.FormatFloat(usd, 'f', -1, 64))
		case "token_used":
			values = append(values, strconv.Itoa(row.TokenUsed))
		case "cache_read_tokens":
			values = append(values, strconv.FormatInt(row.CacheReadTokens, 10))
		case "cache_creation_tokens":
			values = append(values, strconv.FormatInt(row.CacheCreationTokens, 10))
		case "token_id":
			values = append(values, strconv.Itoa(row.TokenID))
		case "node_name":
			values = append(values, row.NodeName)
		default:
			return nil, ErrLogExportDataSource
		}
	}
	return values, nil
}

func writeQuotaDataExport(ctx context.Context, job *LogExportJob) error {
	if job.QuotaFilters == nil || job.SelfExport || job.EmployeeScope != nil || job.NeedsSummary() || job.GroupRatioOverride != nil || len(job.Columns) == 0 {
		return ErrLogExportDataSource
	}
	if quotaExportHasRootColumn(job.Columns) && job.CreatorRole < common.RoleRootUser {
		return ErrLogExportAccessChanged
	}
	if (job.QuotaFilters.TokenID != nil || job.QuotaFilters.NodeName != "") && job.CreatorRole < common.RoleRootUser {
		return ErrLogExportAccessChanged
	}
	for _, key := range job.Columns {
		if _, ok := quotaExportHeaders[key]; !ok {
			return ErrLogExportDataSource
		}
	}
	reader, err := newQuotaExportReader(job)
	if err != nil {
		return err
	}
	validationCtx, cancel := context.WithTimeout(ctx, logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec()))
	err = reader.Validate(validationCtx, true)
	cancel()
	if err != nil {
		return err
	}
	output, err := newLogExportOutput(job, reader.Header())
	if err != nil {
		return err
	}
	defer output.Abort()
	scanner := newLogExportScanner(job, nil, false, nil)
	scanner.reader = reader
	err = scanner.runBatches(ctx, func(batch LogExportBatch) error {
		if err := CheckLogExportDiskSpace(); err != nil {
			return err
		}
		for _, row := range batch.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := output.Write(row.Values, row.At); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	validationCtx, cancel = context.WithTimeout(ctx, logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec()))
	err = reader.Validate(validationCtx, true)
	cancel()
	if err != nil {
		return err
	}
	return output.Finish()
}
