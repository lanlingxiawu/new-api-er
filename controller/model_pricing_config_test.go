package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelPricingPreviewDescribesBarePriceForms(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	for _, test := range []struct {
		expression, kind string
		branches         int
	}{
		{`p*2+c*4`, "flat", 1},
		{`v1:len<=100?p*2+c*4:p*3+c*6`, "tiered", 2},
		{`hour("UTC")<8?p*2:p*4`, "custom", 0},
	} {
		response := performPriceMonitorDraftHTTP(t, "POST", "/api/option/model_pricing/preview", map[string]any{"model_name": "editor-model", "pricing": model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": test.expression}}, PreviewModelPricing)
		require.Equal(t, true, response["success"], response["message"])
		data := response["data"].(map[string]any)
		require.Contains(t, data, "effective")
		editor := data["editor"].(map[string]any)
		require.Equal(t, test.kind, editor["kind"])
		require.Equal(t, test.expression, editor["source"])
		require.Equal(t, float64(1), editor["schema_version"])
		require.Len(t, editor["branches"], test.branches)
	}
}

func TestModelPricingPreviewRejectsUnsupportedEngine(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"model_name":"test","pricing":{"billing_setting.billing_mode":"tiered_expr","billing_setting.billing_expr":"unknown_billing_function(1)"}}`))
	PreviewModelPricing(c)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
	require.NotContains(t, recorder.Body.String(), "unknown name")
}

func TestModelPricingPreviewPreservesExplicitZero(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"model_name":"test","pricing":{"ModelPrice":0}}`))
	PreviewModelPricing(c)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"ModelPrice":0`)
}

func TestModelPricingConfigRejectsMixedContracts(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(`{"changes":[{"model_name":"m","pricing":{"ModelPrice":0}}],"options":{},"expected_options":{}}`))
	UpdateModelPricingConfig(c)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestModelPricingPreviewRuleDocumentCompilation(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	source := `v1:max(p*2,c*4)`
	pricing := model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": source}
	read := performPriceMonitorDraftHTTP(t, "POST", "/", map[string]any{"model_name": "rule-document-test", "pricing": pricing}, PreviewModelPricing)
	require.Equal(t, true, read["success"])
	require.Contains(t, read["data"].(map[string]any), "rule_document")
	doc, err := billingexpr.DescribeRuleDocument(source)
	require.NoError(t, err)
	doc.Root.Args[0].Args[1].Value = "6"
	request := map[string]any{"model_name": "rule-document-test", "pricing": pricing, "rule_document": doc, "source_hash": doc.SourceHash}
	compiled := performPriceMonitorDraftHTTP(t, "POST", "/", request, PreviewModelPricing)
	require.Equal(t, true, compiled["success"], compiled["message"])
	data := compiled["data"].(map[string]any)
	candidate := data["pricing"].(map[string]any)["billing_setting.billing_expr"].(string)
	require.Contains(t, candidate, "p * 6")
	require.Equal(t, candidate, data["effective"].(map[string]any)["billing_setting.billing_expr"])
	require.Equal(t, billingexpr.ExprHashString(candidate), data["rule_document"].(map[string]any)["source_hash"])
	snapshot, err := model.GetModelPricingSnapshot([]string{"rule-document-test"})
	require.NoError(t, err)
	require.Empty(t, snapshot.Entries[0].Configured)
	request["source_hash"] = "stale"
	rejected := performPriceMonitorDraftHTTP(t, "POST", "/", request, PreviewModelPricing)
	require.Equal(t, false, rejected["success"])
	require.NotContains(t, rejected["message"], "source or version")
}
