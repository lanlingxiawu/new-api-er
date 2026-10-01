package service

import (
	"net/http/httptrace"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// Cost bearing when our own deadline ends a request
// (docs/design/relay-timeout-cost-bearing.md): the "input" timeout billing mode
// and the admin-only record of what the platform absorbed.
//
// Everything here runs only on the timeout failure path; a request that does
// not hit our deadline never reaches it.

const (
	// relayTimeoutInfoKey holds the RelayInfo of a request whose per-user
	// timeout was started, so the channel error log (which only has the gin
	// context) can price what a refund leaves to the platform.
	relayTimeoutInfoKey = "relay_timeout_relay_info"
	// relayTimeoutCostKey holds the pending *relayTimeoutCost until the
	// request's settlement log consumes it.
	relayTimeoutCostKey = "relay_timeout_cost"

	relayTimeoutKindNonStream = "non_stream"
	relayTimeoutKindStream    = "stream"

	// relayTimeoutAbsorbedKey is the admin_info key of the record.
	relayTimeoutAbsorbedKey = "timeout_absorbed"
)

// relayTimeoutCost is what the settlement log of a timed-out request needs.
type relayTimeoutCost struct {
	// absorbed is admin_info.timeout_absorbed; nil when nothing is recorded.
	absorbed map[string]any
	// inputOnly: the request is settled on its input only, so the consume log
	// says so instead of the plain timeout note.
	inputOnly bool
	// refundOnSettleFailure: a non-stream input settlement whose funding step
	// fails is refunded instead (the controller skipped its refund).
	refundOnSettleFailure bool
}

func relayTimeoutCostFrom(c *gin.Context) *relayTimeoutCost {
	if c == nil {
		return nil
	}
	value, ok := c.Get(relayTimeoutCostKey)
	if !ok {
		return nil
	}
	cost, _ := value.(*relayTimeoutCost)
	return cost
}

// takeRelayTimeoutCost returns the pending cost once; a request has one
// settlement log.
func takeRelayTimeoutCost(c *gin.Context) *relayTimeoutCost {
	cost := relayTimeoutCostFrom(c)
	if cost != nil {
		c.Set(relayTimeoutCostKey, (*relayTimeoutCost)(nil))
	}
	return cost
}

// BindRelayTimeoutInfo remembers the request's RelayInfo once its per-user
// timeout is running; only managed requests can end in our timeout.
func BindRelayTimeoutInfo(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || info == nil || !IsRelayTimeoutManaged(c) {
		return
	}
	c.Set(relayTimeoutInfoKey, info)
}

func boundRelayTimeoutInfo(c *gin.Context) *relaycommon.RelayInfo {
	value, ok := c.Get(relayTimeoutInfoKey)
	if !ok {
		return nil
	}
	info, _ := value.(*relaycommon.RelayInfo)
	return info
}

// relayTimeoutPerCall reports a model priced per call: it has no input share,
// so "input only" cannot be charged and the input mode refunds instead.
func relayTimeoutPerCall(info *relaycommon.RelayInfo) bool {
	return info.PriceData.UsePrice
}

// StreamTimeoutSettlement decides how a stream cut by our own deadline without
// any effective delivery is billed, by the user's timeout billing mode:
//   - charge: confirmed usage, else estimated input plus received output (source only);
//   - input: confirmed input, else estimated input; output 0 (inputOnly);
//   - refund, and input on a per-call priced model or for a request that never
//     fully reached the upstream: not billed ("").
//
// confirmed tells whether upstream usage evidence exists.
func StreamTimeoutSettlement(c *gin.Context, info *relaycommon.RelayInfo, confirmed bool) (source string, inputOnly bool) {
	switch RelayTimeoutBillingMode(c) {
	case NonStreamTimeoutBillingCharge:
	case NonStreamTimeoutBillingInput:
		// A per-call price has no input share; a request that never fully
		// reached the upstream cost it nothing.
		if info == nil || relayTimeoutPerCall(info) || !relayUpstreamRequestSent(c) {
			return "", false
		}
		inputOnly = true
	default:
		return "", false
	}
	if confirmed {
		return "upstream", inputOnly
	}
	return "estimated", inputOnly
}

// StreamInputOnlyEvidence copies confirmed stream usage evidence without its
// output-side counts, so a usage built from it charges input (and confirmed
// cache) only.
func StreamInputOnlyEvidence(evidence map[string]int) map[string]int {
	out := make(map[string]int, len(evidence))
	for key, value := range evidence {
		if key == "output_tokens" || key == "reasoning_tokens" || key == "image_count" || strings.HasPrefix(key, "output_") {
			continue
		}
		out[key] = value
	}
	return out
}

// estimatedInputUsage is the pre-consume input estimate as a usage; an
// estimate never includes cache.
func estimatedInputUsage(tokens int) *dto.Usage {
	u := &dto.Usage{PromptTokens: tokens, TotalTokens: tokens}
	u.PromptTokensDetails.TextTokens = tokens
	return u
}

// relayTimeoutUsageQuota prices usage the way text settlement does (ratios,
// cache ratios, per-call price, tiered expression), without tool surcharges
// and without touching the request's billing state. Only used for the
// absorbed-cost record.
func relayTimeoutUsageQuota(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) int {
	if usage == nil || info == nil {
		return 0
	}
	billing := effectiveBillingUsage(usage)
	if snap := info.TieredBillingSnapshot; snap != nil && snap.BillingMode == "tiered_expr" {
		isClaude := usageSemanticFromUsage(info, billing) == "anthropic"
		request := billingexpr.RequestInput{}
		if info.BillingRequestInput != nil {
			request = *info.BillingRequestInput
		}
		result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, BuildTieredTokenParams(billing, isClaude, billingexpr.UsedVars(snap.ExprString)), request)
		if err != nil {
			return 0
		}
		return result.ActualQuotaAfterGroup
	}
	savedClamp := info.QuotaClamp
	summary := calculateTextQuotaSummary(c, info, billing)
	info.QuotaClamp = savedClamp
	return max(0, summary.Quota-int(summary.ToolCallSurchargeQuota.Round(0).IntPart()))
}

// relayConfirmedToolSurchargeQuota is the tool surcharge reported by the
// upstream usage for this request (web search counts and the like), in quota.
// The web_search_preview fee is inferred from the model name, not reported, so
// it is left out. Only used for the absorbed-cost record.
func relayConfirmedToolSurchargeQuota(c *gin.Context, info *relaycommon.RelayInfo) int {
	summary := textQuotaSummary{ModelName: info.OriginModelName, GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio}
	calculateTextToolCallSurcharge(c, info, &summary)
	total := decimal.Zero
	for _, item := range summary.ToolSurchargeItems {
		if item.Name == dto.BuildInToolWebSearchPreview {
			continue
		}
		total = total.Add(decimal.NewFromFloat(item.Price).
			Mul(decimal.NewFromInt(int64(item.Count))).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(summary.GroupRatio)).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	}
	return int(total.Round(0).IntPart())
}

// relayTimeoutAbsorbedRecord builds admin_info.timeout_absorbed. requestSent
// false marks a request that never fully reached the upstream.
func relayTimeoutAbsorbedRecord(mode, kind string, input int, inputEstimated bool, receivedOutput int, absorbed int, perCall, requestSent bool) map[string]any {
	record := map[string]any{
		"mode":                   mode,
		"kind":                   kind,
		"input_tokens":           input,
		"input_estimated":        inputEstimated,
		"received_output_tokens": receivedOutput,
		"absorbed_quota_min":     absorbed,
	}
	if perCall {
		record["per_call"] = true
	}
	if !requestSent {
		record["request_sent"] = false
	}
	return record
}

// relaySettlementDoneKey marks a request whose usage was already settled and
// logged (FinalizeConsumptionSettlement, EnqueueConsumeLogWithCost). A deadline
// that fires after a handler settled (between reading the upstream body and
// writing it to the client) still ends the request with relay_timeout; the
// timeout paths must not settle or record it a second time.
const relaySettlementDoneKey = "relay_settlement_done"

func markRelaySettlementDone(c *gin.Context) {
	if c != nil {
		c.Set(relaySettlementDoneKey, true)
	}
}

// relayUpstreamSentKey holds the *relayUpstreamSent of a managed request.
const relayUpstreamSentKey = "relay_timeout_upstream_sent"

// relayUpstreamExchange is one upstream HTTP exchange; its transport callback
// sets wrote once the whole request (headers and body) was written.
type relayUpstreamExchange struct{ wrote atomic.Bool }

// relayUpstreamSent tracks the current attempt's main upstream exchange. The
// transport callback holds only its exchange, never the pooled gin context.
type relayUpstreamSent struct {
	current atomic.Pointer[relayUpstreamExchange]
}

func relayUpstreamSentState(c *gin.Context, create bool) *relayUpstreamSent {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(relayUpstreamSentKey); ok {
		if state, _ := value.(*relayUpstreamSent); state != nil {
			return state
		}
	}
	if !create {
		return nil
	}
	state := &relayUpstreamSent{}
	c.Set(relayUpstreamSentKey, state)
	return state
}

// relayUpstreamWroteRequestTrace starts tracking a managed request's main
// upstream exchange; nil when the request is not managed.
func relayUpstreamWroteRequestTrace(c *gin.Context) func(httptrace.WroteRequestInfo) {
	if !IsRelayTimeoutManaged(c) {
		return nil
	}
	exchange := &relayUpstreamExchange{}
	relayUpstreamSentState(c, true).current.Store(exchange)
	return func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			exchange.wrote.Store(true)
		}
	}
}

// resetRelayUpstreamSent forgets the previous attempt's exchange.
func resetRelayUpstreamSent(c *gin.Context) {
	if state := relayUpstreamSentState(c, false); state != nil {
		state.current.Store(nil)
	}
}

// relayUpstreamRequestSent reports whether the last attempt's main upstream
// request reached the upstream in full. Unknown (no HTTP exchange this attempt,
// such as SDK or WebSocket channels) counts as sent.
func relayUpstreamRequestSent(c *gin.Context) bool {
	state := relayUpstreamSentState(c, false)
	if state == nil {
		return true
	}
	exchange := state.current.Load()
	return exchange == nil || exchange.wrote.Load()
}

// nonStreamTimeoutEligible reports a request ended by our own deadline before
// anything was written, that nobody settled yet: non-stream requests and
// unified streams still waiting for response headers use this fallback.
// Active streams settle through their own lifecycle, and so does an adapted
// (charge) request once its buffered handler ran (it settles, which sets the
// settled mark); an adapted request cut before response headers lands here. A
// handler that settled before the deadline fired owns the request. The last
// attempt itself must have been cut by the deadline: when the deadline fires
// between attempts, the last upstream call had already answered with an error.
func nonStreamTimeoutEligible(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) bool {
	return c != nil && info != nil && apiErr != nil &&
		apiErr.GetErrorCode() == types.ErrorCodeRelayTimeout &&
		IsRelayRequestTimeout(c) &&
		// Before successful response headers the stream session has not taken
		// ownership. The controller must settle this timeout just as it does
		// an unadapted non-stream request, using the recorded write evidence.
		(!common.GetContextKeyBool(c, constant.ContextKeyIsStream) ||
			(info.StreamSession != nil && !info.StreamSession.Active())) &&
		info.StreamResult == nil &&
		!c.Writer.Written() && !c.GetBool(relaySettlementDoneKey) &&
		info.LastError != nil && info.LastError.GetErrorCode() == types.ErrorCodeRelayTimeout
}

// nonStreamTimeoutPlan decides a timed-out non-stream request: settle its
// input, or refund it, and the record for admin_info.timeout_absorbed.
//   - input and charge settle the estimated input (an unadapted charge request
//     cannot observe any output either, so it is billed exactly like input);
//   - refund, a per-call priced model (its whole price is not "input"), an
//     input that prices to 0 (free model) and a request that never fully
//     reached the upstream are refunded.
//
// A settled request absorbs 0 measurable cost (its output is not observable);
// a refunded one absorbs its input price, or nothing if it was never sent.
func nonStreamTimeoutPlan(c *gin.Context, info *relaycommon.RelayInfo) (settle bool, record map[string]any) {
	mode := RelayTimeoutBillingMode(c)
	input := info.GetEstimatePromptTokens()
	perCall := relayTimeoutPerCall(info)
	sent := relayUpstreamRequestSent(c)
	inputQuota := relayTimeoutUsageQuota(c, info, estimatedInputUsage(input))
	settle = (mode == NonStreamTimeoutBillingCharge || mode == NonStreamTimeoutBillingInput) &&
		!perCall && sent && inputQuota > 0
	absorbed := 0
	if !settle && sent {
		absorbed = inputQuota
	}
	kind := relayTimeoutKindNonStream
	if common.GetContextKeyBool(c, constant.ContextKeyIsStream) {
		kind = relayTimeoutKindStream
	}
	return settle, relayTimeoutAbsorbedRecord(mode, kind, input, true, 0, absorbed, perCall, sent)
}

// SettleRelayTimeoutInputIfNeeded settles a non-stream or pre-header stream request that our own
// deadline ended (error code relay_timeout, never a client disconnect, an
// upstream error or a local error) on its input only when the user's timeout
// billing is input or charge: the pre-consume input estimate at the model's
// input price, output 0, no tool surcharges. It returns true when it took over
// the pre-consumed quota, so the caller must not refund. A failed settlement
// falls back to a refund here.
func SettleRelayTimeoutInputIfNeeded(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) bool {
	if !nonStreamTimeoutEligible(c, info, apiErr) {
		return false
	}
	settle, record := nonStreamTimeoutPlan(c, info)
	if !settle {
		return false
	}
	c.Set(relayTimeoutCostKey, &relayTimeoutCost{absorbed: record, inputOnly: true, refundOnSettleFailure: true})
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	PostTextConsumeQuota(c, info, estimatedInputUsage(info.GetEstimatePromptTokens()), []string{MessageWithCurrentRequestId(c, relayTimeoutInputOnlyNote(c))})
	return true
}

// appendRelayTimeoutAbsorbedToErrorLog adds timeout_absorbed to the channel
// error log of a non-stream request our deadline ended and that is refunded.
// Requests settled on input record it in their consume log instead, so each
// request is recorded once.
func appendRelayTimeoutAbsorbedToErrorLog(c *gin.Context, other map[string]interface{}, err error) {
	apiErr, ok := err.(*types.NewAPIError)
	if !ok || other == nil {
		return
	}
	info := boundRelayTimeoutInfo(c)
	if !nonStreamTimeoutEligible(c, info, apiErr) {
		return
	}
	if settle, record := nonStreamTimeoutPlan(c, info); !settle {
		streamLogAdminInfo(other)[relayTimeoutAbsorbedKey] = record
	}
}

// NoteStreamTimeoutAbsorbed records what the platform bears for a stream our
// deadline cut before any effective delivery. input is the input-only usage
// (confirmed where upstream reported it, else the estimate; inputEstimated
// tells which) and receivedOutput the output received but never delivered:
//   - refund, and input on a per-call priced model: input plus received output;
//   - input: the received output;
//   - charge: nothing (the user pays what was received);
//
// plus, in refund and input, the tool surcharges the upstream reported (input
// only never charges them). A request that never fully reached the upstream
// absorbs nothing. The record and the input-only flag reach the request's
// settlement log.
func NoteStreamTimeoutAbsorbed(c *gin.Context, info *relaycommon.RelayInfo, input *dto.Usage, inputEstimated bool, receivedOutput int, inputOnly bool) {
	if c == nil || info == nil || input == nil {
		return
	}
	mode := RelayTimeoutBillingMode(c)
	if mode == NonStreamTimeoutBillingCharge {
		return
	}
	sent := relayUpstreamRequestSent(c)
	absorbed := 0
	if sent {
		withOutput := *input
		withOutput.BillingUsage = nil
		withOutput.CompletionTokens = receivedOutput
		withOutput.CompletionTokenDetails.TextTokens = receivedOutput
		withOutput.TotalTokens = withOutput.PromptTokens + receivedOutput
		inputUsage := *input
		inputUsage.BillingUsage = nil
		absorbed = relayTimeoutUsageQuota(c, info, &withOutput)
		if inputOnly {
			absorbed = max(0, absorbed-relayTimeoutUsageQuota(c, info, &inputUsage))
		}
		absorbed += relayConfirmedToolSurchargeQuota(c, info)
	}
	c.Set(relayTimeoutCostKey, &relayTimeoutCost{
		absorbed:  relayTimeoutAbsorbedRecord(mode, relayTimeoutKindStream, effectiveBillingUsage(input).PromptTokens, inputEstimated, receivedOutput, absorbed, relayTimeoutPerCall(info), sent),
		inputOnly: inputOnly,
	})
}

// relayTimeoutInputOnlySettling reports that the pending settlement charges
// input only, so text settlement leaves tool surcharges out.
func relayTimeoutInputOnlySettling(c *gin.Context) bool {
	cost := relayTimeoutCostFrom(c)
	return cost != nil && cost.inputOnly
}

// relayTimeoutInputOnlyNote is our consume-log text for an input-only
// settlement, in English like the rest of the stored content.
func relayTimeoutInputOnlyNote(c *gin.Context) string {
	return i18n.Translate(i18n.LangEn, i18n.MsgRelayTimeoutInputOnly, map[string]any{"Seconds": RelayRequestTimeoutSeconds(c)})
}

// settleRelayTimeoutInputQuota settles a pending non-stream input settlement.
// settled reports that it did (the caller must not settle again); ok=false
// means the settlement failed before funding was committed and the
// pre-consumed quota was refunded, so no consume log may be written.
func settleRelayTimeoutInputQuota(c *gin.Context, info *relaycommon.RelayInfo, cost *relayTimeoutCost, quota int) (settled, ok bool) {
	if cost == nil || !cost.refundOnSettleFailure {
		return false, true
	}
	err := SettleBilling(c, info, quota)
	if err == nil {
		return true, true
	}
	committed := false
	if session, isSession := info.Billing.(interface{ FundingCommitted() bool }); isSession {
		committed = session.FundingCommitted()
	}
	if committed || info.Billing == nil {
		logger.LogError(c, "relay timeout input settlement adjusted funding but failed afterwards: "+err.Error())
		return true, true
	}
	logger.LogError(c, "relay timeout input settlement failed, refunding instead: "+err.Error())
	info.Billing.Refund(c)
	return true, false
}

// isRelayTimeoutInputSettlement reports a pending non-stream input settlement
// (SettleRelayTimeoutInputIfNeeded); read before the settlement consumes it.
func isRelayTimeoutInputSettlement(c *gin.Context) bool {
	cost := relayTimeoutCostFrom(c)
	return cost != nil && cost.refundOnSettleFailure
}

// AdaptedTimeoutWithoutOutput adjusts the settlement of a stream-adapted
// (charge) non-stream request that our deadline cut before it received any
// output. charge must never bill less than input would for the same request,
// so the usage is at least the input: upstream-confirmed input when a usage
// frame arrived, else the pre-consume estimate; output 0 and no tool
// surcharges. A per-call priced model stays unbilled, as in the input mode.
// It returns the usage to settle and the consume-log note, and records
// admin_info.timeout_absorbed (the output is not observable: 0; a per-call
// price is absorbed in full).
func AdaptedTimeoutWithoutOutput(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) (*dto.Usage, []string) {
	if c == nil || info == nil {
		return usage, nil
	}
	estimate := info.GetEstimatePromptTokens()
	perCall := relayTimeoutPerCall(info)
	inputEstimated := common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens)
	if perCall {
		absorbed := relayTimeoutUsageQuota(c, info, estimatedInputUsage(estimate))
		c.Set(relayTimeoutCostKey, &relayTimeoutCost{
			absorbed: relayTimeoutAbsorbedRecord(NonStreamTimeoutBillingCharge, relayTimeoutKindNonStream, estimate, true, 0, absorbed, true, true),
		})
		return &dto.Usage{}, nil
	}
	if usage == nil || usage.PromptTokens <= 0 {
		usage = estimatedInputUsage(estimate)
		inputEstimated = true
	}
	if relayTimeoutUsageQuota(c, info, usage) == 0 {
		// Free model: nothing to bill, settle empty as before (no usage counted).
		c.Set(relayTimeoutCostKey, &relayTimeoutCost{
			absorbed: relayTimeoutAbsorbedRecord(NonStreamTimeoutBillingCharge, relayTimeoutKindNonStream, usage.PromptTokens, inputEstimated, 0, 0, false, true),
		})
		return &dto.Usage{}, nil
	}
	if inputEstimated {
		common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	}
	inputOnly := usage.CompletionTokens == 0
	c.Set(relayTimeoutCostKey, &relayTimeoutCost{
		absorbed:  relayTimeoutAbsorbedRecord(NonStreamTimeoutBillingCharge, relayTimeoutKindNonStream, usage.PromptTokens, inputEstimated, 0, 0, false, true),
		inputOnly: inputOnly,
	})
	if !inputOnly {
		return usage, nil
	}
	return usage, []string{MessageWithCurrentRequestId(c, relayTimeoutInputOnlyNote(c))}
}
