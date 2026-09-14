package aws

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// TestUnifiedAwsOriginalErrorInvalidUsage 用 t 和本地 SDK 事件验证原 error 的保留、SSE 多行包装及既有证据原子性。
func TestUnifiedAwsOriginalErrorInvalidUsage(t *testing.T) {
	for _, payload := range []string{
		`{"type":"error","error":{"type":"api_error","message":"original"},"usage":"invalid"}`,
		`{"type":"error","error":{"type":"api_error","message":"original"},"usage":{"input_tokens":999,"output_tokens":-1}}`,
		" \n{\r\n \"type\":\"error\",\n \"error\":{\"type\":\"api_error\",\"message\":\"original\"},\n \"usage\":\"invalid\"\r\n}\t ",
	} {
		var raw bytes.Buffer
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"m","model":"claude","usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`, payload,
		} {
			require.NoError(t, writeAwsStreamEvent(&raw, event))
		}
		rec := httptest.NewRecorder()
		c := newAwsTestContext(rec, context.Background())
		info := newAwsTestRelayInfo()
		info.RelayFormat = types.RelayFormatClaude
		service.BeginStreamAttempt(c, info)
		client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(raw.Bytes()))), nil
		})})
		err, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
		require.NotNil(t, err)
		final := service.FinalizeStreamUsage(c, info, usage)
		require.Equal(t, 10, final.PromptTokens)
		require.Positive(t, final.CompletionTokens)
		require.Equal(t, "mixed", info.StreamResult.UsageSource)
		require.Equal(t, 10, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
		require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), info.StreamStatus.EndReason)
		body := rec.Body.String()
		require.Equal(t, 1, strings.Count(body, "event: error\n"))
		start := strings.Index(body, "event: error\n")
		require.GreaterOrEqual(t, start, 0)
		event, data := relaycommon.StreamFramePayload([]byte(body[start:]))
		require.Equal(t, "error", event)
		want := strings.ReplaceAll(strings.ReplaceAll(payload, "\r\n", "\n"), "\r", "\n")
		require.Equal(t, want, string(data), "SDK 原 JSON 只添加 SSE 外壳，不重写字段或丢弃换行后的正文")
		service.FinalizeStreamUsage(c, info, usage)
		require.Equal(t, body, rec.Body.String(), "重复结算不追加第二个错误")
	}
}
