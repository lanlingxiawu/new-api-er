package controller

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
	"net/http"
)

func GetModelPricingConfig(c *gin.Context) {
	names := c.QueryArray("model")
	if len(names) > 100 {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorTooManyModels)
		return
	}
	snapshot, err := model.GetModelPricingSnapshot(names)
	if err != nil {
		logger.LogError(c, "read model pricing failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": snapshot})
}

func PreviewModelPricing(c *gin.Context) {
	var request struct {
		ModelName    string                    `json:"model_name"`
		Pricing      model.PricingValues       `json:"pricing"`
		RuleDocument *billingexpr.RuleDocument `json:"rule_document"`
		SourceHash   string                    `json:"source_hash"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.RuleDocument != nil || request.SourceHash != "" {
		source, _ := request.Pricing["billing_setting.billing_expr"].(string)
		generated, err := billingexpr.CompileRuleDocument(source, request.SourceHash, request.RuleDocument)
		if err != nil {
			logger.LogWarn(c, "model pricing rule compilation rejected: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		request.Pricing["billing_setting.billing_expr"] = generated
		request.Pricing["billing_setting.billing_mode"] = "tiered_expr"
	}
	effective, err := model.PreviewModelPricing(request.ModelName, request.Pricing)
	if err != nil {
		logger.LogWarn(c, "model pricing preview rejected: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	data := gin.H{"effective": effective, "pricing": request.Pricing, "cache_write_mode": model.ResolveCacheWriteMode(request.ModelName, request.Pricing), "billing_details": model.ResolveLegacyBillingDetails(request.ModelName, effective, request.Pricing)}
	if effective["billing_setting.billing_mode"] == "tiered_expr" {
		expression, _ := effective["billing_setting.billing_expr"].(string)
		editor, editorErr := billingexpr.DescribeEditor(expression)
		if editorErr != nil {
			logger.LogWarn(c, "model pricing editor analysis rejected: "+editorErr.Error())
		} else {
			data["editor"] = editor
		}
		document, documentErr := billingexpr.DescribeRuleDocument(expression)
		if documentErr != nil {
			logger.LogWarn(c, "model pricing rule analysis rejected: "+documentErr.Error())
		} else {
			data["rule_document"] = document
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func UpdateModelPricingConfig(c *gin.Context) {
	var request struct {
		Changes         []model.ModelPricingChange `json:"changes"`
		Options         map[string]string          `json:"options"`
		ExpectedOptions map[string]string          `json:"expected_options"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(request.Changes) > 0 && (request.Options != nil || request.ExpectedOptions != nil) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var saveErr error
	if request.Options != nil || request.ExpectedOptions != nil {
		saveErr = model.UpdateModelPricingOptionMaps(request.Options, request.ExpectedOptions)
	} else {
		saveErr = model.UpdateModelPricing(request.Changes)
	}
	if err := saveErr; err != nil {
		logger.LogWarn(c, "model pricing update rejected: "+err.Error())
		if errors.Is(err, model.ErrModelPricingConflict) {
			common.ApiErrorI18n(c, i18n.MsgPricingConfigChanged)
		} else {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		}
		return
	}
	names := make([]string, 0, len(request.Changes))
	for _, change := range request.Changes {
		names = append(names, change.ModelName)
	}
	optionKeys := make([]string, 0, len(request.Options))
	for key := range request.Options {
		optionKeys = append(optionKeys, key)
	}
	recordManageAudit(c, "model.pricing.update", map[string]any{"models": names, "option_keys": optionKeys})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"updated_models": names}})
}

func PreviewModelPricingConversion(c *gin.Context) {
	var request struct {
		ModelName string              `json:"model_name"`
		Pricing   model.PricingValues `json:"pricing"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	result, err := model.PreviewModelPricingConversion(request.ModelName, request.Pricing)
	if err != nil {
		logger.LogWarn(c, "model pricing conversion rejected: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
