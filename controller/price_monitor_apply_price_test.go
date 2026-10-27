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
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousMap := common.OptionMap

	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	require.NoError(t, store.Save(snapshot))
	priceMonitorStore = store

	db, err := gorm.Open(sqlite.Open("file:apply_price_"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}))
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	model.InitColumnNames()
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
		priceMonitorStore = previousStore
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		model.InitColumnNames()
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

func TestPriceMonitorFieldNameForOptionKey(t *testing.T) {
	require.Equal(t, "model_ratio", priceMonitorFieldNameForOptionKey("ModelRatio"))
	require.Equal(t, "audio_completion_ratio", priceMonitorFieldNameForOptionKey("AudioCompletionRatio"))
	require.Equal(t, "Unknown", priceMonitorFieldNameForOptionKey("Unknown"))
}
