package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	MaxRelayTimeoutSeconds                    = 7 * 24 * 60 * 60
	RelayTimeoutUnlimited                     = -1
	RelayStreamResponseTimeoutModeFirstOutput = "first_output"
	RelayStreamResponseTimeoutModeIdle        = "idle"
)

func ValidateRelayTimeoutOverride(seconds int) error {
	if seconds == RelayTimeoutUnlimited || seconds == 0 {
		return nil
	}
	if seconds < 1 || seconds > MaxRelayTimeoutSeconds {
		return fmt.Errorf("relay timeout override is out of range")
	}
	return nil
}

func NormalizeRelayStreamResponseTimeoutMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case RelayStreamResponseTimeoutModeIdle:
		return RelayStreamResponseTimeoutModeIdle
	default:
		return RelayStreamResponseTimeoutModeFirstOutput
	}
}

func ValidateRelayStreamResponseTimeoutMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", RelayStreamResponseTimeoutModeFirstOutput, RelayStreamResponseTimeoutModeIdle:
		return nil
	default:
		return fmt.Errorf("stream response timeout mode is invalid")
	}
}

func ResolveRelayTimeoutOverride(userSeconds, globalSeconds int) int {
	if userSeconds == RelayTimeoutUnlimited {
		return 0
	}
	if userSeconds > 0 && userSeconds <= MaxRelayTimeoutSeconds {
		return userSeconds
	}
	if globalSeconds > 0 {
		return globalSeconds
	}
	return 0
}

type RelayResponseMarker interface {
	MarkResponse(bool) bool
}

// WriteRelayTerminalError lets a stream send its one in-band error after a
// managed deadline without reopening the response for ordinary content.
// WriteRelayTerminalError 为流式终止错误选用受管超时写入通道，避免重新放开普通业务内容。
// 参数 c：保存超时控制器的上下文；write：非 nil 的同步错误写入回调，无控制器时直接执行。
func WriteRelayTerminalError(c *gin.Context, write func()) {
	if value, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl); ok {
		if control, ok := value.(interface{ WriteTerminalError(func()) }); ok {
			control.WriteTerminalError(write)
			return
		}
	}
	write()
}

type relayResponseRestarter interface {
	RestartResponse(bool) bool
}

type relayResponsePending interface {
	RelayResponsePending() bool
}

type managedRelayTimeoutContextKey struct{}

type managedRelayTimeoutState struct {
	hasTotalLimit bool
}

// WithManagedRelayTimeoutContext records whether response-body cleanup has a
// finite request-local boundary after the shared http.Client timeout is removed.
func WithManagedRelayTimeoutContext(parent context.Context, hasTotalLimit bool) context.Context {
	return context.WithValue(parent, managedRelayTimeoutContextKey{}, managedRelayTimeoutState{hasTotalLimit: hasTotalLimit})
}

func managedRelayTimeoutContext(ctx context.Context) (managedRelayTimeoutState, bool) {
	if ctx == nil {
		return managedRelayTimeoutState{}, false
	}
	state, ok := ctx.Value(managedRelayTimeoutContextKey{}).(managedRelayTimeoutState)
	return state, ok
}

type RelayTimeoutObserver interface {
	RelayResponseMarker
	RelayTimeoutDeadline() (time.Time, bool)
}

func RelayResponseMarkerFromContext(c *gin.Context) (RelayResponseMarker, bool) {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return nil, false
	}
	marker, ok := value.(RelayResponseMarker)
	return marker, ok
}

type relayTimeoutKindReader interface {
	RelayTimeoutKind() string
}

func relayTimeoutControlValue(c *gin.Context) (any, bool) {
	if c == nil {
		return nil, false
	}
	return common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl)
}

func RelayTimeoutObserverFromContext(c *gin.Context) (RelayTimeoutObserver, bool) {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return nil, false
	}
	observer, ok := value.(RelayTimeoutObserver)
	return observer, ok
}

func IsRelayTimeoutManaged(c *gin.Context) bool {
	_, ok := relayTimeoutControlValue(c)
	return ok
}

// RelayRequestContext preserves the legacy background context unless the
// per-request timeout controller owns cancellation for this relay request.
func RelayRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil && IsRelayTimeoutManaged(c) {
		return c.Request.Context()
	}
	return context.Background()
}

// BindRelayRequestContext applies request-local cancellation without changing
// unmanaged callers or mutating their original request.
func BindRelayRequestContext(c *gin.Context, req *http.Request) *http.Request {
	if req == nil || c == nil || c.Request == nil || !IsRelayTimeoutManaged(c) {
		return req
	}
	return req.WithContext(c.Request.Context())
}

// RelayHTTPClient removes only the process-wide timeout for managed requests,
// allowing the user's request-local override to be authoritative. The cloned
// client shares the original Transport and therefore its connection pool.
func RelayHTTPClient(c *gin.Context, client *http.Client) *http.Client {
	if client == nil || c == nil || !IsRelayTimeoutManaged(c) || client.Timeout == 0 {
		return client
	}
	relayClient := *client
	relayClient.Timeout = 0
	return &relayClient
}

// RelayResponseTraceContext marks the first upstream byte for non-streaming
// requests. It captures the request-local marker rather than the pooled Gin
// context because transport callbacks may run after client.Do returns.
func RelayResponseTraceContext(c *gin.Context, parent context.Context) context.Context {
	if c == nil || parent == nil || common.GetContextKeyBool(c, constant.ContextKeyIsStream) {
		return parent
	}
	marker, ok := RelayResponseMarkerFromContext(c)
	if !ok {
		return parent
	}
	pending, ok := marker.(relayResponsePending)
	if !ok || !pending.RelayResponsePending() {
		return parent
	}
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { marker.MarkResponse(false) }}
	return httptrace.WithClientTrace(parent, trace)
}

// RelayContextError translates only cancellation owned by the request-local
// timeout feature. Unmanaged requests keep the relay chain's original error.
func RelayContextError(c *gin.Context) *types.NewAPIError {
	if c == nil || c.Request == nil || !IsRelayTimeoutManaged(c) {
		return nil
	}
	if IsRelayRequestTimeout(c) {
		return types.NewErrorWithStatusCode(
			context.DeadlineExceeded,
			types.ErrorCodeRelayTimeout,
			http.StatusGatewayTimeout,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if err := c.Request.Context().Err(); err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func RelayRequestTimeoutKind(c *gin.Context) string {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return ""
	}
	reader, ok := value.(relayTimeoutKindReader)
	if !ok {
		return ""
	}
	return reader.RelayTimeoutKind()
}

func IsRelayRequestTimeout(c *gin.Context) bool {
	return RelayRequestTimeoutKind(c) != ""
}

func MarkRelayResponse(c *gin.Context) bool {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return false
	}
	marker, ok := value.(RelayResponseMarker)
	return ok && marker.MarkResponse(common.GetContextKeyBool(c, constant.ContextKeyIsStream))
}

func RestartRelayResponseTimeout(c *gin.Context) bool {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return false
	}
	restarter, ok := value.(relayResponseRestarter)
	return ok && restarter.RestartResponse(common.GetContextKeyBool(c, constant.ContextKeyIsStream))
}

func RelayRequestDeadline(c *gin.Context) (time.Time, bool) {
	observer, ok := RelayTimeoutObserverFromContext(c)
	if !ok {
		return time.Time{}, false
	}
	return observer.RelayTimeoutDeadline()
}
