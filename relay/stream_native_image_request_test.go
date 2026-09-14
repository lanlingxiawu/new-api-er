package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestUnifiedStreamNativeImageFinalRequest 使用本地 503 截获实际出站正文，再验证响应约束；t 不执行计费持久化。
// 覆盖原生协议字段、覆盖增减/删除/null、入口缺省/Extra、透传以及同一 info 的多次尝试。
func TestUnifiedStreamNativeImageFinalRequest(t *testing.T) {
	for _, provider := range []struct {
		channel int    // 本次渠道，决定最终原生计数字段。
		model   string // 适配器实际支持的本地夹具模型名。
		path    string // 原生出站张数字段。
		body    string // 已完成的一张原生图片响应，不访问图片 URL。
	}{
		{constant.ChannelTypeMiniMax, "image-01", "n", `{"data":{"image_urls":["https://fixture.invalid/image"]},"base_resp":{"status_code":0}}`},
		{constant.ChannelTypeAli, "qwen-image", "parameters.n", `{"output":{"results":[{"url":"https://fixture.invalid/image"}]}}`},
	} {
		for _, passthrough := range []bool{false, true} {
			for _, entry := range []string{``, `,"n":2`, `,"n":2,"parameters":{"n":1}`, `,"parameters":{"n":3}`} {
				t.Run(provider.model+"/"+strconv.FormatBool(passthrough)+"/"+entry, func(t *testing.T) {
					received := make(chan string, 1)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						received <- string(body)
						w.WriteHeader(503) // 阻止真实结算，仅检查发送前约束。
						_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
					}))
					defer srv.Close()
					raw := `{"model":"` + provider.model + `","prompt":"fixture","stream":true` + entry + `}`
					req := &dto.ImageRequest{}
					require.NoError(t, common.UnmarshalJsonStr(raw, req))
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(raw))
					c.Request.Header.Set("Content-Type", "application/json")
					common.SetContextKey(c, constant.ContextKeyChannelType, provider.channel)
					common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
					common.SetContextKey(c, constant.ContextKeyOriginalModel, provider.model)
					common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
					info := &relaycommon.RelayInfo{IsStream: req.IsStream(c.Request), DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations, RequestURLPath: c.Request.URL.Path, OriginModelName: provider.model, Request: req, ChannelMeta: &relaycommon.ChannelMeta{}}
					for _, value := range []any{1, 3, nil, "delete", "unchanged"} {
						override := map[string]any{}
						if value != "unchanged" {
							op := map[string]any{"mode": "set", "path": provider.path, "value": value}
							if value == "delete" {
								op["mode"] = "delete"
							}
							override["operations"] = []any{op}
						}
						common.SetContextKey(c, constant.ContextKeyChannelParamOverride, override)
						service.BeginStreamAttempt(c, info)
						apiErr := ImageHelper(c, info)
						require.NotNil(t, apiErr)
						require.Equal(t, 503, apiErr.StatusCode)
						body := <-received
						if passthrough {
							require.Equal(t, raw, body, "透传正文保持原字节，不应用覆盖")
						}
						want := max(1, int(gjson.Get(body, provider.path).Int()))
						require.Equal(t, want, info.StreamSession.ExpectedImages, body)
						require.False(t, info.StreamSession.Active())
						require.Nil(t, info.StreamResult)
						resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(provider.body))}
						info.StreamSession.ObserveTransport(resp, nil)
						info.StreamSession.ObserveHTTP(resp)
						_, err := io.ReadAll(resp.Body)
						require.Equal(t, want == 1, err == nil, body)
						require.Equal(t, want == 1, info.StreamSession.ProtocolComplete(), body)
						info.StreamSession.CloseUpstream()
					}
				})
			}
		}
	}
}
