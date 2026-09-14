package common

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamNativeImageJSON 验证原生图片结构及渠道隔离；t 为测试上下文，仅使用内存响应。
func TestStreamNativeImageJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string // 用例名与转换前的原始 JSON。
		channel, n int    // 渠道和请求图片数。
		valid      bool   // 是否足量、完整且已终结。
	}{
		{"minimax url", `{"data":{"image_urls":["https://fixture.invalid/i"]},"base_resp":{"status_code":0}}`, constant.ChannelTypeMiniMax, 1, true},
		{"minimax base64", `{"data":{"image_base64":["aGk="]}}`, constant.ChannelTypeMiniMax, 1, true},
		{"minimax multiple", `{"data":{"image_urls":["a","b"]}}`, constant.ChannelTypeMiniMax, 2, true},
		{"minimax empty", `{"data":{"image_urls":[]}}`, constant.ChannelTypeMiniMax, 1, false},
		{"minimax blank", `{"data":{"image_urls":[" "]}}`, constant.ChannelTypeMiniMax, 1, false},
		{"minimax damaged", `{"data":{"image_urls":["a",{}]}}`, constant.ChannelTypeMiniMax, 1, false},
		{"minimax insufficient", `{"data":{"image_urls":["a"]}}`, constant.ChannelTypeMiniMax, 2, false},
		{"minimax error", `{"data":{"image_urls":["a"]},"base_resp":{"status_code":1001}}`, constant.ChannelTypeMiniMax, 1, false},
		{"ali result url", `{"output":{"results":[{"url":"https://fixture.invalid/i"}]}}`, constant.ChannelTypeAli, 1, true},
		{"ali result base64", `{"output":{"results":[{"b64_image":"aGk="}]}}`, constant.ChannelTypeAli, 1, true},
		{"ali choices", `{"output":{"choices":[{"message":{"content":[{"image":"aGk="},{"text":"caption"}]}}]}}`, constant.ChannelTypeAli, 1, true},
		{"ali choices count per choice", `{"output":{"choices":[{"message":{"content":[{"image":"a"},{"image":"b"}]}}]}}`, constant.ChannelTypeAli, 2, false},
		{"ali text not image", `{"output":{"choices":[{"message":{"content":[{"text":"caption"}]}}]}}`, constant.ChannelTypeAli, 1, false},
		{"ali empty", `{"output":{"results":[{}]}}`, constant.ChannelTypeAli, 1, false},
		{"ali error", `{"code":"Failed","message":"fixture","output":{"results":[{"url":"a"}]}}`, constant.ChannelTypeAli, 1, false},
		{"sync excludes task", `{"output":{"task_id":"fixture","task_status":"PENDING"}}`, constant.ChannelTypeAli, 1, false},
		{"other channel isolation", `{"data":{"image_urls":["a"]}}`, constant.ChannelTypeOpenAI, 1, false},
		{"openai unchanged", `{"data":[{"url":"a"}]}`, constant.ChannelTypeOpenAI, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ChannelType, s.ExpectedImages = tc.channel, tc.n
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			s.ObserveHTTP(resp)
			body, err := io.ReadAll(resp.Body)
			require.Equal(t, tc.body, string(body))
			require.Equal(t, tc.valid, s.Snapshot().Complete)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, tc.n, s.Snapshot().Evidence["image_count"])
			} else {
				require.Error(t, err)
				require.Empty(t, s.Snapshot().Evidence)
			}
		})
	}
}

// nativeImageReadError 在完整正文的最后一次 Read 同时返回传输错误，用于验证中间态不掩盖断流。
type nativeImageReadError struct{ *strings.Reader }

// Read 向 p 复制已有字节，正文读尽时返回 unexpected EOF 而非正常 EOF。
func (r *nativeImageReadError) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.Len() == 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// Close 关闭内存夹具，无外部资源。
func (r *nativeImageReadError) Close() error { return nil }

// TestStreamAliTaskIntermediateEOF 验证任务阶段声明、字段类型及 EOF/读取错误分离；t 为测试上下文。
func TestStreamAliTaskIntermediateEOF(t *testing.T) {
	for _, tc := range []struct {
		name, body         string // 阶段场景与原始正文。
		readError, invalid bool   // 是否注入底层错误及是否预期失败。
	}{
		{"pending", `{"output":{"task_id":"id","task_status":"PENDING"}}`, false, false},
		{"running", `{"output":{"task_id":"id","task_status":"RUNNING"}}`, false, false},
		{"numeric id", `{"output":{"task_id":123,"task_status":"PENDING"}}`, false, true},
		{"empty id", `{"output":{"task_id":" ","task_status":"PENDING"}}`, false, true},
		{"unknown stage", `{"output":{"task_id":"id","task_status":"OTHER"}}`, false, true},
		{"pending read failure", `{"output":{"task_id":"id","task_status":"PENDING"}}`, true, true},
		{"final read failure", `{"output":{"task_id":"id","task_status":"SUCCEEDED","results":[{"url":"a"}]}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ChannelType, s.ExpectedImages = constant.ChannelTypeAli, 1
			var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
			if tc.readError {
				body = &nativeImageReadError{strings.NewReader(tc.body)}
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
			s.ObserveHTTP(resp)
			UseStreamImageTaskResponse(resp)
			raw, err := io.ReadAll(resp.Body)
			require.Equal(t, tc.body, string(raw))
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.False(t, s.ProtocolComplete())
			require.NotContains(t, s.Snapshot().Evidence, "image_count")
			if tc.readError {
				require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
			}
		})
	}
}
