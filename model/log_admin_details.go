package model

import "github.com/QuantumNous/new-api/common"

// RecordLogWithAdminDetails 写入一条日志，details 原样存入 other.admin_info。
// 用于员工业绩、佣金、客户绑定等内部排障信息：字段不固定，普通用户查询日志时
// admin_info 会被剥离，因此不适用结构化的 AuditAdminInfo。
func RecordLogWithAdminDetails(userId int, logType int, content string, details map[string]any) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(details) > 0 {
		log.Other = common.MapToJsonStr(map[string]any{"admin_info": details})
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}
