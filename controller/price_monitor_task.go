package controller

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	priceMonitorTickInterval     = time.Minute
	officialRatioPresetEndpoint  = "/llm-metadata/api/newapi/ratio_config-v1-base.json"
	priceMonitorPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

var (
	priceMonitorStartOnce   sync.Once
	priceMonitorStoreOnce   sync.Once
	priceMonitorStore       *priceMonitorSnapshotStore
	priceMonitorRunning     atomic.Bool
	priceMonitorLastAttempt atomic.Int64
	priceMonitorRuntimeMu   sync.RWMutex
	priceMonitorLastError   string
)

func getPriceMonitorStore() *priceMonitorSnapshotStore {
	priceMonitorStoreOnce.Do(func() {
		priceMonitorStore = newPriceMonitorSnapshotStore(priceMonitorSnapshotPath())
		if err := priceMonitorStore.Load(); err != nil {
			common.SysError("failed to load price monitor snapshot: " + err.Error())
		}
	})
	return priceMonitorStore
}

func StartPriceMonitorTask() {
	priceMonitorStartOnce.Do(func() {
		getPriceMonitorStore()
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					common.SysError(fmt.Sprintf("price monitor task panic: %v", recovered))
				}
			}()
			common.SysLog("price monitor task started")
			runPriceMonitorCheckIfDue(time.Now())
			ticker := time.NewTicker(priceMonitorTickInterval)
			defer ticker.Stop()
			for now := range ticker.C {
				runPriceMonitorCheckIfDue(now)
			}
		})
	})
}

func priceMonitorCheckDue(enabled bool, intervalMinutes int, checkedAt, lastAttemptAt int64, now time.Time) bool {
	if !enabled {
		return false
	}
	if intervalMinutes < 5 {
		intervalMinutes = 5
	}
	lastRun := checkedAt
	if lastAttemptAt > lastRun {
		lastRun = lastAttemptAt
	}
	return lastRun == 0 || now.Unix()-lastRun >= int64(intervalMinutes*60)
}

func runPriceMonitorCheckIfDue(now time.Time) {
	setting := price_monitor_setting.GetPriceMonitorSetting().Normalized()
	snapshot := getPriceMonitorStore().Get()
	if setting.Enabled && priceMonitorSnapshotNeedsRefresh(snapshot) {
		triggerPriceMonitorCheck()
		return
	}
	if !priceMonitorCheckDue(setting.Enabled, setting.IntervalMinutes, snapshot.CheckedAt, priceMonitorLastAttempt.Load(), now) {
		return
	}
	triggerPriceMonitorCheck()
}

func priceMonitorSnapshotNeedsRefresh(snapshot PriceMonitorSnapshot) bool {
	return snapshot.MatrixVersion < priceMonitorMatrixVersion || len(snapshot.SourceHeaders) == 0
}

func triggerPriceMonitorCheck() bool {
	if !priceMonitorRunning.CompareAndSwap(false, true) {
		return false
	}
	gopool.Go(func() {
		defer priceMonitorRunning.Store(false)
		defer func() {
			if recovered := recover(); recovered != nil {
				setPriceMonitorRuntimeError("price check failed unexpectedly")
				common.SysError(fmt.Sprintf("price monitor check panic: %v", recovered))
			}
		}()
		runPriceMonitorCheck(context.Background(), time.Now())
	})
	return true
}

func runPriceMonitorCheck(ctx context.Context, startedAt time.Time) {
	priceMonitorLastAttempt.Store(startedAt.Unix())
	setting := price_monitor_setting.GetPriceMonitorSetting().Normalized()
	marketplaceModels := filterPriceMonitorMarketplaceModels(model.GetPricing(), setting.ModelWhitelist)
	if len(marketplaceModels) == 0 {
		setPriceMonitorRuntimeError("the model whitelist excludes all marketplace models")
		return
	}

	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		setPriceMonitorRuntimeError("failed to load enabled channels")
		common.SysError("price monitor failed to load channels: " + err.Error())
		return
	}
	upstreams := make([]dto.UpstreamDTO, 0, len(channels)+2)
	sourceTypes := make(map[string]string)
	sourceModels := make(map[string]map[string]struct{})
	sourceURLs := make(map[string]string)
	for _, channel := range channels {
		baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
		if channel.Status != common.ChannelStatusEnabled || !strings.HasPrefix(baseURL, "http") {
			continue
		}
		endpoint := defaultEndpoint
		if channel.Type == constant.ChannelTypeOpenRouter {
			endpoint = "openrouter"
		}
		upstream := dto.UpstreamDTO{ID: channel.Id, Name: channel.Name, BaseURL: baseURL, Endpoint: endpoint}
		upstreams = append(upstreams, upstream)
		sourceName := pricingSourceDisplayName(upstream)
		sourceTypes[sourceName] = priceSourceChannel
		sourceURLs[sourceName] = priceMonitorDisplayURL(baseURL)
		enabledModels := make(map[string]struct{})
		for _, modelName := range channel.GetModels() {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" {
				enabledModels[modelName] = struct{}{}
			}
		}
		sourceModels[sourceName] = enabledModels
	}
	officialUpstream := dto.UpstreamDTO{ID: officialRatioPresetID, Name: officialRatioPresetName, BaseURL: officialRatioPresetBaseURL, Endpoint: officialRatioPresetEndpoint}
	upstreams = append(upstreams, officialUpstream)
	sourceTypes[pricingSourceDisplayName(officialUpstream)] = priceSourceOfficial
	if setting.IncludeModelsDev {
		upstream := dto.UpstreamDTO{ID: modelsDevPresetID, Name: modelsDevPresetName, BaseURL: modelsDevPresetBaseURL, Endpoint: modelsDevPresetBaseURL + modelsDevPath}
		upstreams = append(upstreams, upstream)
		sourceTypes[pricingSourceDisplayName(upstream)] = priceSourceOfficial
	}
	if len(upstreams) == 0 {
		setPriceMonitorRuntimeError("no enabled pricing sources are available")
		return
	}

	differences, testResults, successfulSources := fetchUpstreamPricingSnapshotData(ctx, upstreams, setting.TimeoutSeconds)
	sourceOK := 0
	for _, result := range testResults {
		if result.Status == "success" {
			sourceOK++
		}
	}
	if sourceOK == 0 {
		setPriceMonitorRuntimeError("all pricing sources failed")
		return
	}
	items := buildPriceMonitorItems(differences, marketplaceModels, sourceTypes, sourceModels)
	successfulByName := make(map[string]pricingSource, len(successfulSources))
	for _, source := range successfulSources {
		successfulByName[source.name] = source
	}
	matrixSources := make([]pricingSource, 0, len(upstreams))
	for _, upstream := range upstreams {
		name := pricingSourceDisplayName(upstream)
		source, ok := successfulByName[name]
		if !ok {
			source = pricingSource{name: name, failed: true}
		}
		if sourceTypes[name] == priceSourceChannel {
			source.applicableModels = sourceModels[name]
			source.apiURL = sourceURLs[name]
		}
		matrixSources = append(matrixSources, source)
	}
	sourceHeaders, matrixItems := buildPriceMonitorMatrix(getLocalPricingSyncData(), matrixSources, marketplaceModels, sourceTypes)
	password, err := generatePriceMonitorPassword(8)
	if err != nil {
		setPriceMonitorRuntimeError("failed to create the access password")
		common.SysError("price monitor password generation failed: " + err.Error())
		return
	}
	checkedAt := time.Now()
	status := "success"
	if sourceOK < len(upstreams) {
		status = "partial"
	}
	snapshot := PriceMonitorSnapshot{
		CheckedAt:        checkedAt.Unix(),
		Status:           status,
		SourceTotal:      len(upstreams),
		SourceOK:         sourceOK,
		SourceError:      len(upstreams) - sourceOK,
		ModelCount:       len(matrixItems),
		ItemCount:        len(items),
		AccessPassword:   password,
		PasswordExpireAt: checkedAt.Add(time.Duration(setting.IntervalMinutes) * time.Minute).Unix(),
		Items:            items,
		SourceHeaders:    sourceHeaders,
		MatrixItems:      matrixItems,
		MatrixVersion:    priceMonitorMatrixVersion,
	}
	if err := getPriceMonitorStore().Save(snapshot); err != nil {
		setPriceMonitorRuntimeError("failed to save the latest price check")
		common.SysError("price monitor snapshot save failed: " + err.Error())
		return
	}
	setPriceMonitorRuntimeError("")
}

func filterPriceMonitorMarketplaceModels(pricing []model.Pricing, whitelist string) map[string]struct{} {
	excluded := make(map[string]struct{})
	for _, modelName := range strings.FieldsFunc(whitelist, func(char rune) bool {
		return char == ',' || char == '\n' || char == '\r'
	}) {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			excluded[modelName] = struct{}{}
		}
	}
	models := make(map[string]struct{}, len(pricing))
	for _, item := range pricing {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		if _, skip := excluded[modelName]; !skip {
			models[modelName] = struct{}{}
		}
	}
	return models
}

func pricingSourceDisplayName(upstream dto.UpstreamDTO) string {
	if upstream.ID == 0 {
		return upstream.Name
	}
	return fmt.Sprintf("%s(%d)", upstream.Name, upstream.ID)
}

func generatePriceMonitorPassword(length int) (string, error) {
	password := make([]byte, length)
	limit := big.NewInt(int64(len(priceMonitorPasswordAlphabet)))
	for index := range password {
		value, err := cryptorand.Int(cryptorand.Reader, limit)
		if err != nil {
			return "", err
		}
		password[index] = priceMonitorPasswordAlphabet[value.Int64()]
	}
	return string(password), nil
}

func setPriceMonitorRuntimeError(message string) {
	priceMonitorRuntimeMu.Lock()
	priceMonitorLastError = message
	priceMonitorRuntimeMu.Unlock()
}

func getPriceMonitorRuntimeError() string {
	priceMonitorRuntimeMu.RLock()
	defer priceMonitorRuntimeMu.RUnlock()
	return priceMonitorLastError
}
