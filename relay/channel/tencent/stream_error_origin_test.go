package tencent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// tencentReadFailureBody 在帧已读完后返回指定错误，模拟正常尾帧后连接复位或中途断流。
type tencentReadFailureBody struct {
	*strings.Reader       // 已准备的原始上游帧。
	failure         error // 全部字节读完后的底层错误。
}

// Read 复制上游字节到 p；数据耗尽后以 failure 替代 EOF。
func (b *tencentReadFailureBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF && b.failure != nil {
		return n, b.failure
	}
	return n, err
}

// Close 满足本地响应体接口；夹具没有外部资源。
func (b *tencentReadFailureBody) Close() error { return nil }

// TestStreamTencentErrorOrigin 验证正常完成后读错不改判、DTO 错误及中途读错保留来源；t 为上下文。
func TestStreamTencentErrorOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, extra, finish, reason string // 用例名、附加字段、结束标记与期望首因。
		readErr                     error  // 帧后的运输层错误。
	}{
		{"complete then read error", "", "stop", "", io.ErrUnexpectedEOF},
		{"incomplete read error", "", "", "upstream_read_error", io.ErrUnexpectedEOF},
		{"incomplete EOF", "", "", "upstream_incomplete", nil},
		{"invalid DTO on stop", `,"Note":123`, "stop", "upstream_json_error", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeTencent)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			service.BeginStreamAttempt(c, info)
			body := `data: {"Choices":[{"Delta":{"Content":"hello"},"FinishReason":"` + tc.finish + `"}],"Usage":{"PromptTokens":10,"CompletionTokens":3,"TotalTokens":13}` + tc.extra + "}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &tencentReadFailureBody{strings.NewReader(body), tc.readErr}}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := tencentStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, relaycommon.StreamEndReason(tc.reason), info.StreamSession.Snapshot().Reason)
			require.Equal(t, tc.reason != "", info.StreamResult.Failed)
			require.Equal(t, tc.reason != "", info.StreamResult.DiagnosticAvailable)
			if tc.reason == "" {
				require.True(t, info.StreamSession.ProtocolComplete())
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.NotContains(t, rec.Body.String(), "upstream_stream_error")
			} else {
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.Contains(t, rec.Body.String(), "upstream_stream_error")
			}
		})
	}
}
