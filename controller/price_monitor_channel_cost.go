package controller

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"
)

// 成本系数核对：每个渠道的上游分组倍率与本站成本系数的对比。
// 设计见 docs/design/price-monitor-channel-cost-check.md。

const (
	priceMonitorRatioSourceOfficial = "official"
	priceMonitorRatioSourceLog      = "log"
	priceMonitorRatioSourceSub2API  = "sub2api"

	// 上游是哪种网关：取到倍率时记下，下一轮直接走对应接口，不再白打另一种。
	priceMonitorUpstreamKindNewAPI  = "new-api"
	priceMonitorUpstreamKindSub2API = "sub2api"

	priceMonitorCostStatusUnknown = "unknown"
	priceMonitorCostStatusFree    = "free"
	priceMonitorCostStatusMatch   = "match"
	priceMonitorCostStatusLow     = "cost_low"
	priceMonitorCostStatusHigh    = "cost_high"

	// 取不到上游倍率的原因，只回传固定枚举，不把上游错误串透给页面。
	priceMonitorRatioReasonNoSource           = "no_source"           // 没配上游账号令牌，日志也没取到
	priceMonitorRatioReasonKeyNotInAccount    = "key_not_in_account"  // 渠道密钥不属于所配置的上游账号
	priceMonitorRatioReasonAutoGroup          = "auto_group"          // 令牌是 auto 分组，倍率不固定
	priceMonitorRatioReasonGroupMissing       = "group_missing"       // 上游账号的可用分组里没有该分组
	priceMonitorRatioReasonRejected           = "credential_rejected" // 401/403
	priceMonitorRatioReasonRateLimited        = "rate_limited"        // 429
	priceMonitorRatioReasonUnavailable        = "unavailable"         // 超时、网络、5xx、响应无法解析
	priceMonitorRatioReasonNotSupported       = "not_supported"       // 上游没有 new-api 的这些接口
	priceMonitorRatioReasonNoConsumeLog       = "no_consume_log"      // 日志里没有带倍率的消费记录
	priceMonitorRatioReasonWaiting            = "waiting"             // 本轮日志查询配额用完，排到下一轮
	priceMonitorRatioReasonUnsupportedSource  = "unsupported_channel" // OpenRouter 等非 new-api 上游
	priceMonitorRatioReasonSub2APIUnsupported = "sub2api_unsupported" // sub2api 没有倍率接口（旧版或简易模式）
	priceMonitorRatioReasonSub2APINoGroup     = "sub2api_no_group"    // sub2api 密钥未分配分组或分组不可用

	// 官方接口按渠道逐把密钥查分组，多密钥渠道只查前几把，避免一轮打出上百次请求。
	priceMonitorOfficialMaxKeys = 5
	// 上游响应体上限：这几个接口的正常响应都在几 KB 以内。
	priceMonitorUpstreamBodyLimit = 1 << 20
	// 并行处理的上游主机数；同一主机内顺序请求，照顾上游的按 IP 限流。
	priceMonitorUpstreamHostWorkers = 4
)

// PriceMonitorChannelCost 是一个渠道的成本系数核对结果，存进快照。
type PriceMonitorChannelCost struct {
	ChannelId   int    `json:"channel_id"`
	SourceKey   string `json:"source_key"`
	ChannelName string `json:"channel_name"`
	// UpstreamGroup 是渠道密钥在上游的分组；多把密钥分属不同分组时列在 UpstreamGroups，
	// 核对取其中倍率最高的一个（按最坏情况估成本）。
	UpstreamGroup  string   `json:"upstream_group,omitempty"`
	UpstreamGroups []string `json:"upstream_groups,omitempty"`
	UpstreamRatio  *float64 `json:"upstream_ratio,omitempty"`
	RatioSource    string   `json:"ratio_source,omitempty"`
	// ObservedAt 是 UpstreamRatio 的获取时间；AttemptedAt 是最近一次尝试获取的时间，
	// 两者分开是为了失败时沿用旧值、又不在刷新间隔内反复重试。
	ObservedAt  int64 `json:"observed_at,omitempty"`
	AttemptedAt int64 `json:"attempted_at,omitempty"`
	// Reason 是最近一次没取到（或只取到旧值）的原因，取值见 priceMonitorRatioReason*。
	Reason string `json:"reason,omitempty"`
	// UpstreamKind 是识别出的上游网关（priceMonitorUpstreamKind*），取数失败时保留。
	UpstreamKind string `json:"upstream_kind,omitempty"`
	// SourceFingerprint 是取数所依赖的渠道配置的摘要（见 priceMonitorRatioSourceFingerprint）。
	// 渠道 ID 不变但换了上游地址或密钥时，旧倍率不再代表这个渠道，必须作废重取，
	// 否则刷新间隔乃至最长沿用期内都拿旧倍率判亏损、算保本价。
	SourceFingerprint string `json:"source_fingerprint,omitempty"`
	// PeakMultiplier / PeakWindow：sub2api 分组设了高峰倍率时，UpstreamRatio 已按高峰时段
	// 的倍率计（最坏情况），这里记下高峰倍率与时段供页面说明。
	PeakMultiplier      *float64 `json:"peak_multiplier,omitempty"`
	PeakWindow          string   `json:"peak_window,omitempty"`
	CostRatio           float64  `json:"cost_ratio"`
	CostRatioConfigured bool     `json:"cost_ratio_configured"`
	Status              string   `json:"status"`
	Deviation           *float64 `json:"deviation,omitempty"`
}

// priceMonitorCostRatioEpsilon 只吸收浮点运算误差（如 (0.1+0.2)/0.3−1 约为 2e-16 而不是 0），不是业务容差：
// 两边都是手填的倍率，不相等就是成本账与上游实际扣费不一致，哪怕只差 1% 也要报。
const priceMonitorCostRatioEpsilon = 1e-9

// priceMonitorChannelCostStatus 比较成本系数 r 与上游分组倍率 g：偏差 = r/g − 1，
// 不相等即报，偏低与偏高分开标（设计 §4）。
func priceMonitorChannelCostStatus(upstreamRatio *float64, costRatio float64) (string, *float64) {
	if upstreamRatio == nil {
		return priceMonitorCostStatusUnknown, nil
	}
	if *upstreamRatio <= 0 {
		return priceMonitorCostStatusFree, nil
	}
	deviation := costRatio / *upstreamRatio - 1
	switch {
	case math.Abs(deviation) <= priceMonitorCostRatioEpsilon:
		return priceMonitorCostStatusMatch, floatPointer(deviation)
	case deviation < 0:
		return priceMonitorCostStatusLow, floatPointer(deviation)
	default:
		return priceMonitorCostStatusHigh, floatPointer(deviation)
	}
}

// applyPriceMonitorChannelCostStatus 用当前成本系数重算每个渠道的核对状态。
// configured 为有成本配置行的渠道；没有配置行的按 1.0 核对并标出。
func applyPriceMonitorChannelCostStatus(costs map[string]PriceMonitorChannelCost, costRatioOf func(channelId int) float64, configured map[int]bool) {
	for key, cost := range costs {
		cost.CostRatio = costRatioOf(cost.ChannelId)
		cost.CostRatioConfigured = configured[cost.ChannelId]
		cost.Status, cost.Deviation = priceMonitorChannelCostStatus(cost.UpstreamRatio, cost.CostRatio)
		costs[key] = cost
	}
}

// priceMonitorCostRatioConfigured 返回有成本配置行的渠道集合。表很小（每渠道一行）。
func priceMonitorCostRatioConfigured() map[int]bool {
	configs, err := model.GetAllChannelCostConfigs()
	if err != nil {
		common.SysError("price monitor failed to load channel cost configs: " + err.Error())
		return map[int]bool{}
	}
	configured := make(map[int]bool, len(configs))
	for _, config := range configs {
		configured[config.ChannelId] = true
	}
	return configured
}

func countPriceMonitorCostRatioMismatch(costs map[string]PriceMonitorChannelCost) int {
	count := 0
	for _, cost := range costs {
		if cost.Status == priceMonitorCostStatusLow || cost.Status == priceMonitorCostStatusHigh {
			count++
		}
	}
	return count
}

// ── 获取上游分组倍率 ────────────────────────────────────────────────────────

// priceMonitorRatioObservation 是一次获取的结果；ok 为假时 reason 说明原因。
type priceMonitorRatioObservation struct {
	ok     bool
	group  string
	groups []string
	ratio  float64
	source string
	reason string
	kind   string
	// 只有 sub2api 分组设了高峰倍率时才有。
	peakMultiplier *float64
	peakWindow     string
}

// priceMonitorUpstreamRatioFetcher 是几种取数方式的接缝，便于测试替换。
type priceMonitorUpstreamRatioFetcher struct {
	official func(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation
	sub2api  func(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation
	log      func(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation
}

// newPriceMonitorUpstreamRatioFetcher 为一轮巡检建立取数器：同一上游账号的令牌列表与
// 分组倍率在本轮内只取一次；每个上游请求受 timeout 约束。
// sub2apiRun 是本轮与价格步骤共用的 sub2api 取数（密钥答复在两步间复用）；nil 时新开一次。
func newPriceMonitorUpstreamRatioFetcher(timeout time.Duration, sub2apiRun *priceMonitorSub2APIRun) priceMonitorUpstreamRatioFetcher {
	official := &priceMonitorOfficialResolver{timeout: timeout, accounts: map[string]*priceMonitorAccount{}}
	sub2api := newPriceMonitorSub2APIProber(timeout, sub2apiRun)
	return priceMonitorUpstreamRatioFetcher{
		official: official.fetch,
		sub2api:  sub2api.fetch,
		log: func(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation {
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return fetchPriceMonitorLogGroupRatio(callCtx, channel)
		},
	}
}

// priceMonitorHasAccountCredential 与账号余额查询的判断一致：访问令牌与用户 ID 都要有，
// 官方用户级接口缺了 New-Api-User 会直接拒绝。
func priceMonitorHasAccountCredential(channel *model.Channel) bool {
	setting := channel.GetSetting()
	return strings.TrimSpace(setting.AccountBalanceToken) != "" && strings.TrimSpace(setting.AccountBalanceUserID) != ""
}

// priceMonitorRatioHost 是日志配额的计数单位：官方按 IP 限流，同一上游主机共享配额。
func priceMonitorRatioHost(channel *model.Channel) string {
	parsed, err := url.Parse(strings.TrimSpace(channel.GetBaseURL()))
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(channel.GetBaseURL())
	}
	return strings.ToLower(parsed.Host)
}

// priceMonitorRatioSourceFingerprint 摘要决定"取到哪个上游的哪个倍率"的渠道配置：类型、
// 上游地址、全部密钥、上游账号令牌与用户 ID。密钥取原始配置而不是启用中的那几把：多密钥渠道
// 自动禁用某把密钥是常态，不能因此把倍率作废。快照会落盘，只存摘要不存原文；接口不返回摘要。
func priceMonitorRatioSourceFingerprint(channel *model.Channel) string {
	setting := channel.GetSetting()
	material := strings.Join([]string{
		strconv.Itoa(channel.Type),
		strings.TrimSpace(channel.GetBaseURL()),
		channel.Key,
		strings.TrimSpace(setting.AccountBalanceToken),
		strings.TrimSpace(setting.AccountBalanceUserID),
	}, "\x00")
	return hex.EncodeToString(common.Sha256Raw([]byte(material)))[:16]
}

// resolvePriceMonitorUpstreamRatios 为每个渠道来源取得上游分组倍率（设计 §3）。
//
// 官方接口为主：配了上游账号令牌的渠道用它查"密钥所属分组"与"该分组对本账号的倍率"。
// 日志补充：没配令牌、官方接口失败或分组为 auto 的渠道，用渠道密钥查 /api/log/token，
// 每个上游主机每轮最多 UpstreamLogQueriesPerHost 次，按上次尝试最早的优先。
// 刷新间隔内沿用上一轮结果；新取数失败时，未过期的旧结果继续使用。
func resolvePriceMonitorUpstreamRatios(ctx context.Context, channels []*model.Channel, sourceNames map[int]string, previous map[string]PriceMonitorChannelCost, setting price_monitor_setting.PriceMonitorSetting, now time.Time, fetcher priceMonitorUpstreamRatioFetcher) map[string]PriceMonitorChannelCost {
	refresh := time.Duration(setting.UpstreamRatioRefreshHours) * time.Hour
	maxAge := time.Duration(setting.UpstreamRatioMaxAgeDays) * 24 * time.Hour
	result := make(map[string]PriceMonitorChannelCost, len(channels))
	dueByHost := make(map[string][]*model.Channel)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		sourceKey, ok := sourceNames[channel.Id]
		if !ok {
			continue
		}
		key := strconv.Itoa(channel.Id)
		cost := previous[key]
		cost.ChannelId, cost.SourceKey, cost.ChannelName = channel.Id, sourceKey, channel.Name
		fingerprint := priceMonitorRatioSourceFingerprint(channel)
		if cost.SourceFingerprint != "" && cost.SourceFingerprint != fingerprint {
			// 换了上游或密钥：上一轮的倍率、网关类型和失败原因都属于旧配置，作废并立即重取。
			clearPriceMonitorRatioObservation(&cost)
			cost.UpstreamKind = ""
			cost.Reason = ""
			cost.AttemptedAt = 0
		}
		// 没有摘要的是升级前的快照，无从判断是否换过，直接记下当前配置。
		cost.SourceFingerprint = fingerprint
		if cost.ObservedAt > 0 && now.Sub(time.Unix(cost.ObservedAt, 0)) > maxAge {
			clearPriceMonitorRatioObservation(&cost)
		}
		if channel.Type == constant.ChannelTypeOpenRouter {
			clearPriceMonitorRatioObservation(&cost)
			cost.Reason = priceMonitorRatioReasonUnsupportedSource
			result[key] = cost
			continue
		}
		result[key] = cost
		if cost.AttemptedAt > 0 && now.Sub(time.Unix(cost.AttemptedAt, 0)) < refresh {
			continue
		}
		host := priceMonitorRatioHost(channel)
		dueByHost[host] = append(dueByHost[host], channel)
	}

	// 上次尝试最早的先查：日志配额不够时轮换，不会总是同几个渠道。
	// 排序在启动 worker 之前完成：之后 result 只在持锁时读写。
	for _, due := range dueByHost {
		sort.SliceStable(due, func(i, j int) bool {
			return result[strconv.Itoa(due[i].Id)].AttemptedAt < result[strconv.Itoa(due[j].Id)].AttemptedAt
		})
	}

	var mu sync.Mutex
	hosts := make(chan string, len(dueByHost))
	for host := range dueByHost {
		hosts <- host
	}
	close(hosts)
	var wg sync.WaitGroup
	for worker := 0; worker < priceMonitorUpstreamHostWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					common.SysError(fmt.Sprintf("price monitor upstream ratio worker panic: %v", recovered))
				}
			}()
			for host := range hosts {
				logBudget := setting.UpstreamLogQueriesPerHost
				for _, channel := range dueByHost[host] {
					if ctx.Err() != nil {
						return
					}
					key := strconv.Itoa(channel.Id)
					mu.Lock()
					kind := result[key].UpstreamKind
					mu.Unlock()
					observation, usedLog, rateLimited := resolvePriceMonitorChannelRatio(ctx, channel, kind, logBudget > 0, setting.UpstreamLogQueriesPerHost > 0, fetcher)
					// 预算用完时进行中的请求被取消，失败不是上游的问题：丢弃，不记为已尝试，下一轮优先。
					if ctx.Err() != nil {
						return
					}
					if usedLog {
						logBudget--
					}
					if rateLimited {
						logBudget = 0
					}
					mu.Lock()
					cost := result[key]
					applyPriceMonitorRatioObservation(&cost, observation, now)
					result[key] = cost
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return result
}

// resolvePriceMonitorChannelRatio 取一个渠道的上游倍率（设计 sub2api-upstream-support.md §4.1）：
//   - Sub2API 类型的渠道、或上一轮已识别为 sub2api 的渠道，只问 sub2api 的倍率接口；
//   - 其他渠道先走官方 new-api 接口；没配账号令牌或上游没有这些接口时，用渠道密钥问一次
//     sub2api；仍取不到再用 new-api 日志补充。
//
// kind 是上一轮识别出的上游网关。logAllowed 表示本轮该主机还有日志配额；logsEnabled 表示
// 日志补充是否开启。
func resolvePriceMonitorChannelRatio(ctx context.Context, channel *model.Channel, kind string, logAllowed, logsEnabled bool, fetcher priceMonitorUpstreamRatioFetcher) (observation priceMonitorRatioObservation, usedLog bool, rateLimited bool) {
	if channel.Type == constant.ChannelTypeSub2API || kind == priceMonitorUpstreamKindSub2API {
		// sub2api 没有 new-api 的日志接口，不往下补。
		observation = fetcher.sub2api(ctx, channel)
		return observation, false, observation.reason == priceMonitorRatioReasonRateLimited
	}
	official := priceMonitorRatioObservation{reason: priceMonitorRatioReasonNoSource}
	if priceMonitorHasAccountCredential(channel) {
		official = fetcher.official(ctx, channel)
		if official.ok {
			official.kind = priceMonitorUpstreamKindNewAPI
			return official, false, false
		}
	}
	// 官方接口给出了与密钥相关的答复（密钥不在账号下、auto 分组等）说明上游就是 new-api，不必再问 sub2api。
	if kind != priceMonitorUpstreamKindNewAPI && (official.reason == priceMonitorRatioReasonNoSource || official.reason == priceMonitorRatioReasonNotSupported) {
		// 取到倍率，或失败但响应确认上游就是 sub2api（例如密钥未分配分组）：以 sub2api 的结果为准，
		// 不再去问 new-api 的日志（sub2api 没有），页面也显示 sub2api 给出的原因。
		if fromSub2API := fetcher.sub2api(ctx, channel); fromSub2API.ok || fromSub2API.kind == priceMonitorUpstreamKindSub2API {
			return fromSub2API, false, fromSub2API.reason == priceMonitorRatioReasonRateLimited
		}
	}
	if !logAllowed {
		// 日志补充开着、只是本轮配额用完：排队，下一轮优先（不必等一个刷新间隔）。
		// 日志补充关闭时没有别的来源，报官方接口的原因（没配令牌即 no_source）。
		if logsEnabled {
			official.reason = priceMonitorRatioReasonWaiting
		}
		return official, false, false
	}
	fromLog := fetcher.log(ctx, channel)
	if fromLog.ok {
		fromLog.kind = priceMonitorUpstreamKindNewAPI
		return fromLog, true, false
	}
	rateLimited = fromLog.reason == priceMonitorRatioReasonRateLimited
	// 官方接口的失败原因比日志更具体（例如密钥不属于该账号），优先报告它。
	if official.reason != priceMonitorRatioReasonNoSource {
		return official, true, rateLimited
	}
	return fromLog, true, rateLimited
}

func applyPriceMonitorRatioObservation(cost *PriceMonitorChannelCost, observation priceMonitorRatioObservation, now time.Time) {
	// 排队等配额、被上游限流的渠道不算尝试过：下一轮优先重试，而不是等一个刷新间隔。
	if observation.reason == priceMonitorRatioReasonWaiting || observation.reason == priceMonitorRatioReasonRateLimited {
		if cost.UpstreamRatio == nil || observation.reason == priceMonitorRatioReasonRateLimited {
			cost.Reason = observation.reason
		}
		return
	}
	cost.AttemptedAt = now.Unix()
	if !observation.ok {
		// 沿用未过期的旧结果，同时记下这次为什么没取到新的。
		cost.Reason = observation.reason
		switch {
		case observation.kind != "":
			// 失败响应也能确认上游类型（例如 sub2api 答复密钥未分配分组）。
			cost.UpstreamKind = observation.kind
		case observation.reason == priceMonitorRatioReasonSub2APIUnsupported && cost.UpstreamKind == priceMonitorUpstreamKindSub2API,
			observation.reason == priceMonitorRatioReasonNotSupported && cost.UpstreamKind == priceMonitorUpstreamKindNewAPI:
			// 记下的网关不再提供对应接口（换了网关、改了地址或降级），下一轮重新识别。
			cost.UpstreamKind = ""
		}
		return
	}
	if observation.kind != "" {
		cost.UpstreamKind = observation.kind
	}
	cost.PeakMultiplier = observation.peakMultiplier
	cost.PeakWindow = observation.peakWindow
	cost.UpstreamGroup = observation.group
	cost.UpstreamGroups = observation.groups
	cost.UpstreamRatio = floatPointer(observation.ratio)
	cost.RatioSource = observation.source
	cost.ObservedAt = now.Unix()
	cost.Reason = ""
}

func clearPriceMonitorRatioObservation(cost *PriceMonitorChannelCost) {
	cost.UpstreamGroup = ""
	cost.UpstreamGroups = nil
	cost.UpstreamRatio = nil
	cost.RatioSource = ""
	cost.ObservedAt = 0
	cost.PeakMultiplier = nil
	cost.PeakWindow = ""
}

// priceMonitorCostsDueNow 返回一份所有渠道都到期的核对结果（不改传入的快照）：快照升级后
// 新增了取数方式（如 sub2api），上一轮取不到的渠道应当立即重试，而不是等一个刷新间隔。
// 已知的倍率保留，直到取到新的。
func priceMonitorCostsDueNow(costs map[string]PriceMonitorChannelCost) map[string]PriceMonitorChannelCost {
	due := make(map[string]PriceMonitorChannelCost, len(costs))
	for key, cost := range costs {
		cost.AttemptedAt = 0
		due[key] = cost
	}
	return due
}

// ── 官方接口 ────────────────────────────────────────────────────────────────

type priceMonitorUpstreamStatusError struct{ status int }

func (e *priceMonitorUpstreamStatusError) Error() string {
	return fmt.Sprintf("upstream status %d", e.status)
}

// priceMonitorUpstreamClient 是没配代理时用的独立客户端：不与 relay 共用连接池。
// 配了代理的渠道沿用渠道代理（与余额查询一致）。
// 在第一次使用时才克隆 http.DefaultTransport：启动时 common.InitEnv 会给它加上
// TLS_INSECURE_SKIP_VERIFY 等设置，包加载时克隆会漏掉。
var (
	priceMonitorUpstreamClientOnce sync.Once
	priceMonitorUpstreamClient     *http.Client
)

func priceMonitorUpstreamHTTPClient(proxy string) (*http.Client, error) {
	if strings.TrimSpace(proxy) == "" {
		priceMonitorUpstreamClientOnce.Do(func() {
			priceMonitorUpstreamClient = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
		})
		return priceMonitorUpstreamClient, nil
	}
	return service.GetHttpClientWithProxy(proxy)
}

// priceMonitorUpstreamGet 以上游账号身份发一个 GET，调用约定与账号余额查询一致
// （Authorization: Bearer <访问令牌>，New-Api-User: <用户 ID>）。整个请求（含读响应体）
// 受 timeout 约束：没有它，一个不回响应的上游会让整轮巡检永远卡住。
// 访问令牌只出现在请求头里，不进日志、不进快照。
func priceMonitorUpstreamGet(ctx context.Context, timeout time.Duration, channel *model.Channel, baseURL, path string, out any) error {
	setting := channel.GetSetting()
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(setting.AccountBalanceToken))
	req.Header.Set("New-Api-User", strings.TrimSpace(setting.AccountBalanceUserID))
	client, err := priceMonitorUpstreamHTTPClient(setting.Proxy)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer service.DrainAndCloseResponseBody(res)
	if res.StatusCode != http.StatusOK {
		return &priceMonitorUpstreamStatusError{status: res.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, priceMonitorUpstreamBodyLimit))
	if err != nil {
		return err
	}
	return common.Unmarshal(body, out)
}

func priceMonitorUpstreamErrorReason(err error) string {
	var statusErr *priceMonitorUpstreamStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.status == http.StatusUnauthorized || statusErr.status == http.StatusForbidden:
			return priceMonitorRatioReasonRejected
		case statusErr.status == http.StatusTooManyRequests:
			return priceMonitorRatioReasonRateLimited
		case statusErr.status == http.StatusNotFound || statusErr.status == http.StatusMethodNotAllowed:
			return priceMonitorRatioReasonNotSupported
		}
	}
	return priceMonitorRatioReasonUnavailable
}

// priceMonitorTokenListResponse 同时对应 /api/token/ 与 /api/token/search（官方分页结构）。
type priceMonitorTokenListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Total int `json:"total"`
		Items []struct {
			Key   string `json:"key"`
			Group string `json:"group"`
		} `json:"items"`
	} `json:"data"`
}

type priceMonitorUserSelfResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Group string `json:"group"`
	} `json:"data"`
}

// ratio 在官方实现里可能是数字，也可能是 auto 分组的说明文字，所以按任意类型解析。
type priceMonitorUserGroupsResponse struct {
	Success bool `json:"success"`
	Data    map[string]struct {
		Ratio any `json:"ratio"`
	} `json:"data"`
}

func priceMonitorAccountBaseURL(channel *model.Channel) string {
	baseURL := strings.TrimSpace(channel.GetSetting().AccountBalanceURL)
	if baseURL == "" {
		baseURL = channel.GetBaseURL()
	}
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

const (
	// 官方令牌列表每页最多 100 条；最多读 10 页，超出的令牌走逐把搜索。
	priceMonitorTokenPageSize = 100
	priceMonitorTokenMaxPages = 10
)

// priceMonitorMaskTokenKey 与官方 model.MaskTokenKey 相同：令牌列表只返回脱敏后的密钥。
func priceMonitorMaskTokenKey(key string) string {
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

// priceMonitorAccount 是一个上游账号在本轮巡检里的数据，同一账号下的渠道共用，只取一次。
// 官方 /api/token/search 挂 SearchRateLimit（默认每用户每分钟 10 次），逐渠道逐把搜索会被限流，
// 所以先取整个令牌列表按脱敏密钥匹配，只在匹配不唯一或列表没读全时才逐把搜索。
type priceMonitorAccount struct {
	mu     sync.Mutex
	loaded bool
	// reason 非空表示账号级数据取不到（令牌被拒、限流、不是 new-api 等），账号下所有渠道同因失败，
	// 失败也缓存，不对同一账号重复请求。
	reason string
	// groupsByMask：脱敏密钥 -> 对应令牌的分组（不同令牌可能脱敏成同一个形式）。
	groupsByMask map[string]map[string]struct{}
	complete     bool
	ratios       map[string]float64
	selfGroup    *string
	// selfReason / searchReason 缓存 /api/user/self 与 /api/token/search 的失败（含限流），
	// 账号下其余渠道直接复用，不再逐个等超时或撞限流。
	selfReason   string
	searchReason string
}

type priceMonitorOfficialResolver struct {
	timeout  time.Duration
	mu       sync.Mutex
	accounts map[string]*priceMonitorAccount
}

func (r *priceMonitorOfficialResolver) account(channel *model.Channel, baseURL string) *priceMonitorAccount {
	setting := channel.GetSetting()
	// 代理也算进账号标识：同一账号走不同代理的渠道，一个代理的失败不该连累另一个。
	key := strings.Join([]string{baseURL, strings.TrimSpace(setting.AccountBalanceUserID), strings.TrimSpace(setting.AccountBalanceToken), strings.TrimSpace(setting.Proxy)}, "\x00")
	r.mu.Lock()
	defer r.mu.Unlock()
	account, ok := r.accounts[key]
	if !ok {
		account = &priceMonitorAccount{}
		r.accounts[key] = account
	}
	return account
}

// load 取账号的令牌列表与各分组倍率。调用方持有 account.mu。
func (r *priceMonitorOfficialResolver) load(ctx context.Context, account *priceMonitorAccount, channel *model.Channel, baseURL string) {
	account.loaded = true
	account.groupsByMask = map[string]map[string]struct{}{}
	read := 0
	for page := 1; page <= priceMonitorTokenMaxPages; page++ {
		var list priceMonitorTokenListResponse
		path := fmt.Sprintf("/api/token/?p=%d&page_size=%d", page, priceMonitorTokenPageSize)
		if err := priceMonitorUpstreamGet(ctx, r.timeout, channel, baseURL, path, &list); err != nil {
			account.reason = priceMonitorUpstreamErrorReason(err)
			return
		}
		if !list.Success {
			account.reason = priceMonitorRatioReasonRejected
			return
		}
		read += len(list.Data.Items)
		for _, item := range list.Data.Items {
			// 2026-03 之前的官方版本在列表里返回完整密钥，之后返回脱敏形式；统一按脱敏形式索引
			// （对已脱敏的 18 位形式再脱敏结果不变），两种版本都能匹配。
			mask := priceMonitorMaskTokenKey(strings.TrimPrefix(strings.TrimSpace(item.Key), "sk-"))
			groups := account.groupsByMask[mask]
			if groups == nil {
				groups = map[string]struct{}{}
				account.groupsByMask[mask] = groups
			}
			groups[strings.TrimSpace(item.Group)] = struct{}{}
		}
		// 有正数 total 时按已读条数判断（上游的每页上限可能小于 100，本页不满不代表读完）；
		// total 为 0 可能是上游计数失败，这时只能以"本页不满"为准。空页一律视为读完。
		complete := read >= list.Data.Total
		if list.Data.Total <= 0 {
			complete = len(list.Data.Items) < priceMonitorTokenPageSize
		}
		if complete || len(list.Data.Items) == 0 {
			account.complete = true
			break
		}
	}
	var groups priceMonitorUserGroupsResponse
	if err := priceMonitorUpstreamGet(ctx, r.timeout, channel, baseURL, "/api/user/self/groups", &groups); err != nil {
		account.reason = priceMonitorUpstreamErrorReason(err)
		return
	}
	if !groups.Success {
		account.reason = priceMonitorRatioReasonUnavailable
		return
	}
	account.ratios = map[string]float64{}
	for name, entry := range groups.Data {
		// auto 的 ratio 是说明文字，不是数字，不进倍率表。
		if ratio, numeric := asFloat64(entry.Ratio); numeric && ratio >= 0 {
			account.ratios[name] = ratio
		}
	}
}

// keyGroup 返回一把密钥的令牌分组（空串表示跟随账号分组）或失败原因。调用方持有 account.mu。
func (r *priceMonitorOfficialResolver) keyGroup(ctx context.Context, account *priceMonitorAccount, channel *model.Channel, baseURL, key string) (string, string) {
	groups := account.groupsByMask[priceMonitorMaskTokenKey(key)]
	if len(groups) == 1 {
		for group := range groups {
			return group, ""
		}
	}
	if len(groups) == 0 && account.complete {
		return "", priceMonitorRatioReasonKeyNotInAccount
	}
	// 脱敏形式对上多个分组，或令牌列表没读全：按密钥精确搜索（不带通配符即精确匹配）。
	if account.searchReason != "" {
		return "", account.searchReason
	}
	var search priceMonitorTokenListResponse
	if err := priceMonitorUpstreamGet(ctx, r.timeout, channel, baseURL, "/api/token/search?token="+url.QueryEscape(key), &search); err != nil {
		// 限流、接口不可用对整个账号成立，缓存；密钥级的"查不到"不缓存。
		account.searchReason = priceMonitorUpstreamErrorReason(err)
		return "", account.searchReason
	}
	if !search.Success {
		account.searchReason = priceMonitorRatioReasonUnavailable
		return "", account.searchReason
	}
	if len(search.Data.Items) == 0 {
		return "", priceMonitorRatioReasonKeyNotInAccount
	}
	return strings.TrimSpace(search.Data.Items[0].Group), ""
}

// fetch 用官方接口取渠道密钥的上游分组与倍率（设计 §3.1）：令牌列表（必要时逐把搜索）得到令牌
// 分组，为空表示跟随账号分组（/api/user/self）；/api/user/self/groups 得到各分组对本账号的倍率
// （已含分组间特殊倍率）。多把密钥分属不同分组时取倍率最高的。
func (r *priceMonitorOfficialResolver) fetch(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation {
	baseURL := priceMonitorAccountBaseURL(channel)
	if !strings.HasPrefix(baseURL, "http") {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonNotSupported}
	}
	account := r.account(channel, baseURL)
	// 同一账号的渠道串行：第一个取数，其余复用。
	account.mu.Lock()
	defer account.mu.Unlock()
	if !account.loaded {
		r.load(ctx, account, channel, baseURL)
	}
	if account.reason != "" {
		return priceMonitorRatioObservation{reason: account.reason}
	}
	keys := priceMonitorEnabledKeys(channel)
	if len(keys) > priceMonitorOfficialMaxKeys {
		keys = keys[:priceMonitorOfficialMaxKeys]
	}
	groupSet := make(map[string]struct{})
	for _, key := range keys {
		group, reason := r.keyGroup(ctx, account, channel, baseURL, strings.TrimPrefix(key, "sk-"))
		// 不在账号下的密钥（已在上游删除）跳过：relay 用它本来就会失败，核对的是能用的密钥；
		// 全部都查不到时才算失败。其他原因（限流、接口不可用）对整个账号成立，直接返回。
		if reason == priceMonitorRatioReasonKeyNotInAccount {
			continue
		}
		if reason != "" {
			return priceMonitorRatioObservation{reason: reason}
		}
		if group == "" {
			if account.selfGroup == nil && account.selfReason == "" {
				var self priceMonitorUserSelfResponse
				if err := priceMonitorUpstreamGet(ctx, r.timeout, channel, baseURL, "/api/user/self", &self); err != nil {
					account.selfReason = priceMonitorUpstreamErrorReason(err)
				} else if !self.Success {
					account.selfReason = priceMonitorRatioReasonUnavailable
				} else {
					selfGroup := strings.TrimSpace(self.Data.Group)
					account.selfGroup = &selfGroup
				}
			}
			if account.selfReason != "" {
				return priceMonitorRatioObservation{reason: account.selfReason}
			}
			group = *account.selfGroup
		}
		if group == "" {
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonGroupMissing}
		}
		groupSet[group] = struct{}{}
	}
	if len(groupSet) == 0 {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonKeyNotInAccount}
	}
	if _, auto := groupSet["auto"]; auto {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonAutoGroup}
	}
	names := make([]string, 0, len(groupSet))
	for group := range groupSet {
		names = append(names, group)
	}
	sort.Strings(names)
	observation := priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceOfficial, ratio: -1}
	for _, group := range names {
		ratio, exists := account.ratios[group]
		if !exists {
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonGroupMissing}
		}
		if ratio > observation.ratio {
			observation.ratio, observation.group = ratio, group
		}
	}
	if len(names) > 1 {
		observation.groups = names
	}
	return observation
}

// priceMonitorEnabledKeys 返回渠道里未被禁用的密钥（去空白、去空串）。多密钥渠道里被禁用的密钥
// 通常是在上游已失效的，拿它查分组只会得到"不在账号下"。状态表里没有记录的密钥是启用的。
func priceMonitorEnabledKeys(channel *model.Channel) []string {
	statuses := channel.ChannelInfo.MultiKeyStatusList
	keys := make([]string, 0)
	for index, key := range channel.GetKeys() {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if channel.ChannelInfo.IsMultiKey {
			if status, recorded := statuses[index]; recorded && status != common.ChannelStatusEnabled {
				continue
			}
		}
		keys = append(keys, key)
	}
	return keys
}

// ── 日志补充 ────────────────────────────────────────────────────────────────

// fetchPriceMonitorLogGroupRatio 用渠道密钥查上游 /api/log/token，取最近一条带倍率的消费日志（设计 §3.2）。
// 倍率：user_group_ratio ≥ 0 时是该用户在该分组的专属倍率，否则取 group_ratio（官方默认 −1 表示无专属倍率）。
func fetchPriceMonitorLogGroupRatio(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation {
	keys := priceMonitorEnabledKeys(channel)
	if len(keys) == 0 {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonNoSource}
	}
	result, err := service.QueryUpstreamLogs(ctx, channel.GetBaseURL(),
		service.UpstreamLogCredential{Token: keys[0]},
		service.UpstreamLogFilters{Type: model.LogTypeConsume}, 1, 50)
	if err != nil {
		return priceMonitorRatioObservation{reason: priceMonitorLogErrorReason(err)}
	}
	var newest *service.UpstreamLogItem
	var newestRatio float64
	for i := range result.Items {
		item := &result.Items[i]
		ratio, ok := priceMonitorLogItemRatio(item.Other)
		if !ok {
			continue
		}
		if newest == nil || item.CreatedAt > newest.CreatedAt {
			newest, newestRatio = item, ratio
		}
	}
	if newest == nil {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonNoConsumeLog}
	}
	group := strings.TrimSpace(newest.Group)
	if group == "" {
		if value, ok := newest.Other["group"].(string); ok {
			group = strings.TrimSpace(value)
		}
	}
	return priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceLog, group: group, ratio: newestRatio}
}

func priceMonitorLogItemRatio(other map[string]interface{}) (float64, bool) {
	if other == nil {
		return 0, false
	}
	if special, ok := asFloat64(other["user_group_ratio"]); ok && special >= 0 {
		return special, true
	}
	if ratio, ok := asFloat64(other["group_ratio"]); ok && ratio >= 0 {
		return ratio, true
	}
	return 0, false
}

func priceMonitorLogErrorReason(err error) string {
	switch {
	case errors.Is(err, service.ErrUpstreamLogRateLimited):
		return priceMonitorRatioReasonRateLimited
	case errors.Is(err, service.ErrUpstreamLogUnauthorized):
		return priceMonitorRatioReasonRejected
	case errors.Is(err, service.ErrUpstreamLogEndpointMissing), errors.Is(err, service.ErrUpstreamLogBaseURLMissing), errors.Is(err, service.ErrUpstreamLogBaseURLInvalid):
		return priceMonitorRatioReasonNotSupported
	case errors.Is(err, service.ErrUpstreamLogBusy):
		return priceMonitorRatioReasonWaiting
	default:
		return priceMonitorRatioReasonUnavailable
	}
}
