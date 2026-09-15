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

	// 管理端查询照常带上保本下限。
	admin := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Page: 1, PageSize: 20})
	require.Len(t, admin.Items, 1)
	assert.NotNil(t, admin.Items[0].RepairFloor)
}
