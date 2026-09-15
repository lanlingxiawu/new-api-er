package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestLogRestoredForPrivateStreamAndMessages(t *testing.T) {
	oldQueue := requestLogQueue.Load()
	oldEnabled, oldName, oldLimit := common.RequestLogEnabled, common.RequestLogUsername, common.RequestLogMaxBodyKB
	t.Cleanup(func() {
		requestLogQueue.Store(oldQueue)
		common.RequestLogEnabled, common.RequestLogUsername, common.RequestLogMaxBodyKB = oldEnabled, oldName, oldLimit
	})
	for _, path := range []string{"/v1/messages", "/v1/messages/", "/v1/chat/completions"} {
		for _, mode := range []string{"normal", "stream", "disconnect", "upstream-error", "non-200", "connection-failed", "disabled", "filtered", "truncated"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				queue := &requestLogQueueHandle{ch: make(chan requestLogTask, 2), done: make(chan struct{})}
				requestLogQueue.Store(queue)
				common.RequestLogEnabled, common.RequestLogUsername, common.RequestLogMaxBodyKB = mode != "disabled", "", 1
				if mode == "filtered" {
					common.RequestLogUsername = "someone-else"
				}
				body, response, status := `{"message":"fixture"}`, "response", 200
				if mode == "truncated" {
					response = strings.Repeat("x", 2048)
				}
				if mode == "non-200" {
					status = 429
				}
				if mode == "connection-failed" || mode == "upstream-error" {
					status = 502
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r := gin.New()
				r.Use(RequestResponseLogger())
				r.POST(path, func(c *gin.Context) {
					defer common.CleanupBodyStorage(c)
					c.Set("username", "fixture")
					if mode != "normal" {
						info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI}
						service.BeginStreamAttempt(c, info)
						require.True(t, common.IsPrivateStream(c))
					}
					if mode == "disconnect" {
						cancel()
					}
					c.String(status, response)
				})
				req := httptest.NewRequest("POST", path, strings.NewReader(body)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer fixture")
				r.ServeHTTP(httptest.NewRecorder(), req)
				if mode == "disabled" || mode == "filtered" {
					require.Empty(t, queue.ch)
					return
				}
				require.Len(t, queue.ch, 1)
				entry := (<-queue.ch).entry
				require.Equal(t, path, entry.Url)
				require.Equal(t, status, entry.StatusCode)
				require.Equal(t, body, entry.RequestBody)
				require.Contains(t, entry.RequestHeaders, "Bearer fixture")
				require.EqualValues(t, len(response), entry.ResponseBodySize)
				if mode == "truncated" {
					require.Contains(t, entry.ResponseBody, "[truncated]")
					require.Less(t, len(entry.ResponseBody), len(response))
				} else {
					require.Equal(t, response, entry.ResponseBody)
				}
			})
		}
	}
}

// Uses the original file-store fixtures to verify actual persistence, not only enqueue.
func TestRequestLogRestoredStreamPersistence(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	const frame = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n"
	r := gin.New()
	r.Use(RequestResponseLogger())
	r.POST("/v1/messages", func(c *gin.Context) {
		defer common.CleanupBodyStorage(c)
		c.Set("username", "fixture")
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.NoError(t, info.StreamSession.ObserveFrame([]byte(frame)))
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString(frame)
		require.NoError(t, err)
		c.Writer.Flush()
		require.True(t, info.StreamSession.Snapshot().Effective)
	})
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	require.Equal(t, `{"stream":true}`, detail.RequestBody)
	require.Equal(t, rec.Body.String(), detail.ResponseBody)
	require.Equal(t, frame, detail.ResponseBody)
}
