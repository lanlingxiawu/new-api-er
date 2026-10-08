package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 中转错误提示的可配置替换在各个出口的落地（设计见
// docs/design/relay-error-message-masking.md）。所有函数都只在请求失败、准备把
// 错误交给客户端时调用；重试判断、自动禁用、错误日志在此之前已用原文完成。
// 任何内部异常都退回原错误输出（Rule 0：新功能失败必须静默）。

// maskedUpstreamErrorCode replaces an upstream's own error code once masked.
const maskedUpstreamErrorCode types.ErrorCode = "upstream_error"

// IsUpstreamRelayError reports whether err was built from an upstream response.
func IsUpstreamRelayError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	return operation_setting.IsUpstreamRelayErrorKind(string(err.GetErrorType()), string(err.GetErrorCode()))
}

// PresentRelayError returns the error and message (request id included) the
// client should receive. responseMessage is the message the caller would send
// unchanged; it is returned as is when nothing is replaced.
func PresentRelayError(c *gin.Context, err *types.NewAPIError, responseMessage, requestID string) (out *types.NewAPIError, message string) {
	out, message = err, responseMessage
	if err == nil || !operation_setting.RelayErrorDisplayEnabled() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("relay error display panic recovered: %v", r))
			out, message = err, responseMessage
		}
	}()
	upstream := IsUpstreamRelayError(err)
	decision := operation_setting.DecideRelayError(operation_setting.RelayErrorInput{
		Upstream:   upstream,
		StatusCode: err.StatusCode,
		ErrorCode:  string(err.GetErrorCode()),
		Message:    err.Error(),
		Lang:       i18n.GetLangFromContext(c),
	})
	if !decision.Replace {
		return
	}
	status := err.StatusCode
	if decision.StatusCode > 0 {
		status = decision.StatusCode
	}
	code := err.GetErrorCode()
	if upstream {
		code = maskedUpstreamErrorCode
	}
	// A fresh local error: the upstream's own type, code, param and metadata
	// cannot survive into ToOpenAIError / ToClaudeError.
	masked := types.NewErrorWithStatusCode(errors.New(decision.Message), code, status)
	logRelayErrorReplaced(c, decision, err.StatusCode, status)
	return masked, common.MessageWithRequestId(decision.Message, requestID)
}

// PresentLocalRelayAbort rewrites an error that middleware writes directly
// (auth, distributor). isRelay limits it to relay routes: dashboard APIs share
// the helper but their responses are not relay errors.
func PresentLocalRelayAbort(c *gin.Context, isRelay bool, statusCode int, message, code string) (outStatus int, outMessage string, outCode string) {
	outStatus, outMessage, outCode = statusCode, message, code
	if !isRelay || !operation_setting.RelayErrorDisplayEnabled() {
		return
	}
	// Named results: a recovered panic must send the original error, not zero
	// values (status 0 would go out as HTTP 200 with an empty message).
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("relay error display panic recovered: %v", r))
			outStatus, outMessage, outCode = statusCode, message, code
		}
	}()
	decision := decideRelayError(operation_setting.RelayErrorInput{
		StatusCode: statusCode,
		ErrorCode:  code,
		Message:    message,
		Lang:       i18n.GetLangFromContext(c),
	})
	if !decision.Replace {
		return statusCode, message, code
	}
	status := statusCode
	if decision.StatusCode > 0 {
		status = decision.StatusCode
	}
	// The caller logs what it sends; keep the original on record here.
	logger.LogInfo(c, "client error message replaced; original: "+common.LocalLogPreview(message))
	logRelayErrorReplaced(c, decision, statusCode, status)
	return status, decision.Message, code
}

// decideRelayError is operation_setting.DecideRelayError; a seam for tests.
var decideRelayError = operation_setting.DecideRelayError

// PresentStreamTerminalMessage decides the message of a stream's terminal
// error frame. upstreamPayload is the upstream error frame's JSON (nil when the
// stream failed without one); fallback is the generic local text. forward=false
// forbids sending the upstream frame verbatim. Headers are already written, so
// no status code can change here.
func PresentStreamTerminalMessage(c *gin.Context, upstreamPayload []byte, fallback string) (message string, forward bool) {
	return presentStreamErrorMessage(c, upstreamPayload, fallback, true)
}

// presentStreamErrorMessage is PresentStreamTerminalMessage; record=false
// decides an error that does not end the stream, whose input must not replace
// the one the stream's error log is decided with, and costs nothing when the
// feature is off.
func presentStreamErrorMessage(c *gin.Context, upstreamPayload []byte, fallback string, record bool) (message string, forward bool) {
	message, forward = fallback, true
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("relay error display panic recovered: %v", r))
			message, forward = fallback, true
		}
	}()
	if c == nil || (!record && !operation_setting.RelayErrorDisplayEnabled()) {
		return
	}
	in := streamTerminalRelayErrorInput(c, upstreamPayload, fallback)
	if record {
		// Kept for this stream's settlement: its error log records the same
		// input, so the user's view of that log decides exactly as this frame
		// did, also when the feature is switched on later (rules apply to past
		// logs).
		c.Set(streamTerminalRelayErrorInputKey, in)
	}
	if !operation_setting.RelayErrorDisplayEnabled() {
		return
	}
	in.Lang = i18n.GetLangFromContext(c)
	decision := operation_setting.DecideRelayError(in)
	if !decision.Replace {
		if !in.Upstream && len(upstreamPayload) > 0 {
			// Our deadline cut the stream, so the error is decided as our own
			// timeout; the upstream frame read before it was never decided and
			// must not go out verbatim (hide_upstream_errors would not apply).
			return fallback, false
		}
		return
	}
	logRelayErrorReplaced(c, decision, 0, 0)
	return decision.Message, false
}

// PresentRealtimeErrorEvent returns the Realtime event the client should
// receive for an upstream event that reports an error: an `error` event, or an
// event that ends the stream as an upstream error (a failed response.done, an
// event carrying an error object such as
// conversation.item.input_audio_transcription.failed). terminal marks the
// event that ends the stream (its input is kept for the stream's error log, as
// a stream's terminal frame is); a recoverable request error, after which the
// connection stays open, is decided without being recorded. When nothing is
// replaced (or the feature is off) the event goes out as received. Otherwise
// an `error` event is rebuilt from scratch, keeping only the recoverable
// classification (error.type invalid_request_error) and the client's own
// error.event_id; any other event keeps its type and shape with each error
// object (error, response.error, response.status_details.error) and a
// top-level message replaced by the decided message.
func PresentRealtimeErrorEvent(c *gin.Context, event []byte, terminal bool) []byte {
	message, forward := presentStreamErrorMessage(c, event, i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed), terminal)
	if forward {
		return event
	}
	original := gjson.ParseBytes(event)
	if original.Get("type").String() == "error" {
		data, err := common.Marshal(map[string]any{"type": "error", "error": realtimeErrorBody(original.Get("error"), message)})
		if err != nil {
			return event
		}
		return data
	}
	out := event
	for _, path := range []string{"error", "response.error", "response.status_details.error"} {
		if !original.Get(path).IsObject() {
			continue
		}
		body, err := common.Marshal(realtimeErrorBody(original.Get(path), message))
		if err == nil {
			out, err = sjson.SetRawBytes(out, path, body)
		}
		if err != nil {
			return realtimeGenericErrorEvent(message)
		}
	}
	if text := original.Get("message"); text.Type == gjson.String {
		var err error
		if out, err = sjson.SetBytes(out, "message", message); err != nil {
			return realtimeGenericErrorEvent(message)
		}
	}
	return out
}

// realtimeErrorBody is the error object sent in place of an upstream one.
func realtimeErrorBody(upstream gjson.Result, message string) map[string]any {
	errorType := "server_error"
	if upstream.Get("type").String() == "invalid_request_error" {
		errorType = "invalid_request_error"
	}
	body := map[string]any{"type": errorType, "code": operation_setting.RelayStreamErrorCode, "message": message}
	if id := upstream.Get("event_id"); id.Type == gjson.String && id.Str != "" {
		body["event_id"] = id.Str
	}
	return body
}

// realtimeGenericErrorEvent is a Realtime error event carrying only message,
// for an upstream event that could not be rewritten in place.
func realtimeGenericErrorEvent(message string) []byte {
	data, _ := common.Marshal(map[string]any{"type": "error", "error": map[string]any{
		"type": "server_error", "code": operation_setting.RelayStreamErrorCode, "message": message}})
	return data
}

// streamTerminalRelayErrorInputKey holds, in the request context, the
// RelayErrorInput a stream's terminal frame was decided with.
const streamTerminalRelayErrorInputKey = "relay_error_display_stream_input"

// streamTerminalRelayErrorInput is the input a stream's terminal frame is
// matched with. A stream cut by our own time limit is a local relay_timeout;
// anything else is an upstream stream error, matched with the upstream frame's
// own error code and message when it has them. The message has URLs, hosts
// and api_key values masked like the log content: it is also stored in the
// stream's error log, which must not keep credential material, and the frame
// must be decided with exactly the text the log keeps. It is masked first (on
// at most operation_setting.MaxRelayErrorMatchLen bytes) and capped afterwards
// (CapRelayErrorText, which also drops a token the cut splits), so a cut never
// leaves part of an address or key that masking would no longer recognise. A
// capped message is marked Truncated: when no rule replaces it, the client gets
// the capped text rather than the upstream frame.
func streamTerminalRelayErrorInput(c *gin.Context, upstreamPayload []byte, fallback string) operation_setting.RelayErrorInput {
	timedOut := IsRelayRequestTimeout(c)
	in := operation_setting.StreamFailureRelayErrorInput(timedOut, fallback)
	if !timedOut && len(upstreamPayload) > 0 {
		v := gjson.ParseBytes(upstreamPayload)
		// Chat Completions / Claude frames carry error.*, Responses frames a
		// top-level code and message or response.error.* (response.failed),
		// Realtime response.done response.status_details.error.*.
		for _, path := range []string{"error.message", "message", "response.error.message", "response.status_details.error.message"} {
			if text := v.Get(path); text.Type == gjson.String && text.Str != "" {
				in.Message = text.Str
				break
			}
		}
		// A code that is null, empty or not a scalar (OpenAI sends "code":null)
		// is no code: the frame keeps upstream_stream_error.
		for _, path := range []string{"error.code", "code", "response.error.code", "response.status_details.error.code"} {
			code := v.Get(path)
			if (code.Type == gjson.String && code.Str != "") || code.Type == gjson.Number {
				in.ErrorCode = code.String()
				break
			}
		}
	}
	prefix, cut := operation_setting.RelayErrorMatchPrefix(in.Message)
	if cut {
		// The token the cut splits goes before masking, which may shorten the
		// text enough that the cap below no longer cuts (and trims) it.
		prefix = operation_setting.CutRelayErrorText(in.Message, len(prefix))
	}
	masked := common.MaskSensitiveInfo(prefix)
	in.Message = operation_setting.CapRelayErrorText(masked)
	in.Truncated = cut || len(in.Message) < len(masked)
	if strings.TrimSpace(in.Message) == "" {
		// Nothing survived the cut (one token longer than the cap).
		in.Message = fallback
	}
	return in
}

// streamTerminalInput returns the input the terminal frame of this request's
// stream was decided with, if one was written.
func streamTerminalInput(c *gin.Context) (operation_setting.RelayErrorInput, bool) {
	if c == nil {
		return operation_setting.RelayErrorInput{}, false
	}
	value, ok := c.Get(streamTerminalRelayErrorInputKey)
	if !ok {
		return operation_setting.RelayErrorInput{}, false
	}
	in, ok := value.(operation_setting.RelayErrorInput)
	return in, ok
}

func logRelayErrorReplaced(c *gin.Context, decision operation_setting.RelayErrorDecision, fromStatus, toStatus int) {
	rule := "upstream fallback"
	if decision.RuleIndex >= 0 {
		rule = fmt.Sprintf("rule #%d %s", decision.RuleIndex+1, decision.RuleName)
	}
	if decision.Truncated {
		rule = "the part the rules checked (" + rule + ")"
		if decision.RuleIndex < 0 {
			rule = "the part the rules checked (no rule matched)"
		}
	}
	logger.LogInfo(c, fmt.Sprintf("client error message replaced by %s (status %d -> %d)", rule, fromStatus, toStatus))
}
