package common

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureTestBody 为只读数据增加关闭计数，用于测试关闭动作只执行一次。
type captureTestBody struct {
	io.Reader     // 本地响应字节来源，不访问真实上游。
	closes    int // 底层关闭方法被调用的次数。
}

// Close 记录模拟响应体的关闭次数，用于检验采集包装器的幂等关闭。
// 接收者 b：本地响应体夹具；无参数；closes 加一并返回 nil。
func (b *captureTestBody) Close() error { b.closes++; return nil }

// TestClaudeHTTPResponseCapture 覆盖 HTTP 200/400/500、读取前不主动消耗 body、截断、EOF、快照独立及幂等关闭。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeHTTPResponseCapture(t *testing.T) {
	for _, status := range []int{200, 400, 500} {
		capture := NewClaudeResponseCapture(2)
		original := &captureTestBody{Reader: strings.NewReader(strings.Repeat("x", 3000))}
		resp := &http.Response{StatusCode: status, Header: http.Header{"X-Upstream": {"a", "b"}, "Large": {strings.Repeat("x", 17000)}}, Body: original}
		capture.Observe(resp)
		require.Zero(t, capture.Snapshot().ObservedBytes, "observing must not drain the response")
		_, err := io.CopyN(io.Discard, resp.Body, 2100)
		require.NoError(t, err)
		first := capture.Snapshot()
		require.Equal(t, 2, first.Attempt)
		require.Equal(t, status, first.StatusCode)
		require.False(t, first.ReadEOF)
		require.EqualValues(t, 2100, first.ObservedBytes)
		require.EqualValues(t, 52, first.OmittedBytes)
		require.True(t, first.HeadersTruncated)
		require.Equal(t, []string{"a", "b"}, first.ResponseHeaders["X-Upstream"])
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.True(t, capture.Snapshot().ReadEOF)
		require.EqualValues(t, 2100, first.ObservedBytes, "snapshots are detached")
		require.NoError(t, resp.Body.Close())
		require.NoError(t, resp.Body.Close())
		require.Equal(t, 1, original.closes)
	}
}

// TestClaudeCaptureRetryAndConcurrency 验证多次 SDK 交换只保留最近四次，并发读取与快照互不破坏，终止错误按尝试隔离。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeCaptureRetryAndConcurrency(t *testing.T) {
	capture := NewClaudeResponseCapture(3)
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ {
		resp := &http.Response{StatusCode: 500 + i, Body: io.NopCloser(strings.NewReader(strings.Repeat("y", 4096)))}
		capture.Observe(resp)
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
		_ = capture.Snapshot()
	}
	wg.Wait()
	snap := capture.Snapshot()
	require.Equal(t, 506, snap.StatusCode)
	require.Len(t, snap.PreviousResponses, 3)
	require.Equal(t, 3, snap.OmittedResponses)
	require.Equal(t, 503, snap.PreviousResponses[0].StatusCode)
	require.EqualValues(t, 4096, snap.ObservedBytes)
	capture.SetError(errors.New("private transport cause"))
	require.Equal(t, "private transport cause", capture.Snapshot().Error)
	require.Empty(t, NewClaudeResponseCapture(4).Snapshot().Error)
}

// captureHTTPTestFunc 的参数为模拟 HTTP 请求，返回夹具响应及错误；配合 Do 方法满足客户端接口。
type captureHTTPTestFunc func(*http.Request) (*http.Response, error)

// Do 把函数夹具适配为 HTTP 客户端接口。
// 接收者 f：测试提供的响应生成函数；参数 req：模拟请求；返回 f 生成的响应及错误，不额外发起网络访问。
func (f captureHTTPTestFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// TestClaudeCaptureTransportRetryDoesNotPoisonSuccess 验证首次连接失败记录在历史响应，后续成功响应不继承为最终错误。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeCaptureTransportRetryDoesNotPoisonSuccess(t *testing.T) {
	capture := NewClaudeResponseCapture(1)
	call := 0
	client := ClaudeDiagnosticHTTPClient{Capture: capture, Client: captureHTTPTestFunc(func(*http.Request) (*http.Response, error) {
		call++
		if call == 1 {
			return nil, errors.New("first connection failed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	_, err := client.Do(nil)
	require.Error(t, err)
	require.Equal(t, "first connection failed", capture.Snapshot().Error)
	resp, err := client.Do(nil)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	snapshot := capture.Snapshot()
	require.Empty(t, snapshot.Error)
	require.Len(t, snapshot.PreviousResponses, 1)
	require.Equal(t, "first connection failed", snapshot.PreviousResponses[0].ReadError)
}
