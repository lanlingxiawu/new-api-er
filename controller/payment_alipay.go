/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/smartwalle/alipay/v3"
	"github.com/thanhpk/randstr"
)

// GetAlipayClient 根据当前配置惰性构建支付宝客户端，未配置完整时返回 nil
func GetAlipayClient() *alipay.Client {
	appId := strings.TrimSpace(setting.AlipayAppId)
	privateKey := strings.TrimSpace(setting.AlipayPrivateKey)
	publicKey := strings.TrimSpace(setting.AlipayPublicKey)
	if appId == "" || privateKey == "" || publicKey == "" {
		return nil
	}

	client, err := alipay.New(appId, privateKey, !setting.AlipaySandbox)
	if err != nil {
		return nil
	}
	if err := client.LoadAliPayPublicKey(publicKey); err != nil {
		return nil
	}
	return client
}

// RequestAlipayAmount 计算支付宝充值所需支付金额
func RequestAlipayAmount(c *gin.Context) {
	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	alipayMinTopup := int64(setting.AlipayMinTopUp)
	if req.Amount < alipayMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", alipayMinTopup)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestAlipayPay 创建支付宝充值订单，返回供前端跳转的支付链接
func RequestAlipayPay(c *gin.Context) {
	if !isAlipayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付宝支付未启用"})
		return
	}

	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	alipayMinTopup := int64(setting.AlipayMinTopUp)
	if req.Amount < alipayMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", alipayMinTopup)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	client := GetAlipayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置支付宝支付信息"})
		return
	}

	// Token 模式下归一化 Amount（存等价货币数量，避免 RechargeAlipay 双重放大）
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = int64(float64(req.Amount) / common.QuotaPerUnit)
		if amount < 1 {
			amount = 1
		}
	}

	tradeNo := fmt.Sprintf("ALI%dNO%d%s", id, time.Now().Unix(), randstr.String(6))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodAlipay,
		PaymentProvider: model.PaymentProviderAlipay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/alipay/notify"
	if setting.AlipayNotifyUrl != "" {
		notifyUrl = setting.AlipayNotifyUrl
	}
	returnUrl := paymentReturnPath("/console/topup?show_history=true")
	if setting.AlipayReturnUrl != "" {
		returnUrl = setting.AlipayReturnUrl
	}

	param := alipay.TradePagePay{}
	param.NotifyURL = notifyUrl
	param.ReturnURL = returnUrl
	param.Subject = fmt.Sprintf("Recharge %d credits", req.Amount)
	param.OutTradeNo = tradeNo
	param.TotalAmount = strconv.FormatFloat(payMoney, 'f', 2, 64)
	param.ProductCode = "FAST_INSTANT_TRADE_PAY"

	payUrl, err := client.TradePagePay(param)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 拉起支付失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f", id, tradeNo, req.Amount, payMoney))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": payUrl.String(),
			"order_id":    tradeNo,
		},
	})
}

// AlipayNotify 处理支付宝异步通知
func AlipayNotify(c *gin.Context) {
	if !isAlipayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("支付宝 webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.String(http.StatusOK, "fail")
		return
	}

	client := GetAlipayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 client 未初始化 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.String(http.StatusOK, "fail")
		return
	}

	if err := c.Request.ParseForm(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 webhook 表单解析失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.String(http.StatusOK, "fail")
		return
	}

	noti, err := client.DecodeNotification(c.Request.Context(), c.Request.Form)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("支付宝 webhook 验签失败 path=%q client_ip=%s error=%q params=%q", c.Request.RequestURI, c.ClientIP(), err.Error(), c.Request.Form.Encode()))
		c.String(http.StatusOK, "fail")
		return
	}

	tradeNo := noti.OutTradeNo
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 webhook 收到通知 trade_no=%s alipay_trade_no=%s trade_status=%s total_amount=%s client_ip=%s", tradeNo, noti.TradeNo, noti.TradeStatus, noti.TotalAmount, c.ClientIP()))

	switch noti.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		LockOrder(tradeNo)
		defer UnlockOrder(tradeNo)

		if err := model.RechargeAlipay(tradeNo, c.ClientIP()); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 充值处理失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
			c.String(http.StatusOK, "fail")
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 充值成功 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
	case alipay.TradeStatusClosed:
		if err := model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderAlipay, common.TopUpStatusFailed); err != nil &&
			!errors.Is(err, model.ErrTopUpNotFound) &&
			!errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 标记失败订单状态失败 trade_no=%s error=%q", tradeNo, err.Error()))
		}
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 webhook 忽略事件 trade_no=%s trade_status=%s client_ip=%s", tradeNo, noti.TradeStatus, c.ClientIP()))
	}

	client.ACKNotification(c.Writer)
}
