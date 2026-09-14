package aws

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/stretchr/testify/require"
)

// TestStreamAwsDTOErrorOrigin 验证 SDK 解码后的 DTO 错误按上游解析分类并保存原二进制；t 为上下文。
func TestStreamAwsDTOErrorOrigin(t *testing.T) {
	var raw bytes.Buffer
	require.NoError(t, writeAwsStreamEvent(&raw, `{"type":"message_start","message":{"id":123,"model":"claude","usage":{"input_tokens":10,"output_tokens":0}}}`))
	rec := httptest.NewRecorder()
	c := newAwsTestContext(rec, context.Background())
	info := newAwsTestRelayInfo()
	service.BeginStreamAttempt(c, info)
	client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
		return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(raw.Bytes()))), nil
	})})
	apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
	require.NotNil(t, apiErr)
	service.FinalizeStreamUsage(c, info, usage)
	require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), info.StreamSession.Snapshot().Reason)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.Equal(t, "none", info.StreamResult.UsageSource)
	require.Equal(t, raw.Bytes(), append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
	require.NotContains(t, rec.Body.String(), "[DONE]")
}

// TestStreamAwsUnknownEventOrigin 验证真实 SDK 的未知联合事件进入上游协议诊断；t 不访问 AWS 或资金库。
func TestStreamAwsUnknownEventOrigin(t *testing.T) {
	var raw bytes.Buffer
	require.NoError(t, eventstream.NewEncoder().Encode(&raw, eventstream.Message{
		Headers: eventstream.Headers{
			{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.EventMessageType)},
			{Name: eventstreamapi.EventTypeHeader, Value: eventstream.StringValue("fixtureUnknown")},
			{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
		},
		Payload: []byte(`{}`),
	}))
	rec := httptest.NewRecorder()
	c := newAwsTestContext(rec, context.Background())
	info := newAwsTestRelayInfo()
	service.BeginStreamAttempt(c, info)
	client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
		return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(raw.Bytes()))), nil
	})})
	apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
	require.NotNil(t, apiErr)
	service.FinalizeStreamUsage(c, info, usage)
	require.Equal(t, relaycommon.StreamEndReason("upstream_protocol_error"), info.StreamSession.Snapshot().Reason)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.Contains(t, info.StreamResult.Diagnostic.Error, "fixtureUnknown")
	require.Equal(t, "none", info.StreamResult.UsageSource)
	require.Equal(t, raw.Bytes(), append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
}
