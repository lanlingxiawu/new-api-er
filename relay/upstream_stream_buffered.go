package relay

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// adaptedScanBufPool recycles the scanner's initial 64 KiB buffer. Allocating
// it per response was the largest single allocation of a short adapted
// response. Only the initial buffer returns to the pool: one the scanner grew
// past it is left to the GC, so the pool never pins an oversized buffer.
var adaptedScanBufPool = sync.Pool{New: func() any {
	buf := make([]byte, helper.InitialScannerBufferSize)
	return &buf
}}

// adaptedScanMaxBytes is the per-line ceiling helper.NewStreamScanner applies
// to every relay stream. The scanner is built here rather than through helper
// because that constructor allocates its own initial buffer and is upstream
// code; keep the two rules identical.
func adaptedScanMaxBytes() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return helper.DefaultMaxScannerBufferSize
}

// settleAdaptedTimeout is a seam: the timeout branch settles real quota, which
// tests in this package cannot reach without a database.
var settleAdaptedTimeout = service.PostTextConsumeQuota

// handleAdaptedUpstreamStream consumes an SSE response for a client that asked
// for a non-stream completion, and returns one assembled JSON body.
//
// On the user's deadline it does not refund: it settles whatever upstream
// actually produced and returns that output as an incomplete completion; with
// no output yet it writes the same 504 the ordinary non-stream path would.
// That is the entire purpose of the adaptation — a plain non-stream call gives
// us nothing to settle, because it emits its first byte only once generation is
// already complete and already billed to us.
func handleAdaptedUpstreamStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	// How the upstream stream ended, for the consume log's stream_status (the
	// client only sees finish_reason). Reasons follow the native stream path.
	status := relaycommon.NewStreamStatus()
	info.StreamStatus = status

	aggregator := newChatStreamAggregator(info.UpstreamModelName)
	completed := false
	var parseErr error
	var streamErr *types.NewAPIError
	// One chunk is decoded into per frame; decodeStreamChunk hands the
	// aggregator only fresh pointers, so reusing it across frames is safe.
	var chunk dto.ChatCompletionsStreamResponse
	// The last decoded frame, for the channel-specific usage fields the plain
	// stream handler reads from it (llama.cpp's timings.cache_n).
	var lastFrame string

	scanBuf := adaptedScanBufPool.Get().(*[]byte)
	defer adaptedScanBufPool.Put(scanBuf)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(*scanBuf, adaptedScanMaxBytes())
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		data, ok := adaptedStreamPayload(scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			completed = true
			status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			break
		}
		// An in-band error frame deserializes cleanly into an all-zero chunk
		// because ChatCompletionsStreamResponse has no error field. Folding it in
		// would mark the stream as having produced content and return HTTP 200
		// with an empty message — while still billing the input. Detect it first.
		if upstreamErr := adaptedStreamErrorFrame(data); upstreamErr != nil {
			logger.LogError(c, "adapted upstream stream: upstream reported an error frame: "+common.LocalLogPreview(data))
			status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, nil)
			status.RecordError("upstream error frame")
			streamErr = upstreamErr
			break
		}
		if err := decodeStreamChunk(data, &chunk); err != nil {
			logger.LogError(c, "adapted upstream stream: malformed chunk: "+err.Error())
			status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, nil)
			status.RecordError("malformed chunk")
			// A malformed frame must not discard output that already arrived:
			// that prefix is real output upstream billed us for, and failing here
			// would refund the user and retry — upstream bills the retry again.
			// Deliver it marked incomplete, as when the budget runs out. A role or
			// usage frame alone is not output: that would be HTTP 200 with an
			// empty message billed for the input, so it takes the error path.
			if aggregator.HasOutput() {
				break
			}
			parseErr = err
			break
		}
		aggregator.AddChunk(&chunk)
		aggregator.AddFrameExtras(data)
		lastFrame = data
		info.ReceivedResponseCount++
		if aggregator.OverBudget() {
			// Reading on would grow nothing but the cost. Stop and settle what
			// the budget did capture; the body is delivered marked incomplete.
			logger.LogWarn(c, "adapted upstream stream exceeded the aggregation budget; truncating")
			status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, nil)
			status.RecordError("aggregation budget exceeded")
			break
		}
	}
	// The connection broke mid-answer (unexpected EOF, reset, an oversized line).
	// As with a malformed frame, output already received is real output upstream
	// billed us for: failing here would refund the user and retry, and upstream
	// bills the retry again. Deliver it marked incomplete. With no usable output
	// yet there is nothing to deliver, and the ordinary retry path is the better
	// answer.
	var readErr error
	// clientGone: the read failed because the client went away (its request
	// context was cancelled), not because the upstream broke. Billing is the same
	// either way — what was received, as on the native stream path — but the
	// record and the log must not blame the channel for it.
	clientGone := false
	if err := scanner.Err(); err != nil && parseErr == nil {
		readErr = err
		clientGone = c.Request.Context().Err() != nil && !service.IsRelayRequestTimeout(c)
		if !aggregator.HasOutput() {
			parseErr = err
		}
	}
	switch {
	case service.IsRelayRequestTimeout(c):
		status.SetEndReason(relaycommon.StreamEndReasonTimeout, readErr)
	case clientGone:
		status.SetEndReason(relaycommon.StreamEndReasonClientGone, readErr)
	case readErr != nil:
		status.SetEndReason(relaycommon.StreamEndReasonScannerErr, readErr)
	default:
		// Only takes effect when nothing above recorded a reason: the body
		// ended without [DONE].
		status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	}
	// Reading is over without a read error (or with one we deliver through), so
	// the answer is as complete as it will get — where a plain non-stream call
	// would have received its response and stopped the response timer. Stop it
	// here too; a timer left running into assembly and settlement could expire
	// after billing and 504 a delivered answer. A deadline hit while reading
	// ends in a read error instead and still reaches the timeout branch below.
	if streamErr == nil && parseErr == nil {
		service.ReleaseRelayResponseTimer(c)
	}

	// An explicit upstream error is a channel failure, not a timeout: surface it
	// so the ordinary retry/refund path applies rather than settling partial
	// usage against a request the upstream itself rejected.
	if streamErr != nil {
		return nil, streamErr
	}

	// The deadline fired. Settle what we received instead of refunding, then
	// answer ourselves: StreamHandledKey tells the controller not to refund,
	// retry or rewrite.
	if service.IsRelayRequestTimeout(c) {
		usage := adaptedStreamUsage(c, info, aggregator, lastFrame)
		// Calls that already arrived were executed by upstream's pricing just the
		// same; the streaming handler counts them on a cut-off stream too.
		aggregator.CountBillableToolCalls(info)
		recordAdaptedContentFilter(c, aggregator)
		var note []string
		if !aggregator.HasOutput() {
			// No output to deliver: bill at least the input, as the input mode
			// would for the same request (relay-timeout-cost-bearing.md §3.2).
			usage, note = service.AdaptedTimeoutWithoutOutput(c, info, usage)
		}
		settleAdaptedTimeout(c, info, usage, note)
		c.Set(relaycommon.StreamHandledKey, true)
		if aggregator.HasOutput() && writeAdaptedPartialResponse(c, info, resp, aggregator, usage, !completed || aggregator.OverBudget()) {
			logger.LogWarn(c, fmt.Sprintf(
				"adapted upstream stream timed out after %d received chunks; returned the partial output and settled its usage",
				info.ReceivedResponseCount))
			return usage, nil
		}
		writeAdaptedStreamTimeout(c)
		logger.LogWarn(c, fmt.Sprintf(
			"adapted upstream stream timed out after %d received chunks with no output; settled at least the input instead of refunding",
			info.ReceivedResponseCount))
		return usage, nil
	}

	if parseErr != nil {
		// The client left before any output: there is nothing to deliver, and the
		// channel did nothing wrong. Answer as a plain non-stream request the
		// client cancelled does — no retry against another channel for a client
		// that is gone.
		if clientGone {
			if ctxErr := service.RelayContextError(c); ctxErr != nil {
				return nil, ctxErr
			}
		}
		return nil, types.NewOpenAIError(parseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if readErr != nil {
		if clientGone {
			logger.LogWarn(c, fmt.Sprintf(
				"client went away after %d received chunks of the adapted upstream stream; settling the received output: %s",
				info.ReceivedResponseCount, readErr.Error()))
		} else {
			logger.LogWarn(c, fmt.Sprintf(
				"adapted upstream stream broke after %d received chunks; delivering the received output marked incomplete: %s",
				info.ReceivedResponseCount, readErr.Error()))
		}
	}
	if !adaptedStreamAnswered(aggregator, completed) {
		return nil, types.NewOpenAIError(fmt.Errorf("upstream stream produced no content"),
			types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	usage := adaptedStreamUsage(c, info, aggregator, lastFrame)
	// Settlement runs in the caller after this returns and reads the counts
	// from info and the reject reason from the context.
	aggregator.CountBillableToolCalls(info)
	recordAdaptedContentFilter(c, aggregator)

	body, err := adaptedResponseBody(c, info, aggregator, usage, !completed || aggregator.OverBudget())
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	writeAdaptedResponse(c, resp, body)
	return usage, nil
}

// adaptedStreamAnswered reports whether a stream that ended without an error
// has an answer to deliver and bill. Usable output always is one. Without it,
// the stream still counts as answered when upstream said it was done — [DONE],
// or a finish_reason on some choice: the plain call would have returned that
// empty completion with HTTP 200 and billed it, and retrying it would only buy
// the same answer again. A stream that just stopped after a role, usage or
// otherwise empty frame said neither, so it is no more an answer than the same
// prefix cut off by a bad frame or a read error, and takes the same
// retry/refund path instead of HTTP 200 with an empty message billed for the
// input.
func adaptedStreamAnswered(aggregator *chatStreamAggregator, completed bool) bool {
	if !aggregator.ReceivedAnything() {
		return false
	}
	return aggregator.HasOutput() || completed || aggregator.UpstreamFinished()
}

// recordAdaptedContentFilter records upstream's content_filter finish for the
// admin, as OpenaiHandler does on the plain path. It checks upstream's own
// reason: the snapshot reports "length" instead once the budget cut output, and
// a filtered answer is still worth recording. Settlement writes the log that
// carries it, so this must run before settlement.
func recordAdaptedContentFilter(c *gin.Context, aggregator *chatStreamAggregator) {
	if aggregator.UpstreamFinishedWith(constant.FinishReasonContentFilter) {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
	}
}

// adaptedResponseBody assembles the chat.completion body the non-stream client
// receives, reporting usage as billed. truncated marks an incomplete answer
// (see Snapshot).
func adaptedResponseBody(c *gin.Context, info *relaycommon.RelayInfo, aggregator *chatStreamAggregator, usage *dto.Usage, truncated bool) ([]byte, error) {
	response := aggregator.Snapshot(helper.GetResponseID(c), truncated)
	response.Usage = *usage
	body, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	// Upstream's own usage object goes out verbatim unless the usage billed is
	// not upstream's (estimated, or its zero input replaced), or the channel
	// forces the standard format, as on the plain path.
	forceFormat := info != nil && info.ChannelMeta != nil && info.ChannelSetting.ForceFormat
	return aggregator.DecorateBody(body, usage == aggregator.Usage(), forceFormat)
}

// writeAdaptedResponse writes an assembled body with HTTP 200.
func writeAdaptedResponse(c *gin.Context, resp *http.Response, body []byte) {
	// Upstream headers pass through as on the plain path, except Content-Type:
	// upstream answered text/event-stream and this body is JSON. src stays nil
	// so that header is not copied back over ours.
	for k, v := range resp.Header {
		if strings.EqualFold(k, "Content-Type") || !service.ShouldCopyUpstreamHeader(c, k, v) {
			continue
		}
		c.Writer.Header().Set(k, v[0])
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	service.IOCopyBytesGracefully(c, nil, body)
}

// writeAdaptedPartialResponse delivers the output received before the deadline:
// the user is charged for it, so they get it. A cut-off answer is marked
// incomplete (finish_reason "length"); truncated is false only when upstream
// had already finished and the deadline fired before the timer was released.
// After the deadline the timeout controller blocks ordinary writes; this body is
// the request's one terminal payload and goes out through the same gate as the
// timeout error. Reports false when the body cannot be built, leaving the
// caller to answer with the timeout error.
func writeAdaptedPartialResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, aggregator *chatStreamAggregator, usage *dto.Usage, truncated bool) bool {
	if c.Writer.Written() {
		return false
	}
	body, err := adaptedResponseBody(c, info, aggregator, usage, truncated)
	if err != nil {
		logger.LogError(c, "adapted upstream stream: cannot assemble the partial output after a timeout: "+err.Error())
		return false
	}
	service.WriteRelayTerminalError(c, func() {
		writeAdaptedResponse(c, resp, body)
	})
	return true
}

// adaptedStreamErrorFrame reports an in-band upstream error carried by an SSE
// data frame, or nil when the frame is ordinary content — including one whose
// error field carries nothing (null, {}, [], "", false, 0), or carries an
// object without a type beside output, which some upstreams put on every
// normal chunk and OpenaiHandler does not fail on.
func adaptedStreamErrorFrame(data string) *types.NewAPIError {
	if !strings.Contains(data, "\"error\"") {
		return nil
	}
	// Decoded into any, as OpenaiHandler decodes the same field of a plain body
	// (OpenAITextResponse.Error): dto.GetOpenAIError reads the object and string
	// forms that yields. Any other Go type — a json.RawMessage included — lands
	// in its catch-all, which prints the value, i.e. the frame's bytes, as the
	// client's message.
	var probe struct {
		Error any `json:"error"`
	}
	if err := common.UnmarshalJsonStr(data, &probe); err != nil {
		return nil
	}
	if emptyErrorValue(probe.Error) {
		return nil
	}
	switch upstreamErr := probe.Error.(type) {
	case map[string]any:
		oaiErr := dto.GetOpenAIError(upstreamErr)
		// A typed object is upstream's own error, as on the plain path, even
		// beside output. The plain path never fails on an untyped one, and some
		// upstreams stamp one ({"code":200}, {"message":"success"}) on every
		// chunk: beside output it is content, and failing would discard an
		// answer upstream already billed, refund, retry and feed channel
		// auto-disable. Only a frame that is nothing but the error is one.
		if oaiErr.Type == "" && adaptedFrameCarriesOutput(data) {
			return nil
		}
		// An untyped one with a message is surfaced (WithOpenAIError types it
		// upstream_error): the message is what channel auto-disable keyword
		// matching reads, and error masking still governs what a client sees.
		if oaiErr.Type != "" || oaiErr.Message != "" {
			return types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
		}
	case string:
		// The plain path surfaces a string error as its message too.
		return types.WithOpenAIError(*dto.GetOpenAIError(upstreamErr), http.StatusInternalServerError)
	}
	// Any other shape is upstream data we cannot read as an error; it goes to
	// the log at the call site, never into the message a client can see.
	return types.NewOpenAIError(errors.New("upstream stream error"),
		types.ErrorCodeBadResponse, http.StatusInternalServerError)
}

// adaptedFrameCarriesOutput reports whether a frame carries anything besides
// its error field: a choice whose delta has a non-empty member (role, text, a
// tool call, ...) or that has a finish_reason, or a usage object with a
// non-empty member — the final usage frame of an upstream that stamps the
// field on every chunk has no choices. It runs only on frames whose error
// field is an untyped object, and gjson reads the frame without decoding it.
func adaptedFrameCarriesOutput(data string) bool {
	carries := false
	gjson.Get(data, "choices").ForEach(func(_, choice gjson.Result) bool {
		if reason := choice.Get("finish_reason"); reason.Type == gjson.String && reason.Str != "" {
			carries = true
			return false
		}
		carries = gjsonHasNonEmptyMember(choice.Get("delta"))
		return !carries
	})
	return carries || gjsonHasNonEmptyMember(gjson.Get(data, "usage"))
}

// gjsonHasNonEmptyMember reports whether object is a JSON object with a member
// that is not null, "", false, 0 or an empty container.
func gjsonHasNonEmptyMember(object gjson.Result) bool {
	if !object.IsObject() {
		return false
	}
	found := false
	object.ForEach(func(_, member gjson.Result) bool {
		switch member.Type {
		case gjson.Null, gjson.False:
		case gjson.String:
			found = member.Str != ""
		case gjson.Number:
			found = member.Num != 0
		case gjson.JSON:
			// An empty container is "{}" or "[]" with optional whitespace.
			found = len(strings.TrimSpace(member.Raw[1:len(member.Raw)-1])) > 0
		default:
			found = true
		}
		return !found
	})
	return found
}

// emptyErrorValue reports an error field that carries nothing: null, "",
// false, 0, an empty array, or an object whose members are all such scalars
// or empty containers (e.g. {"code":0,"message":""}).
func emptyErrorValue(v any) bool {
	if object, ok := v.(map[string]any); ok {
		for _, member := range object {
			if !emptyJSONValue(member) {
				return false
			}
		}
		return true
	}
	return emptyJSONValue(v)
}

// emptyJSONValue reports a decoded JSON value that is null, a zero scalar or
// an empty container.
func emptyJSONValue(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case bool:
		return !value
	case float64:
		return value == 0
	case map[string]any:
		return len(value) == 0
	case []any:
		return len(value) == 0
	}
	return false
}

// adaptedStreamPayload extracts the JSON payload of one SSE data line.
func adaptedStreamPayload(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 5 || !strings.HasPrefix(line, "data:") {
		return "", false
	}
	data := strings.TrimSpace(line[5:])
	if data == "" {
		return "", false
	}
	return data, true
}

// adaptedStreamUsage prefers what upstream reported and falls back to local
// estimation over the assembled text, which is the usual case on a timeout
// because include_usage only reports in the final chunk.
func adaptedStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, aggregator *chatStreamAggregator, lastFrame string) *dto.Usage {
	if usage := aggregator.Usage(); service.ValidUsage(usage) {
		if usage.PromptTokens == 0 {
			// Upstream reported no input: bill the estimate instead, as
			// OpenaiHandler does for the plain call.
			prompt := info.GetEstimatePromptTokens()
			usage = &dto.Usage{
				PromptTokens:     prompt,
				CompletionTokens: usage.CompletionTokens,
				TotalTokens:      prompt + usage.CompletionTokens,
			}
		}
		openai.ApplyUsagePostProcessing(info, usage, common.StringToByteSlice(lastFrame))
		return usage
	}
	if !aggregator.ReceivedAnything() {
		return &dto.Usage{}
	}
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := service.ResponseText2Usage(c, aggregator.AssembledText(),
		info.UpstreamModelName, info.GetEstimatePromptTokens())
	// Mirrors the per-tool-call surcharge the streaming path applies.
	usage.CompletionTokens += aggregator.ToolCallCount() * 7
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	// The plain stream handler applies this to estimated usage too.
	openai.ApplyUsagePostProcessing(info, usage, common.StringToByteSlice(lastFrame))
	return usage
}

func writeAdaptedStreamTimeout(c *gin.Context) {
	if c.Writer.Written() {
		return
	}
	message := i18n.T(c, i18n.MsgRelayTimeout, map[string]any{
		"Seconds": service.RelayRequestTimeoutSeconds(c),
	})
	status, message, code := service.PresentLocalRelayAbort(c, true, http.StatusGatewayTimeout, message, string(types.ErrorCodeRelayTimeout))
	// Like every relay error the controller writes, so a user can report it.
	message = common.MessageWithRequestId(message, c.GetString(common.RequestIdKey))
	service.WriteRelayTerminalError(c, func() {
		c.JSON(status, gin.H{
			"error": gin.H{
				"message": message,
				"type":    "new_api_error",
				"code":    code,
			},
		})
	})
}
