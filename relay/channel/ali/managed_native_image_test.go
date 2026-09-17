package ali

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestManagedNativeImageTypedJSONError 验证原生结构观察通过但 DTO 字段类型错误仍属上游异常；t 为测试上下文。
func TestManagedNativeImageTypedJSONError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, Request: &dto.ImageRequest{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
	service.BeginStreamAttempt(c, info)
	raw := `{"output":{"results":[{"url":"https://fixture.invalid/image"}]},"request_id":123}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(raw))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	apiErr, _ := aliImageHandler(&Adaptor{IsSyncImageModel: true}, c, resp, info)
	require.NotNil(t, apiErr)
	selected := service.FinalizeStreamUsage(c, info, nil)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), info.StreamSession.Snapshot().Reason)
	require.Equal(t, raw, string(info.StreamResult.Diagnostic.BodyHead))
	require.Zero(t, selected.TotalTokens)
}

// TestManagedNativeImageHandler 验证原始 JSON 观察、原适配器转换和结算选择；t 为本地夹具测试上下文。
func TestManagedNativeImageHandler(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAli)
			info := &relaycommon.RelayInfo{IsStream: streaming, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations, Request: &dto.ImageRequest{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
			info.PriceData.UsePrice = true
			service.BeginStreamAttempt(c, info)
			raw := `{"output":{"choices":[{"message":{"content":[{"text":"no image"}]}}]}}`
			if valid {
				raw = `{"output":{"choices":[{"message":{"content":[{"image":"https://fixture.invalid/image"}]}}]}}`
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(raw))}
			if info.StreamSession != nil {
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
			}
			apiErr, usage := aliImageHandler(&Adaptor{IsSyncImageModel: true}, c, resp, info)
			if valid || !streaming {
				require.Nil(t, apiErr)
			} else {
				require.NotNil(t, apiErr)
			}
			selected := service.FinalizeStreamUsage(c, info, usage)
			if !streaming {
				require.Nil(t, info.StreamResult)
				continue
			}
			require.Equal(t, !valid, info.StreamResult.Failed)
			require.Equal(t, valid, info.StreamResult.EffectiveContent)
			require.Equal(t, !valid, info.StreamResult.DiagnosticAvailable)
			if valid {
				require.Contains(t, rec.Body.String(), "https://fixture.invalid/image")
				require.Equal(t, 1, info.StreamSession.Snapshot().Evidence["image_count"])
			} else {
				require.Equal(t, "none", info.StreamResult.UsageSource)
				require.Zero(t, selected.TotalTokens)
				require.Contains(t, rec.Body.String(), "upstream_stream_error")
				require.Equal(t, raw, string(info.StreamResult.Diagnostic.BodyHead))
			}
			otherLog := model.NewLogOther()
			service.AppendStreamLogInfo(info, otherLog)
			other := otherLog.Snapshot()
			if valid {
				require.Nil(t, other["stream_diagnostic"].(relaycommon.StreamDiagnostic).BodyHead)
			}
		}
	}
}
