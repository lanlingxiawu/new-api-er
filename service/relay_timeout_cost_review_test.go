package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressions from the independent review of relay-timeout-cost-bearing (§15):
// each test failed before its fix.

// F1: the total timer can fire after the handler read the upstream body and
// settled (PostTextConsumeQuota) but before the client write; the controller
// still normalizes the request to relay_timeout. The timeout paths must not
// settle a second time or record a fake absorbed cost.
func TestTimeoutAfterHandlerSettledIsNotSettledAgain(t *testing.T) {
	t.Run("input mode", func(t *testing.T) {
		userID := timeoutCostUserBase + 60
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, userID)
		PostTextConsumeQuota(c, info, &dto.Usage{PromptTokens: timeoutCostPrompt, CompletionTokens: 100, TotalTokens: timeoutCostPrompt + 100}, nil)
		require.Equal(t, 1, fixture.settles)
		assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Equal(t, 1, fixture.settles)
		assert.Len(t, logsFor(t, userID, model.LogTypeConsume), 1, "one consume log per request")
	})
	t.Run("refund mode records nothing", func(t *testing.T) {
		c, info, _ := nonStreamTimeoutContext(t, NonStreamTimeoutBillingRefund, timeoutCostUserBase+61)
		BindRelayTimeoutInfo(c, info)
		PostTextConsumeQuota(c, info, &dto.Usage{PromptTokens: timeoutCostPrompt, CompletionTokens: 100, TotalTokens: timeoutCostPrompt + 100}, nil)
		other := model.NewLogOther()
		AppendStreamErrorDiagnostic(c, other, timeoutCostErr())
		assert.NotContains(t, logOtherAdmin(other), "timeout_absorbed")
	})
}

// F4 (deadline between attempts): the last attempt answered with an upstream
// error and the deadline fired before the next one; nothing was cut in flight.
func TestTimeoutBetweenAttemptsIsNotCharged(t *testing.T) {
	c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, 0)
	info.LastError = types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
	assert.Zero(t, fixture.settles)
}

// F4: a request whose upstream write never completed (dial/TLS hang, slow
// upload) cost the upstream nothing: input mode refunds, the record says 0.
func TestUnsentRequestIsNotCharged(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, 0)
		require.NotNil(t, relayUpstreamWroteRequestTrace(c), "the exchange started but was never written")
		assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Zero(t, fixture.settles)

		BindRelayTimeoutInfo(c, info)
		other := model.NewLogOther()
		AppendStreamErrorDiagnostic(c, other, timeoutCostErr())
		absorbed := logOtherAdmin(other)["timeout_absorbed"].(map[string]any)
		assert.Equal(t, 0, absorbed["absorbed_quota_min"])
		assert.Equal(t, false, absorbed["request_sent"])
	})
	t.Run("stream", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput)
		relayUpstreamWroteRequestTrace(c)
		u := FinalizeStreamUsage(c, info, nil)
		assert.Equal(t, "none", info.StreamResult.UsageSource)
		assert.Zero(t, u.TotalTokens)
		assert.Equal(t, 0, streamAbsorbed(c)["absorbed_quota_min"])
	})
	t.Run("written exchange counts as sent", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, timeoutCostUserBase+62)
		relayUpstreamWroteRequestTrace(c)(httptrace.WroteRequestInfo{})
		assert.True(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Equal(t, 1, fixture.settles)
	})
	t.Run("a new attempt forgets the previous exchange", func(t *testing.T) {
		c, _, _ := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, 0)
		relayUpstreamWroteRequestTrace(c)
		require.False(t, relayUpstreamRequestSent(c))
		BeginRelayAttempt(c, &RetryParam{Ctx: c, Retry: common.GetPointer(0)})
		assert.True(t, relayUpstreamRequestSent(c), "no HTTP exchange this attempt: unknown counts as sent")
	})
}

// F4: the WroteRequest marker installed by RelayResponseTraceContext against
// real transports.
func TestRelayResponseTraceTracksWrittenRequest(t *testing.T) {
	managed := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
		return c
	}
	send := func(c *gin.Context, ctx context.Context, url string, body io.Reader) {
		req, err := http.NewRequestWithContext(RelayResponseTraceContext(c, ctx), http.MethodPost, url, body)
		require.NoError(t, err)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
	t.Run("answered", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }))
		t.Cleanup(server.Close)
		c := managed()
		send(c, context.Background(), server.URL, bytes.NewReader([]byte(`{"a":1}`)))
		assert.True(t, relayUpstreamRequestSent(c))
	})
	t.Run("connection refused", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := listener.Addr().String()
		require.NoError(t, listener.Close())
		c := managed()
		send(c, context.Background(), "http://"+addr, bytes.NewReader([]byte(`{"a":1}`)))
		assert.False(t, relayUpstreamRequestSent(c))
	})
	t.Run("upload cut by the deadline", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		t.Cleanup(func() { close(release); server.Close() })
		c := managed()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		send(c, ctx, server.URL, bytes.NewReader(make([]byte, 64<<20))) // never fits in socket buffers
		assert.False(t, relayUpstreamRequestSent(c))
	})
	t.Run("unmanaged requests are not traced", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		common.SetContextKey(c, constant.ContextKeyIsStream, true)
		parent := context.Background()
		assert.Equal(t, parent, RelayResponseTraceContext(c, parent))
	})
}

// F5: input only means no tool surcharges; surcharges the upstream reported are
// recorded as absorbed instead.
func TestInputOnlyExcludesToolSurcharges(t *testing.T) {
	t.Run("non-stream search-preview model", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, timeoutCostUserBase+63)
		info.OriginModelName = "gpt-4o-search-preview"
		require.True(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Equal(t, timeoutCostPrompt*timeoutCostRatio, fixture.actual, "the web_search_preview fee is not charged")
	})
	t.Run("stream with reported web searches", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput)
		c.Set("claude_web_search_requests", 2)
		surcharge := relayConfirmedToolSurchargeQuota(c, info)
		require.Positive(t, surcharge)
		fixture := &timeoutBillingFixture{pre: 1000}
		info.Billing = fixture
		usage := FinalizeStreamUsage(c, info, nil)
		assert.Equal(t, surcharge, streamAbsorbed(c)["absorbed_quota_min"], "no output received; the searches are absorbed")
		PostTextConsumeQuota(c, info, usage, nil)
		assert.Equal(t, 23, fixture.actual, "input only: 23 tokens at ratio 1, no search fee")
	})
}

// F7: an input that prices to 0 (free model) is refunded, not logged as a
// zero-quota "charged for input only" consume log.
func TestZeroInputChargeRefunds(t *testing.T) {
	for _, mode := range []string{NonStreamTimeoutBillingInput, NonStreamTimeoutBillingCharge} {
		t.Run(mode, func(t *testing.T) {
			userID := timeoutCostUserBase + 64
			c, info, fixture := nonStreamTimeoutContext(t, mode, userID)
			info.PriceData.ModelRatio = 0
			info.PriceData.FreeModel = true
			assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
			assert.Zero(t, fixture.settles)
			assert.Empty(t, logsFor(t, userID, model.LogTypeConsume))
		})
	}
}

var _ = relaycommon.RelayInfo{}

// D1 / decision 2026-10-01: requests cut before response headers use the same
// fallback as an unadapted non-stream request.
func TestPreHeaderTimeoutUsesInputFallback(t *testing.T) {
	t.Run("stream before headers settles input", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, timeoutCostUserBase+65)
		common.SetContextKey(c, constant.ContextKeyIsStream, true)
		info.IsStream = true
		info.StreamSession = relaycommon.NewStreamSession(info.RelayFormat)
		info.StreamSession.ResponseGate = &relaycommon.StreamResponseGate{} // never opened: no response headers
		require.True(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Equal(t, timeoutCostPrompt*timeoutCostRatio, fixture.actual)
		logs := logsFor(t, timeoutCostUserBase+65, model.LogTypeConsume)
		require.Len(t, logs, 1)
		absorbed := adminTimeoutAbsorbed(t, logs[0].Other)
		assert.Equal(t, "stream", absorbed["kind"])
	})
	t.Run("stream before headers, never written, refunds", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, 0)
		common.SetContextKey(c, constant.ContextKeyIsStream, true)
		info.StreamSession = relaycommon.NewStreamSession(info.RelayFormat)
		info.StreamSession.ResponseGate = &relaycommon.StreamResponseGate{}
		relayUpstreamWroteRequestTrace(c)
		assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Zero(t, fixture.settles)
	})
	t.Run("adapted charge before headers settles input", func(t *testing.T) {
		c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingCharge, timeoutCostUserBase+66)
		info.UpstreamStreamAdapted = true
		require.True(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
		assert.Equal(t, timeoutCostPrompt*timeoutCostRatio, fixture.actual)
	})
}
