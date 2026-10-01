package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	MaxRelayTimeoutSeconds                    = 7 * 24 * 60 * 60
	RelayTimeoutUnlimited                     = -1
	RelayStreamResponseTimeoutModeFirstOutput = constant.RelayStreamResponseTimeoutModeFirstOutput
	RelayStreamResponseTimeoutModeIdle        = constant.RelayStreamResponseTimeoutModeIdle
	NonStreamTimeoutBillingRefund             = constant.NonStreamTimeoutBillingRefund
	NonStreamTimeoutBillingCharge             = constant.NonStreamTimeoutBillingCharge
	NonStreamTimeoutBillingInput              = constant.NonStreamTimeoutBillingInput
)

// NormalizeNonStreamTimeoutBilling maps a stored value to a known mode; unknown
// and empty values are refund, the default.
func NormalizeNonStreamTimeoutBilling(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case NonStreamTimeoutBillingCharge:
		return NonStreamTimeoutBillingCharge
	case NonStreamTimeoutBillingInput:
		return NonStreamTimeoutBillingInput
	default:
		return NonStreamTimeoutBillingRefund
	}
}

func ValidateNonStreamTimeoutBilling(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", NonStreamTimeoutBillingRefund, NonStreamTimeoutBillingCharge, NonStreamTimeoutBillingInput:
		return nil
	default:
		return fmt.Errorf("non stream timeout billing is invalid")
	}
}

// NonStreamTimeoutChargesUsage reports whether this request must settle received
// usage instead of refunding when its deadline fires.
func NonStreamTimeoutChargesUsage(c *gin.Context) bool {
	return RelayTimeoutBillingMode(c) == NonStreamTimeoutBillingCharge
}

// RelayTimeoutBillingMode returns the user's normalized timeout billing mode
// (non_stream_timeout_billing), which governs every request our own deadline
// ends: refund, charge or input.
func RelayTimeoutBillingMode(c *gin.Context) string {
	if c == nil {
		return NonStreamTimeoutBillingRefund
	}
	return NormalizeNonStreamTimeoutBilling(common.GetContextKeyString(c, constant.ContextKeyUserNonStreamTimeoutBilling))
}

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

// WriteRelayTerminalError lets a request send its one terminal payload after a
// managed deadline — a stream's in-band error, or the partial completion of an
// adapted non-stream request — without reopening the response for ordinary
// content.
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

// BindRelayRequestContext lets the request-local timeout cancel the upstream
// call without letting the client's disconnect cancel it. Unmanaged callers and
// the original request are left unchanged.
//
// Why not the client's context: once the upstream has the request it bills it,
// whether or not we stay to read the answer. Cancelling on disconnect made a
// managed request settle nothing (a non-stream request was refunded in full, a
// stream released its reservation before any byte arrived) while upstream still
// charged us — the one path where the fork billed less than main, whose upstream
// request is never bound to the client (http.NewRequest). Now a disconnected
// non-stream call runs to completion and bills its real usage, as on main; a
// stream still stops reading as soon as the client leaves (the stream handler
// closes the body), as on main, and bills the prompt plus what was received.
//
// The timeout keeps its meaning after the client left: the call is cancelled at
// the deadline the controller would have fired at. A client already gone before
// anything was sent gets the cancelled context, so nothing is sent and the
// reservation is released.
func BindRelayRequestContext(c *gin.Context, req *http.Request) *http.Request {
	if req == nil || c == nil || c.Request == nil || !IsRelayTimeoutManaged(c) {
		return req
	}
	link, parent := newRelayUpstreamLink(c)
	if link == nil {
		return req.WithContext(parent)
	}
	return req.WithContext(context.WithValue(link.ctx, relayUpstreamLinkKey{}, link))
}

// RelayUpstreamContext is BindRelayRequestContext for upstream work that is not
// a single HTTP exchange: WebSocket dials and connection lifetimes, and task
// polling loops. The context ends on our own timeout (immediately while the
// client is connected, at the controller's deadline after it left), never on
// the client leaving — the upstream bills that work either way, and main does
// not tie it to the client. Unmanaged requests keep the legacy background
// context; a client already gone before the work starts gets the cancelled
// context so nothing is started. release must be called once the work is over
// (it is idempotent); it removes the watch and any pending deadline timer.
func RelayUpstreamContext(c *gin.Context) (context.Context, func()) {
	if c == nil || c.Request == nil || !IsRelayTimeoutManaged(c) {
		return context.Background(), func() {}
	}
	link, parent := newRelayUpstreamLink(c)
	if link == nil {
		return parent, func() {}
	}
	link.enter()
	var once sync.Once
	return link.ctx, func() { once.Do(link.exit) }
}

// newRelayUpstreamLink links a detached upstream context to the request's
// timeout controller. It returns nil (with the client context) when the client
// has already gone or the controller cannot report its deadline.
func newRelayUpstreamLink(c *gin.Context) (*relayUpstreamLink, context.Context) {
	parent := c.Request.Context()
	value, _ := relayTimeoutControlValue(c)
	control, ok := value.(relayUpstreamControl)
	if !ok || parent.Err() != nil {
		return nil, parent
	}
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	link := &relayUpstreamLink{parent: parent, ctx: ctx, cancel: cancel, control: control}
	link.closer, _ = value.(relayCloseNotifier)
	link.mu.Lock()
	link.watchLocked()
	link.watchCloseLocked()
	link.mu.Unlock()
	return link, parent
}

// relayUpstreamControl is the part of the request's timeout controller a bound
// upstream call follows: whether our own deadline fired, and when the next one
// is due (false once the request has finished or has no limit left).
type relayUpstreamControl interface {
	relayTimeoutKindReader
	RelayTimeoutDeadline() (time.Time, bool)
}

// relayCloseNotifier is the controller's "request finished" signal. Once the client has
// left and no deadline remains, RelayTimeoutDeadline cannot tell a finished
// request from an unlimited one, so linked work listens for Close directly:
// no upstream work may outlive the handler.
type relayCloseNotifier interface {
	AfterRelayClose(func()) (stop func() bool, registered bool)
}

// errRelayRequestFinished cancels linked upstream work still open when the request ends.
var errRelayRequestFinished = errors.New("relay request finished")

type relayUpstreamLinkKey struct{}

// relayUpstreamDeadlineRecheck is the shortest wait before re-reading the
// controller once its deadline is due: the controller's own timer records the
// expiry a moment after the deadline, and the call is cancelled only then, so
// the handler classifies the end as a timeout rather than as the client leaving.
const relayUpstreamDeadlineRecheck = 10 * time.Millisecond

// relayUpstreamDeadlineGrace bounds that wait: a deadline this far in the past
// with no expiry recorded is treated as expired.
const relayUpstreamDeadlineGrace = time.Second

// relayUpstreamLink ties one bound upstream call to the timeout controller.
// The parent watch (context.AfterFunc, no goroutine until the parent is done)
// is removed once the call's response body is closed (relayUpstreamTransport),
// so a request that ends normally leaves no goroutine or timer behind. When the
// request finishes (the controller closes) any linked work still open is
// cancelled: detaching from the client never lets upstream work outlive the handler.
type relayUpstreamLink struct {
	parent  context.Context
	ctx     context.Context // the detached upstream context this link cancels
	cancel  context.CancelCauseFunc
	control relayUpstreamControl
	closer  relayCloseNotifier // nil when the controller cannot report Close

	mu        sync.Mutex
	stopClose func() bool // removes the request-finished hook; nil when not registered
	finished  bool        // the request has finished; the context is cancelled for good
	tracked   bool        // exchanges are counted (relayUpstreamTransport or RelayUpstreamContext)
	inflight  int         // exchanges (redirect hops) whose body is still open
	stopWatch func() bool // removes the parent watch; nil when not watching
	timer     *time.Timer // the controller's deadline, kept after the client left
}

func (l *relayUpstreamLink) watchLocked() {
	l.stopWatch = context.AfterFunc(l.parent, l.parentDone)
}

// watchCloseLocked registers the request-finished hook; a request that already
// finished cancels the context at once.
func (l *relayUpstreamLink) watchCloseLocked() {
	if l.closer == nil || l.finished || l.stopClose != nil {
		return
	}
	stop, registered := l.closer.AfterRelayClose(l.requestFinished)
	if !registered {
		l.finishLocked()
		return
	}
	l.stopClose = stop
}

// requestFinished runs when the controller closes (the handler has returned).
func (l *relayUpstreamLink) requestFinished() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopClose = nil
	l.finishLocked()
}

func (l *relayUpstreamLink) finishLocked() {
	l.finished = true
	l.cancel(errRelayRequestFinished)
	if l.stopWatch != nil {
		l.stopWatch()
		l.stopWatch = nil
	}
	if l.timer != nil {
		l.timer.Stop()
	}
}

// parentDone runs when the client's side ended: our timeout (cancel now), the
// client leaving (keep going until the controller's deadline), or the request
// finishing (handled by the request-finished hook).
func (l *relayUpstreamLink) parentDone() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopWatch = nil
	l.followDeadlineLocked()
}

func (l *relayUpstreamLink) deadlineDue() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.followDeadlineLocked()
}

func (l *relayUpstreamLink) followDeadlineLocked() {
	if l.tracked && l.inflight == 0 {
		return // the exchange is over; a redirect hop re-arms on entry
	}
	if l.control.RelayTimeoutKind() != "" {
		l.cancel(context.DeadlineExceeded)
		return
	}
	deadline, ok := l.control.RelayTimeoutDeadline()
	if !ok {
		return // no limit left: run until upstream finishes, as on main; the request-finished hook still ends it with the handler
	}
	if time.Since(deadline) > relayUpstreamDeadlineGrace {
		l.cancel(context.DeadlineExceeded)
		return
	}
	wait := max(time.Until(deadline), relayUpstreamDeadlineRecheck)
	if l.timer == nil {
		l.timer = time.AfterFunc(wait, l.deadlineDue)
		return
	}
	l.timer.Reset(wait)
}

// enter is called when an exchange (or redirect hop) starts.
func (l *relayUpstreamLink) enter() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tracked = true
	l.inflight++
	if l.finished {
		return // the context is already cancelled; the hop fails at once
	}
	if l.stopWatch == nil {
		// A later hop after the watch fired or was removed: watch again. A parent
		// that is already done runs parentDone right away.
		l.watchLocked()
	}
	l.watchCloseLocked()
}

// exit is called once per exchange when its body is closed or it failed.
func (l *relayUpstreamLink) exit() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight--
	if l.inflight > 0 {
		return
	}
	if l.stopWatch != nil {
		l.stopWatch()
		l.stopWatch = nil
	}
	if l.stopClose != nil {
		l.stopClose()
		l.stopClose = nil
	}
	if l.timer != nil {
		l.timer.Stop()
	}
}

// relayUpstreamTransport reports each bound exchange's start and end to its
// link. Requests that were not bound pass straight through.
type relayUpstreamTransport struct {
	base http.RoundTripper
}

func (t relayUpstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	link, _ := req.Context().Value(relayUpstreamLinkKey{}).(*relayUpstreamLink)
	if link == nil {
		return t.base.RoundTrip(req)
	}
	link.enter()
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		link.exit()
		return resp, err
	}
	resp.Body = &relayUpstreamBody{ReadCloser: resp.Body, link: link}
	return resp, nil
}

// CloseIdleConnections keeps http.Client.CloseIdleConnections reaching the pool.
func (t relayUpstreamTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type relayUpstreamBody struct {
	io.ReadCloser
	link *relayUpstreamLink
	once sync.Once
}

func (b *relayUpstreamBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.link.exit)
	return err
}

// RelayHTTPClient removes only the process-wide timeout for managed requests,
// allowing the user's request-local override to be authoritative, and routes
// the calls through relayUpstreamTransport so a bound call's timeout link is
// released with its body. The cloned client shares the original Transport and
// therefore its connection pool.
func RelayHTTPClient(c *gin.Context, client *http.Client) *http.Client {
	if client == nil || c == nil || !IsRelayTimeoutManaged(c) {
		return client
	}
	relayClient := *client
	relayClient.Timeout = 0
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	relayClient.Transport = relayUpstreamTransport{base: base}
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

// RelayTimeoutKindResponse names the response-timer expiry, as reported by
// RelayRequestTimeoutKind.
const RelayTimeoutKindResponse = "response_timeout"

// RelayRequestTimeoutSeconds returns the limit whose expiry ended this request,
// for the user-facing timeout message.
func RelayRequestTimeoutSeconds(c *gin.Context) int {
	if RelayRequestTimeoutKind(c) == RelayTimeoutKindResponse {
		return common.GetContextKeyInt(c, constant.ContextKeyRelayResponseTimeoutSeconds)
	}
	return common.GetContextKeyInt(c, constant.ContextKeyRelayTotalTimeoutSeconds)
}

// RelayResponseHolder is implemented by the timeout controller so the relay
// layer can keep the response timer running past the upstream's first byte.
type RelayResponseHolder interface {
	HoldRelayResponse()
}

// HoldRelayResponseTimer must be called when a non-stream request is adapted to
// stream upstream. Without it the first SSE chunk stops the response timer and
// the user's non_stream_response_timeout stops bounding anything — the request
// could then run for as long as upstream keeps emitting.
func HoldRelayResponseTimer(c *gin.Context) bool {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return false
	}
	holder, ok := value.(RelayResponseHolder)
	if !ok {
		return false
	}
	holder.HoldRelayResponse()
	return true
}

// RelayResponseReleaser is implemented by the timeout controller.
type RelayResponseReleaser interface {
	ReleaseRelayResponse() bool
}

// ReleaseRelayResponseTimer ends the hold once an adapted stream has delivered
// its whole answer and stops the response timer — the point where a plain
// non-stream call would have received upstream's response. Left running, the
// timer could fire during body assembly or settlement and turn a completed,
// billed request into a 504 whose body never reaches the client.
func ReleaseRelayResponseTimer(c *gin.Context) bool {
	value, ok := relayTimeoutControlValue(c)
	if !ok {
		return false
	}
	releaser, ok := value.(RelayResponseReleaser)
	return ok && releaser.ReleaseRelayResponse()
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
