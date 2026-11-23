package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetChannelPriorityOrder(c *gin.Context) {
	order, err := model.GetChannelPriorityOrder()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": order})
}

func ReorderChannelPriority(c *gin.Context) {
	var move model.ChannelPriorityMove
	if err := common.DecodeJson(c.Request.Body, &move); err != nil {
		common.ApiErrorT(c, "Invalid channel priority move")
		return
	}
	order, err := model.ReorderChannelPriority(move)
	if err != nil {
		if errors.Is(err, model.ErrChannelPriorityConflict) || errors.Is(err, model.ErrChannelPriorityInvalid) || errors.Is(err, model.ErrChannelPriorityRange) {
			common.ApiErrorT(c, err.Error())
		} else {
			common.ApiError(c, err)
		}
		return
	}
	if !move.Preview && len(order.Changes) > 0 {
		audit := model.RequestAuditChanges(c)
		for _, change := range order.Changes {
			target := model.AuditTarget("channel", change.ID, "")
			target.Consistency = "atomic"
			audit.Add(target, model.AuditFields{"priority": change.Previous}, model.AuditFields{"priority": change.Priority})
		}
		model.InitChannelCache()
	}
	if move.Preview {
		markAuditLogged(c)
	} else {
		recordManageAudit(c, "channel.priority_reorder", map[string]any{"count": len(order.Changes)})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": order})
}
