package xunfei

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type xunfeiDeadlineControl struct {
	xunfeiTimeoutControl
	deadline time.Time
}

func (c xunfeiDeadlineControl) RelayTimeoutDeadline() (time.Time, bool) {
	return c.deadline, true
}

func (xunfeiDeadlineControl) MarkResponse(bool) bool { return true }

func TestXunfeiPolicyReadLimit(t *testing.T) {
	payload := bytes.Repeat([]byte(" "), relaycommon.MaxStreamFrameBytes+1)
	copy(payload, `{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"complete"}]},"usage":{"text":{"prompt_tokens":16,"completion_tokens":5,"total_tokens":21}}}}`)
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 0
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	for _, strict := range []bool{false, true} {
		name := "main_accepts_large_usage"
		if strict {
			name = "strict_keeps_frame_limit"
		}
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				_ = conn.WriteMessage(websocket.TextMessage, payload)
			}))
			defer srv.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &strict},
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			usage, apiErr := managedXunfeiStream(c, dto.GeneralOpenAIRequest{}, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			require.Nil(t, apiErr)
			if strict {
				require.Zero(t, usage.TotalTokens)
				require.ErrorIs(t, info.StreamSession.Snapshot().Err, websocket.ErrReadLimit)
			} else {
				require.Equal(t, 21, usage.TotalTokens)
				require.Empty(t, info.StreamDiagnostic.Snapshot().Error)
			}
		})
	}
}

func TestXunfeiPolicyRequestWriteDeadline(t *testing.T) {
	for _, strict := range []bool{false, true} {
		name := "main_waits_for_request_acceptance"
		if strict {
			name = "strict_keeps_write_deadline"
		}
		t.Run(name, func(t *testing.T) {
			finished := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				select {
				case <-time.After(16 * time.Second):
				case <-finished:
					return
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":2},"usage":{"text":{"prompt_tokens":16,"completion_tokens":5,"total_tokens":21}}}}`))
			}))
			defer srv.Close()
			defer close(finished)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &strict},
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			request := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: strings.Repeat("x", 16<<20)}}}
			usage, apiErr := managedXunfeiStream(c, request, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			if strict {
				require.NotNil(t, apiErr)
				require.Nil(t, usage)
			} else {
				require.Nil(t, apiErr)
				require.Equal(t, 21, usage.TotalTokens)
			}
		})
	}
}

func TestXunfeiPolicyIdleTimeout(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	for _, tc := range []struct {
		name             string
		strict, deadline bool
		want             int
	}{
		{name: "main_waits_for_usage", want: 21},
		{name: "strict_keeps_idle_timeout", strict: true, want: 9},
		{name: "main_keeps_request_deadline", deadline: true, want: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finished := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hi"}]},"usage":{"text":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}}}`))
				select {
				case <-time.After(2 * time.Second):
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"late"}]},"usage":{"text":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}}}`))
				case <-finished:
				}
			}))
			defer srv.Close()
			defer close(finished)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.deadline {
				common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl,
					xunfeiDeadlineControl{deadline: time.Now().Add(500 * time.Millisecond)})
			}
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &tc.strict},
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			usage, apiErr := managedXunfeiStream(c, dto.GeneralOpenAIRequest{}, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			require.Nil(t, apiErr)
			require.Equal(t, tc.want, usage.TotalTokens)
			if !tc.strict && !tc.deadline {
				require.Contains(t, rec.Body.String(), "late")
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.Empty(t, info.StreamDiagnostic.Snapshot().Error)
			}
		})
	}
}

func TestXunfeiMainPolicyAddsUsageAndKeepsMainTerminator(t *testing.T) {
	for _, ending := range []string{"complete", "bad JSON", "disconnect"} {
		t.Run(ending, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				_, _, _ = conn.ReadMessage()
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hi"}]},"usage":{"text":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}}}`))
				if ending == "complete" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":2},"usage":{"text":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}}}`))
				}
				if ending == "bad JSON" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte("{"))
				}
			}))
			defer srv.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(false)}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			u, apiErr := managedXunfeiStream(c, dto.GeneralOpenAIRequest{}, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			require.Nil(t, apiErr)
			require.Same(t, u, service.FinalizeStreamUsage(c, info, u))
			require.Nil(t, info.StreamResult)
			require.Contains(t, rec.Body.String(), "[DONE]")
			require.NotContains(t, rec.Body.String(), "event: error")
			if ending == "complete" {
				require.Equal(t, 21, u.TotalTokens)
				require.Equal(t, 16, u.PromptTokens)
			} else {
				require.Equal(t, 9, u.TotalTokens)
				require.NotEmpty(t, info.StreamDiagnostic.Snapshot().Error)
			}
		})
	}
}

// TestUnifiedXunfeiStream 覆盖讯飞 WS 正常结束、缺少结束及坏 JSON，使用本地上游夹具。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedXunfeiStream(t *testing.T) {
	for _, ending := range []string{`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"ok"}]},"usage":{"text":{"prompt_tokens":9,"completion_tokens":2}}}}`, "{bad", ""} {
		t.Run(ending, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Upstream": []string{"fixture"}})
				if err != nil {
					return
				}
				defer conn.Close()
				_, _, _ = conn.ReadMessage()
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hi"}]}}}`))
				if ending != "" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(ending))
				}
			}))
			defer srv.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			u, _ := managedXunfeiStream(c, dto.GeneralOpenAIRequest{}, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			service.FinalizeStreamUsage(c, info, u)
			require.True(t, info.StreamResult.EffectiveContent)
			if strings.Contains(ending, `"status":2`) {
				require.False(t, info.StreamResult.Failed)
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 2, info.StreamFinalUsage.CompletionTokens)
			} else {
				require.True(t, info.StreamResult.Failed)
				require.Contains(t, rec.Body.String(), "event: error")
				require.NotContains(t, rec.Body.String(), "[DONE]")
			}
			require.Equal(t, "fixture", info.StreamResult.Diagnostic.ResponseHeaders.Get("X-Upstream"))
		})
	}
}
