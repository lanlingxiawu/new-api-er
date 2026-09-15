package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
)

// 渠道每日金额上限的批量设置接口。设计见 docs/design/channel-daily-quota-limit.md §9.6。

// ChannelDailyLimitBatchRequest 批量设置请求。
// 配置字段都用指针：nil = 本次不修改；要清除上限必须显式传 0。
type ChannelDailyLimitBatchRequest struct {
	Ids                      []int  `json:"ids"`
	DailyQuotaLimit          *int64 `json:"daily_quota_limit"`
	DailyLimitAutoRecover    *int   `json:"daily_limit_auto_recover"`
	DailyLimitRecoverMinutes *int   `json:"daily_limit_recover_minutes"`
}

func (r ChannelDailyLimitBatchRequest) edit() model.DailyLimitEdit {
	return model.DailyLimitEdit{
		QuotaLimit:     r.DailyQuotaLimit,
		AutoRecover:    r.DailyLimitAutoRecover,
		RecoverMinutes: r.DailyLimitRecoverMinutes,
	}
}

// validateChannelDailyLimitI18n 在 AddChannel / UpdateChannel 上显式校验每日上限，
// 报错走 ApiErrorI18n。返回 false 表示已经写过响应，调用方直接 return。
//
// 为什么不能只靠 validateChannel → ValidateSettings：那条链把 ValidateDailyLimitConfig
// 返回的 i18n key 包进一句中文前缀后原样 err.Error() 输出，管理员看到的是
//
//	渠道额外设置[channel setting] 格式错误：channel.daily_limit.invalid_amount
//
// ——一个裸键名，既违反 Rule 9（不得把原始错误串返回给客户端）也违反 Rule 13。
// 批量与按 Tag 两条路径本来就走 ApiErrorI18n，同一个校验却给出两种文案；这里补齐
// 单渠道新增/编辑两条，四条写入路径的报错文案就统一了。
//
// 这不是重复校验：ValidateSettings 里的同名检查仍然保留，作为 model 层的兜底
// （CopyChannel 等不经过控制器校验的路径依赖它）。这里先跑一遍只是为了让**用户看到的
// 那条消息**是翻译过的。
func validateChannelDailyLimitI18n(c *gin.Context, channel *model.Channel) bool {
	if channel == nil {
		return true
	}
	if err := model.ValidateDailyLimitConfig(channel.DailyQuotaLimit, channel.DailyLimitRecoverMinutes); err != nil {
		common.ApiErrorI18n(c, err.Error())
		return false
	}
	return true
}

// buildChannelDailyLimitEdit 从单渠道编辑请求里摘出每日上限的配置字段。
//
// 恢复间隔即使原样回传也照常带上：是否开启新一轮由 DailyLimitEdit 在同一条 UPDATE 里按
// 「新旧间隔是否不同」判断，原样回传不会重置本轮用量。
//
// 必须在 channel.Update() **之前**调用：Update() 末尾会 First(channel) 把整行从库里
// 重新读回结构体，之后再读 channel.DailyQuotaLimit 拿到的是库里的旧值（正是被 GORM
// 跳过、没能写进去的那个值），补写就变成了把旧值原样写回，等于什么都没做。
//
// 以 requestData 里字段是否出现为准，而不是看值是否非零：没传的字段一律不动，
// 与 channelHasSensitiveChanges / clearChannelReadOnlyFields 的既有判定方式一致。
func buildChannelDailyLimitEdit(channel *PatchChannel, requestData map[string]any) model.DailyLimitEdit {
	edit := model.DailyLimitEdit{}
	if _, ok := requestData["daily_quota_limit"]; ok {
		limit := channel.DailyQuotaLimit
		edit.QuotaLimit = &limit
	}
	if _, ok := requestData["daily_limit_auto_recover"]; ok && channel.DailyLimitAutoRecover != nil {
		autoRecover := *channel.DailyLimitAutoRecover
		edit.AutoRecover = &autoRecover
	}
	if _, ok := requestData["daily_limit_recover_minutes"]; ok {
		minutes := channel.DailyLimitRecoverMinutes
		edit.RecoverMinutes = &minutes
	}
	return edit
}

// applyChannelDailyLimitEdit 把每日上限字段单独落一次库。
//
// 为什么不能靠 Channel.Update()：它用 Updates(struct) 更新，GORM 会跳过结构体零值，
// 于是 daily_quota_limit = 0（清除上限）与 daily_limit_recover_minutes = 0（回到按日模式）
// 都会被静默丢弃；Update() 也刻意不写限时恢复的两列（见其注释）。批量与按 Tag 两条写入路径
// 早已为此改用 map 更新，这里补齐第三条，让三条路径的写入语义一致。
//
// 取值合法性在更早的 validateChannel → ValidateSettings 已经校验过，因此这里返回的
// 错误只可能来自数据库。
func applyChannelDailyLimitEdit(channelId int, edit model.DailyLimitEdit) error {
	if edit.IsEmpty() {
		return nil
	}
	_, err := model.UpdateChannelDailyLimitByIds([]int{channelId}, edit)
	return err
}

// BatchUpdateChannelDailyLimit 批量设置选中渠道的每日金额上限。
//
// 权限：与 param_override / header_override 的既有做法一致，需要 ChannelSensitiveWrite
// ——单渠道编辑路径下这三个字段同样归类为敏感字段，两条路径门槛保持一致。
func BatchUpdateChannelDailyLimit(c *gin.Context) {
	req := ChannelDailyLimitBatchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if !authz.Can(c.GetInt("id"), c.GetInt("role"), authz.ChannelSensitiveWrite) {
		common.ApiErrorI18n(c, i18n.MsgAuthInsufficientPrivilege)
		return
	}
	edit := req.edit()
	if edit.IsEmpty() {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := edit.Validate(); err != nil {
		common.ApiErrorI18n(c, err.Error())
		return
	}

	affected, err := model.UpdateChannelDailyLimitByIds(req.Ids, edit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 渠道缓存与每日上限的配置快照都要立即失效，否则本节点仍按旧上限判定；
	// 其它节点靠 flush 周期刷新收敛（≤ 一个 flush 间隔）。
	model.InitChannelCache()
	service.RefreshChannelDailyLimitConfigs()
	recordManageAudit(c, "channel.batch_daily_limit", map[string]interface{}{
		"count": affected,
		"total": len(req.Ids),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    affected,
	})
}
