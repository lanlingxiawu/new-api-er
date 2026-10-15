package controller

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type priceMonitorScenario struct {
	ID             string            `json:"id"`
	UsageSemantics string            `json:"usage_semantics"`
	Usage          dto.Usage         `json:"usage"`
	RequestBody    map[string]any    `json:"request_body"`
	Headers        map[string]string `json:"headers"`
	At             time.Time         `json:"at"`
	PluginKey      string            `json:"plugin_key,omitempty"`
	SchemaVersion  string            `json:"schema_version,omitempty"`
}

type priceMonitorScenarioSide struct {
	SourceKey    string   `json:"source_key"`
	Status       string   `json:"status"`
	AmountUSD    *float64 `json:"amount_usd,omitempty"`
	MatchedRules string   `json:"matched_rules,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type priceMonitorScenarioResult struct {
	ScenarioID string                     `json:"scenario_id"`
	Sides      []priceMonitorScenarioSide `json:"sides"`
}

func validatePriceMonitorScenario(scenario priceMonitorScenario) error {
	if scenario.At.IsZero() || len(scenario.ID) > 128 || len(scenario.Headers) > 32 {
		return errors.New("invalid scenario")
	}
	if scenario.UsageSemantics != "openai" && scenario.UsageSemantics != "claude" && scenario.UsageSemantics != "plugin" {
		return errors.New("unknown usage semantics")
	}
	u := scenario.Usage
	for _, count := range []int{u.PromptTokens, u.CompletionTokens, u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CachedCreationTokens, u.PromptTokensDetails.CacheWriteTokens, u.PromptTokensDetails.ImageTokens, u.PromptTokensDetails.AudioTokens, u.CompletionTokenDetails.ImageTokens, u.CompletionTokenDetails.AudioTokens, u.ClaudeCacheCreation5mTokens, u.ClaudeCacheCreation1hTokens} {
		if count < 0 || count > 1_000_000_000 {
			return errors.New("invalid token count")
		}
	}
	for key, value := range scenario.Headers {
		name := strings.ToLower(strings.TrimSpace(key))
		if len(key) > 256 || len(value) > 4096 || name == "authorization" || name == "cookie" || strings.Contains(name, "api-key") {
			return errors.New("invalid scenario header")
		}
	}
	body, err := common.Marshal(scenario.RequestBody)
	if err != nil || len(body) > 65536 {
		return errors.New("scenario body too large")
	}
	return nil
}

func priceMonitorEvaluateCell(key string, cell PriceMonitorPriceCell, scenario priceMonitorScenario) priceMonitorScenarioSide {
	return priceMonitorEvaluateCompiledCell(key, cell, scenario, make(map[string]*billingexpr.PreviewProgram))
}

func priceMonitorEvaluateCompiledCell(key string, cell PriceMonitorPriceCell, scenario priceMonitorScenario, programs map[string]*billingexpr.PreviewProgram) priceMonitorScenarioSide {
	cell.Mode = cell.actualBillingMode()
	side := priceMonitorScenarioSide{SourceKey: key, Status: "unknown", Reason: "price_monitor.preview_unsupported"}
	if scenario.UsageSemantics == "plugin" || scenario.PluginKey != "" || cell.UnavailableReason != "" {
		return side
	}
	if cell.Mode == priceMonitorModeRequest && cell.Price != nil {
		value := *cell.Price
		if value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			side.Status = "evaluated"
			side.AmountUSD = &value
			side.Reason = ""
		}
		return side
	}
	expression := cell.Expr
	if cell.Mode == priceMonitorModeToken {
		// Legacy multimodal semantics are provider-specific. Do not use a synthetic
		// expression to assert equality for cache/audio/image usage.
		u := scenario.Usage
		if u.PromptTokensDetails.CachedTokens != 0 || u.PromptTokensDetails.CacheCreationTokensTotal() != 0 || u.PromptTokensDetails.ImageTokens != 0 || u.PromptTokensDetails.AudioTokens != 0 || u.CompletionTokenDetails.ImageTokens != 0 || u.CompletionTokenDetails.AudioTokens != 0 || u.ClaudeCacheCreation5mTokens != 0 || u.ClaudeCacheCreation1hTokens != 0 {
			return side
		}
		if cell.Input == nil || cell.Output == nil {
			return side
		}
		for _, value := range []float64{*cell.Input, *cell.Output} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return side
			}
		}
		value := (*cell.Input*float64(u.PromptTokens) + *cell.Output*float64(u.CompletionTokens)) / 1e6
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return side
		}
		side.Status = "evaluated"
		side.AmountUSD = &value
		side.Reason = ""
		return side
	}
	if cell.Mode != priceMonitorModeExpression || expression == "" {
		return side
	}
	program, cached := programs[expression]
	if !cached {
		program, _ = billingexpr.CompilePreview(expression)
		programs[expression] = program
	}
	if program == nil {
		return side
	}
	usage := scenario.Usage
	claude := scenario.UsageSemantics == "claude"
	if claude {
		usage.UsageSemantic = "anthropic"
	} else {
		usage.UsageSemantic = "openai"
	}
	params := service.BuildTieredTokenParams(&usage, claude, program.UsedVars)
	body, err := common.Marshal(scenario.RequestBody)
	if err != nil {
		return side
	}
	result, trace, err := program.RunAt(params, billingexpr.RequestInput{Headers: scenario.Headers, Body: body}, scenario.At)
	if err != nil || result < 0 || math.IsNaN(result) || math.IsInf(result, 0) {
		return side
	}
	dollars := result / 1e6
	side.Status = "evaluated"
	side.AmountUSD = &dollars
	side.MatchedRules = trace.MatchedTier
	side.Reason = ""
	return side
}

func EvaluatePriceMonitorPrice(c *gin.Context) {
	var request struct {
		ModelName       string                 `json:"model_name"`
		ExpectedVersion string                 `json:"expected_version"`
		Pricing         model.PricingValues    `json:"pricing"`
		SourceKeys      []string               `json:"source_keys"`
		Scenarios       []priceMonitorScenario `json:"scenarios"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(request.Scenarios) == 0 || len(request.Scenarios) > 32 || len(request.SourceKeys) > 10 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	seen := make(map[string]bool)
	for _, scenario := range request.Scenarios {
		if scenario.ID == "" || seen[scenario.ID] || validatePriceMonitorScenario(scenario) != nil {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		seen[scenario.ID] = true
	}
	pricing, err := model.GetModelPricingSnapshot([]string{request.ModelName})
	if err != nil {
		logger.LogError(c, "price monitor scenario pricing failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	if len(pricing.Entries) != 1 || pricing.Entries[0].Version != request.ExpectedVersion {
		common.ApiErrorI18n(c, i18n.MsgPricingConfigChanged)
		return
	}
	effective, err := model.PreviewModelPricing(request.ModelName, request.Pricing)
	if err != nil {
		logger.LogWarn(c, "price monitor scenario draft invalid: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	draft, _ := priceMonitorPricingCell(request.ModelName, effective)
	platform, _ := priceMonitorPricingCell(request.ModelName, pricing.Entries[0].Effective)
	snapshot := getPriceMonitorStore().Get()
	sources := map[string]PriceMonitorPriceCell{}
	for _, row := range snapshot.MatrixItems {
		if row.Model == request.ModelName {
			sources = row.Prices
			break
		}
	}
	programs := make(map[string]*billingexpr.PreviewProgram)
	results := make([]priceMonitorScenarioResult, 0, len(request.Scenarios))
	for _, scenario := range request.Scenarios {
		result := priceMonitorScenarioResult{ScenarioID: scenario.ID, Sides: []priceMonitorScenarioSide{priceMonitorEvaluateCompiledCell("platform", platform, scenario, programs), priceMonitorEvaluateCompiledCell("draft", draft, scenario, programs)}}
		for _, key := range request.SourceKeys {
			if key == priceMonitorPlatformKey || key == "draft" {
				continue
			}
			source, ok := sources[key]
			if !ok {
				result.Sides = append(result.Sides, priceMonitorScenarioSide{SourceKey: key, Status: "unknown", Reason: "price_monitor.preview_source_unavailable"})
				continue
			}
			// Remote expressions do not declare the normalization engine version.
			// Keep such quotes unknown instead of assuming local Claude semantics.
			if source.actualBillingMode() == priceMonitorModeExpression && scenario.UsageSemantics == "claude" {
				result.Sides = append(result.Sides, priceMonitorScenarioSide{SourceKey: key, Status: "unknown", Reason: "price_monitor.preview_semantics_unknown"})
				continue
			}
			result.Sides = append(result.Sides, priceMonitorEvaluateCompiledCell(key, source, scenario, programs))
		}
		for index := range result.Sides {
			if result.Sides[index].Reason != "" {
				result.Sides[index].Reason = common.TranslateMessage(c, result.Sides[index].Reason)
			}
		}
		results = append(results, result)
	}
	common.ApiSuccess(c, gin.H{"results": results, "coverage": "scenarios_only", "checked_at": snapshot.CheckedAt})
}
