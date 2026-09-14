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
// AfterFind 移除私有响应证据，按 bb6317462 保留独立策略原因，仅透出写入时已确认的新诊断标记。
// 接收者 l：当前读取的日志对象，Other 会原地替换；参数 _：GORM 传入的数据库句柄，本钩子不使用。
// 返回 nil；Other 解析/序列化异常时置为空对象，避免私有数据被普通列表或导出带出。
func (l *Log) AfterFind(_ *gorm.DB) error {
	if !strings.Contains(l.Other, "claude_diagnostic") && !strings.Contains(l.Other, "stream_diagnostic") && !strings.Contains(l.Other, "claude_stream") {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(l.Other, &fields); err != nil {
		l.Other = "{}"
		return nil
	}
	// 历史字段只在读取时归一化，新字段即使为空也优先；不批量修改数据库。
	for old, current := range map[string]string{
		"claude_stream":             "stream_result",
		"claude_diagnostic_attempt": "stream_diagnostic_attempt",
	} {
		if _, exists := fields[current]; !exists {
			if value, present := fields[old]; present {
				fields[current] = value
			}
		}
		delete(fields, old)
	}
	private, exists := fields["stream_diagnostic"]
	if !exists {
		private = fields["claude_diagnostic"]
	}
	// 兼容曾收进私有 envelope 的历史原因；仅投影字符串，不把其他原始响应/错误/用量证据带出。
	var reason string
	if len(fields["reject_reason"]) == 0 || (common.Unmarshal(fields["reject_reason"], &reason) == nil && reason == "") {
		var legacy struct {
			RejectReason string `json:"reject_reason"`
		}
		if common.Unmarshal(private, &legacy) == nil && legacy.RejectReason != "" {
			fields["reject_reason"], _ = common.Marshal(legacy.RejectReason)
		}
	}
	delete(fields, "claude_diagnostic")
	delete(fields, "stream_diagnostic")
	delete(fields, "claude_diagnostic_available")
	// 禁止按旧标记、body 或策略原因合成 true；字符串 "true" 也不当作布尔资格。
	var available bool
	if common.Unmarshal(fields["stream_diagnostic_available"], &available) != nil || !available {
		delete(fields, "stream_diagnostic_available")
	}
	data, err := common.Marshal(fields)
	if err != nil {
		l.Other = "{}"
		return nil
	}
	l.Other = string(data)
	return nil
}

// GetStreamDiagnostic is used only by the RootAuth detail endpoint.
// Both keys are required: SQL uses request_id's index, ClickHouse its time key.
// GetStreamDiagnostic 按请求、时间及尝试编号读取私有诊断，使用独立字段投影避开普通日志过滤钩子。
// 参数 ctx：查询取消/超时上下文；requestID：请求 ID；createdAt：日志创建时间（Unix 秒）；attempts：可选尝试编号，只取首项，缺省 0。
// 返回原始诊断 JSON 及错误；最多检查 128 条同请求同时间记录，无匹配返回记录不存在错误。调用入口须先通过 RootAuth。
func GetStreamDiagnostic(ctx context.Context, requestID string, createdAt int64, attempts ...int) (json.RawMessage, error) {
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
	var diagnostic json.RawMessage // 暂存早期渠道错误；同秒同尝试的终止日志含补发后的完整证据，优先返回。
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if err = common.UnmarshalJsonStr(row.Other, &fields); err != nil {
			continue
		}
		value, exists := fields["stream_diagnostic"]
		if !exists {
			value = fields["claude_diagnostic"]
		}
		if len(value) > 0 {
			var meta struct {
				Attempt int `json:"attempt"`
			}
			if common.Unmarshal(value, &meta) == nil && meta.Attempt == wantAttempt {
				if _, terminal := fields["stream_result"]; terminal {
					return value, nil
				}
				if diagnostic == nil {
					diagnostic = value
				}
			}
		} else if value := fields["reject_reason"]; len(value) > 0 && wantAttempt == 0 {
			if diagnostic == nil {
				diagnostic, _ = common.Marshal(map[string]json.RawMessage{"reject_reason": value})
			}
		}
	}
	if diagnostic != nil {
		return diagnostic, nil
	}
	return nil, gorm.ErrRecordNotFound
}
