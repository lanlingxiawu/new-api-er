package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 分享页（持分享口令的外部人员）不返回保本下限：逐字段保本价与决定它的渠道是内部定价信息。
func TestPublicPriceMonitorQueryHidesRepairFloor(t *testing.T) {
	snapshot := applyPriceSnapshotWithFloor("gpt-4o", completionFloor(3, 6))
	snapshot.AccessPassword = "share123"
	snapshot.PasswordExpireAt = time.Now().Add(time.Hour).Unix()
	useApplyPriceEnv(t, snapshot)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/price_monitor/public_query",
		strings.NewReader(`{"password":"share123","page":1,"page_size":20}`))
	c.Request.Header.Set("Content-Type", "application/json")
	PublicPriceMonitorQuery(c)

	body := recorder.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "gpt-4o")
	assert.NotContains(t, body, "repair_floor")
	for _, field := range []string{"sell_factor", "measured_factor", "configured_factor", "upstream_factor", "loss_lines", "channel_id"} {
		assert.NotContains(t, body, field)
	}

	// 管理端查询照常带上保本下限。
	admin := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Page: 1, PageSize: 20})
	require.Len(t, admin.Items, 1)
	assert.NotNil(t, admin.Items[0].RepairFloor)
}

// The share page shows loss badges but none of the four loss factors: the
// cost ratio and upstream group ratio are internal, and the measured factor
// (which includes the upstream ratio) would give it away next to the prices.
// Channel ids are dropped too.
func TestPublicPriceMonitorResultHidesLossFactors(t *testing.T) {
	result := priceMonitorMatrixQueryResult{
		SourceHeaders:          []PriceMonitorSourceHeader{{Key: "c(1)", Type: priceSourceChannel, ChannelId: 1}},
		AvailableSourceHeaders: []PriceMonitorSourceHeader{{Key: "c(1)", Type: priceSourceChannel, ChannelId: 1}},
		Items: []PriceMonitorMatrixItem{{
			Model: "m",
			Prices: map[string]PriceMonitorPriceCell{
				"c(1)": {LossKinds: []string{priceMonitorLossKindMeasured}, SellFactor: floatPointer(1), MeasuredFactor: floatPointer(1.8), ConfiguredFactor: floatPointer(1.5), UpstreamFactor: floatPointer(1.5)},
			},
			RepairFloor: &PriceMonitorRepairFloor{Mode: priceMonitorModeToken},
		}},
	}
	stripPriceMonitorPublicResult(&result)
	cell := result.Items[0].Prices["c(1)"]
	assert.Equal(t, []string{priceMonitorLossKindMeasured}, cell.LossKinds, "the badge stays")
	assert.Nil(t, cell.SellFactor)
	assert.Nil(t, cell.MeasuredFactor)
	assert.Nil(t, cell.ConfiguredFactor)
	assert.Nil(t, cell.UpstreamFactor)
	assert.Nil(t, result.Items[0].RepairFloor)
	assert.Zero(t, result.SourceHeaders[0].ChannelId)
	assert.Zero(t, result.AvailableSourceHeaders[0].ChannelId)
}

// Every view carries the platform cell (repricing needs the current price), but
// only views that compare against the platform show its column.
func TestQueryMatrixAlwaysCarriesPlatformCell(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "c(1)", Type: priceSourceChannel, ChannelId: 1},
		},
		MatrixItems: []PriceMonitorMatrixItem{{
			Model: "m",
			Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: {Mode: priceMonitorModeToken, Input: floatPointer(1)},
				"official":              {Mode: priceMonitorModeToken, Input: floatPointer(2), Different: true, InputDifferent: true},
				"c(1)":                  {Mode: priceMonitorModeToken, Input: floatPointer(3), Different: true, InputDifferent: true},
			},
		}},
	}
	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "channel_official", Page: 1, PageSize: 20})
	require.Len(t, result.Items, 1)
	assert.Contains(t, result.Items[0].Prices, priceMonitorPlatformKey)
	for _, header := range result.SourceHeaders {
		assert.NotEqual(t, priceMonitorPlatformKey, header.Key, "the platform column stays hidden in this view")
	}
}
