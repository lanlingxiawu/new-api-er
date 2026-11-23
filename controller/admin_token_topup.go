package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminReconcileTopUp never accepts a client payment verdict or amount. Provider
// IO happens before the completion transaction, whose snapshot check prevents
// a changed order from receiving credit based on the earlier provider result.
func AdminReconcileTopUp(c *gin.Context) {
	var request struct {
		TradeNo string `json:"trade_no"`
	}
	if !decodeAdminTokenUserBody(c, &request, "trade_no") {
		return
	}
	request.TradeNo = strings.TrimSpace(request.TradeNo)
	if request.TradeNo == "" || len(request.TradeNo) > 255 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var order model.TopUp
	if err := model.DB.WithContext(c.Request.Context()).Select("id", "user_id", "trade_no", "amount", "money", "payment_provider", "payment_method", "provider_order_id", "payment_currency", "status").Where("trade_no = ?", request.TradeNo).Take(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupChanged)
			return
		}
		writeAdminTokenUserError(c, err)
		return
	}
	var user model.User
	if err := model.DB.WithContext(c.Request.Context()).Select("id", "role").First(&user, order.UserId).Error; err != nil {
		writeAdminTokenUserError(c, err)
		return
	}
	if user.Role != common.RoleCommonUser {
		writeAdminTokenUserError(c, model.ErrUserQuotaPermission)
		return
	}
	providerVerified := false
	if order.Status != common.TopUpStatusSuccess {
		if order.Status != common.TopUpStatusPending {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupChanged)
			return
		}
		if order.PaymentProvider != model.PaymentProviderAlipay && order.PaymentProvider != model.PaymentProviderInfini {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupUnsupported)
			return
		}
		snapshot, err := queryTopUpPlatformPaymentStatus(c.Request.Context(), &order)
		if err != nil {
			logger.LogWarn(c, "token topup reconciliation provider query failed: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupQueryFailed)
			return
		}
		if snapshot.Status != model.PlatformPaymentStatusCredited {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupUnpaid)
			return
		}
		providerVerified = true
	}
	result, err := model.CompleteVerifiedTopUp(c.Request.Context(), order, c.ClientIP())
	if err != nil {
		logger.LogError(c, "verified topup completion failed: "+err.Error())
		if errors.Is(err, model.ErrUserQuotaPermission) {
			writeAdminTokenUserError(c, err)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgAdminTokenTopupChanged)
		return
	}
	recordManageAuditFor(c, result.UserID, "user.topup_complete", map[string]any{"trade_no": order.TradeNo, "provider_verified": providerVerified, "already_completed": result.AlreadyCompleted})
	common.ApiSuccess(c, gin.H{"trade_no": order.TradeNo, "user_id": result.UserID, "quota_added": result.QuotaAdded, "quota_per_unit": common.QuotaPerUnit, "already_completed": result.AlreadyCompleted, "status": common.TopUpStatusSuccess})
}
