package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	exchangeRateCacheKey = "exchange_rate:usd_cny"
	exchangeRateCacheTTL = time.Hour
	binanceP2PURL        = "https://p2p.binance.com/bapi/c2c/v2/friendly/c2c/adv/search"
	binanceTimeout       = 10 * time.Second
	exchangeRateFallback = 7.3 // 默认兜底汇率
)

// 内存兜底缓存（Redis 未启用时使用）
var (
	memExchangeRate     float64
	memExchangeRateTime time.Time
	memExchangeRateLock sync.RWMutex
)

type binanceP2PRequest struct {
	Asset         string   `json:"asset"`
	Fiat          string   `json:"fiat"`
	MerchantCheck bool     `json:"merchantCheck"`
	Page          int      `json:"page"`
	PayTypes      []string `json:"payTypes"`
	Rows          int      `json:"rows"`
	TradeType     string   `json:"tradeType"`
}

type binanceP2PResponse struct {
	Data []struct {
		Adv struct {
			Price string `json:"price"`
		} `json:"adv"`
	} `json:"data"`
}

// fetchRateFromBinance 向 Binance P2P 接口发起请求获取 USDT/CNY 参考价格
// 支持 HTTPS_PROXY / HTTP_PROXY 环境变量代理
func fetchRateFromBinance() (float64, error) {
	payload := binanceP2PRequest{
		Asset:         "USDT",
		Fiat:          "CNY",
		MerchantCheck: false,
		Page:          1,
		PayTypes:      []string{},
		Rows:          2,
		TradeType:     "BUY",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	// 读取环境变量 BINANCE_PROXY_URL，有值才创建代理客户端，否则直连
	var client *http.Client
	proxyURL := strings.TrimSpace(os.Getenv("BINANCE_PROXY_URL"))
	if proxyURL != "" {
		proxyClient, err := GetHttpClientWithProxy(proxyURL)
		if err != nil {
			common.SysError(fmt.Sprintf("exchange_rate: 创建代理客户端失败 proxy=%s error=%v", proxyURL, err))
			client = &http.Client{Timeout: binanceTimeout}
		} else {
			client = &http.Client{
				Transport: proxyClient.Transport,
				Timeout:   binanceTimeout,
			}
		}
	} else {
		client = &http.Client{Timeout: binanceTimeout}
	}

	req, err := http.NewRequest(http.MethodPost, binanceP2PURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; rate-fetcher/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求 Binance P2P 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Binance P2P 返回 HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, err
	}

	var result binanceP2PResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("解析 Binance P2P 响应失败: %w", err)
	}
	if len(result.Data) == 0 || result.Data[0].Adv.Price == "" {
		return 0, fmt.Errorf("Binance P2P 响应数据为空")
	}

	rate, err := strconv.ParseFloat(result.Data[0].Adv.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("汇率字段解析失败 price=%s: %w", result.Data[0].Adv.Price, err)
	}
	if rate <= 0 || rate > 100 {
		return 0, fmt.Errorf("汇率数值异常: %f", rate)
	}
	return rate, nil
}

// resolveUSDCNYRate 尝试从实时来源（Redis 缓存 → 内存缓存 → Binance P2P）取 USD/CNY 汇率，
// 返回 (rate, ok)。ok=false 表示所有实时来源均不可用（未写入任何兜底值），
// 由调用方决定兜底策略：GetUSDCNYRate 用硬编码兜底值，Stripe 用管理员配置的手动汇率。
func resolveUSDCNYRate() (float64, bool) {
	// 1. 尝试 Redis
	if common.RedisEnabled {
		if cached, err := common.RedisGet(exchangeRateCacheKey); err == nil && cached != "" {
			if rate, err := strconv.ParseFloat(cached, 64); err == nil && rate > 0 {
				return rate, true
			}
		}
	}

	// 2. 尝试内存缓存（Redis 不可用时）
	memExchangeRateLock.RLock()
	if memExchangeRate > 0 && time.Since(memExchangeRateTime) < exchangeRateCacheTTL {
		rate := memExchangeRate
		memExchangeRateLock.RUnlock()
		return rate, true
	}
	memExchangeRateLock.RUnlock()

	// 3. 从 Binance P2P 拉取
	rate, err := fetchRateFromBinance()
	if err != nil {
		common.SysLog(fmt.Sprintf("exchange_rate: 从 Binance 获取汇率失败 error=%v", err))
		return 0, false
	}

	common.SysLog(fmt.Sprintf("exchange_rate: 从 Binance P2P 获取 USD/CNY 汇率 %.4f", rate))
	rateStr := strconv.FormatFloat(rate, 'f', 4, 64)

	// 回写 Redis
	if common.RedisEnabled {
		if err := common.RedisSet(exchangeRateCacheKey, rateStr, exchangeRateCacheTTL); err != nil {
			common.SysError(fmt.Sprintf("exchange_rate: 写入 Redis 失败: %v", err))
		}
	}

	// 回写内存
	memExchangeRateLock.Lock()
	memExchangeRate = rate
	memExchangeRateTime = time.Now()
	memExchangeRateLock.Unlock()

	return rate, true
}

// GetUSDCNYRate 获取 USD/CNY 实时参考汇率，优先从 Redis 缓存读取（1h TTL）
// Redis 不可用时降级到内存缓存；所有来源失败时返回兜底值
func GetUSDCNYRate() float64 {
	if rate, ok := resolveUSDCNYRate(); ok {
		return rate
	}
	common.SysLog(fmt.Sprintf("exchange_rate: 实时汇率不可用，使用兜底值 %.2f", exchangeRateFallback))
	return exchangeRateFallback
}

// GetUSDCNYRateWithOK 与 GetUSDCNYRate 相同，但额外返回是否取到实时值。
// ok=false 表示实时来源均不可用（未套用任何兜底），供调用方自定义兜底（如 Stripe 手动汇率）。
func GetUSDCNYRateWithOK() (float64, bool) {
	return resolveUSDCNYRate()
}

// RefreshUSDCNYRate 强制刷新汇率缓存（可供定时任务或管理接口调用）
func RefreshUSDCNYRate() (float64, error) {
	rate, err := fetchRateFromBinance()
	if err != nil {
		return 0, err
	}
	rateStr := strconv.FormatFloat(rate, 'f', 4, 64)

	if common.RedisEnabled {
		_ = common.RedisSet(exchangeRateCacheKey, rateStr, exchangeRateCacheTTL)
	}

	memExchangeRateLock.Lock()
	memExchangeRate = rate
	memExchangeRateTime = time.Now()
	memExchangeRateLock.Unlock()

	return rate, nil
}
