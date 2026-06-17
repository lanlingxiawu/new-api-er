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
	"context"
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
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// GetWechatClient 根据当前配置惰性构建微信支付客户端，未配置完整时返回错误
func GetWechatClient(ctx context.Context) (*core.Client, error) {
	mchId := strings.TrimSpace(setting.WechatMchId)
	apiV3Key := strings.TrimSpace(setting.WechatApiV3Key)
	certSerialNo := strings.TrimSpace(setting.WechatMchCertSerialNo)
	privateKeyStr := strings.TrimSpace(setting.WechatMchPrivateKey)
	if mchId == "" || apiV3Key == "" || certSerialNo == "" || privateKeyStr == "" {
		return nil, errors.New("微信支付未配置")
	}

	privateKey, err := utils.LoadPrivateKey(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("加载微信支付商户私钥失败: %w", err)
	}

	return core.NewClient(ctx, option.WithWechatPayAutoAuthCipher(mchId, certSerialNo, privateKey, apiV3Key))
}

// getWechatNotifyHandler 构建用于解密、验签微信支付回调通知的 Handler
func getWechatNotifyHandler(ctx context.Context) (*notify.Handler, error) {
	mchId := strings.TrimSpace(setting.WechatMchId)
	apiV3Key := strings.TrimSpace(setting.WechatApiV3Key)
	certSerialNo := strings.TrimSpace(setting.WechatMchCertSerialNo)
	privateKeyStr := strings.TrimSpace(setting.WechatMchPrivateKey)
	if mchId == "" || apiV3Key == "" || certSerialNo == "" || privateKeyStr == "" {
		return nil, errors.New("微信支付未配置")
	}

	privateKey, err := utils.LoadPrivateKey(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("加载微信支付商户私钥失败: %w", err)
	}

	mgr := downloader.MgrInstance()
	if !mgr.HasDownloader(ctx, mchId) {
		if err := mgr.RegisterDownloaderWithPrivateKey(ctx, privateKey, certSerialNo, mchId, apiV3Key); err != nil {
			return nil, fmt.Errorf("注册微信支付平台证书下载器失败: %w", err)
		}
	}

	certVisitor := mgr.GetCertificateVisitor(mchId)
	return notify.NewRSANotifyHandler(apiV3Key, verifiers.NewSHA256WithRSAVerifier(certVisitor))
}

// wechatAmountToFen 将元（float64）金额转换为分（int64），向上取整且至少为 1 分
func wechatAmountToFen(amount float64) int64 {
	fen := decimal.NewFromFloat(amount).Mul(decimal.NewFromInt(100)).Ceil().IntPart()
	if fen < 1 {
		fen = 1
	}
	return fen
}

// RequestWechatAmount 计算微信支付充值所需支付金额
func RequestWechatAmount(c *gin.Context) {
	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	wechatMinTopup := int64(setting.WechatMinTopUp)
	if req.Amount < wechatMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", wechatMinTopup)})
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

// RequestWechatPay 创建微信支付 Native 充值订单，返回供前端生成二维码的 code_url
func RequestWechatPay(c *gin.Context) {
	if !isWechatTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "微信支付未启用"})
		return
	}

	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	wechatMinTopup := int64(setting.WechatMinTopUp)
	if req.Amount < wechatMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", wechatMinTopup)})
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

	client, err := GetWechatClient(c.Request.Context())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 客户端初始化失败 user_id=%d error=%q", id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置微信支付信息"})
		return
	}

	// Token 模式下归一化 Amount（存等价货币数量，避免 RechargeWechat 双重放大）
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = int64(float64(req.Amount) / common.QuotaPerUnit)
		if amount < 1 {
			amount = 1
		}
	}

	tradeNo := fmt.Sprintf("WX%dNO%d%s", id, time.Now().Unix(), randstr.String(6))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWechat,
		PaymentProvider: model.PaymentProviderWechat,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/wechat/notify"
	if setting.WechatNotifyUrl != "" {
		notifyUrl = setting.WechatNotifyUrl
	}

	svc := native.NativeApiService{Client: client}
	resp, _, err := svc.Prepay(c.Request.Context(), native.PrepayRequest{
		Appid:       core.String(setting.WechatAppId),
		Mchid:       core.String(setting.WechatMchId),
		Description: core.String(fmt.Sprintf("Recharge %d credits", req.Amount)),
		OutTradeNo:  core.String(tradeNo),
		NotifyUrl:   core.String(notifyUrl),
		Amount: &native.Amount{
			Total:    core.Int64(wechatAmountToFen(payMoney)),
			Currency: core.String("CNY"),
		},
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 拉起支付失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	codeUrl := ""
	if resp.CodeUrl != nil {
		codeUrl = *resp.CodeUrl
	}
	if codeUrl == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 下单响应缺少 code_url user_id=%d trade_no=%s", id, tradeNo))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f", id, tradeNo, req.Amount, payMoney))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"code_url": codeUrl,
			"order_id": tradeNo,
		},
	})
}

// wechatTerminalFailureStates 微信支付的终态失败交易状态
var wechatTerminalFailureStates = map[string]bool{
	"CLOSED":   true,
	"REVOKED":  true,
	"PAYERROR": true,
}

// RequestWechatOrderQuery 供前端轮询订单状态，必要时主动查询微信订单结果并完成入账
func RequestWechatOrderQuery(c *gin.Context) {
	tradeNo := c.Query("trade_no")
	if tradeNo == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	id := c.GetInt("id")
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.UserId != id || topUp.PaymentProvider != model.PaymentProviderWechat {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "订单不存在"})
		return
	}

	if topUp.Status != common.TopUpStatusPending {
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
		return
	}

	client, err := GetWechatClient(c.Request.Context())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 订单查询客户端初始化失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
		return
	}

	svc := native.NativeApiService{Client: client}
	resp, _, err := svc.QueryOrderByOutTradeNo(c.Request.Context(), native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(tradeNo),
		Mchid:      core.String(setting.WechatMchId),
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查询订单失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
		return
	}

	tradeState := ""
	if resp.TradeState != nil {
		tradeState = *resp.TradeState
	}

	switch {
	case tradeState == "SUCCESS":
		LockOrder(tradeNo)
		defer UnlockOrder(tradeNo)

		// PAY-004：主动查单与 webhook 路径保持相同校验强度
		// Mchid 和 Amount.Total 是 SUCCESS 状态下的必要字段，缺失时视为异常响应，拒绝落账
		respMchid := ""
		if resp.Mchid != nil {
			respMchid = *resp.Mchid
		}
		if expectedMchId := strings.TrimSpace(setting.WechatMchId); expectedMchId != "" {
			if respMchid == "" {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查单响应缺少 mchid trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
				c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
				return
			}
			if respMchid != expectedMchId {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查单 mchid 不匹配 trade_no=%s resp_mchid=%s expected=%s client_ip=%s", tradeNo, respMchid, expectedMchId, c.ClientIP()))
				c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
				return
			}
		}
		if resp.Amount == nil || resp.Amount.Total == nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查单响应缺少 amount.total trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
			c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
			return
		}
		if expectedTotal := wechatAmountToFen(topUp.Money); *resp.Amount.Total != expectedTotal {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查单金额不匹配 trade_no=%s resp_total=%d expected=%d client_ip=%s", tradeNo, *resp.Amount.Total, expectedTotal, c.ClientIP()))
			c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
			return
		}
		// Appid 存在时校验（非必须字段，但配置了就要匹配）
		if resp.Appid != nil && *resp.Appid != "" {
			if expectedAppId := strings.TrimSpace(setting.WechatAppId); expectedAppId != "" && *resp.Appid != expectedAppId {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 查单 appid 不匹配 trade_no=%s resp_appid=%s expected=%s client_ip=%s", tradeNo, *resp.Appid, expectedAppId, c.ClientIP()))
				c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
				return
			}
		}

		if err := model.RechargeWechat(tradeNo, c.ClientIP()); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 充值处理失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": topUp.Status}})
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 充值成功（订单查询） trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": common.TopUpStatusSuccess}})
	case wechatTerminalFailureStates[tradeState]:
		if err := model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderWechat, common.TopUpStatusFailed); err != nil &&
			!errors.Is(err, model.ErrTopUpNotFound) &&
			!errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 标记失败订单状态失败 trade_no=%s error=%q", tradeNo, err.Error()))
		}
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": common.TopUpStatusFailed}})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"status": common.TopUpStatusPending}})
	}
}

// WechatNotify 处理微信支付异步通知
func WechatNotify(c *gin.Context) {
	if !isWechatWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("微信支付 webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.JSON(http.StatusForbidden, gin.H{"code": "FAIL", "message": "webhook disabled"})
		return
	}

	handler, err := getWechatNotifyHandler(c.Request.Context())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 webhook handler 初始化失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "internal error"})
		return
	}

	transaction := new(payments.Transaction)
	_, err = handler.ParseNotifyRequest(c.Request.Context(), c.Request, transaction)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("微信支付 webhook 验签/解密失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "invalid notification"})
		return
	}

	tradeNo := ""
	if transaction.OutTradeNo != nil {
		tradeNo = *transaction.OutTradeNo
	}
	tradeState := ""
	if transaction.TradeState != nil {
		tradeState = *transaction.TradeState
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 webhook 收到通知 trade_no=%s trade_state=%s client_ip=%s", tradeNo, tradeState, c.ClientIP()))

	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("微信支付 webhook 缺少 out_trade_no client_ip=%s", c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
		return
	}

	switch {
	case tradeState == "SUCCESS":
		LockOrder(tradeNo)
		defer UnlockOrder(tradeNo)

		notifiedMchId := ""
		if transaction.Mchid != nil {
			notifiedMchId = *transaction.Mchid
		}
		// 与 PAY-005 保持一致：mchid 必须存在且匹配，缺失时同样拒绝
		if expectedMchId := strings.TrimSpace(setting.WechatMchId); expectedMchId != "" {
			if notifiedMchId == "" {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 webhook 缺少 mchid trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
				c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "mchid missing"})
				return
			}
			if notifiedMchId != expectedMchId {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 webhook mchid 不匹配 trade_no=%s notify_mchid=%s expected_mchid=%s client_ip=%s", tradeNo, notifiedMchId, expectedMchId, c.ClientIP()))
				c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "mchid mismatch"})
				return
			}
		}

		if topUp := model.GetTopUpByTradeNo(tradeNo); topUp != nil && topUp.Status == common.TopUpStatusPending {
			var notifiedTotal int64
			if transaction.Amount != nil && transaction.Amount.Total != nil {
				notifiedTotal = *transaction.Amount.Total
			}
			if expectedTotal := wechatAmountToFen(topUp.Money); notifiedTotal != expectedTotal {
				logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 webhook 金额不匹配 trade_no=%s notify_total=%d order_total=%d client_ip=%s", tradeNo, notifiedTotal, expectedTotal, c.ClientIP()))
				c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "amount mismatch"})
				return
			}
		}

		if err := model.RechargeWechat(tradeNo, c.ClientIP()); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 充值处理失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "process failed"})
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 充值成功 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
	case wechatTerminalFailureStates[tradeState]:
		if err := model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderWechat, common.TopUpStatusFailed); err != nil &&
			!errors.Is(err, model.ErrTopUpNotFound) &&
			!errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 标记失败订单状态失败 trade_no=%s error=%q", tradeNo, err.Error()))
		}
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 webhook 忽略事件 trade_no=%s trade_state=%s client_ip=%s", tradeNo, tradeState, c.ClientIP()))
	}

	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
}
