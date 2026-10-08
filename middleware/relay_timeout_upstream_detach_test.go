package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 与真实超时控制器联测 service.BindRelayRequestContext：受管请求的上游调用不随客户端断开而取消
// （与主分支一致，上游已计费），但仍受用户配置的时限约束，且时限到期时按超时分类。

// detachTestContext 构造一个挂在"客户端连接" context 下的真实超时控制器。
func detachTestContext(t *testing.T, responseTimeout, totalTimeout time.Duration) (*gin.Context, context.CancelFunc, *relayTimeoutControl) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client, clientGone := context.WithCancel(context.Background())
	t.Cleanup(clientGone)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, control := newRelayTimeoutControl(client, responseTimeout, totalTimeout, service.RelayStreamResponseTimeoutModeFirstOutput)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	common.SetContextKey(c, constant.ContextKeyIsStream, false)
	t.Cleanup(control.Close)
	return c, clientGone, control
}

func slowUpstream(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
			_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":19,"completion_tokens":44}}`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func callBound(c *gin.Context, url string) (string, error) {
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req = service.BindRelayRequestContext(c, req)
	resp, err := service.RelayHTTPClient(c, &http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

// TestManagedNonStreamUpstreamCompletesAfterClientDisconnect 非流式：客户端在上游作答前断开，
// 上游在时限内完成，调用拿到完整 usage（修复前立即 context canceled → 全额退款）。
func TestManagedNonStreamUpstreamCompletesAfterClientDisconnect(t *testing.T) {
	c, clientGone, control := detachTestContext(t, 2*time.Second, 0)
	server := slowUpstream(t, 200*time.Millisecond)
	time.AfterFunc(50*time.Millisecond, clientGone)
	body, err := callBound(c, server.URL)
	require.NoError(t, err)
	require.Contains(t, body, `"completion_tokens":44`)
	require.Empty(t, control.RelayTimeoutKind())
}

// TestManagedUpstreamStillBoundedByResponseTimeoutAfterDisconnect 客户端先断开，上游超过响应时限：
// 仍在时限处取消，并按超时（而非客户端断开）分类。
func TestManagedUpstreamStillBoundedByResponseTimeoutAfterDisconnect(t *testing.T) {
	c, clientGone, control := detachTestContext(t, 250*time.Millisecond, 0)
	server := slowUpstream(t, 5*time.Second)
	time.AfterFunc(30*time.Millisecond, clientGone)
	started := time.Now()
	_, err := callBound(c, server.URL)
	require.Error(t, err)
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, 240*time.Millisecond, "cancelled with the client instead of at the deadline")
	require.Less(t, elapsed, 2*time.Second)
	require.Equal(t, service.RelayTimeoutKindResponse, control.RelayTimeoutKind(),
		"the call must end only after the controller recorded its own expiry")
}

// TestManagedUpstreamCancelledByTimeoutWhileClientConnected 客户端在线时我方时限照旧取消上游。
func TestManagedUpstreamCancelledByTimeoutWhileClientConnected(t *testing.T) {
	c, _, control := detachTestContext(t, 0, 150*time.Millisecond)
	server := slowUpstream(t, 5*time.Second)
	started := time.Now()
	_, err := callBound(c, server.URL)
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)
	require.Equal(t, string(relayTimeoutKindTotal), control.RelayTimeoutKind())
}

// TestManagedUpstreamCancelledWhenRequestFinishes 真实控制器：客户端离开后（无响应时限在途），
// 请求结束（Close）即取消仍在进行的上游调用，不让它比处理器活得更久（审查 M1）。
func TestManagedUpstreamCancelledWhenRequestFinishes(t *testing.T) {
	c, clientGone, control := detachTestContext(t, 0, 0)
	server := slowUpstream(t, 5*time.Second)
	time.AfterFunc(30*time.Millisecond, clientGone)
	time.AfterFunc(150*time.Millisecond, control.Close)
	started := time.Now()
	_, err := callBound(c, server.URL)
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(started), 140*time.Millisecond, "the client leaving alone must not cancel it")
	require.Less(t, time.Since(started), 2*time.Second, "the finished request must cancel it")
}

// TestRelayTimeoutAfterRelayCloseRunsOnceAndStops Close 只执行仍登记的回调一次；已结束后不再登记。
func TestRelayTimeoutAfterRelayCloseRunsOnceAndStops(t *testing.T) {
	_, _, control := detachTestContext(t, 0, 0)
	var ran, removed atomic.Int32
	_, ok := control.AfterRelayClose(func() { ran.Add(1) })
	require.True(t, ok)
	stop, ok := control.AfterRelayClose(func() { removed.Add(1) })
	require.True(t, ok)
	require.True(t, stop())
	require.False(t, stop())
	control.Close()
	control.Close()
	require.EqualValues(t, 1, ran.Load())
	require.Zero(t, removed.Load())
	_, ok = control.AfterRelayClose(func() {})
	require.False(t, ok, "a finished request registers nothing")
}
