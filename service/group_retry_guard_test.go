package service

import (
	"context"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
)

func BenchmarkRetryGuardUnmanagedTrace(b *testing.B) {
	old := operation_setting.GetGroupRetryGuardSetting()
	b.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			operation_setting.ReplaceGroupRetryGuardSetting(operation_setting.GroupRetryGuardSetting{Enabled: enabled, Rules: "{}"})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			parent := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resetRelayUpstreamSent(c)
				ctx := RelayResponseTraceContext(c, parent)
				if trace := httptrace.ContextClientTrace(ctx); trace != nil && trace.WroteRequest != nil {
					trace.WroteRequest(httptrace.WroteRequestInfo{})
				}
			}
		})
	}
}

func TestRetryGuardTracksUnmanagedUpstreamFailure(t *testing.T) {
	old := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"overloaded"}}`)
	}))
	t.Cleanup(upstream.Close)
	for _, enabled := range []bool{false, true} {
		operation_setting.ReplaceGroupRetryGuardSetting(operation_setting.GroupRetryGuardSetting{Enabled: enabled, Rules: "{}"})
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		require.False(t, IsRelayTimeoutManaged(c))
		req, err := http.NewRequestWithContext(RelayResponseTraceContext(c, context.Background()), http.MethodGet, upstream.URL, nil)
		require.NoError(t, err)
		resp, err := upstream.Client().Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		info := &relaycommon.RelayInfo{RetryGuardSampled: &atomic.Bool{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
		info.LastError = types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCode("overloaded"), resp.StatusCode)
		require.Equal(t, enabled, ClaimGroupRetryGuardSample(c, info, false), "provider error codes must not hide actual upstream failures")
		require.False(t, ClaimGroupRetryGuardSample(c, info, false), "one completion sample per client request")
		resetRelayUpstreamSent(c)
		info.RetryGuardSampled = &atomic.Bool{}
		require.False(t, ClaimGroupRetryGuardSample(c, info, false), "previous attempt evidence must not leak into a local failure")
	}
}

func TestClaimGroupRetryGuardSample(t *testing.T) {
	old := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	operation_setting.ReplaceGroupRetryGuardSetting(operation_setting.GroupRetryGuardSetting{Enabled: true, Rules: "{}"})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{RetryGuardSampled: &atomic.Bool{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
	require.False(t, ClaimGroupRetryGuardSample(c, info, false), "local failure has no upstream evidence")
	info.LastError = types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeBadResponseStatusCode, 429)
	require.True(t, ClaimGroupRetryGuardSample(c, info, false))
	require.False(t, ClaimGroupRetryGuardSample(c, info, false), "settlement and error paths cannot count twice")
	info.RetryGuardSampled = &atomic.Bool{}
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	require.False(t, ClaimGroupRetryGuardSample(c, info, true), "client cancellation is not a slow sample")
}

func TestClaimGroupRetryGuardSampleEligibility(t *testing.T) {
	old := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	operation_setting.ReplaceGroupRetryGuardSetting(operation_setting.GroupRetryGuardSetting{Enabled: true, Rules: "{}"})
	for _, tc := range []struct {
		name   string
		change func(*relaycommon.RelayInfo)
		want   bool
	}{
		{"success", func(*relaycommon.RelayInfo) {}, true},
		{"uninitialized", func(i *relaycommon.RelayInfo) { i.RetryGuardSampled = nil }, false},
		{"no channel", func(i *relaycommon.RelayInfo) { i.ChannelMeta = nil }, false},
		{"channel test", func(i *relaycommon.RelayInfo) { i.IsChannelTest = true }, false},
		{"realtime", func(i *relaycommon.RelayInfo) { i.RelayFormat = types.RelayFormatOpenAIRealtime }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{RetryGuardSampled: &atomic.Bool{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
			tc.change(info)
			require.Equal(t, tc.want, ClaimGroupRetryGuardSample(nil, info, true))
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{RetryGuardSampled: &atomic.Bool{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
	exchange := &relayUpstreamExchange{}
	relayUpstreamSentState(c, true).current.Store(exchange)
	require.False(t, ClaimGroupRetryGuardSample(c, info, true), "unsent request is excluded")
	exchange.wrote.Store(true)
	require.True(t, ClaimGroupRetryGuardSample(c, info, false), "sent request failure still has upstream evidence")
	require.False(t, ClaimGroupRetryGuardSample(c, nil, true))
	operation_setting.ReplaceGroupRetryGuardSetting(old)
	info.RetryGuardSampled = &atomic.Bool{}
	disabled := old
	disabled.Enabled = false
	operation_setting.ReplaceGroupRetryGuardSetting(disabled)
	require.False(t, ClaimGroupRetryGuardSample(c, info, true))
}
