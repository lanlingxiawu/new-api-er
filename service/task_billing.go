package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		var contents []string
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
		}
		if snap := info.TieredBillingSnapshot; snap != nil {
			for key, value := range snap.UsageFacts {
				contents = append(contents, fmt.Sprintf("%s: %v", key, value))
			}
		}
		if len(contents) > 0 {
			logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
		}
	}
	other := model.NewLogOther()
	other.SetPublic("is_task", true)
	other.SetPublic("request_path", c.Request.URL.Path)
	other.SetPublic("model_price", info.PriceData.ModelPrice)
	if info.PriceData.ModelRatio > 0 {
		other.SetPublic("model_ratio", info.PriceData.ModelRatio)
	}
	appendTaskPricingMetadata(other, info.PriceData.PricingMetadata)
	other.SetPublic("group_ratio", info.PriceData.GroupRatioInfo.GroupRatio)
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", info.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	if info.IsModelMapped {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", info.UpstreamModelName)
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		other.SetPublic("billing_mode", "tiered_expr")
		other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
		other.SetPublic("matched_tier", snap.EstimatedTier)
		if len(snap.UsageFacts) > 0 {
			other.SetPublic("usage_facts", snap.UsageFacts)
		}
	}
	appendTaskLogInfo(task, other)
	attachQuotaSaturation(c, info, other)
	upstreamBase := taskSubmitUpstreamBase(c, info)
	EnqueueConsumeLogWithCost(c, info, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	}, info.PriceData.Quota, upstreamBase)
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) *model.LogOther {
	other := model.NewLogOther()
	if bc := task.PrivateData.BillingContext; bc != nil {
		other.SetPublic("model_price", bc.ModelPrice)
		if bc.ModelRatio > 0 {
			other.SetPublic("model_ratio", bc.ModelRatio)
		}
		other.SetPublic("group_ratio", bc.GroupRatio)
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				if !other.SetPublic(k, v) {
					common.SysError("task billing other ratio key rejected: " + k)
				}
			}
		}
		if snap := bc.TieredSnapshot; snap != nil {
			other.SetPublic("billing_mode", "tiered_expr")
			other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
			other.SetPublic("matched_tier", snap.EstimatedTier)
			if len(snap.UsageFacts) > 0 {
				other.SetPublic("usage_facts", snap.UsageFacts)
			}
		}
		appendTaskPricingMetadata(other, bc.PricingMetadata)
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", props.UpstreamModelName)
	}
	appendTaskLogInfo(task, other)
	return other
}

// TaskBillingOther 返回任务计费信息中用户可见的部分（供任务列表 Cost 列展示）。
// admin_info / root_info / audit_info 等分级字段不随任务 DTO 下发。
func TaskBillingOther(task *model.Task) map[string]any {
	other := taskBillingOther(task).Snapshot()
	for key := range other {
		if isReservedTaskDtoOtherKey(key) {
			delete(other, key)
		}
	}
	return other
}

func isReservedTaskDtoOtherKey(key string) bool {
	switch key {
	case "admin_info", "root_info", "audit_info":
		return true
	default:
		return false
	}
}

func appendTaskLogInfo(task *model.Task, other *model.LogOther) {
	if task == nil || other == nil {
		return
	}
	if task.TaskID != "" {
		other.SetPublic("task_id", task.TaskID)
	}
	if task.PrivateData.Execution != nil {
		AppendTaskPluginAuditInfo(other, task.PrivateData.Execution.TaskPlugin)
	}
	if task.PrivateData.UpstreamTaskID == "" && task.PrivateData.NodeName == "" {
		return
	}
	if task.PrivateData.UpstreamTaskID != "" {
		other.SetRoot("upstream_task_id", task.PrivateData.UpstreamTaskID)
	}
	if task.PrivateData.NodeName != "" {
		other.SetRoot("node_name", task.PrivateData.NodeName)
	}
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *hosttypes.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &hosttypes.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// taskFallbackGroupRatio 在计费快照缺失时解析任务的分组倍率。
// 必须走 ResolveGroupRatio：用户专属倍率与分组对分组倍率都会改变实际计费倍率，
// 只读全局分组倍率会低估成本基准，让成本账与提成偏离真实扣费。
func taskFallbackGroupRatio(task *model.Task) float64 {
	ratio, _ := ratio_setting.ResolveGroupRatio(model.GetUserGroupRatios(task.UserId), task.Group, task.Group)
	return ratio
}

func buildTaskLedgerRelayInfo(task *model.Task) *relaycommon.RelayInfo {
	modelName := taskModelName(task)
	groupRatio := taskFallbackGroupRatio(task)
	modelRatio := float64(0)
	modelPrice := float64(0)
	usePrice := false
	var otherRatios map[string]float64
	var pricingMetadata map[string]string

	if bc := task.PrivateData.BillingContext; bc != nil {
		groupRatio = bc.GroupRatio
		modelRatio = bc.ModelRatio
		modelPrice = bc.ModelPrice
		usePrice = bc.PerCallBilling
		if bc.OriginModelName != "" {
			modelName = bc.OriginModelName
		}
		if len(bc.OtherRatios) > 0 {
			otherRatios = make(map[string]float64, len(bc.OtherRatios))
			for key, value := range bc.OtherRatios {
				otherRatios[key] = value
			}
		}
		if len(bc.PricingMetadata) > 0 {
			pricingMetadata = make(map[string]string, len(bc.PricingMetadata))
			for key, value := range bc.PricingMetadata {
				pricingMetadata[key] = value
			}
		}
	} else {
		modelRatio, _, _ = ratio_setting.GetModelRatio(modelName)
	}

	info := &relaycommon.RelayInfo{
		UserId:          task.UserId,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: task.ChannelId},
		TokenId:         task.PrivateData.TokenId,
		UsingGroup:      task.Group,
		OriginModelName: modelName,
		PriceData: hosttypes.PriceData{
			ModelPrice:      modelPrice,
			ModelRatio:      modelRatio,
			UsePrice:        usePrice,
			PricingMetadata: pricingMetadata,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: groupRatio},
		},
	}
	info.PriceData.ReplaceOtherRatios(otherRatios)
	return info
}

func appendTaskPricingMetadata(other *model.LogOther, metadata map[string]string) {
	for key, value := range metadata {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		other.SetPublic("pricing_"+trimmedKey, trimmedValue)
	}
}

// TaskUpstreamBase 是差额结算的「上游消耗」基数（分组倍率取 1 的额度，quota 单位）。
// 表达式计价的任务在免费分组下预扣额与结算额都恒为 0，无法用「结算额 ÷ 分组倍率」倒推基数；
// 提交时已按 Before 累计过，结算只需补 After - Before。
type TaskUpstreamBase struct {
	Before float64 // 提交时已累计的基数（表达式乘分组倍率之前的额度）。
	After  float64 // 本次结算后的基数。
}

// delta 求本次结算应补累的基数增量。基数只增不减：结算额下调时返回 0。
func (b *TaskUpstreamBase) delta() int64 {
	if b == nil {
		return 0
	}
	d := int64(common.QuotaRound(b.After)) - int64(common.QuotaRound(b.Before))
	if d < 0 {
		return 0
	}
	return d
}

// taskSettledUpstreamBaseKey 承载「提交即完成」结算出的、乘分组倍率之前的额度。
// 快照的 EstimatedQuotaBeforeGroup 按上游约定恒为提交前的预估值（重试换分组时还要拿它
// 重算预估），不能被结算值覆盖；而消费日志的「上游消耗」要的是结算值，因此单独用请求
// 上下文传递。
const taskSettledUpstreamBaseKey = "task_settled_upstream_base"

// SetTaskSettledUpstreamBase 供提交即完成的结算路径登记实际上游消耗基数。
func SetTaskSettledUpstreamBase(c *gin.Context, quotaBeforeGroup float64) {
	if c == nil {
		return
	}
	c.Set(taskSettledUpstreamBaseKey, quotaBeforeGroup)
}

// taskSubmitUpstreamBase 求任务提交时的「上游消耗」基数。表达式计价的任务必须显式给出：
// 快照里 ModelPrice 为 0 且不是按次计费，免费分组下 upstreamBaseQuota 的兜底会算出 0，
// 渠道每日上限对这类流量彻底失效。
func taskSubmitUpstreamBase(c *gin.Context, info *relaycommon.RelayInfo) *int64 {
	if c != nil {
		if settled, ok := c.Get(taskSettledUpstreamBaseKey); ok {
			if quotaBeforeGroup, ok := settled.(float64); ok {
				base := int64(common.QuotaRound(quotaBeforeGroup))
				return &base
			}
		}
	}
	snap := info.TieredBillingSnapshot
	if snap == nil {
		return nil
	}
	base := int64(common.QuotaRound(snap.EstimatedQuotaBeforeGroup))
	return &base
}

// recordTaskUpstreamOnly 只累计渠道每日上限。免费分组的差额恒为 0，没有台账可记，
// 但上游消耗照常发生——限额是止损控制，口径独立于成本/提成台账。
func recordTaskUpstreamOnly(channelId int, baseQuota int64) {
	if baseQuota <= 0 {
		return
	}
	go recordChannelDailyUpstream(channelId, baseQuota)
}

func recordTaskCostAndCommission(task *model.Task, quota int, logId int, explicitBase *int64) {
	if task == nil || quota == 0 {
		return
	}
	ledgerInfo := buildTaskLedgerRelayInfo(task)
	go func() {
		// 差额补扣也是上游消耗；退款（quota < 0）不减少限额累计，upstreamBaseQuota 返回 0。
		recordChannelDailyUpstream(task.ChannelId, upstreamBaseQuota(&ledgerInfo.PriceData, quota, explicitBase))
		RecordCostAndSettleEmployeeCommission(ledgerInfo, quota, logId)
	}()
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，退还资金与令牌额度，并回减用户和渠道用量。
// 返回资金来源是否已成功退还；失败时保留 quota，供显式重试或人工对账。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) bool {
	quota := task.Quota
	if quota == 0 {
		return true
	}

	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 回减预扣时累计的用户和渠道用量，请求次数保持不变
	model.UpdateUserUsedQuota(task.UserId, -quota)
	model.UpdateChannelUsedQuota(task.ChannelId, -quota)

	// 4. 记录日志
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("reason", reason)
	logId := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})
	// 4. 记录成本与提成（fork 记账）。必须在清除 task.Quota 之前，
	// 因为提成结算依赖退款额度。
	recordTaskCostAndCommission(task, -quota, logId, nil)

	// 5. 资金退款完成后再清除持久化标记；失败时保留非零 quota，
	// 由后续对账重试。回写失败必须显式告警，避免漏掉潜在的重复退款风险。
	task.Quota = 0
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("退款成功但清除 task quota 失败 task %s: %s", task.TaskID, err.Error()))
	}
	return true
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// upstreamBase 可选：表达式计价的任务传入「上游消耗」基数，免费分组只有它能还原真实消耗。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, upstreamBase *TaskUpstreamBase, clamps ...*common.QuotaClamp) {
	if actualQuota < 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota
	baseDelta := upstreamBase.delta()

	if quotaDelta == 0 {
		// 免费分组的预扣额与结算额都是 0，差额为 0，但上游用量可能已经涨了。
		recordTaskUpstreamOnly(task.ChannelId, baseDelta)
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		return
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算回写 quota 失败 task %s: %s", task.TaskID, err.Error()))
	}

	// 提交阶段已经累计过一次请求；结算阶段只调整最终用量。
	model.UpdateUserUsedQuota(task.UserId, quotaDelta)
	model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("pre_consumed_quota", preConsumedQuota)
	other.SetPublic("actual_quota", actualQuota)
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	logId := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
	var explicitBase *int64
	if upstreamBase != nil {
		explicitBase = &baseDelta
	}
	recordTaskCostAndCommission(task, quotaDelta, logId, explicitBase)
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) bool {
	if totalTokens <= 0 {
		return false
	}

	modelName := taskModelName(task)
	var modelRatio, finalGroupRatio float64

	if bc := task.PrivateData.BillingContext; isLegacyThirdPartySD2BillingContext(task) {
		// 旧 SD2 任务的倍率由提交时的分辨率 × 参考视频矩阵决定，全局模型倍率中没有该模型，
		// 只能按冻结在计费快照里的倍率与分组倍率结算。
		modelRatio = bc.ModelRatio
		finalGroupRatio = bc.GroupRatio
	} else {
		var hasRatioSetting bool
		modelRatio, hasRatioSetting, _ = ratio_setting.GetModelRatio(modelName)
		if !hasRatioSetting {
			return false
		}

		if bc != nil && bc.GroupRatio > 0 {
			// 预扣时冻结在计费快照里的倍率是唯一权威口径。任务只存了实际使用的分组，
			// 重新解析时只能把它同时当作用户分组，分组对分组倍率（GroupGroupRatio）就会
			// 落空：预扣按 0.3、重算按 0.5，用户会被多扣。
			finalGroupRatio = bc.GroupRatio
		} else {
			group := task.Group
			if group == "" {
				user, err := model.GetUserById(task.UserId, false)
				if err == nil {
					group = user.Group
				}
			}
			if group == "" {
				return false
			}

			// per-user exclusive (if enabled) -> group-group ratio -> group ratio (design §5.3)
			finalGroupRatio, _ = ratio_setting.ResolveGroupRatio(model.GetUserGroupRatios(task.UserId), group, group)
		}
	}
	// 只有按倍率计费（倍率 > 0）的任务才按 token 重算；倍率为 0 的按次任务保持预扣额度，
	// 否则会按 0 额度把预扣全部退回。
	if modelRatio <= 0 {
		return false
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	RecalculateTaskQuota(ctx, task, actualQuota, reason, nil, clamp)
	return true
}

// legacyThirdPartySD2PricingMode 是旧 ThirdPartySD2 Go 适配器写入 BillingContext.PricingMetadata 的定价模式。
const legacyThirdPartySD2PricingMode = "resolution_video_matrix"

// isLegacyThirdPartySD2BillingContext 识别旧 ThirdPartySD2 Go 适配器创建的任务：
// 计费快照没有表达式快照，并带有矩阵定价元数据或旧的数字平台 "58"。
// 插件创建的 SD2 任务走表达式结算，不会进入 token 重算。
func isLegacyThirdPartySD2BillingContext(task *model.Task) bool {
	if task == nil {
		return false
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.TieredSnapshot != nil || bc.PerCallBilling {
		return false
	}
	if strings.TrimSpace(bc.PricingMetadata["pricing_mode"]) == legacyThirdPartySD2PricingMode {
		return true
	}
	return task.Platform == constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeThirdPartySD2))
}

// EvaluateTaskCompletionUsage evaluates actual facts against the frozen task
// expression. It neither mutates the snapshot nor moves funds: synchronous
// submission and polling have different persistence and settlement barriers.
func EvaluateTaskCompletionUsage(snap *billingexpr.BillingSnapshot, facts map[string]any) (billingexpr.TieredResult, map[string]any, error) {
	if snap == nil {
		return billingexpr.TieredResult{}, nil, fmt.Errorf("task billing snapshot is missing")
	}
	usage := make(map[string]any, len(snap.UsageFacts)+len(facts))
	maps.Copy(usage, snap.UsageFacts)
	maps.Copy(usage, facts)
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
	if err == nil && (result.ActualQuotaBeforeGroup < 0 || math.IsNaN(result.ActualQuotaBeforeGroup)) {
		err = fmt.Errorf("task completion expression produced an invalid cost")
	}
	return result, usage, err
}
