package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end retry billing through the real middleware chain (TokenAuth →
// RelayRequestTimeout → Distribute) and controller.Relay, against an httptest
// upstream (Rule 15.4). Design relay-retry-time-budget.md §7/§15 asks for these
// assertions; its §17 left end-to-end amounts to staging:
//   - pre-consume happens once, before the loop, not once per attempt;
//   - every gate-terminated failure refunds in full;
//   - a success after retries is charged exactly once.
//
// The model is priced per call (ModelPrice), so the pre-consumed amount equals
// the settled amount and the checks do not depend on token counting.

const retryAuditPricePerCall = 0.01 // USD per call → 0.01 * QuotaPerUnit quota

// retryAuditHang makes the fake upstream hold the request open.
const retryAuditHang = -1

// retryAuditSSEThenHang makes it stream one chunk and then hold the request.
const retryAuditSSEThenHang = -2

// retryAuditSSEError answers a stream request with 200 and an in-band error
// frame before any content.
const retryAuditSSEError = -3

// retryAuditSlowOK answers 200 after retryAuditSlowDelay, even if the caller
// has gone by then (as a real upstream that bills the generation does).
const retryAuditSlowOK = -4

// retryAuditSlow500 answers a retryable 500 after retryAuditSlowDelay, even if
// the caller has gone by then.
const retryAuditSlow500 = -5

const retryAuditSlowDelay = 400 * time.Millisecond

type retryAuditFixture struct {
	user      *model.User
	token     *model.Token
	modelName string
	quota     int
	upstream  *httptest.Server
	engine    *gin.Engine

	// Indexed by upstream call, which is not always the retry attempt: an
	// adapted call's 500 adds a plain re-send within the same attempt.
	mu                sync.Mutex
	hits              int
	quotaAtAttempt    []int
	statusByAttempt   []int
	defaultStatusCode int
}

func (f *retryAuditFixture) record(userQuota int) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits++
	f.quotaAtAttempt = append(f.quotaAtAttempt, userQuota)
	status := f.defaultStatusCode
	if f.hits <= len(f.statusByAttempt) {
		status = f.statusByAttempt[f.hits-1]
	}
	return f.hits, status
}

func (f *retryAuditFixture) snapshot() (int, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits, append([]int(nil), f.quotaAtAttempt...)
}

func retryAuditStoredQuotas(t *testing.T, userID, tokenID int) (userQuota, tokenQuota int) {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.Select("quota").First(&u, userID).Error)
	var tk model.Token
	require.NoError(t, model.DB.Select("remain_quota").First(&tk, tokenID).Error)
	return u.Quota, tk.RemainQuota
}

func newRetryAuditFixture(t *testing.T, statuses []int, defaultStatus int) *retryAuditFixture {
	t.Helper()
	requireDB(t)

	f := &retryAuditFixture{
		modelName:         "ba-retry-" + strings.ReplaceAll(uniq("m"), "_", "-"),
		quota:             1_000_000, // below the trust quota, so pre-consume really happens
		statusByAttempt:   statuses,
		defaultStatusCode: defaultStatus,
	}

	// Global state touched by the relay chain, restored afterwards.
	prevPrice := ratio_setting.ModelPrice2JSONString()
	prevRetry := common.RetryTimes
	prevMemCache := common.MemoryCacheEnabled
	prevErrLog := constant.ErrorLogEnabled
	prevLogConsume := common.LogConsumeEnabled
	prevGroups := operation_setting.GroupRetryTimes2JSONString()
	prevTimeout := operation_setting.GetRelayTimeoutSetting()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(prevPrice)
		common.RetryTimes = prevRetry
		common.MemoryCacheEnabled = prevMemCache
		constant.ErrorLogEnabled = prevErrLog
		common.LogConsumeEnabled = prevLogConsume
		_ = operation_setting.UpdateGroupRetryTimesByJSONString(prevGroups)
		operation_setting.ReplaceRelayTimeoutSetting(prevTimeout)
	})
	common.MemoryCacheEnabled = false
	constant.ErrorLogEnabled = false
	common.LogConsumeEnabled = false
	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`{}`))
	timeout := prevTimeout
	timeout.Enabled = true
	timeout.MaxTotalAttempts = 0
	timeout.RetryMinBudgetSeconds = 0
	operation_setting.ReplaceRelayTimeoutSetting(timeout)

	prices := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(prevPrice, &prices))
	prices[f.modelName] = retryAuditPricePerCall
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encoded)))

	f.user = mkUser(t, func(u *model.User) { u.Quota = f.quota })
	f.token = mkToken(t, f.user.Id, func(tk *model.Token) { tk.RemainQuota = f.quota })

	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		wantsStream := bytes.Contains(bodyBytes, []byte(`"stream":true`))
		var u model.User
		_ = model.DB.Select("quota").First(&u, f.user.Id).Error
		_, status := f.record(u.Quota)
		if status == retryAuditSSEThenHang && wantsStream {
			// Streams one content chunk, then stalls: an adapted non-stream
			// attempt that has partial output when its deadline fires. A
			// plain call gets nothing before its deadline.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-ba","object":"chat.completion.chunk","created":1,"model":"`+f.modelName+
				`","choices":[{"index":0,"delta":{"role":"assistant","content":"partial answer"}}]}`+"\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			status = retryAuditHang
		}
		if status == retryAuditHang || status == retryAuditSSEThenHang {
			// Never answers within the test's response timeout.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		if status == retryAuditSlowOK {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(retryAuditSlowDelay):
			}
			status = http.StatusOK
		}
		if status == retryAuditSlow500 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(retryAuditSlowDelay):
			}
			status = http.StatusInternalServerError
		}
		if wantsStream && (status == http.StatusOK || status == retryAuditSSEError) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if status == retryAuditSSEError {
				_, _ = io.WriteString(w, `data: {"error":{"message":"upstream stream boom","type":"server_error","code":"boom"}}`+"\n\n")
				return
			}
			chunk := `data: {"id":"chatcmpl-ba","object":"chat.completion.chunk","created":1,"model":"` + f.modelName + `",`
			_, _ = io.WriteString(w,
				chunk+`"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`+"\n\n"+
					chunk+`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
					chunk+`"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`+"\n\n"+
					"data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = io.WriteString(w, `{"id":"chatcmpl-ba","object":"chat.completion","created":1,"model":"`+f.modelName+
				`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
				`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
			return
		}
		_, _ = io.WriteString(w, `{"error":{"message":"upstream boom","type":"server_error","code":"boom"}}`)
	}))
	t.Cleanup(f.upstream.Close)

	baseURL := f.upstream.URL
	autoBan := 0
	priority := int64(0)
	weight := uint(0)
	channel := mkChannel(t, func(ch *model.Channel) {
		ch.Type = constant.ChannelTypeOpenAI
		ch.Key = "sk-upstream-test"
		ch.BaseURL = &baseURL
		ch.Models = f.modelName
		ch.Group = "default"
		ch.AutoBan = &autoBan
		ch.Priority = &priority
		ch.Weight = &weight
	})
	require.NoError(t, channel.AddAbilities(nil))
	t.Cleanup(func() { model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}) })

	f.engine = gin.New()
	f.engine.Use(middleware.TokenAuth(), middleware.RelayRequestTimeout(), middleware.Distribute())
	f.engine.POST("/v1/chat/completions", func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
	return f
}

func (f *retryAuditFixture) send(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return f.sendBody(t, `{"model":"`+f.modelName+`","messages":[{"role":"user","content":"hi"}]}`)
}

func (f *retryAuditFixture) sendStream(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return f.sendBody(t, `{"model":"`+f.modelName+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
}

func (f *retryAuditFixture) sendBody(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	return rec
}

// Refunds run on a gopool goroutine; wait for the stored balances.
func (f *retryAuditFixture) waitForQuotas(t *testing.T, wantUser, wantToken int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		userQuota, tokenQuota := retryAuditStoredQuotas(t, f.user.Id, f.token.Id)
		if userQuota == wantUser && tokenQuota == wantToken {
			return
		}
		if time.Now().After(deadline) {
			require.Failf(t, "balances did not settle", "user=%d (want %d) token=%d (want %d)", userQuota, wantUser, tokenQuota, wantToken)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func retryAuditPrice() int {
	return int(retryAuditPricePerCall * common.QuotaPerUnit)
}

// All attempts fail with a retryable 500: attempts = RetryTimes+1 (main's
// count), the quota is pre-consumed once for the whole loop, and the user and
// token are refunded in full.
func TestBranchAuditRelayRetryExhaustedRefundsOncePreConsumedOnce(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
	common.RetryTimes = 2

	rec := f.send(t)
	assert.NotEqual(t, http.StatusOK, rec.Code, rec.Body.String())

	hits, quotaSeen := f.snapshot()
	require.Equal(t, 3, hits, "RetryTimes=2 means one attempt plus two retries")
	for i, seen := range quotaSeen {
		assert.Equal(t, f.quota-retryAuditPrice(), seen,
			"attempt %d: pre-consume must happen once for the request, not per attempt", i+1)
	}
	f.waitForQuotas(t, f.quota, f.quota)
}

// Each gate that ends the loop early (group quota -1, user override, total
// cap) must still return the last real error and refund in full.
func TestBranchAuditRelayRetryGatesRefundInFull(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *retryAuditFixture)
		want  int
	}{
		{"group quota -1", func(t *testing.T, f *retryAuditFixture) {
			require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`{"default":-1}`))
		}, 1},
		{"user override 1 beats global 4", func(t *testing.T, f *retryAuditFixture) {
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.user.Id).Update("retry_times", 1).Error)
		}, 2},
		{"total attempt cap 2", func(t *testing.T, f *retryAuditFixture) {
			s := operation_setting.GetRelayTimeoutSetting()
			s.MaxTotalAttempts = 2
			operation_setting.ReplaceRelayTimeoutSetting(s)
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
			common.RetryTimes = 4
			tc.setup(t, f)

			rec := f.send(t)
			assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "upstream boom", "the last real upstream error is returned, not a gate error")

			hits, _ := f.snapshot()
			assert.Equal(t, tc.want, hits)
			f.waitForQuotas(t, f.quota, f.quota)
		})
	}
}

// Two retryable failures then a success: charged exactly once (no refund of
// the success, no second pre-consume for the retries).
func TestBranchAuditRelayRetrySuccessChargedOnce(t *testing.T) {
	f := newRetryAuditFixture(t, []int{http.StatusInternalServerError, http.StatusInternalServerError}, http.StatusOK)
	common.RetryTimes = 3

	rec := f.send(t)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	hits, quotaSeen := f.snapshot()
	require.Equal(t, 3, hits)
	for i, seen := range quotaSeen {
		assert.Equal(t, f.quota-retryAuditPrice(), seen, "attempt %d", i+1)
	}
	// Give an erroneous async refund time to land before asserting it did not.
	time.Sleep(200 * time.Millisecond)
	f.waitForQuotas(t, f.quota-retryAuditPrice(), f.quota-retryAuditPrice())
}

// A non-retryable upstream status ends the loop after one attempt with a full
// refund, regardless of the configured quota.
func TestBranchAuditRelayNonRetryableStatusStopsImmediately(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusBadRequest)
	common.RetryTimes = 5
	require.False(t, operation_setting.ShouldRetryByStatusCode(http.StatusBadRequest), "fixture assumption")

	rec := f.send(t)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	hits, _ := f.snapshot()
	assert.Equal(t, 1, hits)
	f.waitForQuotas(t, f.quota, f.quota)
}

func (f *retryAuditFixture) setUserColumns(t *testing.T, columns map[string]any) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.user.Id).Updates(columns).Error)
}

// A retry that then times out: the retryable failure is followed by one
// attempt that exceeds the user's non-stream response window. The timeout
// carries SkipRetry, so no third attempt starts even though quota remains,
// the client gets 504, and the default refund billing returns the quota.
func TestBranchAuditRelayTimeoutAfterRetryRefunds(t *testing.T) {
	f := newRetryAuditFixture(t, []int{http.StatusInternalServerError, retryAuditHang}, http.StatusOK)
	common.RetryTimes = 3
	f.setUserColumns(t, map[string]any{"non_stream_response_timeout": 1})

	started := time.Now()
	rec := f.send(t)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code, rec.Body.String())
	assert.Less(t, time.Since(started), 4*time.Second, "the response window, not the upstream, must end the request")

	hits, _ := f.snapshot()
	assert.Equal(t, 2, hits, "a timed-out attempt is never retried")
	f.waitForQuotas(t, f.quota, f.quota)
}

// The remaining-budget gate end to end: a 3s total budget is below the 10s
// minimum, so after the first retryable failure no retry starts; the request
// returns that failure and refunds.
func TestBranchAuditRelayBudgetGateStopsRetryAndRefunds(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
	common.RetryTimes = 3
	s := operation_setting.GetRelayTimeoutSetting()
	s.RetryMinBudgetSeconds = 10
	operation_setting.ReplaceRelayTimeoutSetting(s)
	f.setUserColumns(t, map[string]any{"non_stream_total_timeout": 3})

	rec := f.send(t)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "upstream boom")
	hits, _ := f.snapshot()
	assert.Equal(t, 1, hits)
	f.waitForQuotas(t, f.quota, f.quota)
}

// Control for the case above: the same user without a total timeout has no
// budget to measure, so the gate stays out of the way.
func TestBranchAuditRelayBudgetGateInertWithoutTotalTimeout(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
	common.RetryTimes = 3
	s := operation_setting.GetRelayTimeoutSetting()
	s.RetryMinBudgetSeconds = 10
	operation_setting.ReplaceRelayTimeoutSetting(s)
	f.setUserColumns(t, map[string]any{"non_stream_response_timeout": 30})

	rec := f.send(t)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	hits, _ := f.snapshot()
	assert.Equal(t, 4, hits)
	f.waitForQuotas(t, f.quota, f.quota)
}

// Design §7.4 cross test: a user on non_stream_timeout_billing=charge whose
// first attempt fails retryably and whose second (adapted to an upstream
// stream) times out with partial output. Only that last attempt is settled —
// once — and the earlier failure costs nothing; with refund billing the same
// sequence costs nothing at all.
//
// For a charge user the first attempt is two upstream calls: the adapted call
// answered 500, then fallBackFromRejectedAdaptation's plain re-send on the same
// channel (documented in non-stream-timeout-loss-prevention.md §17 #1).
func TestBranchAuditRelayChargeTimeoutAfterRetrySettlesOnce(t *testing.T) {
	for _, tc := range []struct {
		billing    string
		script     []int
		wantStatus int
		wantHits   int
		wantSpent  int
	}{
		// charge: the partial output is delivered as a normal completion
		// (finish_reason "length", design §2) and paid for once.
		{"charge", []int{http.StatusInternalServerError, http.StatusInternalServerError, retryAuditSSEThenHang}, http.StatusOK, 3, retryAuditPrice()},
		// refund: the plain call has nothing before its deadline → 504, free.
		{"refund", []int{http.StatusInternalServerError, retryAuditSSEThenHang}, http.StatusGatewayTimeout, 2, 0},
	} {
		t.Run(tc.billing, func(t *testing.T) {
			f := newRetryAuditFixture(t, tc.script, http.StatusOK)
			common.RetryTimes = 3
			f.setUserColumns(t, map[string]any{
				"non_stream_response_timeout": 1,
				"non_stream_timeout_billing":  tc.billing,
			})

			rec := f.send(t)
			assert.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantStatus == http.StatusOK {
				assert.Contains(t, rec.Body.String(), `"finish_reason":"length"`)
				assert.Contains(t, rec.Body.String(), "partial answer")
			}
			hits, _ := f.snapshot()
			assert.Equal(t, tc.wantHits, hits, "a timed-out attempt is never retried")

			time.Sleep(200 * time.Millisecond)
			f.waitForQuotas(t, f.quota-tc.wantSpent, f.quota-tc.wantSpent)
		})
	}
}

// The plain re-send after an adapted call's 500 is another upstream call. It
// does not consume the per-group retry quota, but it must consume the request's
// max_total_attempts budget. With a cap of two, the first adapted call and its
// plain fallback exhaust the request and no ordinary retry starts.
func TestBranchAuditRelayChargeUserPlainResendConsumesTotalCallBudget(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
	common.RetryTimes = 5
	s := operation_setting.GetRelayTimeoutSetting()
	s.MaxTotalAttempts = 2
	operation_setting.ReplaceRelayTimeoutSetting(s)
	f.setUserColumns(t, map[string]any{
		"non_stream_response_timeout": 30,
		"non_stream_timeout_billing":  "charge",
	})

	rec := f.send(t)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	hits, _ := f.snapshot()
	assert.Equal(t, 2, hits, "the adapted call and plain fallback consume the full two-call budget")
	f.waitForQuotas(t, f.quota, f.quota)
}

// Stream requests take the stream-session path (service/stream_lifecycle.go)
// on every attempt. A retryable status before the stream opens, then a
// successful stream: delivered once, settled once.
func TestBranchAuditRelayStreamRetrySuccessChargedOnce(t *testing.T) {
	f := newRetryAuditFixture(t, []int{http.StatusInternalServerError}, http.StatusOK)
	common.RetryTimes = 2

	rec := f.sendStream(t)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"content":"ok"`)
	assert.Equal(t, 1, strings.Count(rec.Body.String(), `"content":"ok"`), "only the successful attempt reaches the client")
	hits, quotaSeen := f.snapshot()
	require.Equal(t, 2, hits)
	for i, seen := range quotaSeen {
		assert.Equal(t, f.quota-retryAuditPrice(), seen, "attempt %d", i+1)
	}
	time.Sleep(200 * time.Millisecond)
	f.waitForQuotas(t, f.quota-retryAuditPrice(), f.quota-retryAuditPrice())
}

// Every stream attempt fails before the upstream stream opens: the error is
// written by the controller (not as SSE), and the quota is refunded.
func TestBranchAuditRelayStreamRetryExhaustedRefunds(t *testing.T) {
	f := newRetryAuditFixture(t, nil, http.StatusInternalServerError)
	common.RetryTimes = 2

	rec := f.sendStream(t)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "upstream boom")
	hits, _ := f.snapshot()
	assert.Equal(t, 3, hits)
	f.waitForQuotas(t, f.quota, f.quota)
}

// An upstream that opens the stream (200) and then sends only an in-band
// error frame. The downstream stream is already committed, so the request ends
// there — no retry even though quota remains — the error frame reaches the
// client, and with no content and no usage nothing is charged.
func TestBranchAuditRelayStreamInBandErrorEndsWithoutRetryOrCharge(t *testing.T) {
	f := newRetryAuditFixture(t, []int{retryAuditSSEError}, http.StatusOK)
	common.RetryTimes = 2

	rec := f.sendStream(t)
	body := rec.Body.String()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body, "upstream stream boom")
	assert.NotContains(t, body, `"content":"ok"`)
	hits, _ := f.snapshot()
	assert.Equal(t, 1, hits)

	time.Sleep(200 * time.Millisecond)
	f.waitForQuotas(t, f.quota, f.quota)
}

// sendAndLeave sends a non-stream request whose client disconnects after leaveAfter.
func (f *retryAuditFixture) sendAndLeave(t *testing.T, leaveAfter time.Duration) time.Duration {
	t.Helper()
	return f.sendBodyAndLeave(t, `{"model":"`+f.modelName+`","messages":[{"role":"user","content":"hi"}]}`, leaveAfter)
}

func (f *retryAuditFixture) sendBodyAndLeave(t *testing.T, body string, leaveAfter time.Duration) time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
	time.AfterFunc(leaveAfter, cancel)
	started := time.Now()
	f.engine.ServeHTTP(httptest.NewRecorder(), req)
	return time.Since(started)
}

// A managed request whose client goes away mid-attempt: the client leaving does
// not cancel the upstream call (the upstream bills it either way, and main's
// upstream call is never bound to the client), the user's own deadline still
// bounds it, and the ended attempt is not retried for a client that has gone.
// The upstream never answers here, so the deadline refunds as usual.
func TestBranchAuditRelayClientCancelKeepsUpstreamUntilDeadline(t *testing.T) {
	f := newRetryAuditFixture(t, []int{retryAuditHang}, http.StatusInternalServerError)
	common.RetryTimes = 3
	f.setUserColumns(t, map[string]any{"non_stream_response_timeout": 1})

	elapsed := f.sendAndLeave(t, 150*time.Millisecond)
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond, "the client leaving must not cancel the upstream call")
	assert.Less(t, elapsed, 4*time.Second, "the user's deadline still bounds the upstream call")

	hits, _ := f.snapshot()
	assert.Equal(t, 1, hits, "no retry after the client has gone")
	f.waitForQuotas(t, f.quota, f.quota)
}

// Regression (relay-control report: a managed non-stream request cancelled
// the upstream call and refunded when the client left, while the upstream
// billed it): the upstream answers after the client has gone, and the request
// is settled for it like on main instead of refunded.
func TestBranchAuditRelayClientCancelBillsCompletedNonStreamUpstream(t *testing.T) {
	f := newRetryAuditFixture(t, []int{retryAuditSlowOK}, http.StatusInternalServerError)
	common.RetryTimes = 3
	f.setUserColumns(t, map[string]any{"non_stream_response_timeout": 30})

	elapsed := f.sendAndLeave(t, 100*time.Millisecond)
	assert.GreaterOrEqual(t, elapsed, retryAuditSlowDelay-50*time.Millisecond, "the upstream answer must be awaited")

	hits, _ := f.snapshot()
	assert.Equal(t, 1, hits)
	// Pre-consume equals the per-call charge; give an asynchronous refund time to land.
	time.Sleep(300 * time.Millisecond)
	f.waitForQuotas(t, f.quota-retryAuditPrice(), f.quota-retryAuditPrice())
}

// Regression (openai-format D-3, DC1/DC3): a stream whose client leaves before
// the first byte was released (charged 0) while the upstream billed it. The
// upstream accepted the request, so it is settled like main (estimated prompt;
// here the per-call price) — with and without the per-user timeout feature.
func TestBranchAuditRelayStreamClientGoneBeforeFirstByteIsCharged(t *testing.T) {
	// The charge is the estimated prompt, so local token counting must be on
	// (it is by default in production; the test environment leaves it off).
	prevCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = prevCountToken })
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			f := newRetryAuditFixture(t, []int{retryAuditSlowOK}, http.StatusInternalServerError)
			common.RetryTimes = 3
			if managed {
				f.setUserColumns(t, map[string]any{"stream_response_timeout": 30})
			}
			useRetryAuditRatioPricing(t, f)
			// max_tokens makes the pre-consume larger than the settled estimate, so a
			// settlement is distinguishable from both a release and a kept pre-consume.
			f.sendBodyAndLeave(t, `{"model":"`+f.modelName+`","stream":true,"max_tokens":2000,"messages":[{"role":"user","content":"hi"}]}`, 100*time.Millisecond)
			hits, quotaAtHit := f.snapshot()
			require.Equal(t, 1, hits, "no retry for a client that has gone")
			require.Less(t, quotaAtHit[0], f.quota, "precondition: quota was pre-consumed")
			deadline := time.Now().Add(3 * time.Second)
			for {
				userQuota, tokenQuota := retryAuditStoredQuotas(t, f.user.Id, f.token.Id)
				if userQuota > quotaAtHit[0] && userQuota < f.quota && tokenQuota == userQuota {
					break // settled: part of the pre-consume returned, the estimated prompt kept
				}
				require.False(t, time.Now().After(deadline), "not settled: user=%d token=%d pre-consumed=%d full=%d", userQuota, tokenQuota, quotaAtHit[0], f.quota)
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// useRetryAuditRatioPricing prices the fixture's model by token ratio instead
// of per call, so an estimated-usage settlement differs from the pre-consume.
func useRetryAuditRatioPricing(t *testing.T, f *retryAuditFixture) {
	t.Helper()
	prevPrice := ratio_setting.ModelPrice2JSONString()
	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(prevPrice)
		_ = ratio_setting.UpdateModelRatioByJSONString(prevRatio)
	})
	prices := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(prevPrice, &prices))
	delete(prices, f.modelName)
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encoded)))
	ratios := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(prevRatio, &ratios))
	ratios[f.modelName] = 1
	encoded, err = common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
}

// Regression (review L2): the first attempt fails with a retryable 500 after
// the client has gone. No second attempt starts — it would cost upstream calls
// for an answer nobody reads (and a managed attempt would be refused before
// sending, logging a channel error against an untouched channel) — and the
// failed request is refunded.
func TestBranchAuditRelayNoRetryAfterClientLeft(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			f := newRetryAuditFixture(t, []int{retryAuditSlow500}, http.StatusOK)
			common.RetryTimes = 3
			if managed {
				f.setUserColumns(t, map[string]any{"non_stream_response_timeout": 30})
			}
			f.sendAndLeave(t, 100*time.Millisecond)
			time.Sleep(100 * time.Millisecond)
			hits, _ := f.snapshot()
			assert.Equal(t, 1, hits, "no retry for a client that has gone")
			f.waitForQuotas(t, f.quota, f.quota)
		})
	}
}
