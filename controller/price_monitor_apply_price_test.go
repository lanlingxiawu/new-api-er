package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// useApplyPriceEnv 隔离三样东西：巡检快照存储、价格 option 所在的数据库、内存 OptionMap。
//
// 与 model/pricing_options_test.go 同理，这里刻意不用真实项目库：ModelRatio 等键是共享
// 开发库里正在运行的实例的活价格配置，用真库跑就等于改掉开发环境的模型价格。
func useApplyPriceEnv(t *testing.T, snapshot PriceMonitorSnapshot) {
	t.Helper()
	// 先触发一次 sync.Once，否则 handler 里的 getPriceMonitorStore() 会用默认路径的 store
	// 覆盖掉这里注入的那个。
	getPriceMonitorStore()
	previousStore := priceMonitorStore
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousMap := common.OptionMap

	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	require.NoError(t, store.Save(snapshot))
	priceMonitorStore = store

	db, err := gorm.Open(sqlite.Open("file:apply_price_"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
		priceMonitorStore = previousStore
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
}

func seedApplyPriceOption(t *testing.T, key, value string) {
	t.Helper()
	option := model.Option{Key: key}
	require.NoError(t, model.DB.Where(model.Option{Key: key}).FirstOrCreate(&option).Error)
	option.Value = value
	require.NoError(t, model.DB.Save(&option).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
}

func applyPriceOptionValue(t *testing.T, key, modelName string) (float64, bool) {
	t.Helper()
	var option model.Option
	if err := model.DB.Where("`key` = ?", key).Take(&option).Error; err != nil {
		return 0, false
	}
	values := make(map[string]float64)
	require.NoError(t, common.UnmarshalJsonStr(option.Value, &values))
	value, ok := values[modelName]
	return value, ok
}

func applyPriceSnapshot(checkedAt int64, mode string) PriceMonitorSnapshot {
	platform := PriceMonitorPriceCell{Mode: mode, Input: floatPointer(2), Output: floatPointer(6)}
	if mode == priceMonitorModeExpression {
		platform = PriceMonitorPriceCell{Mode: mode, Tiers: []PriceMonitorPriceTier{{Range: "all", Input: 1, Output: 2}}}
	}
	return PriceMonitorSnapshot{
		CheckedAt: checkedAt,
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "channel-a", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{{Model: "gpt-4o", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             {Mode: mode, Input: floatPointer(3), Output: floatPointer(6), Different: true},
		}}},
	}
}

func performApplyPrice(t *testing.T, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/price_monitor/apply_price", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	ApplyPriceMonitorPrice(c)

	response := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &response))
	return recorder, response
}

func TestApplyPriceMonitorPriceAppliesAndReportsUnchanged(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1,"other":9}`)
	seedApplyPriceOption(t, "CompletionRatio", `{"gpt-4o":3}`)

	_, response := performApplyPrice(t, `{
		"checked_at": 100,
		"pricing_version": 0,
		"items": [{
			"model": "gpt-4o",
			"fields":   {"model_ratio": 2.5, "completion_ratio": 3},
			"expected": {"model_ratio": 1,   "completion_ratio": 3}
		}]
	}`)

	require.Equal(t, true, response["success"], response["message"])
	data := response["data"].(map[string]any)
	require.Equal(t, float64(1), data["pricing_version"])
	results := data["results"].([]any)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	require.Equal(t, []any{"model_ratio"}, result["applied"])
	require.Equal(t, []any{"completion_ratio"}, result["unchanged"])

	value, ok := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.True(t, ok)
	require.InDelta(t, 2.5, value, 1e-9)
	other, ok := applyPriceOptionValue(t, "ModelRatio", "other")
	require.True(t, ok)
	require.InDelta(t, 9, other, 1e-9, "other models must not be touched")
}

func TestApplyPriceMonitorPriceRejectsStaleSnapshot(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	_, response := performApplyPrice(t, `{"checked_at": 99, "pricing_version": 0, "items": [{"model":"gpt-4o","fields":{"model_ratio":2},"expected":{"model_ratio":1}}]}`)

	require.Equal(t, false, response["success"])
	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.InDelta(t, 1, value, 1e-9, "a stale snapshot must not write anything")
}

func TestApplyPriceMonitorPriceRejectsStalePricingVersion(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	// 模拟倍率设置页在此期间做了一次整块保存。
	require.NoError(t, model.UpdateOption("ModelRatio", `{"gpt-4o":5}`))

	_, response := performApplyPrice(t, `{"checked_at": 100, "pricing_version": 0, "items": [{"model":"gpt-4o","fields":{"model_ratio":2},"expected":{"model_ratio":1}}]}`)

	require.Equal(t, false, response["success"])
	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.InDelta(t, 5, value, 1e-9, "the inline edit must not overwrite the whole-block save")
}

func TestApplyPriceMonitorPriceRejectsValueConflictWithoutPartialWrite(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)
	seedApplyPriceOption(t, "CompletionRatio", `{"gpt-4o":3}`)

	_, response := performApplyPrice(t, `{
		"checked_at": 100, "pricing_version": 0,
		"items": [{"model":"gpt-4o","fields":{"model_ratio":7,"completion_ratio":8},"expected":{"model_ratio":1,"completion_ratio":99}}]
	}`)

	require.Equal(t, false, response["success"])
	ratio, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	completion, _ := applyPriceOptionValue(t, "CompletionRatio", "gpt-4o")
	require.InDelta(t, 1, ratio, 1e-9)
	require.InDelta(t, 3, completion, 1e-9)
}

func TestApplyPriceMonitorPriceRejectsTieredPricing(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeExpression))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	_, response := performApplyPrice(t, `{"checked_at": 100, "pricing_version": 0, "items": [{"model":"gpt-4o","fields":{"model_ratio":2},"expected":{"model_ratio":1}}]}`)

	require.Equal(t, false, response["success"])
	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.InDelta(t, 1, value, 1e-9)
}

func TestApplyPriceMonitorPriceRejectsInvalidRequests(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	bodies := map[string]string{
		"empty items":       `{"checked_at":100,"pricing_version":0,"items":[]}`,
		"unknown model":     `{"checked_at":100,"pricing_version":0,"items":[{"model":"nope","fields":{"model_ratio":2}}]}`,
		"unknown field":     `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"billing_expr":2}}]}`,
		"negative value":    `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"model_ratio":-1}}]}`,
		"no fields":         `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{}}]}`,
		"duplicate model":   `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"model_ratio":2}},{"model":"gpt-4o","fields":{"model_ratio":3}}]}`,
		"blank model name":  `{"checked_at":100,"pricing_version":0,"items":[{"model":"  ","fields":{"model_ratio":2}}]}`,
		"malformed payload": `{`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			_, response := performApplyPrice(t, body)
			require.Equal(t, false, response["success"])
		})
	}
	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.InDelta(t, 1, value, 1e-9, "no invalid request may write anything")
}

func TestApplyPriceMonitorPriceEnforcesBatchLimit(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeToken))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	items := make([]string, 0, priceMonitorApplyMaxModels+1)
	for index := 0; index <= priceMonitorApplyMaxModels; index++ {
		items = append(items, `{"model":"gpt-4o","fields":{"model_ratio":2}}`)
	}
	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[`+strings.Join(items, ",")+`]}`)
	require.Equal(t, false, response["success"], "a batch larger than apply_max_models must be rejected before any write")

	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	require.InDelta(t, 1, value, 1e-9)
}

func TestPriceMonitorApplyResultsGroupsFieldsByModel(t *testing.T) {
	items := []priceMonitorApplyPriceItem{
		{Model: "gpt-4o", Fields: map[string]*float64{"model_ratio": floatPointer(1), "completion_ratio": floatPointer(2)}},
		{Model: "claude", Fields: map[string]*float64{"model_ratio": floatPointer(3)}},
	}
	applied := map[string][]model.PricingPatch{
		"ModelRatio": {{OptionKey: "ModelRatio", Model: "gpt-4o"}},
	}

	results := priceMonitorApplyResults(items, applied)
	require.Len(t, results, 2)
	require.Equal(t, []string{"model_ratio"}, results[0].Applied)
	require.Equal(t, []string{"completion_ratio"}, results[0].Unchanged)
	require.Empty(t, results[1].Applied)
	require.Equal(t, []string{"model_ratio"}, results[1].Unchanged)
}

func TestPriceMonitorFieldNameForOptionKey(t *testing.T) {
	require.Equal(t, "model_ratio", priceMonitorFieldNameForOptionKey("ModelRatio"))
	require.Equal(t, "audio_completion_ratio", priceMonitorFieldNameForOptionKey("AudioCompletionRatio"))
	require.Equal(t, "Unknown", priceMonitorFieldNameForOptionKey("Unknown"))
}
