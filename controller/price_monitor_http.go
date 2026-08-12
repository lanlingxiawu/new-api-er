package controller

import (
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"

	"github.com/gin-gonic/gin"
)

type publicPriceMonitorQueryRequest struct {
	Password   string   `json:"password"`
	Model      string   `json:"model"`
	Field      string   `json:"field"`
	Source     string   `json:"source"`
	SourceKeys []string `json:"source_keys"`
	Comparison string   `json:"comparison"`
	Page       int      `json:"page"`
	PageSize   int      `json:"page_size"`
}

type priceMonitorShareSummary struct {
	CheckedAt        int64  `json:"checked_at"`
	Status           string `json:"status"`
	SourceTotal      int    `json:"source_total"`
	SourceOK         int    `json:"source_ok"`
	SourceError      int    `json:"source_err"`
	ModelCount       int    `json:"model_count"`
	ItemCount        int    `json:"item_count"`
	ShareURL         string `json:"share_url"`
	AccessPassword   string `json:"access_password"`
	PasswordExpireAt int64  `json:"password_expire_at"`
}

func buildPriceMonitorShareSummary(snapshot PriceMonitorSnapshot) priceMonitorShareSummary {
	return priceMonitorShareSummary{
		CheckedAt:        snapshot.CheckedAt,
		Status:           snapshot.Status,
		SourceTotal:      snapshot.SourceTotal,
		SourceOK:         snapshot.SourceOK,
		SourceError:      snapshot.SourceError,
		ModelCount:       snapshot.ModelCount,
		ItemCount:        snapshot.ItemCount,
		ShareURL:         paymentReturnPath("/price_monitor/view"),
		AccessPassword:   snapshot.AccessPassword,
		PasswordExpireAt: snapshot.PasswordExpireAt,
	}
}

func GetPriceMonitorStatus(c *gin.Context) {
	snapshot := getPriceMonitorStore().Get()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"config": price_monitor_setting.GetPriceMonitorSetting().Normalized(),
			"snapshot": gin.H{
				"checked_at":         snapshot.CheckedAt,
				"status":             snapshot.Status,
				"source_total":       snapshot.SourceTotal,
				"source_ok":          snapshot.SourceOK,
				"source_err":         snapshot.SourceError,
				"model_count":        snapshot.ModelCount,
				"item_count":         snapshot.ItemCount,
				"access_password":    snapshot.AccessPassword,
				"password_expire_at": snapshot.PasswordExpireAt,
			},
			"running":            priceMonitorRunning.Load(),
			"last_attempt_at":    priceMonitorLastAttempt.Load(),
			"last_attempt_error": getPriceMonitorRuntimeError(),
			"storage_scope":      priceMonitorStorageScope(),
			"is_master":          common.IsMasterNode,
		},
	})
}

func RunPriceMonitor(c *gin.Context) {
	if !common.IsMasterNode {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorMasterRequired)
		return
	}
	if !triggerPriceMonitorCheck() {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorAlreadyRunning)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func GetPriceMonitorResults(c *gin.Context) {
	query := priceMonitorQueryFromContext(c)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": queryPriceMonitorMatrix(getPriceMonitorStore().Get(), query)})
}

func GetPriceMonitorInconsistencies(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": buildPriceMonitorShareSummary(getPriceMonitorStore().Get())})
}

func PublicPriceMonitorQuery(c *gin.Context) {
	var request publicPriceMonitorQueryRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	snapshot := getPriceMonitorStore().Get()
	if !validPriceMonitorPassword(snapshot, request.Password, time.Now()) {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorInvalidPassword)
		return
	}
	query := priceMonitorQuery{Model: request.Model, Source: request.Source, SourceKeys: request.SourceKeys, SourceKeysSet: request.SourceKeys != nil, Comparison: request.Comparison, Page: request.Page, PageSize: request.PageSize}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": queryPriceMonitorMatrix(snapshot, query)})
}

func PriceMonitorView(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data: https:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	logo := strings.TrimSpace(common.Logo)
	if logo == "" {
		logo = "/logo.png"
	}
	page := strings.ReplaceAll(priceMonitorHTML, "__LOGO_SRC__", html.EscapeString(logo))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

func priceMonitorQueryFromContext(c *gin.Context) priceMonitorQuery {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	sourceKeysValue, sourceKeysSet := c.GetQuery("source_keys")
	var sourceKeys []string
	if sourceKeysSet {
		sourceKeys = make([]string, 0)
		for _, key := range strings.Split(sourceKeysValue, ",") {
			if key = strings.TrimSpace(key); key != "" {
				sourceKeys = append(sourceKeys, key)
			}
		}
	}
	return priceMonitorQuery{
		Model:         strings.TrimSpace(c.Query("model")),
		Field:         strings.TrimSpace(c.Query("field")),
		Source:        strings.TrimSpace(c.Query("source")),
		SourceKeys:    sourceKeys,
		SourceKeysSet: sourceKeysSet,
		Comparison:    strings.TrimSpace(c.Query("comparison")),
		Page:          page,
		PageSize:      pageSize,
	}
}
