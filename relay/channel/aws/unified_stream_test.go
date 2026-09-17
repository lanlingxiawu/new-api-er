package aws

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"
)

// TestStreamResponseGateAWSRetry 使用真实 SDK 的内部重试验证非 200 不采集、后续 200 恢复资格。
// 参数 t 为测试上下文；HTTP 客户端和退避均为零延迟本地夹具，不访问 AWS。
func TestStreamResponseGateAWSRetry(t *testing.T) {
	for _, finalSuccess := range []bool{false, true} {
		c := newAwsTestContext(httptest.NewRecorder(), context.Background())
		info := newAwsTestRelayInfo()
		service.BeginStreamAttempt(c, info)
		attempts := 0
		var raw bytes.Buffer
		require.NoError(t, writeAwsStreamEvent(&raw, `{"type":"ping"}`))
		client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 2 && finalSuccess {
				return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(raw.Bytes()))), nil
			}
			return &http.Response{StatusCode: 503, Request: r, Header: http.Header{"Content-Type": {"application/json"}, "X-Failed-Exchange": {"not-captured"}}, Body: io.NopCloser(strings.NewReader(`{"message":"retry fixture"}`))}, nil
		})})
		resp, err := client.InvokeModelWithResponseStream(context.Background(), newAwsStreamInput(), func(o *bedrockruntime.Options) {
			o.Retryer = retry.NewStandard(func(s *retry.StandardOptions) {
				s.MaxAttempts = 2
				s.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
			})
		})
		require.Equal(t, 2, attempts)
		if finalSuccess {
			require.NoError(t, err)
			for range resp.GetStream().Events() {
			}
			require.NoError(t, resp.GetStream().Close())
			d := info.StreamDiagnostic.Snapshot()
			require.True(t, info.StreamSession.Active())
			require.Equal(t, 200, d.StatusCode)
			require.Empty(t, d.PreviousResponses)
			require.Empty(t, d.ResponseHeaders.Get("X-Failed-Exchange"))
			require.Equal(t, raw.Bytes(), append(d.BodyHead, d.BodyTail...))
		} else {
			require.Error(t, err)
			require.False(t, service.FinalizeStreamFailure(c, info, err))
			require.Nil(t, info.StreamResult)
			other := model.NewLogOther()
			service.AppendStreamErrorDiagnostic(c, other, err)
			require.Empty(t, other.Snapshot())
			require.Equal(t, relaycommon.StreamDiagnostic{}, info.StreamDiagnostic.Snapshot())
		}
	}
}

// TestUnifiedAwsSDKStream 验证 SDK 二进制原始采集与解码后结束校验，两者不重复计费或补成功。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedAwsSDKStream(t *testing.T) {
	for _, complete := range []bool{true, false} {
		var raw bytes.Buffer
		events := []string{`{"type":"message_start","message":{"id":"m","model":"claude","usage":{"input_tokens":10,"output_tokens":0}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`}
		if complete {
			events = append(events, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`, `{"type":"message_stop"}`)
		}
		for _, e := range events {
			require.NoError(t, writeAwsStreamEvent(&raw, e))
		}
		rec := httptest.NewRecorder()
		c := newAwsTestContext(rec, context.Background())
		info := newAwsTestRelayInfo()
		service.BeginStreamAttempt(c, info)
		client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(raw.Bytes()))), nil
		})})
		err, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
		require.Nil(t, err)
		service.FinalizeStreamUsage(c, info, usage)
		require.Equal(t, !complete, info.StreamResult.Failed)
		require.Equal(t, !complete, info.StreamResult.DiagnosticAvailable)
		require.True(t, info.StreamResult.EffectiveContent)
		require.Equal(t, raw.Bytes(), append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
		if complete {
			require.Equal(t, 2, info.StreamFinalUsage.BillingUsage.ClaudeUsage.OutputTokens)
			require.Contains(t, rec.Body.String(), "[DONE]")
		} else {
			require.Positive(t, info.StreamFinalUsage.CompletionTokens)
			require.Equal(t, "mixed", info.StreamResult.UsageSource)
			require.Equal(t, 10, info.StreamFinalUsage.PromptTokens)
			require.NotContains(t, rec.Body.String(), "[DONE]")
			require.Contains(t, rec.Body.String(), "event: error")
		}
	}
}

// TestUnifiedAwsNativeClaudeDelta 验证真实 SDK 顺序下 message_delta 在 message_stop 前即可交付，截断不补成功。
// 参数 t 为测试上下文；本地 HTTP 夹具只生成 AWS 二进制事件，不请求外部服务。
func TestUnifiedAwsNativeClaudeDelta(t *testing.T) {
	for _, complete := range []bool{false, true} {
		var raw bytes.Buffer
		events := []string{`{"type":"message_start","message":{"id":"m","model":"claude","usage":{"input_tokens":10,"output_tokens":0}}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`}
		if complete {
			events = append(events, `{"type":"message_stop"}`)
		}
		for _, event := range events {
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
		require.Nil(t, err)
		service.FinalizeStreamUsage(c, info, usage)
		require.Contains(t, rec.Body.String(), `"stop_reason":"end_turn"`)
		require.Contains(t, rec.Body.String(), `"output_tokens":2`)
		require.Equal(t, complete, strings.Contains(rec.Body.String(), "event: message_stop"))
		require.Equal(t, !complete, info.StreamResult.Failed)
	}
}

// TestUnifiedAwsNovaIncompleteJSON 验证格式不完整的 Nova JSON 进入异常结算而非数组越界 panic。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedAwsNovaIncompleteJSON(t *testing.T) {
	c := newAwsTestContext(httptest.NewRecorder(), context.Background())
	info := newAwsTestRelayInfo()
	service.BeginStreamAttempt(c, info)
	client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"output":{"message":{"content":[]}}}`))}, nil
	})})
	err, _ := handleNovaRequest(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsInvokeModelInput()})
	require.NotNil(t, err)
	require.True(t, info.StreamSession.Snapshot().DiagnosticAvailable(false), "upstream schema error is not a local conversion failure")
}

// TestUnifiedAwsNovaCompleteJSON 验证 SDK 完整 JSON 成功路径仍保留文本与计量，缺失 stopReason 则进入异常。
// 参数 t 为测试上下文；用本地 HTTP 客户端夹具执行 SDK 反序列化，兼顾实际 ReadAll 行为。
func TestUnifiedAwsNovaCompleteJSON(t *testing.T) {
	for _, complete := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c := newAwsTestContext(rec, context.Background())
		info := newAwsTestRelayInfo()
		service.BeginStreamAttempt(c, info)
		stop := ""
		if complete {
			stop = `,"stopReason":"end_turn"`
		}
		body := `{"output":{"message":{"content":[{"text":"NOVA_TEXT"}]}},"usage":{"inputTokens":4,"outputTokens":1,"totalTokens":5}` + stop + `}`
		client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		err, usage := handleNovaRequest(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsInvokeModelInput()})
		final := service.FinalizeStreamUsage(c, info, usage)
		require.Equal(t, !complete, info.StreamResult.Failed)
		if complete {
			require.Nil(t, err)
			require.Contains(t, rec.Body.String(), "NOVA_TEXT")
			require.Equal(t, 5, final.TotalTokens)
		} else {
			require.NotNil(t, err)
			require.NotContains(t, rec.Body.String(), "NOVA_TEXT")
			require.Zero(t, final.TotalTokens)
		}
	}
}

// TestStreamDiagnosticSDKDecodeSource 区分 SDK 解码错误与调用前的本地配置错误；t 为测试上下文。
func TestStreamDiagnosticSDKDecodeSource(t *testing.T) {
	for _, received := range []bool{false, true} {
		for _, decode := range []bool{false, true} {
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			info := newAwsTestRelayInfo()
			service.BeginStreamAttempt(c, info)
			if received {
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			}
			var err error = errors.New("local SDK configuration")
			if decode {
				err = &smithy.DeserializationError{Err: io.ErrUnexpectedEOF}
			}
			markAwsStreamResponseError(info, err)
			require.Equal(t, received && decode, info.StreamSession.Snapshot().DiagnosticAvailable(false))
		}
	}
}
