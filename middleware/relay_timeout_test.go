package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRelayTimeoutSetting(t *testing.T, setting operation_setting.RelayTimeoutSetting) {
	t.Helper()
	previous := operation_setting.GetRelayTimeoutSetting()
	operation_setting.ReplaceRelayTimeoutSetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayTimeoutSetting(previous) })
}

func timeoutTestContext(t *testing.T, responseTimeout, totalTimeout time.Duration, isStream bool) (*gin.Context, context.Context, *relayTimeoutControl) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, control := newRelayTimeoutControl(context.Background(), responseTimeout, totalTimeout)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	common.SetContextKey(c, constant.ContextKeyIsStream, isStream)
	writer := &relayTimeoutResponseWriter{ResponseWriter: c.Writer}
	writer.state.Store(&relayTimeoutWriterState{control: control, isStream: isStream})
	c.Writer = writer
	t.Cleanup(control.Close)
	return c, ctx, control
}

func waitForTimeout(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		require.Fail(t, "timeout control did not cancel the request")
	}
}

func TestRelayTimeoutResponseLimitWins(t *testing.T) {
	_, ctx, control := timeoutTestContext(t, 15*time.Millisecond, 200*time.Millisecond, true)
	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
	assert.ErrorIs(t, context.Cause(ctx), errRelayResponseTimeout)
}

func TestRelayTimeoutTotalLimitWins(t *testing.T) {
	_, ctx, control := timeoutTestContext(t, 200*time.Millisecond, 15*time.Millisecond, true)
	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())
	assert.ErrorIs(t, context.Cause(ctx), errRelayTotalTimeout)
}

func TestRelayTimeoutStreamOutputResetsResponseButNotTotal(t *testing.T) {
	startedAt := time.Now()
	c, ctx, control := timeoutTestContext(t, 500*time.Millisecond, 100*time.Millisecond, true)

	for range 3 {
		time.Sleep(25 * time.Millisecond)
		_, err := c.Writer.Write([]byte("data: output\n\n"))
		require.NoError(t, err)
		assert.NoError(t, ctx.Err())
	}

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind(), "stream output must not reset the absolute total limit")
	assert.Less(t, time.Since(startedAt), 170*time.Millisecond, "stream output must not move the total deadline")
}

func TestRelayTimeoutStreamBecomesResponseTimeoutAfterOutputStops(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 25*time.Millisecond, 300*time.Millisecond, true)
	time.Sleep(15 * time.Millisecond)
	_, err := c.Writer.Write([]byte("data: output\n\n"))
	require.NoError(t, err)
	time.Sleep(15 * time.Millisecond)
	assert.NoError(t, ctx.Err(), "valid output must restart the response timeout window")

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

func TestRelayTimeoutHeartbeatDoesNotResetResponseLimit(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 20*time.Millisecond, 300*time.Millisecond, true)
	for _, data := range [][]byte{[]byte(": PING\n\n"), []byte("\n"), []byte(": upstream comment\n\n")} {
		_, err := c.Writer.Write(data)
		require.NoError(t, err)
	}
	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

func TestRelayTimeoutNonStreamFirstResponseStopsOnlyResponseLimit(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 20*time.Millisecond, 55*time.Millisecond, false)
	time.Sleep(10 * time.Millisecond)
	service.MarkRelayResponse(c)
	time.Sleep(20 * time.Millisecond)
	assert.NoError(t, ctx.Err(), "first response must stop the non-stream response timer")

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind(), "total timer must continue after the first response")
}

func TestRelayTimeoutNonStreamRetryRestartsResponseLimit(t *testing.T) {
	_, ctx, control := timeoutTestContext(t, 50*time.Millisecond, 300*time.Millisecond, false)
	time.Sleep(20 * time.Millisecond)
	assert.True(t, control.MarkResponse(false))
	time.Sleep(20 * time.Millisecond)
	assert.True(t, control.RestartResponse(false))
	time.Sleep(35 * time.Millisecond)
	assert.NoError(t, ctx.Err(), "retry must receive a fresh response timeout window")

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

func TestRelayTimeoutClientCancellationIsNotOwnedTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, cancel := context.WithCancel(context.Background())
	ctx, control := newRelayTimeoutControl(parent, time.Minute, time.Minute)
	t.Cleanup(control.Close)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)

	cancel()
	<-ctx.Done()
	assert.False(t, IsRelayRequestTimeout(c))
	assert.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
}

func TestRelayTimeoutWriterRejectsLateBusinessOutputButAllowsTimeoutResponse(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	control.expireTotal()
	waitForTimeout(t, ctx)

	c.Writer.WriteHeaderNow()
	c.Writer.Flush()
	assert.False(t, c.Writer.Written())

	n, err := c.Writer.Write([]byte("late success"))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, c.Writer.Written())
	assert.True(t, control.lateWriteRejected.Load(), "a rejected post-timeout write must remain observable")

	WriteRelayTimeoutResponse(c, func() {
		c.String(http.StatusGatewayTimeout, "timeout")
	})
	assert.True(t, c.Writer.Written())
}

type relayTimeoutWriterWrapper struct {
	gin.ResponseWriter
}

func (writer *relayTimeoutWriterWrapper) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func TestRelayTimeoutResponseBypassesNestedWriterAfterExpiry(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	c.Writer = &relayTimeoutWriterWrapper{ResponseWriter: c.Writer}
	control.expireTotal()
	waitForTimeout(t, ctx)

	WriteRelayTimeoutResponse(c, func() {
		c.String(http.StatusGatewayTimeout, "timeout")
	})

	assert.True(t, c.Writer.Written())
	assert.Equal(t, http.StatusGatewayTimeout, c.Writer.Status())
}

func TestRelayTimeoutWriterPublishesImmutableStateAndSettlesNonStreamResponse(t *testing.T) {
	c, _, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	writer, ok := c.Writer.(*relayTimeoutResponseWriter)
	require.True(t, ok)
	state := writer.state.Load()
	require.NotNil(t, state)
	assert.Same(t, control, state.control)
	assert.False(t, state.isStream)
	assert.True(t, control.responseActive.Load())

	_, err := writer.Write([]byte(`{"ok":true}`))
	require.NoError(t, err)
	assert.False(t, control.responseActive.Load(), "non-stream output must disable further response scanning")
}

func TestEffectiveRelayTimeoutsSelectModeSpecificUserValues(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 300, TotalTimeoutSeconds: 600})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting, operation_setting.GetRelayTimeoutSnapshot())
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, 10)
	common.SetContextKey(c, constant.ContextKeyUserStreamTotalTimeout, 20)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamResponseTimeout, 30)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, 40)

	response, total := effectiveRelayTimeouts(c, true)
	assert.Equal(t, 10, response)
	assert.Equal(t, 20, total)

	response, total = effectiveRelayTimeouts(c, false)
	assert.Equal(t, 30, response)
	assert.Equal(t, 40, total)
}

func TestEffectiveRelayTimeoutsInheritAndDisableIndependently(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 300, TotalTimeoutSeconds: 600})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting, operation_setting.GetRelayTimeoutSnapshot())
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, 0)
	common.SetContextKey(c, constant.ContextKeyUserStreamTotalTimeout, -1)

	response, total := effectiveRelayTimeouts(c, true)
	assert.Equal(t, 300, response)
	assert.Zero(t, total)
}

func TestRelayRequestTimeoutDisabledLeavesLegacyChainUntouched(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: false, ResponseTimeoutSeconds: 1, TotalTimeoutSeconds: 1})
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, 1)
	originalWriter := c.Writer

	managed := true
	RelayRequestTimeout()(c)
	StartRelayRequestTimeout(c, true)
	managed = service.IsRelayTimeoutManaged(c)

	assert.False(t, managed)
	assert.Same(t, originalWriter, c.Writer)
}

func TestRelayRequestTimeoutEnabledWithoutUserOverridesLeavesLegacyChainUntouched(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 1, TotalTimeoutSeconds: 1})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	originalWriter := c.Writer

	RelayRequestTimeout()(c)
	StartRelayRequestTimeout(c, true)

	assert.False(t, service.IsRelayTimeoutManaged(c))
	assert.Same(t, originalWriter, c.Writer)
	_, exists := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl)
	assert.False(t, exists)
	legacyClient := &http.Client{Timeout: time.Second}
	assert.Same(t, legacyClient, service.RelayHTTPClient(c, legacyClient))
}

func TestRelayRequestTimeoutAnyUserOverrideActivatesManagedPath(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 11, TotalTimeoutSeconds: 22})
	setters := []struct {
		name             string
		setOverride      func(*gin.Context)
		expectedResponse int
		expectedTotal    int
	}{
		{"stream response", func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, 1)
		}, 11, 22},
		{"stream total", func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserStreamTotalTimeout, -1)
		}, 11, 22},
		{"non-stream response", func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserNonStreamResponseTimeout, 1)
		}, 1, 22},
		{"non-stream total", func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, -1)
		}, 11, 0},
	}

	for _, testCase := range setters {
		t.Run(testCase.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			testCase.setOverride(c)

			RelayRequestTimeout()(c)
			StartRelayRequestTimeout(c, false)

			assert.True(t, service.IsRelayTimeoutManaged(c))
			control, ok := relayTimeoutControlFromContext(c)
			require.True(t, ok)
			control.Close()
			assert.Equal(t, testCase.expectedResponse, common.GetContextKeyInt(c, constant.ContextKeyRelayResponseTimeoutSeconds))
			assert.Equal(t, testCase.expectedTotal, common.GetContextKeyInt(c, constant.ContextKeyRelayTotalTimeoutSeconds))
		})
	}
}

// A user override that only applies to the other mode must not disarm the
// legacy guards for this one: taking the request over drops the stream
// scanner's STREAMING_TIMEOUT ticker and strips RELAY_TIMEOUT from the shared
// client, so a managed request that resolves to no limits would be strictly
// less protected than an unmanaged one.
func TestRelayRequestTimeoutZeroEffectiveLimitsLeavesLegacyChainUntouched(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 0, TotalTimeoutSeconds: 0})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	// Configured for non-stream only; this request is a streaming one.
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, 600)
	originalWriter := c.Writer

	RelayRequestTimeout()(c)
	StartRelayRequestTimeout(c, true)

	assert.False(t, service.IsRelayTimeoutManaged(c))
	assert.Same(t, originalWriter, c.Writer)
	legacyClient := &http.Client{Timeout: time.Second}
	assert.Same(t, legacyClient, service.RelayHTTPClient(c, legacyClient),
		"unmanaged requests must keep RELAY_TIMEOUT on the shared client")
}

// -1 is an explicit "no limit for me", so the request stays managed (and the
// legacy guards stay off) even though no timer is armed.
func TestRelayRequestTimeoutExplicitUnlimitedStaysManaged(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 0, TotalTimeoutSeconds: 0})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, service.RelayTimeoutUnlimited)

	RelayRequestTimeout()(c)
	StartRelayRequestTimeout(c, true)

	require.True(t, service.IsRelayTimeoutManaged(c))
	control, ok := relayTimeoutControlFromContext(c)
	require.True(t, ok)
	t.Cleanup(control.Close)
	assert.Nil(t, control.responseTimer)
	assert.Nil(t, control.totalTimer)
	assert.Zero(t, common.GetContextKeyInt(c, constant.ContextKeyRelayResponseTimeoutSeconds))
	assert.Zero(t, common.GetContextKeyInt(c, constant.ContextKeyRelayTotalTimeoutSeconds))
}

func TestRelayRequestTimeoutEnabledWithoutStartLeavesWriterUntouched(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 1, TotalTimeoutSeconds: 1})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/fetch", nil)
	originalWriter := c.Writer

	RelayRequestTimeout()(c)

	assert.Same(t, originalWriter, c.Writer, "routes that never start relay timeout must keep the original writer")
	assert.False(t, service.IsRelayTimeoutManaged(c))
}

func TestRelayRequestTimeoutCapturesHotSettingForNewRequest(t *testing.T) {
	useRelayTimeoutSetting(t, operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 11, TotalTimeoutSeconds: 22})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, -1)

	RelayRequestTimeout()(c)
	operation_setting.ReplaceRelayTimeoutSetting(operation_setting.RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 33, TotalTimeoutSeconds: 44})
	StartRelayRequestTimeout(c, false)
	value, exists := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl)
	require.True(t, exists)
	control, ok := value.(*relayTimeoutControl)
	require.True(t, ok)
	t.Cleanup(control.Close)

	assert.Equal(t, 11, common.GetContextKeyInt(c, constant.ContextKeyRelayResponseTimeoutSeconds))
	assert.Equal(t, 22, common.GetContextKeyInt(c, constant.ContextKeyRelayTotalTimeoutSeconds))
}

func TestRelayNonOutputClassification(t *testing.T) {
	for _, heartbeat := range []string{"", "\n", ": PING\n\n", "  : comment\r\n:保活\n"} {
		assert.True(t, isRelayNonOutput([]byte(heartbeat)))
		assert.True(t, isRelayNonOutputString(heartbeat))
	}
	for _, output := range []string{"data: {}\n\n", "event: message\n", ": ping\ndata: payload\n"} {
		assert.False(t, isRelayNonOutput([]byte(output)))
		assert.False(t, isRelayNonOutputString(output))
	}
}
