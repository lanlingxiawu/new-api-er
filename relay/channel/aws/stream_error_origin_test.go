package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"
)

func TestStreamAwsExceptionLogMessage(t *testing.T) {
	for _, tc := range []struct {
		name, exception, payload, want string
		truncate                       bool
	}{
		{"model error", "modelStreamErrorException", `{"message":"model stream failed api_key:secret (request id: stale)","originalMessage":"private-detail"}`, "model stream failed api_key:***", false},
		{"throttling", "throttlingException", `{"message":"rate exceeded"}`, "rate exceeded", false},
		{"unknown API exception", "fixtureException", `{"message":"explicit SDK message"}`, "explicit SDK message", false},
		{"empty message", "validationException", `{"message":""}`, "", false},
		{"blank message", "validationException", `{"message":" \t (request id: stale)"}`, "", false},
		{"truncated frame", "validationException", `{"message":"private-incomplete"}`, "", true},
	} {
		for _, delivered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delivered=%t", tc.name, delivered), func(t *testing.T) {
				var raw bytes.Buffer
				if delivered {
					for _, event := range []string{
						`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`,
						`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
						`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
					} {
						require.NoError(t, writeAwsStreamEvent(&raw, event))
					}
				}
				require.NoError(t, eventstream.NewEncoder().Encode(&raw, eventstream.Message{
					Headers: eventstream.Headers{
						{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.ExceptionMessageType)},
						{Name: eventstreamapi.ExceptionTypeHeader, Value: eventstream.StringValue(tc.exception)},
						{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
					},
					Payload: []byte(tc.payload),
				}))
				wire := raw.Bytes()
				if tc.truncate {
					wire = wire[:len(wire)-1]
				}
				rec := httptest.NewRecorder()
				c := newAwsTestContext(rec, context.Background())
				info := newAwsTestRelayInfo()
				service.BeginStreamAttempt(c, info)
				client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
					return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(wire))), nil
				})})
				apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
				require.Nil(t, apiErr, "retain the managed stream settlement path")
				snapshot := info.StreamSession.Snapshot()
				require.Equal(t, relaycommon.StreamEndReason("upstream_read_error"), snapshot.Reason)
				wantDiagnostic := snapshot.Err.Error()
				if !tc.truncate {
					var sdkErr smithy.APIError
					require.ErrorAs(t, snapshot.Err, &sdkErr, "retain SDK error identity")
					wantDiagnostic = sdkErr.Error()
				}
				finalUsage := service.FinalizeStreamUsage(c, info, usage)
				require.True(t, info.StreamResult.DiagnosticAvailable)
				require.Equal(t, wantDiagnostic, info.StreamResult.Diagnostic.Error, "private SDK cause remains unchanged")
				require.Equal(t, wire, append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
				require.Equal(t, delivered, info.StreamResult.EffectiveContent)
				if delivered {
					require.Equal(t, "upstream", info.StreamResult.UsageSource)
					require.Equal(t, 10, finalUsage.PromptTokens)
					require.Equal(t, 1, finalUsage.CompletionTokens)
				} else {
					require.Equal(t, "none", info.StreamResult.UsageSource)
					require.Zero(t, finalUsage.TotalTokens)
				}
				want := tc.want
				if want == "" {
					want = i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
				}
				require.Equal(t, want, info.StreamResult.ErrorMessage)
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.NotContains(t, rec.Body.String(), "private-detail")
				require.Contains(t, rec.Body.String(), "upstream_stream_error", "downstream response remains unchanged")
				if tc.want != "" {
					require.NotContains(t, rec.Body.String(), tc.want, "log selection does not replace the terminal response")
				}
			})
		}
	}
}

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
