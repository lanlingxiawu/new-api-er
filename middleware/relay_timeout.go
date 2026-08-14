package middleware

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type relayTimeoutKind string

const (
	relayTimeoutKindNone     relayTimeoutKind = ""
	relayTimeoutKindResponse relayTimeoutKind = "response_timeout"
	relayTimeoutKindTotal    relayTimeoutKind = "total_timeout"
)

var (
	errRelayResponseTimeout = errors.New("relay response timeout")
	errRelayTotalTimeout    = errors.New("relay total timeout")
)

type relayTimeoutControl struct {
	mu sync.Mutex

	cancel context.CancelCauseFunc
	closed bool

	responseTimer      *time.Timer
	responseTimeout    time.Duration
	responseDeadline   time.Time
	responseStopped    bool
	responseActive     atomic.Bool
	streamResponseMode string

	totalTimer    *time.Timer
	totalDeadline time.Time

	expiredKind relayTimeoutKind
	expired     atomic.Bool

	allowExpiredWrite atomic.Bool
	lateWriteRejected atomic.Bool
}

func newRelayTimeoutControl(parent context.Context, responseTimeout, totalTimeout time.Duration, streamResponseMode string) (context.Context, *relayTimeoutControl) {
	ctx, cancel := context.WithCancelCause(parent)
	control := &relayTimeoutControl{
		cancel:             cancel,
		responseTimeout:    responseTimeout,
		streamResponseMode: service.NormalizeRelayStreamResponseTimeoutMode(streamResponseMode),
	}

	if responseTimeout > 0 {
		control.responseActive.Store(true)
		control.responseDeadline = time.Now().Add(responseTimeout)
		control.responseTimer = time.AfterFunc(responseTimeout, control.expireResponse)
	}
	if totalTimeout > 0 {
		control.totalDeadline = time.Now().Add(totalTimeout)
		control.totalTimer = time.AfterFunc(totalTimeout, control.expireTotal)
	}
	return ctx, control
}

func (control *relayTimeoutControl) expireResponse() {
	control.mu.Lock()
	if control.closed || control.expiredKind != relayTimeoutKindNone || control.responseStopped {
		control.mu.Unlock()
		return
	}
	if remaining := time.Until(control.responseDeadline); remaining > 0 {
		control.responseTimer.Reset(remaining)
		control.mu.Unlock()
		return
	}
	control.expiredKind = relayTimeoutKindResponse
	control.responseActive.Store(false)
	control.expired.Store(true)
	control.cancel(errRelayResponseTimeout)
	control.mu.Unlock()
}

func (control *relayTimeoutControl) expireTotal() {
	control.mu.Lock()
	if control.closed || control.expiredKind != relayTimeoutKindNone {
		control.mu.Unlock()
		return
	}
	control.expiredKind = relayTimeoutKindTotal
	control.responseActive.Store(false)
	control.expired.Store(true)
	control.cancel(errRelayTotalTimeout)
	control.mu.Unlock()
}

func (control *relayTimeoutControl) MarkResponse(isStream bool) bool {
	if control == nil || !control.responseActive.Load() {
		return false
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.closed || control.expiredKind != relayTimeoutKindNone || control.responseTimer == nil {
		return false
	}
	if isStream && control.streamResponseMode == service.RelayStreamResponseTimeoutModeIdle {
		control.responseStopped = false
		control.responseDeadline = time.Now().Add(control.responseTimeout)
		control.responseTimer.Stop()
		control.responseTimer.Reset(control.responseTimeout)
		control.responseActive.Store(true)
		return true
	}
	if control.responseStopped {
		return false
	}
	control.responseStopped = true
	control.responseActive.Store(false)
	control.responseTimer.Stop()
	return true
}

func (control *relayTimeoutControl) RestartResponse(isStream bool) bool {
	if control == nil || isStream {
		return false
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.closed || control.expiredKind != relayTimeoutKindNone || control.responseTimer == nil {
		return false
	}
	control.responseStopped = false
	control.responseDeadline = time.Now().Add(control.responseTimeout)
	control.responseTimer.Stop()
	control.responseTimer.Reset(control.responseTimeout)
	control.responseActive.Store(true)
	return true
}

func (control *relayTimeoutControl) RelayResponsePending() bool {
	if control == nil || !control.responseActive.Load() {
		return false
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	return !control.closed && control.expiredKind == relayTimeoutKindNone && control.responseTimer != nil && !control.responseStopped
}

func (control *relayTimeoutControl) Close() {
	if control == nil {
		return
	}
	control.mu.Lock()
	if control.closed {
		control.mu.Unlock()
		return
	}
	control.closed = true
	control.responseActive.Store(false)
	if control.responseTimer != nil {
		control.responseTimer.Stop()
	}
	if control.totalTimer != nil {
		control.totalTimer.Stop()
	}
	control.mu.Unlock()
	control.cancel(context.Canceled)
}

func (control *relayTimeoutControl) ExpiredKind() relayTimeoutKind {
	if control == nil {
		return relayTimeoutKindNone
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.expiredKind
}

func (control *relayTimeoutControl) RelayTimeoutKind() string {
	return string(control.ExpiredKind())
}

func (control *relayTimeoutControl) NextDeadline() (time.Time, bool) {
	if control == nil {
		return time.Time{}, false
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.closed || control.expiredKind != relayTimeoutKindNone {
		return time.Time{}, false
	}
	deadline := control.totalDeadline
	if !control.responseStopped && !control.responseDeadline.IsZero() && (deadline.IsZero() || control.responseDeadline.Before(deadline)) {
		deadline = control.responseDeadline
	}
	return deadline, !deadline.IsZero()
}

func (control *relayTimeoutControl) RelayTimeoutDeadline() (time.Time, bool) {
	return control.NextDeadline()
}

type relayTimeoutWriterState struct {
	control  *relayTimeoutControl
	isStream bool
}

type relayTimeoutResponseWriter struct {
	gin.ResponseWriter
	state atomic.Pointer[relayTimeoutWriterState]
}

func (writer *relayTimeoutResponseWriter) canWrite(state *relayTimeoutWriterState) bool {
	if state == nil || state.control == nil || !state.control.expired.Load() || state.control.allowExpiredWrite.Load() {
		return true
	}
	state.control.lateWriteRejected.Store(true)
	return false
}

func (writer *relayTimeoutResponseWriter) WriteHeader(code int) {
	if writer.canWrite(writer.state.Load()) {
		writer.ResponseWriter.WriteHeader(code)
	}
}

func (writer *relayTimeoutResponseWriter) WriteHeaderNow() {
	if writer.canWrite(writer.state.Load()) {
		writer.ResponseWriter.WriteHeaderNow()
	}
}

func (writer *relayTimeoutResponseWriter) Flush() {
	if writer.canWrite(writer.state.Load()) {
		writer.ResponseWriter.Flush()
	}
}

func (writer *relayTimeoutResponseWriter) Write(data []byte) (int, error) {
	state := writer.state.Load()
	if !writer.canWrite(state) {
		return 0, context.DeadlineExceeded
	}
	n, err := writer.ResponseWriter.Write(data)
	if n > 0 && state != nil && state.control != nil && state.control.responseActive.Load() && !isRelayNonOutput(data[:n]) {
		state.control.MarkResponse(state.isStream)
	}
	return n, err
}

func (writer *relayTimeoutResponseWriter) WriteString(data string) (int, error) {
	state := writer.state.Load()
	if !writer.canWrite(state) {
		return 0, context.DeadlineExceeded
	}
	n, err := writer.ResponseWriter.WriteString(data)
	if n > 0 && state != nil && state.control != nil && state.control.responseActive.Load() && !isRelayNonOutputString(data[:n]) {
		state.control.MarkResponse(state.isStream)
	}
	return n, err
}

func (writer *relayTimeoutResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// WriteRelayTimeoutResponse temporarily permits the controller's standardized
// timeout payload while late business output remains blocked.
func WriteRelayTimeoutResponse(c *gin.Context, write func()) {
	if write == nil {
		return
	}
	control, ok := relayTimeoutControlFromContext(c)
	if !ok {
		write()
		return
	}
	previous := control.allowExpiredWrite.Swap(true)
	defer control.allowExpiredWrite.Store(previous)
	write()
}

func relayTimeoutControlFromContext(c *gin.Context) (*relayTimeoutControl, bool) {
	if c == nil {
		return nil, false
	}
	value, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl)
	if !ok {
		return nil, false
	}
	control, ok := value.(*relayTimeoutControl)
	return control, ok && control != nil
}

func isRelayNonOutput(data []byte) bool {
	for len(data) > 0 {
		line := data
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			line = data[:index]
			data = data[index+1:]
		} else {
			data = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] != ':' {
			return false
		}
	}
	return true
}

func isRelayNonOutputString(data string) bool {
	for len(data) > 0 {
		line := data
		if index := strings.IndexByte(data, '\n'); index >= 0 {
			line = data[:index]
			data = data[index+1:]
		} else {
			data = ""
		}
		line = strings.TrimSpace(line)
		if len(line) > 0 && line[0] != ':' {
			return false
		}
	}
	return true
}

// relayTimeoutUserOverrides returns the two raw user values that apply to the
// mode this request runs in. Zero inherits the global default, -1 disables.
func relayTimeoutUserOverrides(c *gin.Context, isStream bool) (userResponse, userTotal int) {
	if isStream {
		return common.GetContextKeyInt(c, constant.ContextKeyUserStreamResponseTimeout),
			common.GetContextKeyInt(c, constant.ContextKeyUserStreamTotalTimeout)
	}
	return common.GetContextKeyInt(c, constant.ContextKeyUserNonStreamResponseTimeout),
		common.GetContextKeyInt(c, constant.ContextKeyUserNonStreamTotalTimeout)
}

func effectiveRelayTimeouts(c *gin.Context, isStream bool) (responseSeconds, totalSeconds int) {
	setting, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutSetting)
	if !ok {
		return 0, 0
	}
	timeoutSetting, ok := setting.(*operation_setting.RelayTimeoutSetting)
	// Defensive guard for direct callers and tests. The middleware and starter
	// already short-circuit disabled snapshots on the production path.
	if !ok || timeoutSetting == nil || !timeoutSetting.Enabled {
		return 0, 0
	}
	userResponse, userTotal := relayTimeoutUserOverrides(c, isStream)
	responseSeconds = service.ResolveRelayTimeoutOverride(userResponse, timeoutSetting.ResponseTimeoutSeconds)
	totalSeconds = service.ResolveRelayTimeoutOverride(userTotal, timeoutSetting.TotalTimeoutSeconds)
	return responseSeconds, totalSeconds
}

// StartRelayRequestTimeout starts both limits after request parsing has
// determined whether this is a streaming request.
func StartRelayRequestTimeout(c *gin.Context, isStream bool) {
	if c == nil || c.Request == nil {
		return
	}
	setting, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutSetting)
	if !ok {
		return
	}
	timeoutSetting, ok := setting.(*operation_setting.RelayTimeoutSetting)
	if !ok || timeoutSetting == nil || !timeoutSetting.Enabled {
		return
	}
	if _, exists := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl); exists {
		return
	}
	if common.GetContextKeyInt(c, constant.ContextKeyUserStreamResponseTimeout) == 0 &&
		common.GetContextKeyInt(c, constant.ContextKeyUserStreamTotalTimeout) == 0 &&
		common.GetContextKeyInt(c, constant.ContextKeyUserNonStreamResponseTimeout) == 0 &&
		common.GetContextKeyInt(c, constant.ContextKeyUserNonStreamTotalTimeout) == 0 {
		return
	}
	common.SetContextKey(c, constant.ContextKeyIsStream, isStream)
	responseSeconds, totalSeconds := effectiveRelayTimeouts(c, isStream)
	// Taking the request over disables the legacy guards for it: the stream
	// scanner drops its STREAMING_TIMEOUT ticker and the shared client loses
	// RELAY_TIMEOUT. So only take over when this mode actually enforces
	// something, or when the user explicitly asked for no limit with -1.
	// Otherwise a user whose override applies to the *other* mode — or whose
	// inherited global default is 0 — would end up with no time bound at all,
	// which is strictly weaker than not having the feature.
	if responseSeconds == 0 && totalSeconds == 0 {
		userResponse, userTotal := relayTimeoutUserOverrides(c, isStream)
		if userResponse != service.RelayTimeoutUnlimited && userTotal != service.RelayTimeoutUnlimited {
			return
		}
	}
	ctx, control := newRelayTimeoutControl(
		c.Request.Context(),
		time.Duration(responseSeconds)*time.Second,
		time.Duration(totalSeconds)*time.Second,
		common.GetContextKeyString(c, constant.ContextKeyUserStreamResponseTimeoutMode),
	)
	ctx = service.WithManagedRelayTimeoutContext(ctx, totalSeconds > 0)
	common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, responseSeconds)
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, totalSeconds)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	writer := &relayTimeoutResponseWriter{ResponseWriter: c.Writer}
	writer.state.Store(&relayTimeoutWriterState{control: control, isStream: isStream})
	c.Writer = writer

	c.Request = c.Request.WithContext(ctx)
}

// RelayRequestTimeout installs the response observer and owns cleanup. Timers
// are started later by StartRelayRequestTimeout once stream mode is known.
func RelayRequestTimeout() gin.HandlerFunc {
	return func(c *gin.Context) {
		setting := operation_setting.GetRelayTimeoutSnapshot()
		if setting == nil || !setting.Enabled {
			c.Next()
			return
		}
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting, setting)
		defer func() {
			if control, ok := relayTimeoutControlFromContext(c); ok {
				if control.lateWriteRejected.Load() {
					logger.LogWarn(c, "relay timeout rejected response output after the deadline")
				}
				control.Close()
			}
		}()
		c.Next()
	}
}

func IsRelayRequestTimeout(c *gin.Context) bool {
	return RelayRequestTimeoutKind(c) != ""
}

func RelayRequestTimeoutKind(c *gin.Context) string {
	return service.RelayRequestTimeoutKind(c)
}

func RelayRequestTimeoutSeconds(c *gin.Context) int {
	if RelayRequestTimeoutKind(c) == string(relayTimeoutKindResponse) {
		return common.GetContextKeyInt(c, constant.ContextKeyRelayResponseTimeoutSeconds)
	}
	return common.GetContextKeyInt(c, constant.ContextKeyRelayTotalTimeoutSeconds)
}
