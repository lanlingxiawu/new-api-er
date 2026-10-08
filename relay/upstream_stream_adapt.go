package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// shouldAdaptUpstreamStream decides whether a non-stream client request should
// talk to upstream in streaming mode and be re-assembled before it is returned.
//
// The point is billing, not latency: a non-stream upstream call produces its
// first byte only once generation has finished, which is exactly when
// non_stream_response_timeout fires. Cancelling there leaves zero evidence of
// output usage, so the request can only be refunded while upstream still bills
// us. Streaming upstream means a timeout has real partial usage to settle.
//
// Every condition below is a reason the adaptation would either be pointless or
// unsafe, so all of them must hold. Notably the request must actually be able
// to time out: a request that never can has nothing to lose, and changing how
// it talks to upstream would be pure risk. Being owned by the timeout subsystem
// is not enough — a user who set both non-stream limits to -1 (unlimited) is
// owned with no timer at all — so this asks for a pending deadline.
func shouldAdaptUpstreamStream(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) bool {
	if c == nil || info == nil || request == nil || info.ChannelMeta == nil {
		return false
	}
	if info.IsStream {
		return false
	}
	if !service.NonStreamTimeoutChargesUsage(c) {
		return false
	}
	// Evaluated at the start of an attempt, after BeginRelayAttempt restarted
	// the response window, so a configured response limit is always pending
	// here; the total limit counts whether or not it fires first.
	if _, ok := service.RelayRequestDeadline(c); !ok {
		return false
	}
	if info.RelayFormat != types.RelayFormatOpenAI || info.RelayMode != relayconstant.RelayModeChatCompletions {
		return false
	}
	if !info.SupportStreamOptions {
		return false
	}
	// RelayFormat is the CLIENT's protocol; it says nothing about what the
	// upstream speaks. streamSupportedChannels includes Anthropic, Gemini, AWS
	// and others whose native SSE is not chat.completion.chunk — the buffered
	// handler would parse those frames into empty chunks and silently drop the
	// model's output. Only adapt when the upstream itself is OpenAI-wire.
	if info.ApiType != constant.APITypeOpenAI {
		return false
	}
	// Chat Completions routed through the Responses API is answered by that
	// path's own buffered handler, which never settles a timeout: the stream
	// flag would only change how upstream is called, not what the user is billed.
	if service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		return false
	}
	return requestSupportsUpstreamStream(request, info)
}

// requestSupportsUpstreamStream rejects requests whose streaming semantics
// would make a re-assembled response differ from a real non-stream one.
func requestSupportsUpstreamStream(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) bool {
	// Pass-through sends the client's original bytes upstream; we must not
	// rewrite a body we promised to forward verbatim.
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return false
	}
	// n > 1 fans out into multiple choices whose stream deltas interleave by
	// index; re-assembly is not a straight concatenation.
	if request.N != nil && *request.N != 1 {
		return false
	}
	// logprobs are reported per chunk with a different shape than the
	// non-stream response carries.
	if request.LogProbs != nil && *request.LogProbs {
		return false
	}
	if request.TopLogProbs != nil {
		return false
	}
	// Audio output cannot survive the round trip: streamed audio is pcm16-only,
	// so a wav/mp3 request may be rejected outright once it streams, and the
	// aggregator does not assemble audio deltas at all.
	if requestsAudioOutput(request) {
		return false
	}
	// The legacy functions API streams delta.function_call, which the stream
	// DTO does not model; the client would get finish_reason "function_call"
	// with no function to call.
	if jsonFieldSet(request.Functions) || jsonFieldSet(request.FunctionCall) {
		return false
	}
	return true
}

// requestsAudioOutput reports an audio-output request. An audio parameter alone
// is enough; a modalities value that cannot be read is treated as audio, since
// adapting a request we cannot classify is the unsafe direction.
func requestsAudioOutput(request *dto.GeneralOpenAIRequest) bool {
	if jsonFieldSet(request.Audio) {
		return true
	}
	if !jsonFieldSet(request.Modalities) {
		return false
	}
	var modalities []string
	if err := common.Unmarshal(request.Modalities, &modalities); err != nil {
		return true
	}
	for _, modality := range modalities {
		if modality == "audio" {
			return true
		}
	}
	return false
}

// jsonFieldSet reports a raw JSON field that is present and not null.
func jsonFieldSet(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// applyUpstreamStreamAdaptation rewrites the outbound request to stream and asks
// for usage in the final chunk. The client-facing mode is untouched: info.IsStream
// stays false so the response is still assembled and returned as one JSON body.
func applyUpstreamStreamAdaptation(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if request == nil || info == nil {
		return
	}
	// Must happen before the upstream call: the first SSE chunk would otherwise
	// stop the response timer and void the user's non-stream deadline.
	service.HoldRelayResponseTimer(c)
	request.Stream = common.GetPointer(true)
	// Without include_usage the stream carries no usage at all and every
	// settlement - timeout or not - would fall back to local estimation, which
	// defeats the purpose of adapting in the first place.
	request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	info.UpstreamStreamAdapted = true
	info.ShouldIncludeUsage = true
}

// adaptedRejectionPreviewBytes bounds how much of upstream's rejection is read
// for the log; the body is discarded either way.
const adaptedRejectionPreviewBytes = 4 << 10

// fallBackFromRejectedAdaptation re-sends an adapted request as the plain
// non-stream call it replaced when upstream fails it before any output.
//
// The adaptation must never change a request's outcome, and the stream flag
// can change which code path an upstream takes:
//   - some upstreams refuse stream:true outright — OpenAI only lets verified
//     organizations stream certain models (400);
//   - a relay upstream can fail differently on its stream path — a new-api
//     upstream answered an unreachable image URL with 400 on the plain call
//     (the provider rejects it) but 500 on the stream call (its own token
//     counting downloads the image and fails), which our retry loop then
//     retried as a server error.
//
// So any failure the stream path may have caused gets one immediate plain
// re-send on the same channel. Failures are not billed, and if the error had
// nothing to do with streaming the re-send returns it again, so the user sees
// exactly what the unadapted request would have shown.
//
// The re-sent body is returned for the caller to close once the response is
// done, like the body of the original call: the transport may still be sending
// it after the response arrives.
func fallBackFromRejectedAdaptation(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, resp *http.Response, adaptedBody common.BodyStorage) (*http.Response, io.Closer, *types.NewAPIError) {
	if info == nil || !info.UpstreamStreamAdapted || resp == nil || adaptedBody == nil || !adaptedFailureWorthPlainResend(resp.StatusCode) {
		return resp, nil, nil
	}
	if allowed, reason := service.ReserveRelayFallbackCall(c); !allowed {
		logger.LogWarn(c, "skip adapted-stream plain fallback: "+reason)
		return resp, nil, nil
	}
	sent, err := adaptedBody.Bytes()
	if err != nil {
		return resp, nil, nil
	}
	plainBody, err := withoutUpstreamStream(sent)
	if err != nil {
		// Cannot rebuild the plain request safely; surface the original error.
		return resp, nil, nil
	}
	rejection, _ := io.ReadAll(io.LimitReader(resp.Body, adaptedRejectionPreviewBytes))
	service.CloseResponseBodyGracefully(resp)
	logger.LogWarn(c, fmt.Sprintf("upstream failed the adapted stream request with %d; re-sending as non-stream: ", resp.StatusCode)+
		common.LocalLogPreview(string(rejection)))

	info.UpstreamStreamAdapted = false
	// A new upstream call on the same channel, like a retry: it gets a fresh
	// response window, and restarting also ends the hold, so this plain call's
	// response stops the timer as usual.
	service.RestartRelayResponseTimeout(c)

	body, closer, err := relaycommon.NewOutboundJSONBody(plainBody)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	plainResp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		_ = closer.Close()
		return nil, nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := plainResp.(*http.Response)
	if !ok || httpResp == nil {
		_ = closer.Close()
		return nil, nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	return httpResp, closer, nil
}

// adaptedFailureWorthPlainResend reports whether an adapted call's error status
// could depend on the stream flag: a client error, or a plain 500 from the
// upstream's own stream-path code. Auth, upstream balance (402), payload size
// (413, and re-sending would upload the large body again), rate limiting,
// timeouts and overload (502/503/529 and the like) cannot. The re-send does not
// consume the per-group retry quota, but ReserveRelayFallbackCall counts it
// against the request-wide upstream-call and time budgets.
func adaptedFailureWorthPlainResend(status int) bool {
	if status == http.StatusInternalServerError {
		return true
	}
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		return false
	}
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusRequestTimeout,
		http.StatusRequestEntityTooLarge, http.StatusTooManyRequests:
		return false
	}
	return true
}

// abandonUpstreamStreamAdaptation handles an upstream that ignored stream:true
// and answered with a plain body: the request is plain non-stream after all.
// The hold put on the response timer for the adapted call ends here with the
// timer stopped — what the plain call's first byte would have done, and did
// not because of the hold. Left held, the timer runs on through reading the
// body and settlement and can turn a completed request into a 504.
func abandonUpstreamStreamAdaptation(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil || !info.UpstreamStreamAdapted {
		return
	}
	info.UpstreamStreamAdapted = false
	service.ReleaseRelayResponseTimer(c)
}

// withoutUpstreamStream removes the two fields the adaptation added, giving the
// body of the plain non-stream request it replaced.
func withoutUpstreamStream(body []byte) ([]byte, error) {
	plain, err := sjson.DeleteBytes(body, "stream")
	if err != nil {
		return nil, err
	}
	return sjson.DeleteBytes(plain, "stream_options")
}
