package controller

import (
	"math"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// maxPriceMonitorCostRatio 只拦明显的误输入（多打一个 0），不是业务上限。
const maxPriceMonitorCostRatio = 100

// GetPriceMonitorChannelCosts 返回各渠道的成本系数核对结果，需要处理的排在前面。
func GetPriceMonitorChannelCosts(c *gin.Context) {
	snapshot := getPriceMonitorStore().Get()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"checked_at": snapshot.CheckedAt,
			"items":      priceMonitorChannelCostList(snapshot),
		},
	})
}

type priceMonitorCostRatioRequest struct {
	CostRatio *float64 `json:"cost_ratio"`
}

// UpdatePriceMonitorChannelCostRatio 在巡检页修改渠道成本系数（「采用上游倍率」与手动修改共用）。
// 写入走与渠道编辑抽屉相同的 UpsertChannelCostConfig（含缓存失效），随后就地重算快照。
func UpdatePriceMonitorChannelCostRatio(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var request priceMonitorCostRatioRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.CostRatio == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	costRatio := *request.CostRatio
	if math.IsNaN(costRatio) || math.IsInf(costRatio, 0) || costRatio < 0 || costRatio > maxPriceMonitorCostRatio {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorCostRatioInvalid, map[string]any{"Max": maxPriceMonitorCostRatio})
		return
	}
	channel, err := model.GetChannelById(channelId, false)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorChannelUnavailable)
		return
	}
	previous := model.GetChannelCostRatio(channelId)
	remark, err := priceMonitorCostRemark(channelId)
	if err != nil {
		// 读不到现有备注就不写：写入会用空备注覆盖它。
		logger.LogError(c, "price monitor failed to read channel cost config: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	if err := model.UpsertChannelCostConfig(channelId, costRatio, remark); err != nil {
		logger.LogError(c, "price monitor failed to update channel cost ratio: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	recordManageAudit(c, "price_monitor.cost_ratio.update", map[string]interface{}{
		"channel_id": channelId,
		"from":       previous,
		"to":         costRatio,
	})
	refreshed := refreshPriceMonitorAfterChange(c)
	var item *PriceMonitorChannelCost
	for _, cost := range priceMonitorChannelCostList(getPriceMonitorStore().Get()) {
		if cost.ChannelId == channelId {
			cost := cost
			item = &cost
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"refreshed": refreshed, "item": item}})
}

// priceMonitorCostRemark 返回已有配置的备注：这里只改系数，不应顺手清掉别处写下的备注。
func priceMonitorCostRemark(channelId int) (string, error) {
	configs, err := model.GetAllChannelCostConfigs()
	if err != nil {
		return "", err
	}
	for _, config := range configs {
		if config.ChannelId == channelId {
			return config.Remark, nil
		}
	}
	return "", nil
}

// refreshPriceMonitorAfterChange 在一次修改成功后就地重算快照。重算失败不影响已经成功的修改，
// 只记日志，结果等下一轮巡检刷新。
func refreshPriceMonitorAfterChange(c *gin.Context) bool {
	refreshed, err := refreshPriceMonitorSnapshotInPlace()
	if err != nil {
		logger.LogError(c, "price monitor in-place refresh failed: "+err.Error())
		return false
	}
	return refreshed
}
