package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetUSDCNYRate 返回 USD/CNY 参考汇率
// 数据来源：Binance P2P（Redis 缓存 1h，内存兜底）
// 前端可以以 5 分钟为周期轮询此接口
func GetUSDCNYRate(c *gin.Context) {
	rate := service.GetUSDCNYRate()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"rate":   rate,
			"source": "binance_p2p",
			"pair":   "USDT/CNY",
		},
	})
}
