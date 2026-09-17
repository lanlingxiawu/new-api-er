package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestUnifiedStreamFinalCandidateRequest 比较真实出站参数与会话约束；t 管理本地 HTTP 夹具，503 提前退出资金路径。
func TestUnifiedStreamFinalCandidateRequest(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeGemini} {
			for _, passthrough := range []bool{false, true} {
				// 透传只测试同协议；跨协议透传的原文并不是目标协议请求。
				if passthrough && native != (channelType == constant.ChannelTypeGemini) {
					continue
				}
				for _, count := range []int{1, 3, 0, -1} {
					received := make(chan string, 1)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						received <- string(body)
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
					}))
					t.Cleanup(srv.Close)
					raw := `{"model":"fixture","stream":true,"n":2,"messages":[{"role":"user","content":"hi"}]}`
					var request dto.Request = &dto.GeneralOpenAIRequest{}
					format, mode, path := types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, "/v1/chat/completions"
					if native {
						raw = `{"generationConfig":{"candidateCount":2},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
						request = &dto.GeminiChatRequest{}
						format, mode, path = types.RelayFormatGemini, relayconstant.RelayModeGemini, "/v1beta/models/fixture:streamGenerateContent"
					}
					require.NoError(t, common.UnmarshalJsonStr(raw, request))
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", path, strings.NewReader(raw))
					c.Request.Header.Set("Content-Type", "application/json")
					common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
					common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
					common.SetContextKey(c, constant.ContextKeyOriginalModel, "fixture")
					common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
					key := "n"
					if channelType == constant.ChannelTypeGemini {
						key = "generationConfig.candidateCount"
					}
					operation := map[string]any{"path": key, "mode": "set", "value": count}
					if count == 0 {
						operation["mode"] = "delete"
					}
					common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{operation}})
					if count < 0 {
						common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{})
					}
					info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, RelayMode: mode, RequestURLPath: path, OriginModelName: "fixture", Request: request, ChannelMeta: &relaycommon.ChannelMeta{}}
					for attempt := 0; attempt < 2; attempt++ {
						want := count
						if attempt == 1 {
							// 重试采用不同的覆盖，验证不沿用前次候选约束。
							want = 3
							if count == 3 {
								want = 1
							}
							common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"path": key, "mode": "set", "value": want}}})
						}
						service.BeginStreamAttempt(c, info)
						handler := TextHelper
						if native {
							handler = GeminiHelper
						}
						err := handler(c, info)
						require.NotNil(t, err)
						require.Equal(t, 503, err.StatusCode)
						require.False(t, info.StreamSession.Active(), "同步候选参数不改变非 200 的旧处理路径")
						require.Nil(t, info.StreamResult)
						body := <-received
						if want < 0 {
							// 不覆盖时，候选数可能被渠道转换保留或移除，须与真正发送的字段一致。
							want = int(gjson.Get(body, key).Int())
						}
						if passthrough {
							want = 2
							require.Equal(t, raw, body)
						}
						require.Equal(t, int64(want), gjson.Get(body, key).Int(), body)
						require.Equal(t, want, info.StreamSession.ExpectedChoices, body)
						info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"index":0,"finish_reason":"stop"}]}`)))
						require.Equal(t, want <= 1, info.StreamSession.ProtocolComplete())
					}
				}
			}
		}
	}
}

// TestUnifiedStreamCandidateConversionPaths 覆盖 Claude、Responses 和 Chat 经 Responses 的最终参数；t 控制返回 503 的本地上游。
func TestUnifiedStreamCandidateConversionPaths(t *testing.T) {
	for _, entry := range []string{"claude", "responses", "chat_via_responses"} {
		t.Run(entry, func(t *testing.T) {
			received := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- string(body)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
			}))
			defer srv.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "fixture")
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, OriginModelName: "fixture", RelayFormat: types.RelayFormatClaude, RelayMode: relayconstant.RelayModeChatCompletions, RequestURLPath: "/v1/messages", ChannelMeta: &relaycommon.ChannelMeta{}}
			var apiErr *types.NewAPIError
			want := 0
			switch entry {
			case "claude":
				info.Request = &dto.ClaudeRequest{}
				require.NoError(t, common.UnmarshalJsonStr(`{"model":"fixture","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, info.Request))
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"n": 3})
				service.BeginStreamAttempt(c, info)
				apiErr = ClaudeHelper(c, info)
				want = 3
			case "responses":
				info.RelayFormat, info.RelayMode, info.RequestURLPath = types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, "/v1/responses"
				info.Request = &dto.OpenAIResponsesRequest{}
				require.NoError(t, common.UnmarshalJsonStr(`{"model":"fixture","stream":true,"input":"hi"}`, info.Request))
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ExpectedChoices = 9
				apiErr = ResponsesHelper(c, info)
			case "chat_via_responses":
				info.RelayFormat, info.RequestURLPath = types.RelayFormatOpenAI, "/v1/chat/completions"
				req := &dto.GeneralOpenAIRequest{}
				require.NoError(t, common.UnmarshalJsonStr(`{"model":"fixture","stream":true,"n":1,"messages":[{"role":"user","content":"hi"}]}`, req))
				info.Request = req
				service.BeginStreamAttempt(c, info)
				info.InitChannelMeta(c)
				adaptor := GetAdaptor(info.ApiType)
				adaptor.Init(info)
				_, apiErr = textRequestViaResponses(c, info, adaptor, req)
			}
			require.NotNil(t, apiErr)
			require.Equal(t, 503, apiErr.StatusCode)
			body := <-received
			require.Equal(t, int64(want), gjson.Get(body, "n").Int(), body)
			require.Equal(t, want, info.StreamSession.ExpectedChoices, body)
		})
	}
}
