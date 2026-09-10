package model

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// All standard log reads (admin, user, employee, token and exports) cross this
// boundary. Root diagnostic retrieval deliberately uses a separate projection.
// AfterFind 作为日志读取钩子移除私有 Claude 证据与历史策略原因，并保留可查询标记。
// 接收者 l：当前读取的日志对象，Other 会原地替换；参数 _：GORM 传入的数据库句柄，本钩子不使用。
// 返回 nil；Other 解析/序列化异常时置为空对象，避免私有数据被普通列表或导出带出。
func (l *Log) AfterFind(_ *gorm.DB) error {
	if !strings.Contains(l.Other, "claude_diagnostic") && !strings.Contains(l.Other, "reject_reason") {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(l.Other, &fields); err != nil {
		l.Other = "{}"
		return nil
	}
	_, hasDiagnostic := fields["claude_diagnostic"]
	_, hasPolicy := fields["reject_reason"]
	if !hasDiagnostic && !hasPolicy {
		return nil
	}
	delete(fields, "claude_diagnostic")
	delete(fields, "reject_reason")
	fields["claude_diagnostic_available"] = json.RawMessage("true")
	data, err := common.Marshal(fields)
	if err != nil {
		l.Other = "{}"
		return nil
	}
	l.Other = string(data)
	return nil
}

// GetClaudeStreamDiagnostic is used only by the RootAuth detail endpoint.
// Both keys are required: SQL uses request_id's index, ClickHouse its time key.
// GetClaudeStreamDiagnostic 按请求、时间及尝试编号读取私有诊断，使用独立字段投影避开普通日志过滤钩子。
// 参数 ctx：查询取消/超时上下文；requestID：请求 ID；createdAt：日志创建时间（Unix 秒）；attempts：可选尝试编号，只取首项，缺省 0。
// 返回原始诊断 JSON 及错误；最多检查 128 条同请求同时间记录，无匹配返回记录不存在错误。调用入口须先通过 RootAuth。
func GetClaudeStreamDiagnostic(ctx context.Context, requestID string, createdAt int64, attempts ...int) (json.RawMessage, error) {
	// Earlier failed attempts may share the final request's ID and timestamp.
	// Match the public attempt selector; old records without one use zero.
	wantAttempt := 0
	if len(attempts) > 0 {
		wantAttempt = attempts[0]
	}
	var rows []struct{ Other string }
	err := LOG_DB.WithContext(ctx).Table("logs").Select("other").Where("request_id = ? AND created_at = ?", requestID, createdAt).Limit(128).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if err = common.UnmarshalJsonStr(row.Other, &fields); err != nil {
			continue
		}
		if value := fields["claude_diagnostic"]; len(value) > 0 {
			var meta struct {
				Attempt int `json:"attempt"`
			}
			if common.Unmarshal(value, &meta) == nil && meta.Attempt == wantAttempt {
				return value, nil
			}
		} else if value := fields["reject_reason"]; len(value) > 0 && wantAttempt == 0 {
			return common.Marshal(map[string]json.RawMessage{"reject_reason": value})
		}
	}
	return nil, gorm.ErrRecordNotFound
}
