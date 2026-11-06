package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/infini_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// Infini API traffic uses a separate client from AI relay traffic.
var infiniAPIBaseURL = setting.GetInfiniBaseUrl
var infiniHTTPClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// infiniPost 向 Infini API 发送 POST 请求并返回原始响应体
func infiniPost(ctx context.Context, path string, payload any) ([]byte, int, error) {
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}

	url := infiniAPIBaseURL() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	if err := infini_setting.SignRequest(req, body); err != nil {
		return nil, 0, err
	}

	resp, err := infiniHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxPlatformStatusResponseSize))
	return respBody, resp.StatusCode, err
}

// ─── 金额计算 ──────────────────────────────────────────────────────────────────

// getInfiniCurrencyOptions 返回当前有效的多币种配置（带合法性检查）
func getInfiniCurrencyOptions() []constant.InfiniCurrencyOption {
	return setting.GetInfiniCurrencyOptions()
}

// resolveInfiniCurrency 解析请求中的 currency：
// 若为空则使用第一个配置项，否则校验必须在允许列表内，返回匹配的配置项。
func resolveInfiniCurrency(requested string) (constant.InfiniCurrencyOption, bool) {
	opts := getInfiniCurrencyOptions()
	if len(opts) == 0 {
		return constant.InfiniCurrencyOption{}, false
	}
	if requested == "" {
		return opts[0], true
	}
	upper := strings.ToUpper(strings.TrimSpace(requested))
	for _, opt := range opts {
		if strings.ToUpper(opt.Currency) == upper {
			return opt, true
		}
	}
	return constant.InfiniCurrencyOption{}, false
}

// formatInfiniAmount 按 Infini 要求格式化金额：零小数位币种取整，其余保留两位小数
func infiniCurrencyScale(currency string) int32 {
	if constant.InfiniZeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

func roundInfiniAmount(amount decimal.Decimal, currency string) decimal.Decimal {
	return amount.Round(infiniCurrencyScale(currency))
}

func formatInfiniAmount(amount decimal.Decimal, currency string) string {
	return roundInfiniAmount(amount, currency).StringFixed(infiniCurrencyScale(currency))
}

func getInfiniPayMoney(amount int64, group string, unitPrice float64, currency string) decimal.Decimal {
	dAmount := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount = dAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	return roundInfiniAmount(
		dAmount.
			Mul(decimal.NewFromFloat(unitPrice)).
			Mul(decimal.NewFromFloat(topupGroupRatio)).
			Mul(decimal.NewFromFloat(discount)),
		currency,
	)
}

// rawQuotaFromPayMoney 按下单时刻的动态汇率把实付美元金额换算为原始额度（raw quota）。
// 公式：rawQuota = round(payMoney × exchangeRate / systemPrice × QuotaPerUnit)。
// Infini 与 Stripe 动态汇率充值共用此函数，保证两条链路的换算精度完全一致。
//
// 结果先与 common.MaxWalletQuota 比较再取整：decimal.IntPart() 对超出 int64 的值会截断出
// 无意义（可能为负）的结果，直接写进订单快照就会变成负额度。超限时返回 MaxWalletQuota+1，
// 由调用方的 validateCreditedQuota 统一拒绝。
func rawQuotaFromPayMoney(payMoney decimal.Decimal, exchangeRate float64, systemPrice float64) int64 {
	if systemPrice <= 0 {
		systemPrice = 1.0
	}

	const rateScale int64 = 10000
	rateInt := decimal.NewFromFloat(exchangeRate).Mul(decimal.NewFromInt(rateScale)).Round(0).IntPart()
	priceInt := decimal.NewFromFloat(systemPrice).Mul(decimal.NewFromInt(rateScale)).Round(0).IntPart()
	if rateInt <= 0 || priceInt <= 0 {
		return 0
	}

	rawQuota := payMoney.
		Mul(decimal.NewFromInt(rateInt)).
		Div(decimal.NewFromInt(priceInt)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Round(0)
	if rawQuota.GreaterThan(decimal.NewFromInt(int64(common.MaxWalletQuota))) {
		return int64(common.MaxWalletQuota) + 1
	}
	if rawQuota.LessThan(decimal.NewFromInt(1)) {
		return 1
	}
	return rawQuota.IntPart()
}

// infiniExchangeRate 返回 Infini 到账折算使用的 USD→CNY 汇率（元/美金：1 美元折算多少人民币）。
//   - 实时模式（InfiniUseRealtimeRate=true）：取实时 USD/CNY 汇率；获取失败时回退到手动汇率 InfiniExchangeRate。
//   - 手动模式：始终使用管理员填写的手动汇率 InfiniExchangeRate。
//
// InfiniExchangeRate <= 0 时统一回退到系统兜底汇率，避免到账额度被算成 0。
// 与 Stripe 的 stripeExchangeRate 完全对齐；每个请求只取一次，报价与下单的换算口径一致。
func infiniExchangeRate() float64 {
	manual := setting.InfiniExchangeRate
	if setting.InfiniUseRealtimeRate {
		if rate, ok := service.GetUSDCNYRateWithOK(); ok && rate > 0 {
			return rate
		}
	}
	if manual > 0 {
		return manual
	}
	return service.GetUSDCNYRate()
}

// infiniMaxTopUpAmount 是单笔 Infini 充值数量上限，与 Stripe 保持一致。
// 没有上限时，超大 amount 会让报价金额与额度快照溢出到无意义的取值。
const infiniMaxTopUpAmount = 10000

// rejectUnsupportedInfiniCurrency 在非 USD 币种时写入用户可理解的错误响应并返回 true。
// 空串表示请求未指定币种，由配置决定，交给解析后的币种再校验。
func rejectUnsupportedInfiniCurrency(c *gin.Context, currency string) bool {
	if strings.TrimSpace(currency) == "" || setting.IsInfiniCurrencySupported(currency) {
		return false
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "error",
		"data": i18n.T(c, i18n.MsgPaymentInfiniCurrencyUnsupported, map[string]any{
			"Currency": strings.ToUpper(strings.TrimSpace(currency)),
		}),
	})
	return true
}

// ─── 用户接口 ──────────────────────────────────────────────────────────────────

type InfiniPayRequest struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"` // 可选，留空使用第一个配置币种
}

// RequestInfiniAmount 返回指定充值量和币种对应的实付金额报价
func RequestInfiniAmount(c *gin.Context) {
	var req InfiniPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	// 客户端显式指定的非 USD 币种直接给出具体原因，而不是笼统的「不支持的币种」
	if rejectUnsupportedInfiniCurrency(c, req.Currency) {
		return
	}

	currOpt, ok := resolveInfiniCurrency(req.Currency)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的币种"})
		return
	}

	if rejectUnsupportedInfiniCurrency(c, currOpt.Currency) {
		return
	}

	if req.Amount < int64(currOpt.MinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", currOpt.MinTopUp)})
		return
	}
	if req.Amount > infiniMaxTopUpAmount {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能大于 %d", infiniMaxTopUpAmount)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	currency := strings.ToUpper(currOpt.Currency)
	payMoney := getInfiniPayMoney(req.Amount, group, currOpt.UnitPrice, currency)
	if payMoney.LessThanOrEqual(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// 报价与下单用同一口径预校验：到账额度必须可表示且不会撑爆钱包上限，
	// 否则用户付完款后 creditTopUpQuota 会整事务回滚，订单永久 pending、webhook 反复重试。
	exchangeRate := infiniExchangeRate()
	if rejectInvalidCreditedQuota(c, id, decimal.NewFromInt(rawQuotaFromPayMoney(payMoney, exchangeRate, operation_setting.Price))) {
		return
	}

	// 同时返回本次报价锁定的到账折算汇率（元/美金），供前端展示实际到账，
	// 避免前端用独立的实时汇率重新计算导致与后端到账口径不一致。
	c.JSON(http.StatusOK, gin.H{
		"message":       "success",
		"data":          formatInfiniAmount(payMoney, currency),
		"exchange_rate": exchangeRate,
	})
}

// RequestInfiniPay 创建 Infini 托管结账订单，返回 checkout_url
func RequestInfiniPay(c *gin.Context) {
	if !setting.InfiniEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Infini 支付未启用"})
		return
	}

	var req InfiniPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	// 客户端显式指定的非 USD 币种直接给出具体原因，而不是笼统的「不支持的币种」
	if rejectUnsupportedInfiniCurrency(c, req.Currency) {
		return
	}

	// 校验币种：必须在允许列表内，服务端解析，不信任客户端传入的原始字符串
	currOpt, ok := resolveInfiniCurrency(req.Currency)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的币种"})
		return
	}

	if rejectUnsupportedInfiniCurrency(c, currOpt.Currency) {
		return
	}

	if req.Amount < int64(currOpt.MinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", currOpt.MinTopUp)})
		return
	}
	if req.Amount > infiniMaxTopUpAmount {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能大于 %d", infiniMaxTopUpAmount)})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	group, _ := model.GetUserGroup(id, true)
	currency := strings.ToUpper(currOpt.Currency)
	payMoney := getInfiniPayMoney(req.Amount, group, currOpt.UnitPrice, currency)
	if payMoney.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// Store the final raw quota snapshot, matching the admin amount adjustment precision.
	amount := rawQuotaFromPayMoney(payMoney, infiniExchangeRate(), operation_setting.Price)

	// 与报价同一口径的「不可入账订单先拒付」预校验：额度必须可表示、为正，
	// 且加上后不超过钱包上限，否则不创建订单，避免用户付款后无法入账。
	if rejectInvalidCreditedQuota(c, id, decimal.NewFromInt(amount)) {
		return
	}

	if !infini_setting.CredentialsConfigured() || strings.TrimSpace(setting.InfiniWebhookSecret) == "" {
		logger.LogWarn(c, "Infini v2 credentials are missing or invalid")
		common.ApiErrorI18n(c, "payment.infini_v2_unavailable")
		return
	}

	tradeNo := fmt.Sprintf("INFINI-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney.InexactFloat64(),
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodInfini,
		PaymentProvider: model.PaymentProviderInfini,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 创建充值订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	returnUrl := paymentReturnPath("/console/topup?show_history=true")
	if setting.InfiniReturnUrl != "" {
		returnUrl = setting.InfiniReturnUrl
	}
	failUrl := paymentReturnPath("/console/topup")
	if setting.InfiniFailUrl != "" {
		failUrl = setting.InfiniFailUrl
	}

	orderPayload := map[string]any{
		"request_id":       uuid.NewString(),
		"amount":           formatInfiniAmount(payMoney, currency),
		"currency":         currency,
		"client_reference": tradeNo,
		"order_desc":       fmt.Sprintf("Recharge %d credits", req.Amount),
		"success_url":      returnUrl,
		"failure_url":      failUrl,
	}

	// 若管理员配置了 pay_methods，传入以限定结账页支付方式
	// 1=加密货币, 2=银行卡, 3=Binance Pay, 5=Apple Pay, 6=Google Pay
	if pm := strings.TrimSpace(setting.InfiniPayMethods); pm != "" && pm != "[]" {
		var payMethodInts []int
		if err := common.UnmarshalJsonStr(pm, &payMethodInts); err == nil && len(payMethodInts) > 0 {
			orderPayload["pay_methods"] = payMethodInts
		}
	}

	respBody, statusCode, err := infiniPost(c.Request.Context(), "/v2/acquiring/order", orderPayload)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 创建订单请求失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		common.ApiErrorI18n(c, "payment.infini_checkout_failed")
		return
	}

	var result struct {
		Code        *int   `json:"code"`
		OrderID     string `json:"order_id"`
		CheckoutURL string `json:"checkout_url"`
		Data        *struct {
			CheckoutURL string `json:"checkout_url"`
			OrderID     string `json:"order_id"`
		} `json:"data"`
	}
	if err := common.Unmarshal(respBody, &result); err != nil || statusCode != http.StatusOK || (result.Code != nil && *result.Code != 0) {
		logger.LogWarn(c, fmt.Sprintf("Infini v2 order rejected user_id=%d trade_no=%s status=%d", id, tradeNo, statusCode))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		common.ApiErrorI18n(c, "payment.infini_checkout_failed")
		return
	}
	if result.Data != nil {
		result.OrderID, result.CheckoutURL = result.Data.OrderID, result.Data.CheckoutURL
	}
	if strings.TrimSpace(result.CheckoutURL) == "" {
		logger.LogError(c, "Infini v2 order response is missing checkout_url")
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		common.ApiErrorI18n(c, "payment.infini_checkout_failed")
		return
	}

	// provider_order_id 是 webhook 双向绑定的关键字段：必须非空且保存成功
	// 任一条件不满足都标记订单 failed 并拒绝返回 checkout_url，防止绑定降级
	infiniOrderId := strings.TrimSpace(result.OrderID)
	if infiniOrderId == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 响应缺少 order_id，拒绝返回支付链接 user_id=%d trade_no=%s", id, tradeNo))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		common.ApiErrorI18n(c, "payment.infini_checkout_failed")
		return
	}
	topUp.ProviderOrderId = infiniOrderId
	topUp.PaymentCurrency = currency
	if err := topUp.Update(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 保存 provider_order_id 失败，拒绝返回支付链接 user_id=%d trade_no=%s infini_order_id=%s error=%q",
			id, tradeNo, infiniOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		common.ApiErrorI18n(c, "payment.infini_checkout_failed")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini 充值订单创建成功 user_id=%d trade_no=%s infini_order_id=%s amount=%d money=%s currency=%s",
		id, tradeNo, infiniOrderId, req.Amount, formatInfiniAmount(payMoney, currency), currency))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": result.CheckoutURL,
			"order_id":    tradeNo,
		},
	})
}

// ─── Webhook ───────────────────────────────────────────────────────────────────

// InfiniWebhook 处理 Infini 支付回调
func InfiniWebhook(c *gin.Context) {
	if !isInfiniWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 被拒绝 reason=webhook_disabled client_ip=%s", c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	signature := c.GetHeader("X-Webhook-Signature")
	timestamp := c.GetHeader("X-Webhook-Timestamp")
	eventId := c.GetHeader("X-Webhook-Event-Id")

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini webhook 收到请求 client_ip=%s event_id=%s", c.ClientIP(), eventId))

	if signature == "" || timestamp == "" || eventId == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 缺少必要头 client_ip=%s", c.ClientIP()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 校验 timestamp 时效窗口（±300s），防止重放攻击
	webhookTs, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || absInt64(time.Now().Unix()-webhookTs) > 300 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook timestamp 超出容忍范围或格式错误 client_ip=%s event_id=%s timestamp=%s", c.ClientIP(), eventId, timestamp))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// 验签：hex(HMAC-SHA256(webhook_secret, "{timestamp}.{event_id}.{payload}"))
	signedContent := fmt.Sprintf("%s.%s.%s", timestamp, eventId, string(bodyBytes))
	mac := hmac.New(sha256.New, []byte(setting.InfiniWebhookSecret))
	mac.Write([]byte(signedContent))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 验签失败 client_ip=%s event_id=%s", c.ClientIP(), eventId))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	var event struct {
		Event           string `json:"event"`
		OrderId         string `json:"order_id"`
		ClientReference string `json:"client_reference"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		Status          string `json:"status"`
	}
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 解析失败 event_id=%s error=%q", eventId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini webhook event=%s client_reference=%s status=%s event_id=%s",
		event.Event, event.ClientReference, event.Status, eventId))

	// 仅处理 order.completed，其余事件直接 200
	if event.Event != "order.completed" {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	tradeNo := event.ClientReference
	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 缺少 client_reference event_id=%s", eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// PAY-001/002/003：order.completed 必须强制校验金额、币种、provider order id
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 订单不存在 trade_no=%s event_id=%s", tradeNo, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if topUp.Status != common.TopUpStatusPending {
		// 已处理（幂等）或已失败，无需再次处理
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini webhook 订单已非 pending 状态，忽略 trade_no=%s status=%s event_id=%s", tradeNo, topUp.Status, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	// PAY-001：金额必须存在且与本地订单一致
	if strings.TrimSpace(event.Amount) == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 缺少 amount 字段，拒绝落账 trade_no=%s event_id=%s", tradeNo, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	notifiedAmount, parseErr := decimal.NewFromString(strings.TrimSpace(event.Amount))
	amountCurrency := topUp.PaymentCurrency
	if amountCurrency == "" {
		amountCurrency = event.Currency
	}
	expectedAmount := roundInfiniAmount(decimal.NewFromFloat(topUp.Money), amountCurrency)
	if parseErr != nil || !notifiedAmount.Equal(expectedAmount) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 金额不匹配 trade_no=%s notify_amount=%s order_money=%.2f client_ip=%s",
			tradeNo, event.Amount, topUp.Money, c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	// PAY-002：币种必须存在且与下单时存储的币种一致（不受运行时配置变更影响）
	if strings.TrimSpace(event.Currency) == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 缺少 currency 字段，拒绝落账 trade_no=%s event_id=%s", tradeNo, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	// 优先使用订单存储的 PaymentCurrency；兜底用当前配置（兼容旧订单）
	expectedCurrency := topUp.PaymentCurrency
	if expectedCurrency == "" {
		// 兜底：兼容旧订单（PaymentCurrency 为空），使用配置列表第一项
		if opts := getInfiniCurrencyOptions(); len(opts) > 0 {
			expectedCurrency = opts[0].Currency
		} else {
			expectedCurrency = "USD"
		}
	}
	if !strings.EqualFold(strings.TrimSpace(event.Currency), expectedCurrency) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 币种不匹配 trade_no=%s notify_currency=%s expected_currency=%s client_ip=%s",
			tradeNo, event.Currency, expectedCurrency, c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	// PAY-003：正常创建流程必保证 ProviderOrderId 非空；
	// 若为空说明历史遗留数据或创建异常，直接拒绝防止绑定降级
	if topUp.ProviderOrderId == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 本地订单缺少 provider_order_id，拒绝落账 trade_no=%s event_id=%s", tradeNo, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	notifyOrderId := strings.TrimSpace(event.OrderId)
	if notifyOrderId == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook 缺少 order_id 字段，拒绝落账 trade_no=%s local_id=%s event_id=%s",
			tradeNo, topUp.ProviderOrderId, eventId))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if topUp.ProviderOrderId != notifyOrderId {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini webhook provider_order_id 不匹配 trade_no=%s local_id=%s notify_id=%s client_ip=%s",
			tradeNo, topUp.ProviderOrderId, notifyOrderId, c.ClientIP()))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	if err := model.RechargeInfini(tradeNo, c.ClientIP()); err != nil {
		if errors.Is(err, model.ErrPaymentMethodMismatch) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini webhook 支付方式不匹配 trade_no=%s", tradeNo))
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
			return
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 充值处理失败 trade_no=%s event_id=%s error=%q", tradeNo, eventId, err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini 充值成功 trade_no=%s event_id=%s client_ip=%s", tradeNo, eventId, c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
