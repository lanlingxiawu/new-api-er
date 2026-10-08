package controller

import (
	"context"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const openRouterPricingEndpoint = "openrouter"

// priceMonitorDefaultEndpoints 是自动探测顺序，取值与"同步上游倍率"弹窗里的内置端点一致。
var priceMonitorDefaultEndpoints = []string{defaultEndpoint, "/api/ratio_config"}

// priceMonitorEndpointCandidates 给出一个渠道本轮要依次尝试的价格接口。
//
// 人工指定的端点只试它自己：配了却取不到价格就应该显示失败，
// 悄悄换成别的端点会把管理员配错地址这件事掩盖掉。
//
// Sub2API 类型的渠道只取模型广场；其他渠道在 new-api 的两个端点之后再试一次 sub2api
// 模型广场（匿名请求，广场没开或不是 sub2api 时不会发出渠道密钥）。
//
// remembered 是上一轮自动探测命中的端点，只用来调整探测顺序，所以只接受自动探测候选里的值
// （见 priceMonitorRememberableEndpoint）。管理员固定过的完整地址不会进入记忆；即便旧快照里
// 存着这样的地址也在这里丢弃——否则去掉固定端点后，渠道仍会一直按那个别处主机的价格比较。
func priceMonitorEndpointCandidates(channelType int, pinned, remembered string) []string {
	if channelType == constant.ChannelTypeOpenRouter {
		return []string{openRouterPricingEndpoint}
	}
	if endpoint := strings.TrimSpace(pinned); endpoint != "" {
		return []string{endpoint}
	}
	if channelType == constant.ChannelTypeSub2API {
		return []string{sub2apiPricingEndpoint}
	}
	defaults := priceMonitorProbeEndpoints()
	candidates := make([]string, 0, len(defaults))
	if endpoint := strings.TrimSpace(remembered); priceMonitorRememberableEndpoint(endpoint) {
		candidates = append(candidates, endpoint)
	}
	for _, endpoint := range defaults {
		if len(candidates) > 0 && candidates[0] == endpoint {
			continue
		}
		candidates = append(candidates, endpoint)
	}
	return candidates
}

// priceMonitorProbeEndpoints 是没有固定端点的普通渠道的自动探测顺序。
func priceMonitorProbeEndpoints() []string {
	return append(append([]string{}, priceMonitorDefaultEndpoints...), sub2apiPricingEndpoint)
}

// priceMonitorRememberableEndpoint 报告一个端点能否写进（或读出）探测记忆：只有自动探测候选
// 本身才行。它们都是相对路径，总是请求渠道自己的 BaseURL。
func priceMonitorRememberableEndpoint(endpoint string) bool {
	for _, candidate := range priceMonitorProbeEndpoints() {
		if endpoint == candidate {
			return true
		}
	}
	return false
}

// priceMonitorEndpointDisplay 只回显路径：完整地址里的主机、查询串可能带有部署信息或凭据，
// 而分享页也会读到表头。
func priceMonitorEndpointDisplay(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || endpoint == openRouterPricingEndpoint {
		return endpoint
	}
	lowered := strings.ToLower(endpoint)
	if !strings.HasPrefix(lowered, "http://") && !strings.HasPrefix(lowered, "https://") {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	if parsed.Path == "" {
		return "/"
	}
	return parsed.Path
}

type priceMonitorSourcePlan struct {
	upstream   dto.UpstreamDTO
	candidates []string
}

type priceMonitorFetchOutcome struct {
	source   pricingSource
	endpoint string
	failure  string
	ok       bool
}

// resolvePriceMonitorSources 按候选端点逐轮抓取，直到每个来源要么取到价格、要么候选用尽。
// 每轮内部仍然走 fetchUpstreamPricingSources 的并发与超时控制，轮数只由候选个数决定
// （人工指定 1 个、自动探测最多 3 个），稳态下第一轮就全部命中。
func resolvePriceMonitorSources(ctx context.Context, plans []priceMonitorSourcePlan, timeoutSeconds int) map[string]*priceMonitorFetchOutcome {
	outcomes := make(map[string]*priceMonitorFetchOutcome, len(plans))
	pending := make([]priceMonitorSourcePlan, 0, len(plans))
	for _, plan := range plans {
		outcomes[pricingSourceDisplayName(plan.upstream)] = &priceMonitorFetchOutcome{}
		if len(plan.candidates) > 0 {
			pending = append(pending, plan)
		}
	}

	for round := 0; len(pending) > 0; round++ {
		batch := make([]dto.UpstreamDTO, 0, len(pending))
		endpointByName := make(map[string]string, len(pending))
		for _, plan := range pending {
			if round >= len(plan.candidates) {
				continue
			}
			upstream := plan.upstream
			upstream.Endpoint = plan.candidates[round]
			batch = append(batch, upstream)
			endpointByName[pricingSourceDisplayName(upstream)] = upstream.Endpoint
		}
		if len(batch) == 0 {
			break
		}

		testResults, sources := fetchUpstreamPricingSources(ctx, batch, timeoutSeconds)
		sourceByName := make(map[string]pricingSource, len(sources))
		for _, source := range sources {
			sourceByName[source.name] = source
		}
		for _, result := range testResults {
			outcome, tracked := outcomes[result.Name]
			if !tracked {
				continue
			}
			if result.Status == "success" {
				outcome.endpoint = endpointByName[result.Name]
				outcome.ok = true
				outcome.failure = ""
				outcome.source = sourceByName[result.Name]
				continue
			}
			// sub2api 是非 Sub2API 渠道的最后一个试探候选：上游看起来根本不是 sub2api 时，保留前面
			// 候选的端点与原因，否则每个取价失败的 new-api 渠道都会显示成 sub2api 失败。
			if endpointByName[result.Name] == sub2apiPricingEndpoint && outcome.failure != "" && strings.HasPrefix(result.Error, sub2apiNotDetectedPrefix) {
				continue
			}
			outcome.endpoint = endpointByName[result.Name]
			outcome.failure = result.Error
		}

		next := make([]priceMonitorSourcePlan, 0, len(pending))
		for _, plan := range pending {
			if outcomes[pricingSourceDisplayName(plan.upstream)].ok {
				continue
			}
			if round+1 < len(plan.candidates) {
				next = append(next, plan)
			}
		}
		pending = next
	}
	return outcomes
}

// priceMonitorFailureKind 把抓取错误收敛成固定枚举，页面据此给文案，不透出上游原始错误。
func priceMonitorFailureKind(failure string) string {
	switch failure {
	case emptyPricingPayloadError:
		return priceMonitorFailureEmpty
	case sub2apiPlazaDisabledError:
		return priceMonitorFailureSub2APIPlazaDisabled
	case sub2apiGroupUnknownError:
		return priceMonitorFailureSub2APIGroupUnknown
	}
	return priceMonitorFailureFetch
}

// countPricingPayloadModels 统计来源返回了多少个模型的价格。
func countPricingPayloadModels(data map[string]any) int {
	return len(pricingPayloadModels(data))
}

// countPricingPayloadMatchedModels 统计其中有多少个能和本平台的模型对上名字。
func countPricingPayloadMatchedModels(data map[string]any, comparable map[string]struct{}) int {
	if len(comparable) == 0 {
		return 0
	}
	matched := 0
	for modelName := range pricingPayloadModels(data) {
		if _, ok := comparable[modelName]; ok {
			matched++
		}
	}
	return matched
}

// pricingPayloadModels 返回来源给出了可用价格的模型，判断与 pricingPayloadHasPrices 相同：
// 值为 null、非数值或空表达式的模型不算"来源提供了价格"。
func pricingPayloadModels(data map[string]any) map[string]struct{} {
	models := make(map[string]struct{})
	for _, field := range pricingPayloadPriceFields {
		for modelName, value := range valueMap(data[field]) {
			if pricingPayloadUsablePrice(field, value) {
				models[modelName] = struct{}{}
			}
		}
	}
	return models
}

// priceMonitorComparableModels 是这个来源本应覆盖的模型集合：
// 渠道只对它自己启用的模型负责，官方与 models.dev 对整个模型广场负责。
func priceMonitorComparableModels(sourceType string, marketplaceModels, applicableModels map[string]struct{}) map[string]struct{} {
	if sourceType != priceSourceChannel || applicableModels == nil {
		return marketplaceModels
	}
	comparable := make(map[string]struct{}, len(applicableModels))
	for modelName := range applicableModels {
		if _, ok := marketplaceModels[modelName]; ok {
			comparable[modelName] = struct{}{}
		}
	}
	return comparable
}

type priceMonitorSourceStatusResult struct {
	status        string
	failureReason string
	fetchedModels int
	matchedModels int
}

// resolvePriceMonitorSourceStatus 判断这个来源本轮是否真的参与了价格对比。
func resolvePriceMonitorSourceStatus(source pricingSource, sourceType string, marketplaceModels map[string]struct{}) priceMonitorSourceStatusResult {
	if source.failed {
		reason := source.failureReason
		if reason == "" {
			reason = priceMonitorFailureFetch
		}
		return priceMonitorSourceStatusResult{status: priceMonitorSourceStatusFailed, failureReason: reason}
	}
	// 渠道一个模型都没配时是配置问题，和"配了但和平台对不上名字"要分开说。
	if sourceType == priceSourceChannel && source.applicableModels != nil && len(source.applicableModels) == 0 {
		return priceMonitorSourceStatusResult{status: priceMonitorSourceStatusNoModels}
	}
	comparable := priceMonitorComparableModels(sourceType, marketplaceModels, source.applicableModels)
	fetched := countPricingPayloadModels(source.data)
	matched := countPricingPayloadMatchedModels(source.data, comparable)
	if matched == 0 {
		return priceMonitorSourceStatusResult{status: priceMonitorSourceStatusNoOverlap, fetchedModels: fetched}
	}
	return priceMonitorSourceStatusResult{status: priceMonitorSourceStatusOK, fetchedModels: fetched, matchedModels: matched}
}
