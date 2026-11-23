package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const LogExportRawColumn = "log_raw"
const logExportRawBatchSize = 100

var (
	ErrLogExportRawForbidden = errors.New("complete log export requires root access")
	ErrLogExportRawOptions   = errors.New("complete log export requires details without repricing")
)

// LogExportHasRawColumn also checks unnormalized input before role filtering.
func LogExportHasRawColumn(columns []string) bool {
	for _, column := range columns {
		if strings.TrimSpace(column) == LogExportRawColumn {
			return true
		}
	}
	return false
}

func (j *LogExportJob) validateRawLogExport(ctx context.Context) error {
	if !LogExportHasRawColumn(j.Columns) {
		return nil
	}
	if j.EffectiveDataSource() != LogExportDataUsage || j.SelfExport || j.EmployeeScope != nil || j.CreatorRole < common.RoleRootUser {
		return ErrLogExportAccessChanged
	}
	if j.IsSummary() || j.GroupRatioOverride != nil {
		return ErrLogExportRawOptions
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	access, err := CurrentLogExportAccess(ctx, j.UserID, "usage_admin")
	if err != nil {
		return errors.Join(ErrLogExportAccessChanged, err)
	}
	if access.Role < common.RoleRootUser {
		return ErrLogExportAccessChanged
	}
	return nil
}

// Separate from Log: no AfterFind hook, nullable storage values, no relay allocation.
// This projection is never migrated or returned by list/detail APIs.
type logExportRawLog struct {
	ID                *int
	UserID            *int
	CreatedAt         *int64
	Type              *int
	Content           *string
	Username          *string
	TokenName         *string
	ModelName         *string
	Quota             *int
	PromptTokens      *int
	CompletionTokens  *int
	UseTime           *int
	IsStream          *bool
	ChannelID         *int
	ChannelName       *string
	TokenID           *int
	Group             *string
	IP                *string
	RequestID         *string
	UpstreamRequestID *string
	Other             *string
}

func logExportRawFields() []string {
	fields := []string{
		"id", "user_id", "created_at", "type", "content", "username", "token_name", "model_name",
		"quota", "prompt_tokens", "completion_tokens", "use_time", "is_stream", "channel_id",
		"token_id", logGroupCol, "ip", "request_id", "upstream_request_id", "other",
	}
	if !usingClickHouseLogDB() {
		fields = append(fields, "channel_name")
	}
	return fields
}

func (r *logExportRawLog) json() (string, error) {
	fields := map[string]any{
		"id": r.ID, "user_id": r.UserID, "created_at": r.CreatedAt, "type": r.Type,
		"content": r.Content, "username": r.Username, "token_name": r.TokenName, "model_name": r.ModelName,
		"quota": r.Quota, "prompt_tokens": r.PromptTokens, "completion_tokens": r.CompletionTokens,
		"use_time": r.UseTime, "is_stream": r.IsStream, "channel_id": r.ChannelID,
		"token_id": r.TokenID, "group": r.Group, "ip": r.IP, "request_id": r.RequestID,
		"upstream_request_id": r.UpstreamRequestID, "other": r.Other,
	}
	if !usingClickHouseLogDB() {
		fields["channel_name"] = r.ChannelName
	}
	data, err := common.Marshal(fields)
	return string(data), err
}

func logExportStoredValue[T any](value *T) (out T) {
	if value != nil {
		return *value
	}
	return out
}

func (r *logExportRawLog) publicView() (*Log, error) {
	l := &Log{
		Id: logExportStoredValue(r.ID), UserId: logExportStoredValue(r.UserID),
		CreatedAt: logExportStoredValue(r.CreatedAt), Type: logExportStoredValue(r.Type),
		Content: logExportStoredValue(r.Content), Username: logExportStoredValue(r.Username),
		TokenName: logExportStoredValue(r.TokenName), ModelName: logExportStoredValue(r.ModelName),
		Quota: logExportStoredValue(r.Quota), PromptTokens: logExportStoredValue(r.PromptTokens),
		CompletionTokens: logExportStoredValue(r.CompletionTokens), UseTime: logExportStoredValue(r.UseTime),
		IsStream: logExportStoredValue(r.IsStream), ChannelId: logExportStoredValue(r.ChannelID),
		TokenId: logExportStoredValue(r.TokenID), Group: logExportStoredValue(r.Group),
		Ip: logExportStoredValue(r.IP), RequestId: logExportStoredValue(r.RequestID),
		UpstreamRequestId: logExportStoredValue(r.UpstreamRequestID), Other: logExportStoredValue(r.Other),
	}
	return l, l.AfterFind(nil)
}

func scanRawLogExportBatch(ctx context.Context, filter LogExportFilter, windowStart, windowEnd int64, cursor *logExportCursor, limit int) (LogExportBatch, error) {
	var batch LogExportBatch
	tx, err := logExportBatchQuery(ctx, filter, logExportRawFields(), windowStart, windowEnd, cursor, min(limit, logExportRawBatchSize))
	if err != nil {
		return batch, err
	}
	if err := tx.Find(&batch.rawLogs).Error; err != nil {
		return batch, err
	}
	batch.Logs = make([]*Log, 0, len(batch.rawLogs))
	for _, raw := range batch.rawLogs {
		view, err := raw.publicView()
		if err != nil {
			return LogExportBatch{}, err
		}
		batch.Logs = append(batch.Logs, view)
	}
	return batch, nil
}
