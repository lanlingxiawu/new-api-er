package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 回归：relay-control 报告"受超时管理的非流请求断开后会取消上游并退款"与 openai-format D-3。
// 受管请求的上游调用曾直接绑定客户端连接的 context：客户端一断开就取消上游，非流式全额退款、
// 流式在首字节前释放预扣，而上游照样计费。主分支的上游调用不绑定客户端（http.NewRequest），
// 非流式读完并按完整用量计费。现在只有我方时限能取消上游调用。

// fakeUpstreamControl 模拟请求级超时控制器：expired 为已记录的超时种类，deadline 为下一个到期时刻。
// kindAt 非零时，到达该时刻后自动报告 total_timeout（模拟控制器自己的定时器在截止时刻触发）。
type fakeUpstreamControl struct {
	mu       sync.Mutex
	expired  string
	deadline time.Time
	kindAt   time.Time
	closed   bool
	hooks    []func()
}

// AfterRelayClose 与真实控制器相同：请求结束时（close）执行一次，已结束则不登记。
func (f *fakeUpstreamControl) AfterRelayClose(hook func()) (func() bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return func() bool { return false }, false
	}
	f.hooks = append(f.hooks, hook)
	index := len(f.hooks) - 1
	return func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		removed := f.hooks[index] != nil
		f.hooks[index] = nil
		return removed
	}, true
}

// close 模拟请求结束（中间件 Close）：此后控制器不再报告截止时刻，并执行结束回调。
func (f *fakeUpstreamControl) close() {
	f.mu.Lock()
	f.closed = true
	f.deadline = time.Time{}
	hooks := f.hooks
	f.hooks = nil
	f.mu.Unlock()
	for _, hook := range hooks {
		if hook != nil {
			hook()
		}
	}
}

func (f *fakeUpstreamControl) RelayTimeoutKind() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.expired == "" && !f.kindAt.IsZero() && !time.Now().Before(f.kindAt) {
		f.expired = "total_timeout"
	}
	return f.expired
}

func (f *fakeUpstreamControl) RelayTimeoutDeadline() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadline, !f.deadline.IsZero() && f.expired == ""
}

func (f *fakeUpstreamControl) expire(kind string) {
	f.mu.Lock()
	f.expired = kind
	f.mu.Unlock()
}

// blockingUpstream 在 release 关闭前不返回响应，并统计到达的请求数。
func blockingUpstream(t *testing.T) (*httptest.Server, chan struct{}, *atomic.Int32) {
	t.Helper()
	release := make(chan struct{})
	hits := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":19,"completion_tokens":44}}`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	return server, release, hits
}

// managedClientContext 构造受管请求：客户端连接的 context 可取消，超时控制器为 control。
func managedClientContext(control any) (*gin.Context, context.CancelFunc) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	return c, cancel
}

type upstreamResult struct {
	body string
	err  error
}

func doBound(c *gin.Context, url string) <-chan upstreamResult {
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req = BindRelayRequestContext(c, req)
	client := RelayHTTPClient(c, &http.Client{})
	done := make(chan upstreamResult, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			done <- upstreamResult{err: err}
			return
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- upstreamResult{body: string(data), err: err}
	}()
	return done
}

// TestBoundUpstreamSurvivesClientDisconnect 客户端断开后上游调用继续，拿到完整响应（主分支口径）。
func TestBoundUpstreamSurvivesClientDisconnect(t *testing.T) {
	server, release, _ := blockingUpstream(t)
	c, clientGone := managedClientContext(&fakeUpstreamControl{})
	done := doBound(c, server.URL)
	time.Sleep(50 * time.Millisecond)
	clientGone()
	select {
	case r := <-done:
		t.Fatalf("upstream call ended with the client: %v", r.err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.Contains(t, r.body, `"completion_tokens":44`)
	case <-time.After(5 * time.Second):
		t.Fatal("upstream call did not finish")
	}
}

// TestBoundUpstreamCancelledByOwnTimeout 客户端仍在时，我方超时照旧立即取消上游调用。
func TestBoundUpstreamCancelledByOwnTimeout(t *testing.T) {
	server, _, _ := blockingUpstream(t)
	control := &fakeUpstreamControl{}
	c, cancel := managedClientContext(control)
	done := doBound(c, server.URL)
	time.Sleep(50 * time.Millisecond)
	control.expire("response_timeout")
	cancel() // 控制器到期时取消请求 context
	select {
	case r := <-done:
		require.Error(t, r.err)
	case <-time.After(2 * time.Second):
		t.Fatal("our timeout did not cancel the upstream call")
	}
}

// TestBoundUpstreamKeepsDeadlineAfterClientLeft 客户端先断开，上游调用仍在控制器原本的截止时刻被取消，
// 不会因为客户端离开而变成不限时。
func TestBoundUpstreamKeepsDeadlineAfterClientLeft(t *testing.T) {
	server, _, _ := blockingUpstream(t)
	deadline := time.Now().Add(300 * time.Millisecond)
	control := &fakeUpstreamControl{deadline: deadline, kindAt: deadline}
	c, clientGone := managedClientContext(control)
	done := doBound(c, server.URL)
	time.Sleep(30 * time.Millisecond)
	clientGone()
	select {
	case r := <-done:
		t.Fatalf("upstream call ended with the client: %v", r.err)
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case r := <-done:
		require.Error(t, r.err)
		require.False(t, time.Now().Before(deadline), "cancelled before the deadline")
	case <-time.After(3 * time.Second):
		t.Fatal("the deadline no longer bounds the upstream call after the client left")
	}
}

// TestBoundUpstreamNotSentWhenClientAlreadyGone 发送前客户端已断开：不发送，调用直接失败（预扣照旧释放）。
func TestBoundUpstreamNotSentWhenClientAlreadyGone(t *testing.T) {
	server, _, hits := blockingUpstream(t)
	c, clientGone := managedClientContext(&fakeUpstreamControl{})
	clientGone()
	r := <-doBound(c, server.URL)
	require.Error(t, r.err)
	require.Zero(t, hits.Load())
}

// TestBoundUpstreamLinkReleasedWithBody 正常结束的调用在关闭响应体时撤下对客户端 context 的监听：
// 此后客户端断开不再触发任何回调、不留下定时器。
func TestBoundUpstreamLinkReleasedWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	t.Cleanup(server.Close)
	control := &fakeUpstreamControl{deadline: time.Now().Add(time.Hour)}
	c, clientGone := managedClientContext(control)
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	req = BindRelayRequestContext(c, req)
	link, ok := req.Context().Value(relayUpstreamLinkKey{}).(*relayUpstreamLink)
	require.True(t, ok)
	resp, err := RelayHTTPClient(c, &http.Client{}).Do(req)
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close(), "a second close must not unbalance the link")
	link.mu.Lock()
	require.Zero(t, link.inflight)
	require.Nil(t, link.stopWatch)
	require.Nil(t, link.stopClose, "the request-finished hook is removed with the last body")
	link.mu.Unlock()
	clientGone()
	time.Sleep(20 * time.Millisecond)
	link.mu.Lock()
	defer link.mu.Unlock()
	require.Nil(t, link.timer, "a finished exchange must not arm the deadline timer")
}

// TestBoundUpstreamSurvivesDisconnectAcrossRedirect 重定向的第二跳同样不因客户端断开而取消。
func TestBoundUpstreamSurvivesDisconnectAcrossRedirect(t *testing.T) {
	target, release, _ := blockingUpstream(t)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)
	c, clientGone := managedClientContext(&fakeUpstreamControl{})
	done := doBound(c, redirect.URL)
	time.Sleep(80 * time.Millisecond)
	clientGone()
	time.Sleep(100 * time.Millisecond)
	close(release)
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.Contains(t, r.body, "completion_tokens")
	case <-time.After(5 * time.Second):
		t.Fatal("redirected upstream call did not finish")
	}
}

// TestUnmanagedRequestIsNotBound 未受超时管理的请求保持原样（与主分支相同，本来就不绑定客户端）。
func TestUnmanagedRequestIsNotBound(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	require.Same(t, req, BindRelayRequestContext(c, req))
}

// TestMidjourneySubmitSurvivesClientDisconnect 受管 Midjourney 提交：客户端在上游作答前离开，
// 提交照常完成并返回 200（控制器随后按次结算，与主分支一致），而不是取消后退款。
func TestMidjourneySubmitSurvivesClientDisconnect(t *testing.T) {
	if GetHttpClient() == nil {
		InitHttpClient()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{"code":1,"description":"submitted","result":"task-1"}`)
	}))
	t.Cleanup(server.Close)
	c, clientGone := managedClientContext(&fakeUpstreamControl{})
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{"prompt":"cat"}`)).WithContext(c.Request.Context())
	time.AfterFunc(50*time.Millisecond, clientGone)
	result, _, err := DoMidjourneyHttpRequest(c, time.Minute, server.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, "task-1", result.Response.Result)
}

// TestRelayUpstreamContextFollowsOwnTimeoutOnly 非 HTTP 上游工作（WS、轮询）的 context：
// 客户端离开不结束，我方时限结束；release 之后撤下监听。未受管请求沿用后台 context。
func TestRelayUpstreamContextFollowsOwnTimeoutOnly(t *testing.T) {
	control := &fakeUpstreamControl{}
	c, clientGone := managedClientContext(control)
	ctx, release := RelayUpstreamContext(c)
	clientGone()
	select {
	case <-ctx.Done():
		t.Fatal("the client leaving ended the upstream work")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	release() // idempotent

	c2, cancel2 := managedClientContext(control)
	ctx2, release2 := RelayUpstreamContext(c2)
	defer release2()
	control.expire("total_timeout")
	cancel2()
	select {
	case <-ctx2.Done():
	case <-time.After(time.Second):
		t.Fatal("our timeout did not end the upstream work")
	}

	unmanaged, _ := gin.CreateTestContext(httptest.NewRecorder())
	unmanaged.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	ctx3, release3 := RelayUpstreamContext(unmanaged)
	release3()
	require.Equal(t, context.Background(), ctx3)
}

// 回归（审查 M1）：客户端先离开、请求随后结束（控制器 Close）时，控制器不再报告截止时刻，
// 链接曾把它当作"不限时"，上游调用永不取消、在处理器返回后继续占用连接与 goroutine。
// 现在请求结束即取消仍在进行的上游工作——有无截止时刻都一样。
func TestBoundUpstreamCancelledWhenRequestFinishesAfterClientLeft(t *testing.T) {
	for _, withDeadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "no deadline", true: "deadline pending"}[withDeadline], func(t *testing.T) {
			server, _, _ := blockingUpstream(t)
			control := &fakeUpstreamControl{}
			if withDeadline {
				control.deadline = time.Now().Add(time.Hour)
			}
			c, clientGone := managedClientContext(control)
			done := doBound(c, server.URL)
			time.Sleep(30 * time.Millisecond)
			clientGone()
			select {
			case r := <-done:
				t.Fatalf("ended with the client: %v", r.err)
			case <-time.After(80 * time.Millisecond):
			}
			control.close()
			select {
			case r := <-done:
				require.Error(t, r.err)
			case <-time.After(2 * time.Second):
				t.Fatal("the upstream call outlived the finished request")
			}
		})
	}
}

// TestRelayUpstreamContextEndsWithRequest 非 HTTP 上游工作同样随请求结束而结束；
// 请求已结束后再申请的上游 context 直接是取消状态。
func TestRelayUpstreamContextEndsWithRequest(t *testing.T) {
	control := &fakeUpstreamControl{}
	c, clientGone := managedClientContext(control)
	ctx, release := RelayUpstreamContext(c)
	defer release()
	clientGone()
	control.close()
	select {
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), errRelayRequestFinished)
	case <-time.After(time.Second):
		t.Fatal("upstream work outlived the finished request")
	}
}

// TestContinueRelayAttemptsStopsAfterClientLeft 审查 L2：客户端离开后不再发起下一次尝试；
// 首次尝试照常；我方超时不走这条（由既有预算与超时规则处理）。
func TestContinueRelayAttemptsStopsAfterClientLeft(t *testing.T) {
	previous := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = previous })

	c, clientGone := managedClientContext(&fakeUpstreamControl{})
	p := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	require.True(t, ContinueRelayAttempts(c, p), "the first attempt always runs")
	BeginRelayAttempt(c, p)
	p.IncreaseRetry()
	require.True(t, ContinueRelayAttempts(c, p), "a retry while the client is connected")
	clientGone()
	require.False(t, ContinueRelayAttempts(c, p), "no retry after the client left")

	unmanaged, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	unmanaged.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	q := &RetryParam{Ctx: unmanaged, Retry: common.GetPointer(0)}
	BeginRelayAttempt(unmanaged, q)
	q.IncreaseRetry()
	cancel()
	require.False(t, ContinueRelayAttempts(unmanaged, q), "the same without the timeout feature")
}
