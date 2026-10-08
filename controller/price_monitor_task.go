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
	priceMonitorTickInterval = time.Minute
	// priceMonitorUpstreamRatioBudget 是一轮巡检里获取上游分组倍率这一步的总时长上限。
	priceMonitorUpstreamRatioBudget = 15 * time.Minute
	officialRatioPresetEndpoint     = "/llm-metadata/api/newapi/ratio_config-v1-base.json"
	priceMonitorPasswordAlphabet    = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
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

// priceMonitorCheckDue 判断是否该开始一轮检查：上次成功时间与上次尝试时间取较晚者 + 间隔。
// 晚于 now 的时间戳不算数：系统时钟往回调过（例如 NTP 校时）时，它们来自调整之前，
// 当真的话要多等"回拨量 + 间隔"才会再检查。忽略之后本轮到期立即检查，检查会记下新的尝试时间。
func priceMonitorCheckDue(enabled bool, intervalMinutes int, checkedAt, lastAttemptAt int64, now time.Time) bool {
	if !enabled {
		return false
	}
	if intervalMinutes < 5 {
		intervalMinutes = 5
	}
	nowUnix := now.Unix()
	lastRun := int64(0)
	for _, at := range []int64{checkedAt, lastAttemptAt} {
		if at <= nowUnix && at > lastRun {
			lastRun = at
		}
	}
	return lastRun == 0 || nowUnix-lastRun >= int64(intervalMinutes*60)
}

// runPriceMonitorCheckIfDue 在每个 tick 判断是否启动一轮检查。
//
// 快照缺少来源表头或矩阵版本过旧（新装、升级）时，本进程的第一次 tick 立即检查一次，不等间隔。
// 之后一律按「上次成功时间与上次尝试时间取较晚者 + 间隔」判断：失败的检查不会更新快照，
// 如果不看上次尝试时间，快照一直「需要刷新」，每分钟都会把所有渠道和官方预设重新拉一遍。
func runPriceMonitorCheckIfDue(now time.Time) {
	setting := price_monitor_setting.GetPriceMonitorSetting().Normalized()
	snapshot := getPriceMonitorStore().Get()
	lastAttemptAt := priceMonitorLastAttempt.Load()
	if setting.Enabled && lastAttemptAt == 0 && priceMonitorSnapshotNeedsRefresh(snapshot) {
		triggerPriceMonitorCheck()
		return
	}
	if !priceMonitorCheckDue(setting.Enabled, setting.IntervalMinutes, snapshot.CheckedAt, lastAttemptAt, now) {
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

	// 价格步骤与倍率步骤都会用渠道密钥查 sub2api：两步共用一次取数，价格步骤问过的密钥
	// 倍率步骤直接复用答复；被拒名额走进程级限额，并给倍率步骤留了一部分（priceMonitorSub2APIRatioReserve）。
	sub2apiRun := newPriceMonitorSub2APIRun()
	outcomes := resolvePriceMonitorSources(withPriceMonitorSub2APIRun(ctx, sub2apiRun), plans, setting.TimeoutSeconds)
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
			// 探测记忆只记自动探测命中的端点；固定端点属于配置，不进记忆（见 priceMonitorEndpointCandidates）。
			if plan.upstream.ID > 0 && setting.CustomEndpointFor(plan.upstream.ID) == "" && priceMonitorRememberableEndpoint(endpoint) {
				resolvedEndpoints[strconv.Itoa(plan.upstream.ID)] = endpoint
			}
		}
		matrixSources = append(matrixSources, source)
	}
	if sourceOK == 0 {
		setPriceMonitorRuntimeError("all pricing sources failed")
		return
	}
	// 上游分组倍率与价格来源无关：取价失败的渠道同样核对成本系数。它要访问上游、可能较慢，
	// 放在读平台定价之前，缩小"读到的平台价已被管理员改过"的窗口。
	// 整步有总预算：单个请求有超时，但上游大量渠道都挂起时逐个等超时仍可能拖上几小时，
	// 巡检的运行标志会一直挡住后续轮次。预算用完时没轮到的渠道沿用上一轮结果。
	fetcher := newPriceMonitorUpstreamRatioFetcher(time.Duration(setting.TimeoutSeconds)*time.Second, sub2apiRun)
	ratioCtx, cancelRatio := context.WithTimeout(ctx, priceMonitorUpstreamRatioBudget)
	previous := getPriceMonitorStore().Get()
	previousCosts := previous.ChannelCosts
	if previous.MatrixVersion < priceMonitorMatrixVersion {
		// 升级后新增了取数方式（如 sub2api），上一轮没取到的渠道立即重试，不等刷新间隔。
		previousCosts = priceMonitorCostsDueNow(previousCosts)
	}
	channelCosts := resolvePriceMonitorUpstreamRatios(ratioCtx, channels, channelSourceNames, previousCosts, setting, time.Now(), fetcher)
	cancelRatio()
	// 从这里开始读平台定价与成本系数；之后发生的改动由保存后的补算覆盖（见 priceMonitorRefreshPending）。
	priceMonitorRefreshPending.Store(false)
	applyPriceMonitorChannelCostStatus(channelCosts, model.GetChannelCostRatio, priceMonitorCostRatioConfigured())
	differences := buildDifferences(getLocalPricingSyncData(), comparableSources)
	items := buildPriceMonitorItems(differences, marketplaceModels, sourceTypes, sourceModels)
	sourceHeaders, matrixItems := buildPriceMonitorMatrix(getLocalPricingSyncData(), matrixSources, marketplaceModels, sourceTypes)
	assignPriceMonitorHeaderChannelIds(sourceHeaders, channelSourceNames)
	// 亏损判定单独走一遍已构建的矩阵，above_platform 的既有路径完全不受影响。
	contexts := buildPriceMonitorLossContexts(channels, channelSourceNames, channelCosts)
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
	comparisonModelCounts.CostRatioMismatch = countPriceMonitorCostRatioMismatch(channelCosts)
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
		ChannelCosts:          channelCosts,
	}
	if err := getPriceMonitorStore().Save(snapshot); err != nil {
		setPriceMonitorRuntimeError("failed to save the latest price check")
		common.SysError("price monitor snapshot save failed: " + err.Error())
		return
	}
	setPriceMonitorRuntimeError("")
	// 本轮读定价之后有人改了价格或成本系数：刚保存的快照不含那次改动，补算一次。
	if priceMonitorRefreshPending.Swap(false) {
		if _, err := recomputePriceMonitorSnapshotInPlace(); err != nil {
			common.SysError("price monitor refresh after check failed: " + err.Error())
		}
	}
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

// assignPriceMonitorHeaderChannelIds 给渠道来源的表头填上渠道 ID。
func assignPriceMonitorHeaderChannelIds(headers []PriceMonitorSourceHeader, channelSourceNames map[int]string) {
	channelIds := make(map[string]int, len(channelSourceNames))
	for channelId, sourceName := range channelSourceNames {
		channelIds[sourceName] = channelId
	}
	for i := range headers {
		if channelId, ok := channelIds[headers[i].Key]; ok && headers[i].Type == priceSourceChannel {
			headers[i].ChannelId = channelId
		}
	}
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
