package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// ─── Infini 请求签名 ───────────────────────────────────────────────────────────

// infiniGMTNow 返回 RFC 1123 GMT 格式的当前时间
func infiniGMTNow() string {
	return time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
}

// infiniSignedHeaders 为 Infini API 构建带 HMAC-SHA256 签名的请求头。
// 签名串格式（末尾含换行）：
//
//	{keyId}\n{METHOD} {path}\ndate: {GMT}\n
func infiniSignedHeaders(method, path string, body []byte) map[string]string {
	keyId := strings.TrimSpace(setting.InfiniApiKey)
	secret := strings.TrimSpace(setting.InfiniApiSecret)
	gmt := infiniGMTNow()

	signingString := fmt.Sprintf("%s\n%s %s\ndate: %s\n", keyId, method, path, gmt)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))


	headers := map[string]string{
		"Date": gmt,
		"Authorization": fmt.Sprintf(
			`Signature keyId="%s",algorithm="hmac-sha256",headers="@request-target date",signature="%s"`,
			keyId, signature,
		),
	}

	if len(body) > 0 {
		h := sha256.Sum256(body)
		headers["Digest"] = "SHA-256=" + base64.StdEncoding.EncodeToString(h[:])
		headers["Content-Type"] = "application/json"
	}
	return headers
}

// infiniPost 向 Infini API 发送 POST 请求并返回原始响应体
func infiniPost(path string, payload any) ([]byte, int, error) {
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}

	url := setting.GetInfiniBaseUrl() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	for k, v := range infiniSignedHeaders("POST", path, body) {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
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
func formatInfiniAmount(amount float64, currency string) string {
	if constant.InfiniZeroDecimalCurrencies[strings.ToUpper(currency)] {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

func getInfiniPayMoney(amount float64, group string, unitPrice float64) float64 {
	originalAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = amount / common.QuotaPerUnit
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(originalAmount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	return amount * unitPrice * topupGroupRatio * discount
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

	currOpt, ok := resolveInfiniCurrency(req.Currency)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的币种"})
		return
	}

	if req.Amount < int64(currOpt.MinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", currOpt.MinTopUp)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getInfiniPayMoney(float64(req.Amount), group, currOpt.UnitPrice)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
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

	// 校验币种：必须在允许列表内，服务端解析，不信任客户端传入的原始字符串
	currOpt, ok := resolveInfiniCurrency(req.Currency)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的币种"})
		return
	}

	if req.Amount < int64(currOpt.MinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", currOpt.MinTopUp)})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	group, _ := model.GetUserGroup(id, true)
	payMoney := getInfiniPayMoney(float64(req.Amount), group, currOpt.UnitPrice)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// 用 decimal 精确运算计算配额单位，避免 float64 累积误差
	// 配额单位数 = payUSD × binanceRate / Price，floor 取整（用户不超额）
	// topUp.Amount 存配额单位，RechargeInfini 再乘 QuotaPerUnit 得到 tokens
	binanceRateForAmount := service.GetUSDCNYRate()
	systemPrice := operation_setting.Price
	if systemPrice <= 0 {
		systemPrice = 1.0
	}
	dPayMoney := decimal.NewFromFloat(payMoney)
	dRate := decimal.NewFromFloat(binanceRateForAmount)
	dPrice := decimal.NewFromFloat(systemPrice)
	quotaUnitsDec := dPayMoney.Mul(dRate).Div(dPrice)
	amount := quotaUnitsDec.Floor().IntPart() // 配额单位（整数）
	if amount < 1 {
		amount = 1
	}

	// 使用服务端解析出的标准化大写币种，不直接使用客户端传入值
	currency := strings.ToUpper(currOpt.Currency)

	tradeNo := fmt.Sprintf("INFINI-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
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

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/infini/webhook"
	if setting.InfiniNotifyUrl != "" {
		notifyUrl = setting.InfiniNotifyUrl
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
		"notify_url":       notifyUrl,
	}

	// 若管理员配置了 pay_methods，传入以限定结账页支付方式
	// 1=加密货币, 2=银行卡, 3=Binance Pay, 5=Apple Pay, 6=Google Pay
	if pm := strings.TrimSpace(setting.InfiniPayMethods); pm != "" && pm != "[]" {
		var payMethodInts []int
		if err := common.UnmarshalJsonStr(pm, &payMethodInts); err == nil && len(payMethodInts) > 0 {
			orderPayload["pay_methods"] = payMethodInts
		}
	}

	respBody, statusCode, err := infiniPost("/v1/acquiring/order", orderPayload)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 创建订单请求失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			CheckoutUrl string `json:"checkout_url"`
			OrderId     string `json:"order_id"`
		} `json:"data"`
	}
	if err := common.Unmarshal(respBody, &result); err != nil || statusCode != http.StatusOK || result.Code != 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Infini 创建订单业务失败 user_id=%d trade_no=%s status=%d code=%d message=%q body=%s",
			id, tradeNo, statusCode, result.Code, result.Message, string(respBody)))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	// provider_order_id 是 webhook 双向绑定的关键字段：必须非空且保存成功
	// 任一条件不满足都标记订单 failed 并拒绝返回 checkout_url，防止绑定降级
	infiniOrderId := strings.TrimSpace(result.Data.OrderId)
	if infiniOrderId == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 响应缺少 order_id，拒绝返回支付链接 user_id=%d trade_no=%s", id, tradeNo))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	topUp.ProviderOrderId = infiniOrderId
	topUp.PaymentCurrency = currency
	if err := topUp.Update(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Infini 保存 provider_order_id 失败，拒绝返回支付链接 user_id=%d trade_no=%s infini_order_id=%s error=%q",
			id, tradeNo, infiniOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Infini 充值订单创建成功 user_id=%d trade_no=%s infini_order_id=%s amount=%d money=%.2f currency=%s",
		id, tradeNo, infiniOrderId, req.Amount, payMoney, currency))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": result.Data.CheckoutUrl,
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
	if parseErr != nil || !notifiedAmount.Equal(decimal.NewFromFloat(topUp.Money)) {
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
