package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 对接 sub2api（Wei-Shaw/sub2api）上游：倍率与模型价格。
// 设计见 docs/design/sub2api-upstream-support.md §4、§5。

const (
	// sub2apiPricingEndpoint 是价格巡检的特殊端点名（与 openrouter 同类）：取模型广场的价格。
	sub2apiPricingEndpoint = "sub2api"

	sub2apiKeyBillingPath   = "/v1/sub2api/billing"
	sub2apiModelPlazaPath   = "/api/v1/model-plaza"
	sub2apiKeyBillingObject = "sub2api.key_billing"

	// 价格来源失败的固定错误串，priceMonitorFailureKind 据此给出页面文案。
	sub2apiPlazaDisabledError = "sub2api model plaza is not enabled"
	sub2apiGroupUnknownError  = "sub2api key group is not visible in the model plaza"

	sub2apiBillingModeToken      = "token"
	sub2apiBillingModePerRequest = "per_request"
)

// errSub2APINotBilling 表示 200 响应但不是 sub2api 的倍率接口（有些网关对任意路径都回 200）。
var errSub2APINotBilling = errors.New("response is not sub2api key billing")

// sub2apiKeyBilling 是 GET /v1/sub2api/billing 的响应（sub2api schema_version 1）。
// 倍率只覆盖按 token 计费；图片/视频独立倍率、按时段定价、按推理强度加价不在其中。
type sub2apiKeyBilling struct {
	Object              string  `json:"object"`
	GroupRateMultiplier float64 `json:"group_rate_multiplier"`
	// ResolvedRateMultiplier 已含给本账号单设的倍率（user_rate_multiplier）。
	ResolvedRateMultiplier float64  `json:"resolved_rate_multiplier"`
	PeakRateEnabled        bool     `json:"peak_rate_enabled"`
	PeakStart              string   `json:"peak_start"`
	PeakEnd                string   `json:"peak_end"`
	PeakRateMultiplier     *float64 `json:"peak_rate_multiplier"`
	Timezone               string   `json:"timezone"`
}

// sub2apiBillingStatusError 是倍率接口的非 200 响应。recognised 表示响应体是 sub2api 网关
// 处理器的错误结构（{"type":"error","error":{"type":...}}），即上游确实是 sub2api：
// 例如密钥未分配分组的 403，与别的网关对未知路径回的 403/404 区分开。
type sub2apiBillingStatusError struct {
	status     int
	recognised bool
}

func (e *sub2apiBillingStatusError) Error() string {
	return fmt.Sprintf("sub2api billing status %d", e.status)
}

// fetchSub2APIKeyBilling 用渠道密钥查它在 sub2api 上实际生效的倍率。密钥只放在请求头里。
func fetchSub2APIKeyBilling(ctx context.Context, client *http.Client, baseURL, key string) (sub2apiKeyBilling, error) {
	var billing sub2apiKeyBilling
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+sub2apiKeyBillingPath, nil)
	if err != nil {
		return billing, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := client.Do(req)
	if err != nil {
		return billing, err
	}
	defer service.DrainAndCloseResponseBody(res)
	body, err := io.ReadAll(io.LimitReader(res.Body, priceMonitorUpstreamBodyLimit))
	if err != nil {
		return billing, err
	}
	if res.StatusCode != http.StatusOK {
		var shape struct {
			Type  string `json:"type"`
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		recognised := common.Unmarshal(body, &shape) == nil && shape.Type == "error" && shape.Error.Type != ""
		return billing, &sub2apiBillingStatusError{status: res.StatusCode, recognised: recognised}
	}
	if err := common.Unmarshal(body, &billing); err != nil || billing.Object != sub2apiKeyBillingObject {
		return billing, errSub2APINotBilling
	}
	return billing, nil
}

// sub2apiBillingErrorReason 把倍率接口的失败收敛成原因枚举。
func sub2apiBillingErrorReason(err error) string {
	if errors.Is(err, errSub2APINotBilling) {
		return priceMonitorRatioReasonSub2APIUnsupported
	}
	var statusErr *sub2apiBillingStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.status {
		case http.StatusNotFound, http.StatusMethodNotAllowed:
			return priceMonitorRatioReasonSub2APIUnsupported
		case http.StatusForbidden:
			return priceMonitorRatioReasonSub2APINoGroup
		case http.StatusUnauthorized:
			return priceMonitorRatioReasonRejected
		case http.StatusTooManyRequests:
			return priceMonitorRatioReasonRateLimited
		}
	}
	return priceMonitorRatioReasonUnavailable
}

// sub2apiBillingRecognised 报告这次失败是否来自 sub2api 本身（而不是别的网关）。
func sub2apiBillingRecognised(err error) bool {
	var statusErr *sub2apiBillingStatusError
	return errors.As(err, &statusErr) && statusErr.recognised
}

// sub2apiWorstCaseRatio 返回按最坏情况估的倍率：分组设了高于 1 的高峰倍率时按高峰时段计。
// 高峰倍率低于 1（高峰打折）时不影响最坏情况，但仍返回供页面说明。
func sub2apiWorstCaseRatio(billing sub2apiKeyBilling) (float64, *float64, string) {
	ratio := billing.ResolvedRateMultiplier
	if !billing.PeakRateEnabled || billing.PeakRateMultiplier == nil {
		return ratio, nil, ""
	}
	peak := *billing.PeakRateMultiplier
	if peak > 1 {
		ratio *= peak
	}
	window := ""
	if billing.PeakStart != "" && billing.PeakEnd != "" {
		window = billing.PeakStart + "-" + billing.PeakEnd
	}
	window = strings.TrimSpace(window + " " + billing.Timezone)
	return ratio, floatPointer(peak), window
}

// sub2api 在同一 IP 60 秒内出现 120 次无效密钥时会封禁该 IP 60 秒，连同我们发往它的转发流量。
// 本进程在任意 60 秒内发往同一主机、被拒的密钥不超过 priceMonitorSub2APIMaxRejectedPerHost 把，
// 远低于封禁阈值。窗口跨巡检轮次与手动同步：连点几次"同步上游倍率"也累计在同一个窗口里。
const (
	priceMonitorSub2APIMaxRejectedPerHost = 20
	priceMonitorSub2APIRejectWindow       = time.Minute
	// priceMonitorSub2APIRatioReserve 是留给倍率步骤的名额：取价（巡检的价格步骤与手动同步）
	// 最多用到上限减去它。否则一个主机上有 20 把以上本地启用、上游已作废的密钥时，价格步骤
	// 每轮都先把名额用完，倍率步骤里没被价格步骤问过的渠道永远轮不到。
	priceMonitorSub2APIRatioReserve   = 5
	priceMonitorSub2APIPriceStepLimit = priceMonitorSub2APIMaxRejectedPerHost - priceMonitorSub2APIRatioReserve
)

// priceMonitorSub2APIKeyLimiter 是进程内按主机计的被拒密钥限额（滑动窗口）。
//
// 窗口内被拒的密钥累计到调用方可用的上限、或收到 429（之后一个窗口内）时，不再向该主机发送密钥。
// 价格步骤并发取价，所以发送前先占一个名额（窗口内被拒数 + 在途数 < 上限），保证并发时被拒数
// 也不会越过上限；名额不够时等在途请求结束再判断，调用方的 ctx 到期就放弃等待、不再发送
// （等待者占着取价的并发槽，不能等过自己的超时）。
// 时间用 time.Now 的单调时钟读数比较，系统时钟回拨不影响窗口。
//
// 限额只在本进程内、按渠道地址的主机名计：多个节点各自计数，同一个 sub2api 用两个主机名接入时
// 各算各的（sub2api 按来源 IP 封禁）。巡检只在主节点跑；手动同步可在任意节点发起，见设计 §4.1「限额的边界」。
type priceMonitorSub2APIKeyLimiter struct {
	mu  sync.Mutex
	now func() time.Time
	// wake 在每次 release 时关闭并换新，唤醒所有等名额的 acquire。
	wake  chan struct{}
	hosts map[string]*priceMonitorSub2APIHostState
}

type priceMonitorSub2APIHostState struct {
	rejected     []time.Time
	inflight     int
	blockedUntil time.Time
}

func newPriceMonitorSub2APIKeyLimiter(now func() time.Time) *priceMonitorSub2APIKeyLimiter {
	return &priceMonitorSub2APIKeyLimiter{now: now, wake: make(chan struct{}), hosts: map[string]*priceMonitorSub2APIHostState{}}
}

// priceMonitorSub2APIKeys 是本进程所有 sub2api 密钥查询（巡检与手动同步）共用的限额。
var priceMonitorSub2APIKeys = newPriceMonitorSub2APIKeyLimiter(time.Now)

// stateLocked 返回主机的计数，并丢掉窗口外的被拒记录。调用方持有 mu。
func (l *priceMonitorSub2APIKeyLimiter) stateLocked(host string, now time.Time) *priceMonitorSub2APIHostState {
	state := l.hosts[host]
	if state == nil {
		state = &priceMonitorSub2APIHostState{}
		l.hosts[host] = state
	}
	expired := 0
	for expired < len(state.rejected) && now.Sub(state.rejected[expired]) >= priceMonitorSub2APIRejectWindow {
		expired++
	}
	state.rejected = state.rejected[expired:]
	return state
}

// acquire 为向 host 发送一把密钥占一个名额；limit 是调用方可用的被拒上限。
// 主机正被停用（窗口内被拒已达 limit，或刚收到 429）时返回 false。ctx 已到期（包括等名额期间
// 到期）时返回 ctx 的错误，不占名额。返回 true 时调用方必须随后调用 release。
func (l *priceMonitorSub2APIKeyLimiter) acquire(ctx context.Context, host string, limit int) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		l.mu.Lock()
		now := l.now()
		state := l.stateLocked(host, now)
		if now.Before(state.blockedUntil) || len(state.rejected) >= limit {
			l.forgetIdleLocked(host, state, now)
			l.mu.Unlock()
			return false, nil
		}
		if len(state.rejected)+state.inflight < limit {
			state.inflight++
			l.mu.Unlock()
			return true, nil
		}
		// 走到这里说明有在途请求（被拒数 < limit 且被拒数 + 在途数 >= limit），它结束时会 release 唤醒。
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// release 归还 acquire 占的名额，并按这次请求的结果（reason 为空表示成功）记账。
func (l *priceMonitorSub2APIKeyLimiter) release(host, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	state := l.stateLocked(host, now)
	state.inflight--
	switch reason {
	case priceMonitorRatioReasonRejected:
		state.rejected = append(state.rejected, now)
	case priceMonitorRatioReasonRateLimited:
		state.blockedUntil = now.Add(priceMonitorSub2APIRejectWindow)
	}
	l.forgetIdleLocked(host, state, now)
	close(l.wake)
	l.wake = make(chan struct{})
}

// forgetIdleLocked 删掉没有任何记录的主机，map 不随历史主机增长。
func (l *priceMonitorSub2APIKeyLimiter) forgetIdleLocked(host string, state *priceMonitorSub2APIHostState, now time.Time) {
	if state.inflight == 0 && len(state.rejected) == 0 && !now.Before(state.blockedUntil) {
		delete(l.hosts, host)
	}
}

// priceMonitorSub2APIRun 是一次取数（一轮巡检，或一次手动同步）向 sub2api 倍率接口发密钥的入口：
// 名额走进程级限额；同一把密钥在本次内只问一次——价格步骤问过的密钥，倍率步骤直接用它的答复，
// 不再发第二次（被拒的密钥也就不会记两次）。并发的调用（多个渠道共用地址与密钥，或价格步骤与
// 倍率步骤同时问）不各发一次：先到的去问，其余等它的答复。
type priceMonitorSub2APIRun struct {
	limiter *priceMonitorSub2APIKeyLimiter
	mu      sync.Mutex
	calls   map[string]*priceMonitorSub2APIKeyCall
}

// priceMonitorSub2APIKeyCall 是本次取数里对一把密钥的查询。done 关闭前 reusable/billing/err
// 已写好；答复不可复用（暂时性失败，或没问成）时条目在关闭 done 前撤下，等待者重新判断。
type priceMonitorSub2APIKeyCall struct {
	done     chan struct{}
	reusable bool
	billing  sub2apiKeyBilling
	err      error
}

func newPriceMonitorSub2APIRun() *priceMonitorSub2APIRun {
	return newPriceMonitorSub2APIRunWith(priceMonitorSub2APIKeys)
}

func newPriceMonitorSub2APIRunWith(limiter *priceMonitorSub2APIKeyLimiter) *priceMonitorSub2APIRun {
	return &priceMonitorSub2APIRun{limiter: limiter, calls: map[string]*priceMonitorSub2APIKeyCall{}}
}

// sub2apiKeyAnswerReusable 报告一次答复能否在本次取数内复用：成功，以及只取决于密钥本身、
// 重问也不会变的失败（被拒、未分配分组、不是 sub2api）。超时、5xx、429 这类暂时性失败不复用。
func sub2apiKeyAnswerReusable(err error) bool {
	if err == nil {
		return true
	}
	switch sub2apiBillingErrorReason(err) {
	case priceMonitorRatioReasonRejected, priceMonitorRatioReasonSub2APINoGroup, priceMonitorRatioReasonSub2APIUnsupported:
		return true
	}
	return false
}

// fetchKeyBilling 在限额内用一把密钥查倍率接口；本次取数问过这把密钥时直接给出上次的答复。
// limit 是调用方可用的被拒上限（取价用 priceMonitorSub2APIPriceStepLimit，倍率步骤用全部）。
// 主机正被停用时返回 answered=false，不发请求。ctx 在等名额或等别的调用答复时到期，返回
// answered=true 与 ctx 的错误（与请求超时同样处理），不发请求。
func (r *priceMonitorSub2APIRun) fetchKeyBilling(ctx context.Context, client *http.Client, host, baseURL, key string, limit int) (sub2apiKeyBilling, bool, error) {
	answerKey := baseURL + "\x00" + key
	for {
		r.mu.Lock()
		call, inProgress := r.calls[answerKey]
		if !inProgress {
			call = &priceMonitorSub2APIKeyCall{done: make(chan struct{})}
			r.calls[answerKey] = call
		}
		r.mu.Unlock()
		if !inProgress {
			return r.ask(ctx, call, answerKey, client, host, baseURL, key, limit)
		}
		select {
		case <-call.done:
		case <-ctx.Done():
			return sub2apiKeyBilling{}, true, ctx.Err()
		}
		if call.reusable {
			return call.billing, true, call.err
		}
		// 先到的调用没问成或遇到暂时性失败，条目已撤下：重新判断（可能由本调用去问）。
	}
}

// ask 由占到 call 的调用执行：在限额内发一次请求并把答复交给等待者。
func (r *priceMonitorSub2APIRun) ask(ctx context.Context, call *priceMonitorSub2APIKeyCall, answerKey string, client *http.Client, host, baseURL, key string, limit int) (billing sub2apiKeyBilling, answered bool, err error) {
	defer func() {
		// panic 时 reusable 仍为 false：条目撤下、等待者被唤醒，不会永远等下去。
		if !call.reusable {
			r.mu.Lock()
			delete(r.calls, answerKey)
			r.mu.Unlock()
		}
		close(call.done)
	}()
	acquired, waitErr := r.limiter.acquire(ctx, host, limit)
	if waitErr != nil {
		return billing, true, waitErr
	}
	if !acquired {
		return billing, false, nil
	}
	reason := ""
	defer func() { r.limiter.release(host, reason) }()
	billing, err = fetchSub2APIKeyBilling(ctx, client, baseURL, key)
	if err != nil {
		reason = sub2apiBillingErrorReason(err)
	}
	if sub2apiKeyAnswerReusable(err) {
		call.billing, call.err, call.reusable = billing, err, true
	}
	return billing, true, err
}

type priceMonitorSub2APIRunKey struct{}

// withPriceMonitorSub2APIRun 把一次取数挂到 context 上，价格步骤经由
// fetchUpstreamPricingSources 取到它（取价函数签名与手动同步共用，不另加参数）。
func withPriceMonitorSub2APIRun(ctx context.Context, run *priceMonitorSub2APIRun) context.Context {
	return context.WithValue(ctx, priceMonitorSub2APIRunKey{}, run)
}

func priceMonitorSub2APIRunFrom(ctx context.Context) *priceMonitorSub2APIRun {
	run, _ := ctx.Value(priceMonitorSub2APIRunKey{}).(*priceMonitorSub2APIRun)
	return run
}

// priceMonitorSub2APIProber 是倍率步骤的 sub2api 查询器，限额见 priceMonitorSub2APIKeyLimiter。
type priceMonitorSub2APIProber struct {
	timeout time.Duration
	run     *priceMonitorSub2APIRun
}

// newPriceMonitorSub2APIProber 建立查询器；run 为 nil 时新开一次取数（仍走进程级限额）。
func newPriceMonitorSub2APIProber(timeout time.Duration, run *priceMonitorSub2APIRun) *priceMonitorSub2APIProber {
	if run == nil {
		run = newPriceMonitorSub2APIRun()
	}
	return &priceMonitorSub2APIProber{timeout: timeout, run: run}
}

// fetchPriceMonitorSub2APIRatio 单独查一个渠道（新开一次取数），供单独调用与测试。
func fetchPriceMonitorSub2APIRatio(ctx context.Context, timeout time.Duration, channel *model.Channel) priceMonitorRatioObservation {
	return newPriceMonitorSub2APIProber(timeout, nil).fetch(ctx, channel)
}

// fetch 用渠道密钥取 sub2api 上的实际倍率（设计 §4.2）。
// 多密钥渠道逐把查未禁用的密钥（最多 priceMonitorOfficialMaxKeys 把），取最高的；
// 被拒（401）或未分配分组（403）的密钥跳过，全部如此才算失败。每个请求受 timeout 约束。
// 失败响应确认上游是 sub2api 时，观察结果带上 kind，下一轮直接走 sub2api。
func (p *priceMonitorSub2APIProber) fetch(ctx context.Context, channel *model.Channel) priceMonitorRatioObservation {
	keys := priceMonitorEnabledKeys(channel)
	if len(keys) == 0 {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonNoSource}
	}
	if len(keys) > priceMonitorOfficialMaxKeys {
		keys = keys[:priceMonitorOfficialMaxKeys]
	}
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if !strings.HasPrefix(baseURL, "http") {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}
	}
	host := priceMonitorRatioHost(channel)
	client, err := priceMonitorUpstreamHTTPClient(channel.GetSetting().Proxy)
	if err != nil {
		return priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}
	}
	best := priceMonitorRatioObservation{ratio: -1}
	failure := priceMonitorRatioObservation{}
	for _, key := range keys {
		callCtx, cancel := context.WithTimeout(ctx, p.timeout)
		billing, answered, err := p.run.fetchKeyBilling(callCtx, client, host, baseURL, key, priceMonitorSub2APIMaxRejectedPerHost)
		cancel()
		if !answered {
			if best.ok {
				return best
			}
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonWaiting}
		}
		if err != nil {
			reason := sub2apiBillingErrorReason(err)
			observed := priceMonitorRatioObservation{reason: reason}
			if sub2apiBillingRecognised(err) {
				observed.kind = priceMonitorUpstreamKindSub2API
			}
			switch reason {
			case priceMonitorRatioReasonRejected, priceMonitorRatioReasonSub2APINoGroup:
				if failure.reason == "" || observed.kind != "" {
					failure = observed
				}
				continue
			}
			return observed
		}
		ratio, peak, window := sub2apiWorstCaseRatio(billing)
		if ratio < 0 {
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}
		}
		if ratio > best.ratio {
			best = priceMonitorRatioObservation{
				ok:             true,
				ratio:          ratio,
				source:         priceMonitorRatioSourceSub2API,
				kind:           priceMonitorUpstreamKindSub2API,
				peakMultiplier: peak,
				peakWindow:     window,
			}
		}
	}
	if best.ok {
		return best
	}
	return failure
}

// ── 模型广场价格 ────────────────────────────────────────────────────────────

type sub2apiPlazaPricing struct {
	BillingMode     string            `json:"billing_mode"`
	InputPrice      *float64          `json:"input_price"`
	OutputPrice     *float64          `json:"output_price"`
	CacheWritePrice *float64          `json:"cache_write_price"`
	CacheReadPrice  *float64          `json:"cache_read_price"`
	PerRequestPrice *float64          `json:"per_request_price"`
	Intervals       []json.RawMessage `json:"intervals"`
}

type sub2apiPlazaGroup struct {
	RateMultiplier     float64 `json:"rate_multiplier"`
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
	// LongContextPricingEnabled 为假时分组只按首档价计费，模型的阶梯不生效。
	LongContextPricingEnabled bool `json:"long_context_pricing_enabled"`
	Models                    []struct {
		Name    string               `json:"name"`
		Pricing *sub2apiPlazaPricing `json:"pricing"`
	} `json:"models"`
}

// sub2apiNotDetectedPrefix 标记"上游看起来不是 sub2api"的取价失败。sub2api 是非 Sub2API
// 渠道的最后一个试探候选，这类失败不应覆盖前面候选的端点与原因（resolvePriceMonitorSources）。
const sub2apiNotDetectedPrefix = "not a sub2api model plaza: "

// fetchSub2APIPlazaPricing 取 sub2api 模型广场里该渠道密钥所在分组的价格（设计 §5）。
//
// 地址与密钥都取自数据库里的渠道，请求地址与渠道地址不一致就拒绝（loadPricingSourceChannel）。
// 先匿名取广场：广场没开或上游根本不是 sub2api 时就此结束，渠道密钥不发出去。
// 广场按分组列价格，再用渠道未禁用的密钥查倍率接口，按倍率在广场里找到它的分组。
func fetchSub2APIPlazaPricing(ctx context.Context, client *http.Client, upstream dto.UpstreamDTO) (map[string]any, error) {
	if upstream.ID == 0 {
		return nil, errors.New(sub2apiNotDetectedPrefix + "no channel to identify the key group")
	}
	channel, err := loadPricingSourceChannel(upstream)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+sub2apiModelPlazaPath, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New(sub2apiNotDetectedPrefix + "request failed")
	}
	defer service.DrainAndCloseResponseBody(res)
	body, err := io.ReadAll(io.LimitReader(res.Body, maxRatioConfigBytes))
	if err != nil {
		return nil, errors.New(sub2apiNotDetectedPrefix + "response could not be read")
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	decodeErr := common.Unmarshal(body, &envelope)
	if res.StatusCode == http.StatusNotFound && decodeErr == nil && envelope.Code == http.StatusNotFound && envelope.Message != "" {
		// sub2api 关闭广场时回 {code:404, message:"Model plaza is not enabled"}；
		// 别的网关的 404 不是这个结构，按"不是 sub2api"处理。
		return nil, errors.New(sub2apiPlazaDisabledError)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%sresponded %s", sub2apiNotDetectedPrefix, res.Status)
	}
	if decodeErr != nil || envelope.Code != 0 || len(envelope.Data) == 0 {
		return nil, errors.New(sub2apiNotDetectedPrefix + "response is not recognised")
	}

	keys := priceMonitorEnabledKeys(channel)
	if len(keys) > priceMonitorOfficialMaxKeys {
		keys = keys[:priceMonitorOfficialMaxKeys]
	}
	run := priceMonitorSub2APIRunFrom(ctx)
	if run == nil {
		run = newPriceMonitorSub2APIRun()
	}
	host := priceMonitorRatioHost(channel)
	lastReason := priceMonitorRatioReasonNoSource
	for _, key := range keys {
		// 取价只用限额里不属于倍率步骤的那部分（见 priceMonitorSub2APIRatioReserve）。
		billing, answered, err := run.fetchKeyBilling(ctx, client, host, baseURL, key, priceMonitorSub2APIPriceStepLimit)
		if !answered {
			// 这个主机近 60 秒内被拒的密钥已到取价可用的上限（或刚收到 429），不再发密钥，下一轮再取。
			lastReason = priceMonitorRatioReasonWaiting
			break
		}
		if err == nil {
			return convertSub2APIPlazaToRatioData(envelope.Data, billing)
		}
		lastReason = sub2apiBillingErrorReason(err)
		// 已删除或未分配分组的密钥换下一把；其他失败对所有密钥都一样，不再重试。
		if lastReason != priceMonitorRatioReasonRejected && lastReason != priceMonitorRatioReasonSub2APINoGroup {
			break
		}
	}
	return nil, fmt.Errorf("sub2api key billing: %s", lastReason)
}

// convertSub2APIPlazaToRatioData 把模型广场里与密钥分组匹配的价格换算成本站价格表结构（设计 §5.3）。
//
// 广场价格是每 token 美元、不含分组倍率，对应本站"上游列表价"；实测成本再乘倍率接口的倍率。
// 分组按倍率与高峰设置匹配；多个分组都匹配时每个字段取最高价（按最坏情况）。
// 不产出价格的情形：分组开了长上下文阶梯且模型有阶梯（任一候选分组如此即跳过）、
// 图片/视频按张计价、没有输入价的按 token 模型。
func convertSub2APIPlazaToRatioData(data []byte, billing sub2apiKeyBilling) (map[string]any, error) {
	var plaza struct {
		Groups []sub2apiPlazaGroup `json:"groups"`
	}
	if err := common.Unmarshal(data, &plaza); err != nil {
		return nil, fmt.Errorf("failed to decode model plaza: %w", err)
	}
	billingPeak := 0.0
	if billing.PeakRateMultiplier != nil {
		billingPeak = *billing.PeakRateMultiplier
	}
	type modelPrices struct {
		input, output, cacheRead, cacheWrite, perRequest *float64
	}
	higher := func(current, next *float64) *float64 {
		if next == nil || *next < 0 {
			return current
		}
		if current == nil || *next > *current {
			return floatPointer(*next)
		}
		return current
	}
	merged := map[string]*modelPrices{}
	tiered := map[string]bool{}
	matched := false
	for _, group := range plaza.Groups {
		if !nearlyEqual(group.RateMultiplier, billing.GroupRateMultiplier) || group.PeakRateEnabled != billing.PeakRateEnabled {
			continue
		}
		if group.PeakRateEnabled && !nearlyEqual(group.PeakRateMultiplier, billingPeak) {
			continue
		}
		matched = true
		for _, entry := range group.Models {
			pricing := entry.Pricing
			if entry.Name == "" || pricing == nil {
				continue
			}
			prices := merged[entry.Name]
			if prices == nil {
				prices = &modelPrices{}
			}
			switch pricing.BillingMode {
			case sub2apiBillingModeToken, "":
				if group.LongContextPricingEnabled && len(pricing.Intervals) > 0 {
					tiered[entry.Name] = true
					continue
				}
				prices.input = higher(prices.input, pricing.InputPrice)
				prices.output = higher(prices.output, pricing.OutputPrice)
				prices.cacheRead = higher(prices.cacheRead, pricing.CacheReadPrice)
				prices.cacheWrite = higher(prices.cacheWrite, pricing.CacheWritePrice)
			case sub2apiBillingModePerRequest:
				prices.perRequest = higher(prices.perRequest, pricing.PerRequestPrice)
			default:
				continue
			}
			merged[entry.Name] = prices
		}
	}
	if !matched {
		return nil, errors.New(sub2apiGroupUnknownError)
	}

	modelRatio := map[string]any{}
	completionRatio := map[string]any{}
	cacheRatio := map[string]any{}
	createCacheRatio := map[string]any{}
	modelPrice := map[string]any{}
	for name, prices := range merged {
		if tiered[name] {
			continue
		}
		if prices.perRequest != nil {
			modelPrice[name] = *prices.perRequest
			continue
		}
		if prices.input == nil {
			continue
		}
		input := *prices.input
		if input == 0 {
			if prices.output == nil || *prices.output == 0 {
				modelRatio[name] = 0.0
			}
			continue
		}
		modelRatio[name] = roundRatioValue(input * 1000 * ratio_setting.USD)
		if prices.output != nil {
			completionRatio[name] = roundRatioValue(*prices.output / input)
		}
		if prices.cacheRead != nil {
			cacheRatio[name] = roundRatioValue(*prices.cacheRead / input)
		}
		if prices.cacheWrite != nil {
			createCacheRatio[name] = roundRatioValue(*prices.cacheWrite / input)
		}
	}
	converted := map[string]any{}
	for field, values := range map[string]map[string]any{
		"model_ratio":        modelRatio,
		"completion_ratio":   completionRatio,
		"cache_ratio":        cacheRatio,
		"create_cache_ratio": createCacheRatio,
		"model_price":        modelPrice,
	} {
		if len(values) > 0 {
			converted[field] = values
		}
	}
	if !pricingPayloadHasPrices(converted) {
		return nil, errors.New(emptyPricingPayloadError)
	}
	return converted, nil
}
