package ali

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// cancelImageTaskBody 在适配器读完并关闭初始响应后模拟用户取消；不影响原始正文校验。
type cancelImageTaskBody struct {
	*strings.Reader                    // 已知任务响应的内存读取游标。
	cancel          context.CancelFunc // 用户请求取消入口。
}

// Close 触发取消，使真实 asyncTaskWait 在首次等待中立即退出，不访问外部网络。
func (b *cancelImageTaskBody) Close() error { b.cancel(); return nil }

// TestManagedAliInitialTaskCancellation 验证真实图片入口声明中间态，随后用户取消仍按确认用量结算；t 为测试上下文。
func TestManagedAliInitialTaskCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, Request: &dto.ImageRequest{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
	service.BeginStreamAttempt(c, info)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: &cancelImageTaskBody{strings.NewReader(`{"output":{"task_id":"fixture","task_status":"PENDING"},"usage":{"input_tokens":3}}`), cancel}}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	apiErr, usage := aliImageHandler(&Adaptor{IsSyncImageModel: false}, c, resp, info)
	require.NotNil(t, apiErr)
	require.ErrorIs(t, apiErr, context.Canceled)
	selected := service.FinalizeStreamUsage(c, info, usage)
	require.True(t, info.StreamResult.ClientGone)
	require.False(t, info.StreamResult.EffectiveContent)
	require.False(t, info.StreamResult.DiagnosticAvailable)
	require.Equal(t, 3, selected.PromptTokens)
	require.Equal(t, "upstream", info.StreamResult.UsageSource)
}

// TestManagedAliImageTaskStages 从初始创建响应推进到真实本地轮询响应；t 为测试上下文，不等待生产轮询定时器。
func TestManagedAliImageTaskStages(t *testing.T) {
	for _, tc := range []struct {
		name, body       string // 场景与最终轮询返回的原始 JSON。
		status           int    // 轮询 HTTP 状态。
		complete, failed bool   // 是否图片完成、是否应停止任务并标记异常。
	}{
		{"pending", `{"output":{"task_id":"fixture","task_status":"RUNNING"}}`, 200, false, false},
		{"success", `{"output":{"task_id":"fixture","task_status":"SUCCEEDED","results":[{"url":"https://fixture.invalid/i"}]}}`, 200, true, false},
		{"failed", `{"output":{"task_id":"fixture","task_status":"FAILED","message":"fixture failure"}}`, 200, false, true},
		{"missing image", `{"output":{"task_id":"fixture","task_status":"SUCCEEDED","results":[]}}`, 200, false, true},
		{"missing task id", `{"output":{"task_status":"RUNNING"}}`, 200, false, true},
		{"bad JSON", `{"output":`, 200, false, true},
		{"poll read failure", `{"output":{"task_id":"fixture","task_status":"SUCCEEDED","results":[{"url":"https://fixture.invalid/i"}]}}`, 200, false, true},
		{"poll HTTP failure", `{"code":"Unavailable","message":"fixture"}`, 503, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Fixture", "poll")
				if tc.name == "poll read failure" {
					w.Header().Set("Content-Length", "4096")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, Request: &dto.ImageRequest{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
			service.BeginStreamAttempt(c, info)
			initial := `{"output":{"task_id":"fixture","task_status":"PENDING"},"usage":{"input_tokens":3}}`
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(initial))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			relaycommon.UseStreamImageTaskResponse(resp)
			_, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.False(t, info.StreamSession.ProtocolComplete())
			require.Empty(t, info.StreamSession.Snapshot().Reason)
			require.NotContains(t, info.StreamSession.Snapshot().Evidence, "image_count")
			_, err, body := updateTask(c, info, "fixture")
			require.Equal(t, tc.body, string(body))
			if tc.failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			snapshot := info.StreamSession.Snapshot()
			require.Equal(t, tc.complete, snapshot.Complete)
			require.Equal(t, tc.failed, snapshot.Reason != "")
			require.Equal(t, 3, snapshot.Evidence["input_tokens"], "task polling must retain prior evidence")
			require.True(t, info.StreamSession.Active())
			diagnostic := info.StreamDiagnostic.Snapshot()
			require.Equal(t, tc.body, string(diagnostic.BodyHead))
			require.Equal(t, "poll", diagnostic.ResponseHeaders.Get("X-Fixture"))
			require.Len(t, diagnostic.PreviousResponses, 1)
		})
	}
}
