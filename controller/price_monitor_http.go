package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"

	"github.com/gin-gonic/gin"
)

type priceMonitorSettingsRequest struct {
	Enabled          bool              `json:"enabled"`
	IntervalMinutes  int               `json:"interval_minutes"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	IncludeModelsDev bool              `json:"include_models_dev"`
	ModelWhitelist   string            `json:"model_whitelist"`
	CustomEndpoints  map[string]string `json:"custom_endpoints"`
	// 成本系数核对的三项用指针：旧客户端不带这些字段时不写入，保留库里的值。
	UpstreamLogQueriesPerHost *int `json:"upstream_log_queries_per_host,omitempty"`
	UpstreamRatioRefreshHours *int `json:"upstream_ratio_refresh_hours,omitempty"`
	UpstreamRatioMaxAgeDays   *int `json:"upstream_ratio_max_age_days,omitempty"`
}

// priceMonitorOptionalInt 校验一个可选整数配置项：未提供时不写入，提供时必须在 [low, high] 内。
func priceMonitorOptionalInt(values map[string]string, key string, value *int, low, high int) bool {
	if value == nil {
		return true
	}
	if *value < low || *value > high {
		return false
	}
	values[key] = strconv.Itoa(*value)
	return true
}

func validatePriceMonitorSettingsRequest(request priceMonitorSettingsRequest) (map[string]string, bool) {
	if request.IntervalMinutes < price_monitor_setting.MinIntervalMinutes || request.IntervalMinutes > price_monitor_setting.MaxIntervalMinutes ||
		request.TimeoutSeconds < price_monitor_setting.MinTimeoutSeconds || request.TimeoutSeconds > price_monitor_setting.MaxTimeoutSeconds {
		return nil, false
	}
	optional := map[string]string{}
	if !priceMonitorOptionalInt(optional, "upstream_log_queries_per_host", request.UpstreamLogQueriesPerHost, 0, price_monitor_setting.MaxUpstreamLogQueriesPerHost) ||
		!priceMonitorOptionalInt(optional, "upstream_ratio_refresh_hours", request.UpstreamRatioRefreshHours, 1, price_monitor_setting.MaxUpstreamRatioRefreshHours) ||
		!priceMonitorOptionalInt(optional, "upstream_ratio_max_age_days", request.UpstreamRatioMaxAgeDays, 1, price_monitor_setting.MaxUpstreamRatioMaxAgeDays) {
		return nil, false
	}
	endpoints, err := price_monitor_setting.ValidateCustomEndpoints(request.CustomEndpoints)
	if err != nil {
		return nil, false
	}
	encodedEndpoints, err := common.Marshal(endpoints)
	if err != nil {
		return nil, false
	}
	values := map[string]string{
		"enabled":            strconv.FormatBool(request.Enabled),
		"interval_minutes":   strconv.Itoa(request.IntervalMinutes),
		"timeout_seconds":    strconv.Itoa(request.TimeoutSeconds),
		"include_official":   "true",
		"include_models_dev": strconv.FormatBool(request.IncludeModelsDev),
		"model_whitelist":    request.ModelWhitelist,
		"custom_endpoints":   string(encodedEndpoints),
	}
	for key, value := range optional {
		values[key] = value
	}
	return values, true
}

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

func priceMonitorStatusSnapshot(snapshot PriceMonitorSnapshot) gin.H {
	return gin.H{
		"checked_at":              snapshot.CheckedAt,
		"status":                  snapshot.Status,
		"source_total":            snapshot.SourceTotal,
		"source_ok":               snapshot.SourceOK,
		"source_err":              snapshot.SourceError,
		"model_count":             snapshot.ModelCount,
		"item_count":              snapshot.ItemCount,
		"comparison_model_counts": snapshot.ComparisonModelCounts,
		"access_password":         snapshot.AccessPassword,
		"password_expire_at":      snapshot.PasswordExpireAt,
	}
}

func GetPriceMonitorStatus(c *gin.Context) {
	snapshot := getPriceMonitorStore().Get()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"config":             price_monitor_setting.GetPriceMonitorSetting().Normalized(),
			"snapshot":           priceMonitorStatusSnapshot(snapshot),
			"running":            priceMonitorRunning.Load(),
			"last_attempt_at":    priceMonitorLastAttempt.Load(),
			"last_attempt_error": getPriceMonitorRuntimeError(),
			"storage_scope":      priceMonitorStorageScope(),
			"is_master":          common.IsMasterNode,
			// 行内改价请求必须带上它，让服务端能检测到「我读到的价格已被其他管理员改过」。
			"pricing_version": model.GetPricingConfigVersion(),
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

func UpdatePriceMonitorSettings(c *gin.Context) {
	var request priceMonitorSettingsRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	values, valid := validatePriceMonitorSettingsRequest(request)
	if !valid {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	applied, err := model.SaveConfigGroup("price_monitor_setting", values)
	if err != nil {
		logger.LogError(c, "failed to update price monitor settings: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	recordManageAudit(c, "price_monitor.settings.update", map[string]interface{}{"applied": applied})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"applied": applied}})
}

func GetPriceMonitorResults(c *gin.Context) {
	query := priceMonitorQueryFromContext(c)
	result := queryPriceMonitorMatrix(getPriceMonitorStore().Get(), query)
	localizePriceMonitorMatrixResult(&result, priceMonitorTranslator(c))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

func priceMonitorTranslator(c *gin.Context) func(key string, args map[string]any) string {
	return func(key string, args map[string]any) string { return i18n.T(c, key, args) }
}

// localizePriceMonitorMatrixResult 按请求语言填写固定来源的表头名称与阶梯档位说明。
// 快照里只存条件与来源类型，旧快照里残留的文字在这里被覆盖。
// 档位切片与快照共用底层数组，必须先复制再改；Prices 是查询时新建的 map，可以直接写回。
func localizePriceMonitorMatrixResult(result *priceMonitorMatrixQueryResult, translate func(key string, args map[string]any) string) {
	names := map[string]string{
		priceMonitorPlatformKey: translate(i18n.MsgPriceMonitorSourcePlatform, nil),
		priceSourceOfficial:     translate(i18n.MsgPriceMonitorSourceOfficial, nil),
		priceSourceModelsDev:    translate(i18n.MsgPriceMonitorSourceModelsDev, nil),
	}
	for _, headers := range [][]PriceMonitorSourceHeader{result.SourceHeaders, result.AvailableSourceHeaders} {
		for i := range headers {
			if name, fixed := names[headers[i].Type]; fixed {
				headers[i].Name = name
			}
		}
	}
	for _, item := range result.Items {
		for key, cell := range item.Prices {
			if len(cell.Tiers) == 0 {
				continue
			}
			tiers := make([]PriceMonitorPriceTier, len(cell.Tiers))
			for i, tier := range cell.Tiers {
				tier.Range = priceMonitorTierLabel(tier, translate)
				tiers[i] = tier
			}
			cell.Tiers = tiers
			item.Prices[key] = cell
		}
	}
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
	result := queryPriceMonitorMatrix(snapshot, query)
	stripPriceMonitorPublicResult(&result)
	localizePriceMonitorMatrixResult(&result, priceMonitorTranslator(c))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

// stripPriceMonitorPublicResult 去掉分享页不该看到的内部信息：
//   - 保本下限（逐字段保本价与决定它的渠道）；
//   - 亏损判定的四个系数：售价系数、实测成本系数、配置成本系数（即成本系数）、上游分组倍率。
//     成本系数与上游倍率说明了我们的采购成本与在上游拿到的折扣；实测系数乘了上游倍率，结合页面上
//     的来源价与平台价即可反推。只留亏损类型徽标；
//   - 渠道 ID。
func stripPriceMonitorPublicResult(result *priceMonitorMatrixQueryResult) {
	for i := range result.Items {
		result.Items[i].RepairFloor = nil
		for key, cell := range result.Items[i].Prices {
			if cell.SellFactor == nil && cell.MeasuredFactor == nil && cell.ConfiguredFactor == nil && cell.UpstreamFactor == nil && cell.LossLines == nil {
				continue
			}
			cell.SellFactor = nil
			cell.MeasuredFactor = nil
			cell.ConfiguredFactor = nil
			cell.UpstreamFactor = nil
			cell.LossLines = nil
			result.Items[i].Prices[key] = cell
		}
	}
	for i := range result.SourceHeaders {
		result.SourceHeaders[i].ChannelId = 0
	}
	for i := range result.AvailableSourceHeaders {
		result.AvailableSourceHeaders[i].ChannelId = 0
	}
}

func PriceMonitorView(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data: https:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	logo := strings.TrimSpace(common.Logo)
	if logo == "" {
		logo = "/logo.png"
	}
	lang := i18n.GetLangFromContext(c)
	c.Header("Content-Language", lang)
	c.Header("Vary", "Accept-Language")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderPriceMonitorPage(lang, logo)))
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
