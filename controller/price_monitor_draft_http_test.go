package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func performPriceMonitorDraftHTTP(t *testing.T, method, path string, body any, handler gin.HandlerFunc) map[string]any {
	t.Helper()
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(encoded))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	response := map[string]any{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestPriceMonitorRejectsRetiredItemWrites(t *testing.T) {
	response := performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", map[string]any{
		"items": []any{map[string]any{"model": "example", "fields": map[string]any{"model_ratio": 1}}},
	}, ApplyPriceMonitorPrice)
	require.Equal(t, false, response["success"])
}

func TestPriceMonitorDraftHTTPGuardLifecycle(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{CheckedAt: 100, MatrixVersion: priceMonitorMatrixVersion})
	previous := priceMonitorRefreshChannels
	priceMonitorRefreshChannels = func() ([]*model.Channel, error) { return nil, nil }
	t.Cleanup(func() { priceMonitorRefreshChannels = previous })
	name := "inspection-new-unpriced-http"
	response := performPriceMonitorDraftHTTP(t, "GET", "/api/option/model_pricing?model="+name, nil, GetModelPricingConfig)
	require.Equal(t, true, response["success"])
	entry := response["data"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	require.Empty(t, entry["configured"])
	changes := []model.ModelPricingChange{{ModelName: name, ExpectedVersion: entry["version"].(string), Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("base",p*0+c*2+cr*0)`}}}
	review := performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/preview_price", priceMonitorDraftRequest{CheckedAt: 100, Changes: changes}, PreviewPriceMonitorPrice)
	require.Equal(t, true, review["success"], review["message"])
	data := review["data"].(map[string]any)
	result := data["models"].([]any)[0].(map[string]any)
	require.Equal(t, "unknown", result["comparison_status"])
	require.Equal(t, float64(1), result["coverage"].(map[string]any)["unknown"])
	apply := map[string]any{"checked_at": 100, "context_version": data["context_version"], "changes": changes}
	rejected := performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", apply, ApplyPriceMonitorPrice)
	require.Equal(t, false, rejected["success"])
	snapshot, err := model.GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	require.Empty(t, snapshot.Entries[0].Configured)

	// Risk acknowledgement is invalidated by any draft edit, even on an unpriced model.
	apply["acknowledge_unknown"] = true
	changes[0].Pricing["billing_setting.billing_expr"] = `tier("base",p*0+c*3+cr*0)`
	rejected = performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", apply, ApplyPriceMonitorPrice)
	require.Equal(t, false, rejected["success"])
	changes[0].Pricing["billing_setting.billing_expr"] = `tier("base",p*0+c*2+cr*0)`
	accepted := performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", apply, ApplyPriceMonitorPrice)
	require.Equal(t, true, accepted["success"], accepted["message"])
	snapshot, err = model.GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	require.Equal(t, `tier("base",p*0+c*2+cr*0)`, snapshot.Entries[0].Configured["billing_setting.billing_expr"])

	// A fresh risk review cannot bypass the independently enforced model version.
	changes[0].Pricing["billing_setting.billing_expr"] = `tier("base",p*1+c*4)`
	review = performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/preview_price", priceMonitorDraftRequest{CheckedAt: 100, Changes: changes}, PreviewPriceMonitorPrice)
	require.Equal(t, true, review["success"])
	apply["context_version"] = review["data"].(map[string]any)["context_version"]
	rejected = performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", apply, ApplyPriceMonitorPrice)
	require.Equal(t, false, rejected["success"])
	snapshot, err = model.GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	require.Equal(t, `tier("base",p*0+c*2+cr*0)`, snapshot.Entries[0].Configured["billing_setting.billing_expr"])
}

func TestPriceMonitorDraftHTTPRejectsMixedLegacyItems(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{CheckedAt: 100})
	response := performPriceMonitorDraftHTTP(t, "POST", "/api/price_monitor/apply_price", map[string]any{
		"checked_at": 100, "acknowledge_unknown": true, "changes": []model.ModelPricingChange{{ModelName: "new", ExpectedVersion: "anything", Pricing: model.PricingValues{"ModelPrice": 0}}},
		"items": []map[string]any{{"model": "new", "fields": map[string]any{"model_price": 1}}},
	}, ApplyPriceMonitorPrice)
	require.Equal(t, false, response["success"])
	snapshot, err := model.GetModelPricingSnapshot([]string{"new"})
	require.NoError(t, err)
	require.Empty(t, snapshot.Entries[0].Configured)
}

func TestPriceMonitorDraftHTTPRejectsRemotePricingChangeWithStaleMemory(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{CheckedAt: 100, MatrixVersion: priceMonitorMatrixVersion})
	previous := priceMonitorRefreshChannels
	priceMonitorRefreshChannels = func() ([]*model.Channel, error) { return nil, nil }
	t.Cleanup(func() { priceMonitorRefreshChannels = previous })
	name := "remote-pricing-cas"
	snapshot, err := model.GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	changes := []model.ModelPricingChange{{ModelName: name, ExpectedVersion: snapshot.EmptyVersion, Pricing: model.PricingValues{"ModelPrice": float64(1)}}}
	review := performPriceMonitorDraftHTTP(t, "POST", "/", priceMonitorDraftRequest{CheckedAt: 100, Changes: changes}, PreviewPriceMonitorPrice)
	require.Equal(t, true, review["success"])
	// A different process changes an unrelated/alias price, without publishing to
	// this process's OptionMap. The per-model configured version remains unchanged.
	require.NoError(t, model.DB.Create(&model.Option{Key: model.PricingConfigVersionKey, Value: "1"}).Error)
	require.Equal(t, int64(0), model.GetPricingConfigVersion())
	response := performPriceMonitorDraftHTTP(t, "POST", "/", map[string]any{"checked_at": 100, "context_version": review["data"].(map[string]any)["context_version"], "changes": changes, "acknowledge_unknown": true}, ApplyPriceMonitorPrice)
	require.Equal(t, false, response["success"])
	after, err := model.GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	require.Empty(t, after.Entries[0].Configured)
}
