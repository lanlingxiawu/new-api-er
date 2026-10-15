package controller

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type priceMonitorDraftCoverage struct {
	Compared int `json:"compared"`
	Unknown  int `json:"unknown"`
}

type priceMonitorDraftPreview struct {
	ModelName        string                       `json:"model_name"`
	ComparisonStatus string                       `json:"comparison_status"`
	Coverage         priceMonitorDraftCoverage    `json:"coverage"`
	Violations       []priceMonitorFloorViolation `json:"violations"`
}

type priceMonitorDraftRequest struct {
	CheckedAt int64                      `json:"checked_at"`
	Changes   []model.ModelPricingChange `json:"changes"`
}

var errPriceMonitorDraftChanged = errors.New("pricing inspection context changed")

// priceMonitorDraftContextVersion binds an acknowledgement to both the complete
// draft and the immutable quotes/group factors used to review it. Expressions
// are explicitly included because public snapshot JSON intentionally omits them.
func priceMonitorDraftContextVersion(snapshot PriceMonitorSnapshot, changes []model.ModelPricingChange, contexts map[string]priceMonitorLossContext, databaseVersion ...int64) string {
	version := model.GetPricingConfigVersion()
	if len(databaseVersion) > 0 {
		version = databaseVersion[0]
	}
	expressions := make(map[string]map[string]string, len(snapshot.MatrixItems))
	for _, item := range snapshot.MatrixItems {
		values := map[string]string{}
		for key, cell := range item.Prices {
			values[key] = cell.Expr
		}
		expressions[item.Model] = values
	}
	encoded, err := common.Marshal(struct {
		PricingVersion int64
		Groups         map[string]float64
		GroupGroups    map[string]map[string]float64
		Snapshot       PriceMonitorSnapshot
		Expressions    map[string]map[string]string
		Changes        []model.ModelPricingChange
		Contexts       map[string]priceMonitorLossContext
	}{version, ratio_setting.GetGroupRatioCopy(), ratio_setting.GetGroupGroupRatioCopy(), snapshot, expressions, changes, contexts})
	if err != nil {
		return ""
	}
	return billingexpr.ExprHashString(string(encoded))
}

func priceMonitorPricingCell(name string, pricing model.PricingValues) (PriceMonitorPriceCell, bool) {
	data := make(map[string]any, len(pricing))
	for field, key := range priceMonitorApplyFieldOptionKeys {
		if value, exists := pricing[key]; exists {
			data[field] = map[string]any{name: value}
		}
	}
	for _, field := range []string{"billing_mode", "billing_expr"} {
		if value, exists := pricing["billing_setting."+field]; exists {
			data[field] = map[string]any{name: value}
		}
	}
	cell, ok := priceMonitorCell(data, name)
	if ok && cell.actualBillingMode() == priceMonitorModeToken && common.QuotaPerUnit > 0 {
		factor := 1_000_000 / common.QuotaPerUnit / 2
		if cell.Input != nil {
			value := *cell.Input * factor
			cell.Input = &value
		}
		if cell.Output != nil {
			value := *cell.Output * factor
			cell.Output = &value
		}
		for index := range cell.Lanes {
			if cell.Lanes[index].Price != nil {
				value := *cell.Lanes[index].Price * factor
				cell.Lanes[index].Price = &value
			}
		}
	}
	return cell, ok
}

// priceMonitorStandardPrice only lifts known token dimensions. Missing source
// dimensions remain unknown; no absent quote is interpreted as a free price.
func priceMonitorStandardPrice(cell PriceMonitorPriceCell) (*billingexpr.StandardAnalysis, error) {
	if cell.UnavailableReason != "" {
		return nil, errors.New("quote unavailable")
	}
	if cell.actualBillingMode() == priceMonitorModeExpression {
		return priceMonitorStandardCellAnalysis(cell)
	}
	if cell.actualBillingMode() != priceMonitorModeToken || cell.Input == nil || cell.Output == nil {
		return nil, errors.New("incomplete token quote")
	}
	terms := []string{fmt.Sprintf("p * %.17g", *cell.Input), fmt.Sprintf("c * %.17g", *cell.Output)}
	for _, lane := range cell.Lanes {
		if lane.Price == nil {
			continue
		}
		for variable, key := range priceMonitorExpressionLaneKeys {
			if key == lane.Key {
				terms = append(terms, fmt.Sprintf("%s * %.17g", variable, *lane.Price))
				break
			}
		}
	}
	return billingexpr.AnalyzeStandard(strings.Join(terms, " + "))
}

func priceMonitorDraftViolations(name string, draft, source PriceMonitorPriceCell, sellFactor, costFactor float64, binding string) ([]priceMonitorFloorViolation, bool) {
	if sellFactor <= 0 || costFactor < 0 || math.IsNaN(sellFactor) || math.IsInf(sellFactor, 0) || math.IsNaN(costFactor) || math.IsInf(costFactor, 0) {
		return nil, false
	}
	if source.UnavailableReason != "" {
		return nil, false
	}
	if draft.actualBillingMode() == priceMonitorModeRequest && source.actualBillingMode() == priceMonitorModeRequest && draft.Price != nil && source.Price != nil {
		floor := *source.Price * costFactor / sellFactor
		if *draft.Price < floor && !nearlyEqual(*draft.Price, floor) {
			return []priceMonitorFloorViolation{{Model: name, Field: "model_price", Value: *draft.Price, Floor: floor, Binding: binding, Submitted: true}}, true
		}
		return nil, true
	}
	left, err := priceMonitorStandardPrice(draft)
	if err != nil {
		return nil, false
	}
	right, err := priceMonitorStandardPrice(source)
	if err != nil {
		return nil, false
	}
	violations := []priceMonitorFloorViolation{}
	complete := true
	for _, a := range left.Segments {
		for _, b := range right.Segments {
			start := max(a.Min, b.Min)
			if a.Max != nil && start > *a.Max || b.Max != nil && start > *b.Max {
				continue
			}
			prices := left.EffectivePrices(a)
			costs := right.EffectivePrices(b)
			keys := make([]string, 0, len(costs))
			for key := range costs {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if !priceMonitorQuotedVariable(source, key) || !priceMonitorQuotedVariable(draft, key) {
					complete = false
					continue
				}
				value, exists := prices[key]
				if !exists {
					return nil, false
				}
				floor := costs[key] * costFactor / sellFactor
				if value < floor && !nearlyEqual(value, floor) {
					violations = append(violations, priceMonitorFloorViolation{Model: name, Field: strconv.FormatInt(start, 10) + ":" + key, Value: value, Floor: floor, Binding: binding, Submitted: true})
				}
			}
		}
	}
	return violations, complete
}

// A missing legacy lane is an absent quote, not an expression's implicit p/c
// fallback. Only an actual expression permits that inference.
func priceMonitorQuotedVariable(cell PriceMonitorPriceCell, variable string) bool {
	if cell.actualBillingMode() == priceMonitorModeExpression {
		return true
	}
	if variable == "p" {
		return cell.Input != nil
	}
	if variable == "c" {
		return cell.Output != nil
	}
	for _, lane := range cell.Lanes {
		if lane.Key == priceMonitorExpressionLaneKeys[variable] && lane.Price != nil {
			return true
		}
	}
	return false
}

func priceMonitorDraftContexts(snapshot PriceMonitorSnapshot) (map[string]priceMonitorLossContext, error) {
	channels, err := priceMonitorRefreshChannels()
	if err != nil {
		return nil, err
	}
	names := make(map[int]string)
	for _, header := range snapshot.SourceHeaders {
		if header.Type == priceSourceChannel {
			names[header.ChannelId] = header.Key
		}
	}
	contexts := buildPriceMonitorLossContexts(channels, names, snapshot.ChannelCosts)
	settings := price_monitor_setting.GetPriceMonitorSetting().Normalized()
	stale := snapshot.CheckedAt <= 0 || time.Now().Unix()-snapshot.CheckedAt > int64(settings.IntervalMinutes)*60
	// The legacy monitor excludes free groups. A draft review must instead
	// disclose that no positive selling price can prove their break-even floor.
	userGroups := append([]string{""}, priceMonitorConfiguredUserGroups()...)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		key, exists := names[channel.Id]
		if !exists {
			continue
		}
		entry := contexts[key]
		if stale {
			entry.Valid = false
		}
		if cost, ok := snapshot.ChannelCosts[strconv.Itoa(channel.Id)]; ok && cost.ObservedAt > 0 && time.Now().Unix()-cost.ObservedAt > int64(settings.UpstreamRatioMaxAgeDays)*86400 {
			entry.UpstreamRatio = nil
		}
		for _, group := range channel.GetGroups() {
			for _, userGroup := range userGroups {
				ratio, _ := ratio_setting.ResolveGroupRatio(nil, userGroup, group)
				if ratio <= 0 {
					entry.SellFactor = 0
				}
			}
		}
		contexts[key] = entry
	}
	return contexts, nil
}

func priceMonitorReviewDrafts(snapshot PriceMonitorSnapshot, changes []model.ModelPricingChange, contexts map[string]priceMonitorLossContext) ([]priceMonitorDraftPreview, error) {
	if len(changes) == 0 || len(changes) > priceMonitorApplyMaxModels {
		return nil, errors.New("invalid batch size")
	}
	rows := make(map[string]PriceMonitorMatrixItem, len(snapshot.MatrixItems))
	for _, item := range snapshot.MatrixItems {
		rows[item.Model] = item
	}
	previews := make([]priceMonitorDraftPreview, 0, len(changes))
	seen := make(map[string]bool)
	for _, change := range changes {
		if seen[change.ModelName] {
			return nil, errors.New("duplicate model")
		}
		seen[change.ModelName] = true
		pricing := change.Pricing
		if change.Reset {
			pricing = model.ResetModelPricingDraft(change.ModelName)
		}
		effective, err := model.PreviewModelPricing(change.ModelName, pricing)
		if err != nil {
			return nil, err
		}
		draft, hasPrice := priceMonitorPricingCell(change.ModelName, effective)
		preview := priceMonitorDraftPreview{ModelName: change.ModelName, ComparisonStatus: "unknown", Violations: []priceMonitorFloorViolation{}}
		row := rows[change.ModelName]
		for _, header := range snapshot.SourceHeaders {
			if header.Type != priceSourceChannel {
				continue
			}
			source, exists := row.Prices[header.Key]
			context, valid := contexts[header.Key]
			if !hasPrice || !exists || !valid || !context.Valid || context.UpstreamRatio == nil {
				preview.Coverage.Unknown++
				continue
			}
			violations, comparable := priceMonitorDraftViolations(change.ModelName, draft, source, context.SellFactor, context.upstreamCostFactor(), header.Key)
			preview.Violations = append(preview.Violations, violations...)
			if !comparable {
				preview.Coverage.Unknown++
				continue
			}
			preview.Coverage.Compared++
			// Source payloads do not identify their token normalization version.
			// Static coefficients cover a known token interpretation, not every
			// provider's cache/media semantics. Preserve that uncovered scope.
			if draft.actualBillingMode() == priceMonitorModeExpression || source.actualBillingMode() == priceMonitorModeExpression {
				preview.Coverage.Unknown++
			}
		}
		if preview.Coverage.Compared == 0 && preview.Coverage.Unknown == 0 {
			preview.Coverage.Unknown = 1
		}
		if preview.Coverage.Compared > 0 {
			preview.ComparisonStatus = "equal"
			if len(preview.Violations) > 0 {
				preview.ComparisonStatus = "different"
			}
			if preview.Coverage.Unknown > 0 {
				preview.ComparisonStatus = "partial"
			}
		}
		previews = append(previews, preview)
	}
	return previews, nil
}

func PreviewPriceMonitorPrice(c *gin.Context) {
	var request priceMonitorDraftRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	snapshot := getPriceMonitorStore().Get()
	if request.CheckedAt != snapshot.CheckedAt {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorSnapshotStale)
		return
	}
	pricingVersion, err := model.ReadPricingConfigVersion()
	if err != nil {
		logger.LogError(c, "price monitor pricing version read failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	contexts, err := priceMonitorDraftContexts(snapshot)
	if err != nil {
		logger.LogError(c, "price monitor preview context failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	previews, err := priceMonitorReviewDrafts(snapshot, request.Changes, contexts)
	if err != nil {
		logger.LogWarn(c, "price monitor draft validation failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	common.ApiSuccess(c, gin.H{"models": previews, "context_version": priceMonitorDraftContextVersion(snapshot, request.Changes, contexts, pricingVersion)})
}

func applyPriceMonitorDrafts(c *gin.Context, request priceMonitorApplyPriceRequest) {
	if len(request.Items) > 0 || len(request.Changes) == 0 || len(request.Changes) > priceMonitorApplyMaxModels {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	snapshot := getPriceMonitorStore().Get()
	if request.CheckedAt != snapshot.CheckedAt {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorSnapshotStale)
		return
	}
	pricingVersion, err := model.ReadPricingConfigVersion()
	if err != nil {
		logger.LogError(c, "price monitor pricing version read failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	contexts, err := priceMonitorDraftContexts(snapshot)
	if err != nil {
		logger.LogError(c, "price monitor apply context failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	contextVersion := priceMonitorDraftContextVersion(snapshot, request.Changes, contexts, pricingVersion)
	if contextVersion == "" || request.ContextVersion != contextVersion {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorSnapshotStale)
		return
	}
	previews, err := priceMonitorReviewDrafts(snapshot, request.Changes, contexts)
	if err != nil {
		logger.LogWarn(c, "price monitor apply validation failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	for _, preview := range previews {
		if len(preview.Violations) > 0 && !request.ForceBelowFloor {
			common.ApiErrorI18n(c, i18n.MsgPriceMonitorBelowFloor)
			return
		}
		if preview.Coverage.Unknown > 0 && !request.AcknowledgeUnknown {
			common.ApiErrorI18n(c, "price_monitor.unknown_risk")
			return
		}
	}
	// Expensive compilation/review has finished before opening the transaction.
	// Model versions protect the reviewed complete drafts; the immutable quote
	// snapshot is checked again at the write boundary, without network or DB work.
	err = model.UpdateModelPricingWithGuard(request.Changes, func(current []model.ModelPricingEntry, _ []model.ModelPricingChange) error {
		if len(current) == 0 || priceMonitorDraftContextVersion(getPriceMonitorStore().Get(), request.Changes, contexts, current[0].PricingVersion) != contextVersion {
			return errPriceMonitorDraftChanged
		}
		return nil
	})
	if err != nil {
		logger.LogWarn(c, "price monitor draft save failed: "+err.Error())
		if errors.Is(err, model.ErrModelPricingConflict) || errors.Is(err, errPriceMonitorDraftChanged) {
			common.ApiErrorI18n(c, i18n.MsgPricingConfigChanged)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	names := make([]string, 0, len(request.Changes))
	for _, change := range request.Changes {
		names = append(names, change.ModelName)
	}
	recordManageAudit(c, "price_monitor.price.apply", map[string]any{"models": names, "force_below_floor": request.ForceBelowFloor, "acknowledge_unknown": request.AcknowledgeUnknown})
	common.ApiSuccess(c, gin.H{"updated_models": names, "refreshed": refreshPriceMonitorAfterChange(c)})
}
