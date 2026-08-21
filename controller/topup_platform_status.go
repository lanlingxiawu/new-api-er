package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/smartwalle/alipay/v3"
)

const (
	maxPlatformStatusTradeNos     = 100
	platformStatusQueryWorkers    = 6
	platformStatusGlobalWorkers   = 12
	platformStatusProviderTimeout = 5 * time.Second
	platformStatusBatchTimeout    = 20 * time.Second
	maxPlatformStatusResponseSize = 1 << 20
)

var platformStatusQuerySlots = make(chan struct{}, platformStatusGlobalWorkers)

type platformPaymentSnapshot struct {
	Status string
	Raw    string
}

type platformStatusRequest struct {
	TradeNos []string `json:"trade_nos"`
}

type platformStatusItem struct {
	TradeNo                        string `json:"trade_no"`
	Provider                       string `json:"provider,omitempty"`
	Result                         string `json:"result"`
	PlatformPaymentStatus          string `json:"platform_payment_status,omitempty"`
	PlatformPaymentStatusRaw       string `json:"platform_payment_status_raw,omitempty"`
	PlatformPaymentStatusCheckedAt int64  `json:"platform_payment_status_checked_at,omitempty"`
}

type platformStatusSummary struct {
	Requested int `json:"requested"`
	Updated   int `json:"updated"`
	// Failed counts orders that were eligible but whose upstream query or
	// persistence failed (query_failed / persist_failed) — these are worth a retry.
	Failed int `json:"failed"`
	// Skipped counts orders that could not be queried by design
	// (unsupported provider / order not found) — retrying will not help.
	Skipped int `json:"skipped"`
}

type platformStatusResponse struct {
	Summary platformStatusSummary `json:"summary"`
	Items   []platformStatusItem  `json:"items"`
}

type platformStatusQueryOutcome struct {
	snapshot platformPaymentSnapshot
	err      error
}

type platformStatusQueryJob struct {
	index int
	order *model.TopUp
}

func normalizePlatformStatusTradeNos(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxPlatformStatusTradeNos {
		return nil, errors.New("invalid trade number count")
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tradeNo := strings.TrimSpace(value)
		if tradeNo == "" {
			return nil, errors.New("trade number cannot be empty")
		}
		if _, exists := seen[tradeNo]; exists {
			continue
		}
		seen[tradeNo] = struct{}{}
		normalized = append(normalized, tradeNo)
	}
	return normalized, nil
}

func mapAlipayPlatformPaymentStatus(status alipay.TradeStatus, subCode string) (platformPaymentSnapshot, bool) {
	subCode = strings.ToUpper(strings.TrimSpace(subCode))
	if subCode == "ACQ.TRADE_NOT_EXIST" || subCode == "TRADE_NOT_EXIST" {
		return platformPaymentSnapshot{
			Status: model.PlatformPaymentStatusNotCredited,
			Raw:    "TRADE_NOT_EXIST",
		}, true
	}

	raw := strings.ToUpper(strings.TrimSpace(string(status)))
	switch alipay.TradeStatus(raw) {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: raw}, true
	case alipay.TradeStatusWaitBuyerPay, alipay.TradeStatusClosed:
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: raw}, true
	default:
		if raw == "" {
			return platformPaymentSnapshot{}, false
		}
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusUnknown, Raw: raw}, true
	}
}

func mapInfiniPlatformPaymentStatus(status string) (platformPaymentSnapshot, bool) {
	raw := strings.ToLower(strings.TrimSpace(status))
	switch raw {
	case "paid":
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: raw}, true
	case "pending", "processing", "partial_paid", "expired":
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: raw}, true
	default:
		if raw == "" {
			return platformPaymentSnapshot{}, false
		}
		return platformPaymentSnapshot{Status: model.PlatformPaymentStatusUnknown, Raw: raw}, true
	}
}

type alipayTradeQuerier interface {
	TradeQuery(context.Context, alipay.TradeQuery) (*alipay.TradeQueryRsp, error)
}

var getAlipayTradeQuerier = func() alipayTradeQuerier {
	client := GetAlipayClient()
	if client == nil {
		return nil
	}
	return client
}

func queryAlipayPlatformPaymentStatus(ctx context.Context, order *model.TopUp) (platformPaymentSnapshot, error) {
	client := getAlipayTradeQuerier()
	if client == nil {
		return platformPaymentSnapshot{}, errors.New("alipay client is not configured")
	}

	response, err := client.TradeQuery(ctx, alipay.TradeQuery{OutTradeNo: order.TradeNo})
	if err != nil {
		return platformPaymentSnapshot{}, fmt.Errorf("alipay trade query failed: %w", err)
	}
	if response == nil {
		return platformPaymentSnapshot{}, errors.New("alipay trade query returned an empty response")
	}
	if snapshot, ok := mapAlipayPlatformPaymentStatus(response.TradeStatus, response.SubCode); ok {
		if response.IsSuccess() || snapshot.Raw == "TRADE_NOT_EXIST" {
			return snapshot, nil
		}
	}
	return platformPaymentSnapshot{}, fmt.Errorf("alipay trade query rejected: code=%s sub_code=%s", response.Code, response.SubCode)
}

type infiniPlatformOrder struct {
	OrderID         string `json:"order_id"`
	Status          string `json:"status"`
	ClientReference string `json:"client_reference"`
}

type infiniPlatformOrderResponse struct {
	Code    *int                 `json:"code"`
	Message string               `json:"message"`
	Data    *infiniPlatformOrder `json:"data"`
	infiniPlatformOrder
}

var platformStatusHTTPClient = &http.Client{Timeout: platformStatusProviderTimeout}
var infiniPlatformStatusBaseURL = setting.GetInfiniBaseUrl

func queryInfiniPlatformPaymentStatus(ctx context.Context, order *model.TopUp) (platformPaymentSnapshot, error) {
	providerOrderID := strings.TrimSpace(order.ProviderOrderId)
	if providerOrderID == "" {
		return platformPaymentSnapshot{}, errors.New("infini provider order id is missing")
	}
	if strings.TrimSpace(setting.InfiniApiKey) == "" || strings.TrimSpace(setting.InfiniApiSecret) == "" {
		return platformPaymentSnapshot{}, errors.New("infini credentials are not configured")
	}

	query := url.Values{}
	query.Set("order_id", providerOrderID)
	requestTarget := "/v1/acquiring/order?" + query.Encode()
	requestURL := strings.TrimRight(infiniPlatformStatusBaseURL(), "/") + requestTarget
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return platformPaymentSnapshot{}, err
	}
	for key, value := range infiniSignedHeaders(http.MethodGet, requestTarget, nil) {
		req.Header.Set(key, value)
	}

	response, err := platformStatusHTTPClient.Do(req)
	if err != nil {
		return platformPaymentSnapshot{}, fmt.Errorf("infini order query failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxPlatformStatusResponseSize))
		return platformPaymentSnapshot{}, fmt.Errorf("infini order query returned status %d", response.StatusCode)
	}

	var payload infiniPlatformOrderResponse
	if err := common.DecodeJson(io.LimitReader(response.Body, maxPlatformStatusResponseSize), &payload); err != nil {
		return platformPaymentSnapshot{}, fmt.Errorf("failed to decode infini order query response: %w", err)
	}
	if payload.Code != nil && *payload.Code != 0 {
		return platformPaymentSnapshot{}, fmt.Errorf("infini order query rejected: code=%d", *payload.Code)
	}

	platformOrder := payload.infiniPlatformOrder
	if payload.Data != nil {
		platformOrder = *payload.Data
	}
	if platformOrder.OrderID != "" && platformOrder.OrderID != providerOrderID {
		return platformPaymentSnapshot{}, errors.New("infini order id does not match")
	}
	if platformOrder.ClientReference != "" && platformOrder.ClientReference != order.TradeNo {
		return platformPaymentSnapshot{}, errors.New("infini client reference does not match")
	}
	snapshot, ok := mapInfiniPlatformPaymentStatus(platformOrder.Status)
	if !ok {
		return platformPaymentSnapshot{}, errors.New("infini order status is empty")
	}
	return snapshot, nil
}

func queryTopUpPlatformPaymentStatus(ctx context.Context, order *model.TopUp) (platformPaymentSnapshot, error) {
	select {
	case platformStatusQuerySlots <- struct{}{}:
		defer func() { <-platformStatusQuerySlots }()
	case <-ctx.Done():
		return platformPaymentSnapshot{}, ctx.Err()
	}

	providerCtx, cancel := context.WithTimeout(ctx, platformStatusProviderTimeout)
	defer cancel()
	switch order.PaymentProvider {
	case model.PaymentProviderAlipay:
		return queryAlipayPlatformPaymentStatus(providerCtx, order)
	case model.PaymentProviderInfini:
		return queryInfiniPlatformPaymentStatus(providerCtx, order)
	default:
		return platformPaymentSnapshot{}, errors.New("unsupported payment provider")
	}
}

// AdminQueryTopUpPlatformStatus queries and persists payment-platform status
// snapshots for one or more top-up orders. The route is admin-only; individual
// provider failures are isolated and reported per item.
func AdminQueryTopUpPlatformStatus(c *gin.Context) {
	var request platformStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.LogWarn(c.Request.Context(), "invalid platform payment status query request")
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	tradeNos, err := normalizePlatformStatusTradeNos(request.TradeNos)
	if err != nil {
		logger.LogWarn(c.Request.Context(), "invalid platform payment status trade numbers")
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	orders, err := model.GetTopUpsForPlatformStatus(tradeNos)
	if err != nil {
		logger.LogError(c.Request.Context(), "failed to load topups for platform payment status query")
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	ordersByTradeNo := make(map[string]*model.TopUp, len(orders))
	for _, order := range orders {
		ordersByTradeNo[order.TradeNo] = order
	}

	items := make([]platformStatusItem, len(tradeNos))
	outcomes := make([]platformStatusQueryOutcome, len(tradeNos))
	jobs := make([]platformStatusQueryJob, 0, len(tradeNos))
	for index, tradeNo := range tradeNos {
		items[index] = platformStatusItem{TradeNo: tradeNo, Result: "not_found"}
		order := ordersByTradeNo[tradeNo]
		if order == nil {
			continue
		}
		items[index].Provider = order.PaymentProvider
		if order.PaymentProvider != model.PaymentProviderAlipay && order.PaymentProvider != model.PaymentProviderInfini {
			items[index].Result = "unsupported"
			continue
		}
		items[index].Result = "query_failed"
		jobs = append(jobs, platformStatusQueryJob{index: index, order: order})
	}

	batchCtx, cancel := context.WithTimeout(c.Request.Context(), platformStatusBatchTimeout)
	defer cancel()
	if len(jobs) > 0 {
		workerCount := platformStatusQueryWorkers
		if len(jobs) < workerCount {
			workerCount = len(jobs)
		}
		jobChannel := make(chan platformStatusQueryJob)
		var workers sync.WaitGroup
		workers.Add(workerCount)
		for range workerCount {
			go func() {
				defer workers.Done()
				for job := range jobChannel {
					snapshot, queryErr := queryTopUpPlatformPaymentStatus(batchCtx, job.order)
					outcomes[job.index] = platformStatusQueryOutcome{snapshot: snapshot, err: queryErr}
				}
			}()
		}
		for _, job := range jobs {
			jobChannel <- job
		}
		close(jobChannel)
		workers.Wait()
	}

	updated := 0
	for _, job := range jobs {
		outcome := outcomes[job.index]
		if outcome.err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf(
				"platform payment status query failed provider=%s trade_no=%s error=%q",
				job.order.PaymentProvider,
				job.order.TradeNo,
				outcome.err.Error(),
			))
			continue
		}

		checkedAt := common.GetTimestamp()
		if err := model.UpdateTopUpPlatformPaymentStatus(
			job.order.Id,
			outcome.snapshot.Status,
			outcome.snapshot.Raw,
			checkedAt,
		); err != nil {
			items[job.index].Result = "persist_failed"
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"failed to persist platform payment status provider=%s trade_no=%s error=%q",
				job.order.PaymentProvider,
				job.order.TradeNo,
				err.Error(),
			))
			continue
		}

		items[job.index].Result = "updated"
		items[job.index].PlatformPaymentStatus = outcome.snapshot.Status
		items[job.index].PlatformPaymentStatusRaw = outcome.snapshot.Raw
		items[job.index].PlatformPaymentStatusCheckedAt = checkedAt
		updated++
	}

	failed := 0
	skipped := 0
	for _, item := range items {
		switch item.Result {
		case "updated":
			// counted via updated above
		case "query_failed", "persist_failed":
			failed++
		default: // unsupported / not_found
			skipped++
		}
	}

	common.ApiSuccess(c, platformStatusResponse{
		Summary: platformStatusSummary{
			Requested: len(items),
			Updated:   updated,
			Failed:    failed,
			Skipped:   skipped,
		},
		Items: items,
	})
}
