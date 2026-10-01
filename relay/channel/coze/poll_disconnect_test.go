package coze

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// cozeTimeoutControl 模拟请求级超时控制器：expired 非空表示我方时限已到。
type cozeTimeoutControl struct{ expired atomic.Value }

func (f *cozeTimeoutControl) RelayTimeoutKind() string {
	kind, _ := f.expired.Load().(string)
	return kind
}
func (f *cozeTimeoutControl) RelayTimeoutDeadline() (time.Time, bool) { return time.Time{}, false }

// newCozeFlowServer 创建对话后第 completeAfter 次查询才完成。
func newCozeFlowServer(t *testing.T, completeAfter int32) *httptest.Server {
	t.Helper()
	var retrieves atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":{"id":"chat1","conversation_id":"conv1","status":"in_progress"}}`)
	})
	mux.HandleFunc("/v3/chat/retrieve", func(w http.ResponseWriter, _ *http.Request) {
		if retrieves.Add(1) < completeAfter {
			_, _ = io.WriteString(w, `{"code":0,"data":{"status":"in_progress"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"status":"completed","usage":{"token_count":10,"output_count":6,"input_count":4}}}`)
	})
	mux.HandleFunc("/v3/chat/message/list", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":[{"type":"answer","content":"hi"}]}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func managedCozeRequest(t *testing.T, control *cozeTimeoutControl, baseURL string) (*Adaptor, *relaycommon.RelayInfo, func() (any, error), context.CancelFunc) {
	t.Helper()
	c, _ := newContext()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL, ApiKey: "tok"}}
	info.ApiKey = "tok"
	a := &Adaptor{}
	return a, info, func() (any, error) {
		return a.DoRequest(c, info, strings.NewReader(`{"bot_id":"b","user_id":"u","additional_messages":[]}`))
	}, cancel
}

// 回归：受超时管理时，轮询曾在客户端断开时立即放弃（上游对话照常生成并计费，而本方退款）。
// 现在只有我方时限结束轮询：客户端离开后仍等到对话完成并取回结果（与主分支一致）。
func TestCozePollingSurvivesClientDisconnect(t *testing.T) {
	server := newCozeFlowServer(t, 2)
	control := &cozeTimeoutControl{}
	_, _, doRequest, clientGone := managedCozeRequest(t, control, server.URL)
	time.AfterFunc(100*time.Millisecond, clientGone)
	resp, err := doRequest()
	require.NoError(t, err)
	httpResp, ok := resp.(*http.Response)
	require.True(t, ok)
	body, _ := io.ReadAll(httpResp.Body)
	_ = httpResp.Body.Close()
	require.Contains(t, string(body), `"answer"`)
}

// TestCozePollingStopsOnOwnTimeout 我方时限到期仍会结束轮询。
func TestCozePollingStopsOnOwnTimeout(t *testing.T) {
	server := newCozeFlowServer(t, 1000)
	control := &cozeTimeoutControl{}
	_, _, doRequest, cancel := managedCozeRequest(t, control, server.URL)
	time.AfterFunc(100*time.Millisecond, func() {
		control.expired.Store("total_timeout")
		cancel()
	})
	started := time.Now()
	_, err := doRequest()
	require.Error(t, err)
	require.Less(t, time.Since(started), 3*time.Second)
}
