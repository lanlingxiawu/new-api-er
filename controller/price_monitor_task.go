package controller

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
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
	rememberedEndpoints := getPriceMonitorStore().Get().SourceEndpoints
	plans := make([]priceMonitorSourcePlan, 0, len(channels)+2)
	sourceTypes := make(map[string]string)
	sourceModels := make(map[string]map[string]struct{})
	sourceURLs := make(map[string]string)
	channelSourceNames := make(map[int]string, len(channels))
	for _, channel := range channels {
		baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
		if channel.Status != common.ChannelStatusEnabled || !strings.HasPrefix(baseURL, "http") {
			continue
		}
		upstream := dto.UpstreamDTO{ID: channel.Id, Name: channel.Name, BaseURL: baseURL}
		plans = append(plans, priceMonitorSourcePlan{
			upstream:   upstream,
			candidates: priceMonitorEndpointCandidates(channel.Type, setting.CustomEndpointFor(channel.Id), rememberedEndpoints[strconv.Itoa(channel.Id)]),
		})
		sourceName := pricingSourceDisplayName(upstream)
		channelSourceNames[channel.Id] = sourceName
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
	officialUpstream := dto.UpstreamDTO{ID: officialRatioPresetID, Name: officialRatioPresetName, BaseURL: officialRatioPresetBaseURL}
	plans = append(plans, priceMonitorSourcePlan{upstream: officialUpstream, candidates: []string{officialRatioPresetEndpoint}})
	sourceTypes[pricingSourceDisplayName(officialUpstream)] = priceSourceOfficial
	if setting.IncludeModelsDev {
		upstream := dto.UpstreamDTO{ID: modelsDevPresetID, Name: modelsDevPresetName, BaseURL: modelsDevPresetBaseURL}
		plans = append(plans, priceMonitorSourcePlan{upstream: upstream, candidates: []string{modelsDevPresetBaseURL + modelsDevPath}})
		sourceTypes[pricingSourceDisplayName(upstream)] = priceSourceModelsDev
	}
	if len(plans) == 0 {
		setPriceMonitorRuntimeError("no enabled pricing sources are available")
		return
	}

	outcomes := resolvePriceMonitorSources(ctx, plans, setting.TimeoutSeconds)
	sourceOK := 0
	resolvedEndpoints := make(map[string]string, len(plans))
	comparableSources := make([]pricingSource, 0, len(plans))
	matrixSources := make([]pricingSource, 0, len(plans))
	for _, plan := range plans {
		name := pricingSourceDisplayName(plan.upstream)
		outcome := outcomes[name]
		source := pricingSource{name: name}
		endpoint := ""
		if outcome != nil {
			endpoint = outcome.endpoint
			if outcome.ok {
				source = outcome.source
			} else {
				source.failed = true
				source.failureReason = priceMonitorFailureKind(outcome.failure)
			}
		} else {
			source.failed = true
			source.failureReason = priceMonitorFailureFetch
		}
		source.endpoint = priceMonitorEndpointDisplay(endpoint)
		if sourceTypes[name] == priceSourceChannel {
			source.applicableModels = sourceModels[name]
			source.apiURL = sourceURLs[name]
		}
		status := resolvePriceMonitorSourceStatus(source, sourceTypes[name], marketplaceModels)
		source.status = status.status
		source.failureReason = status.failureReason
		source.fetchedModels = status.fetchedModels
		source.matchedModels = status.matchedModels
		if source.status == priceMonitorSourceStatusOK {
			sourceOK++
			comparableSources = append(comparableSources, source)
			if plan.upstream.ID > 0 && endpoint != "" {
				resolvedEndpoints[strconv.Itoa(plan.upstream.ID)] = endpoint
			}
		}
		matrixSources = append(matrixSources, source)
	}
	if sourceOK == 0 {
		setPriceMonitorRuntimeError("all pricing sources failed")
		return
	}
	differences := buildDifferences(getLocalPricingSyncData(), comparableSources)
	items := buildPriceMonitorItems(differences, marketplaceModels, sourceTypes, sourceModels)
	sourceHeaders, matrixItems := buildPriceMonitorMatrix(getLocalPricingSyncData(), matrixSources, marketplaceModels, sourceTypes)
	// 亏损判定单独走一遍已构建的矩阵，above_platform 的既有路径完全不受影响。
	contexts := buildPriceMonitorLossContexts(channels, channelSourceNames)
	applyPriceMonitorLossVerdicts(sourceHeaders, matrixItems, contexts)
	applyPriceMonitorRepairFloors(sourceHeaders, matrixItems, contexts)
	password, err := generatePriceMonitorPassword(8)
	if err != nil {
		setPriceMonitorRuntimeError("failed to create the access password")
		common.SysError("price monitor password generation failed: " + err.Error())
		return
	}
	checkedAt := time.Now()
	status := "success"
	if sourceOK < len(plans) {
		status = "partial"
	}
	comparisonModelCounts := countPriceMonitorComparisonModels(sourceHeaders, matrixItems)
	snapshot := PriceMonitorSnapshot{
		CheckedAt:             checkedAt.Unix(),
		Status:                status,
		SourceTotal:           len(plans),
		SourceOK:              sourceOK,
		SourceError:           len(plans) - sourceOK,
		ModelCount:            len(matrixItems),
		ItemCount:             len(items),
		ComparisonModelCounts: comparisonModelCounts,
		AccessPassword:        password,
		PasswordExpireAt:      checkedAt.Add(time.Duration(setting.IntervalMinutes) * time.Minute).Unix(),
		Items:                 items,
		SourceHeaders:         sourceHeaders,
		MatrixItems:           matrixItems,
		MatrixVersion:         priceMonitorMatrixVersion,
		SourceEndpoints:       resolvedEndpoints,
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
