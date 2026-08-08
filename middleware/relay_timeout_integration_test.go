package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relayTimeoutRuntimeProbe struct {
	managed         bool
	isStream        bool
	responseTimeout time.Duration
	totalTimeout    time.Duration
}

func waitForUpstream(t *testing.T, request *http.Request, delay time.Duration) bool {
	t.Helper()
	select {
	case <-time.After(delay):
		return true
	case <-request.Context().Done():
		return false
	}
}

func runRelayTimeoutRuntimeProbe(t *testing.T, probe relayTimeoutRuntimeProbe, upstream http.HandlerFunc) (int, string, time.Duration) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if !probe.managed {
			c.Next()
			return
		}
		ctx, control := newRelayTimeoutControl(c.Request.Context(), probe.responseTimeout, probe.totalTimeout)
		ctx = service.WithManagedRelayTimeoutContext(ctx, probe.totalTimeout > 0)
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
		common.SetContextKey(c, constant.ContextKeyIsStream, probe.isStream)
		writer := &relayTimeoutResponseWriter{ResponseWriter: c.Writer}
		writer.state.Store(&relayTimeoutWriterState{control: control, isStream: probe.isStream})
		c.Writer = writer
		c.Request = c.Request.WithContext(ctx)
		defer control.Close()
		c.Next()
	})
	engine.POST("/probe", func(c *gin.Context) {
		c.Header("X-Relay-Timeout-Managed", strconv.FormatBool(service.IsRelayTimeoutManaged(c)))
		req, err := http.NewRequest(http.MethodGet, upstreamServer.URL, nil)
		require.NoError(t, err)
		req = service.BindRelayRequestContext(c, req)
		if traced := service.RelayResponseTraceContext(c, req.Context()); traced != req.Context() {
			req = req.WithContext(traced)
		}

		client := service.RelayHTTPClient(c, &http.Client{Timeout: time.Second})
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if probe.isStream {
				_, err = io.Copy(c.Writer, resp.Body)
			} else {
				var body []byte
				body, err = io.ReadAll(resp.Body)
				if err == nil {
					c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
				}
			}
		}
		if err == nil || c.Writer.Written() {
			return
		}
		if timeoutErr := service.RelayContextError(c); timeoutErr != nil {
			WriteRelayTimeoutResponse(c, func() {
				c.String(http.StatusGatewayTimeout, service.RelayRequestTimeoutKind(c))
			})
			return
		}
		c.String(http.StatusBadGateway, err.Error())
	})

	downstream := httptest.NewServer(engine)
	t.Cleanup(downstream.Close)
	startedAt := time.Now()
	resp, err := http.Post(downstream.URL+"/probe", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body), time.Since(startedAt)
}

func TestRelayTimeoutRuntimeAgainstLocalUpstream(t *testing.T) {
	t.Run("disabled_preserves_slow_request", func(t *testing.T) {
		status, body, _ := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{}, func(w http.ResponseWriter, r *http.Request) {
			if !waitForUpstream(t, r, 120*time.Millisecond) {
				return
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		})
		assert.Equal(t, http.StatusOK, status)
		assert.JSONEq(t, `{"ok":true}`, body)
	})

	t.Run("non_stream_response_timeout_before_headers", func(t *testing.T) {
		status, body, elapsed := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{
			managed: true, responseTimeout: 80 * time.Millisecond, totalTimeout: 400 * time.Millisecond,
		}, func(w http.ResponseWriter, r *http.Request) {
			if waitForUpstream(t, r, 200*time.Millisecond) {
				_, _ = io.WriteString(w, `{"late":true}`)
			}
		})
		assert.Equal(t, http.StatusGatewayTimeout, status)
		assert.Equal(t, "response_timeout", body)
		assert.Less(t, elapsed, 190*time.Millisecond)
	})

	t.Run("non_stream_first_byte_stops_response_timeout", func(t *testing.T) {
		status, body, _ := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{
			managed: true, responseTimeout: 80 * time.Millisecond, totalTimeout: 400 * time.Millisecond,
		}, func(w http.ResponseWriter, r *http.Request) {
			if !waitForUpstream(t, r, 20*time.Millisecond) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			if waitForUpstream(t, r, 160*time.Millisecond) {
				_, _ = io.WriteString(w, `{"complete":true}`)
			}
		})
		assert.Equal(t, http.StatusOK, status)
		assert.JSONEq(t, `{"complete":true}`, body)
	})

	t.Run("non_stream_total_timeout_after_first_byte", func(t *testing.T) {
		status, body, elapsed := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{
			managed: true, responseTimeout: 80 * time.Millisecond, totalTimeout: 100 * time.Millisecond,
		}, func(w http.ResponseWriter, r *http.Request) {
			if !waitForUpstream(t, r, 20*time.Millisecond) {
				return
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			if waitForUpstream(t, r, 220*time.Millisecond) {
				_, _ = io.WriteString(w, `{"late":true}`)
			}
		})
		assert.Equal(t, http.StatusGatewayTimeout, status)
		assert.Equal(t, "total_timeout", body)
		assert.Less(t, elapsed, 200*time.Millisecond)
	})

	t.Run("stream_output_resets_response_timeout", func(t *testing.T) {
		status, body, _ := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{
			managed: true, isStream: true, responseTimeout: 80 * time.Millisecond, totalTimeout: 400 * time.Millisecond,
		}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for index := 1; index <= 4; index++ {
				_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", index)
				flusher.Flush()
				if index < 4 && !waitForUpstream(t, r, 50*time.Millisecond) {
					return
				}
			}
		})
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "chunk-1")
		assert.Contains(t, body, "chunk-4")
	})

	t.Run("stream_silence_times_out_without_late_output", func(t *testing.T) {
		status, body, elapsed := runRelayTimeoutRuntimeProbe(t, relayTimeoutRuntimeProbe{
			managed: true, isStream: true, responseTimeout: 80 * time.Millisecond, totalTimeout: 400 * time.Millisecond,
		}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			if waitForUpstream(t, r, 200*time.Millisecond) {
				_, _ = io.WriteString(w, "data: late\n\n")
			}
		})
		assert.Equal(t, http.StatusOK, status, "a committed stream keeps its original status")
		assert.True(t, strings.Contains(body, "first"))
		assert.False(t, strings.Contains(body, "late"))
		assert.Less(t, elapsed, 190*time.Millisecond)
	})
}
