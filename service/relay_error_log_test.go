package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageWithCurrentRequestId(t *testing.T) {
	withID, _ := gin.CreateTestContext(httptest.NewRecorder())
	withID.Set(common.RequestIdKey, "current")
	withoutID, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, test := range []struct {
		name    string
		c       *gin.Context
		message string
		want    string
	}{
		{name: "nil context strips stale ids", c: nil, message: "boom (request id: stale)", want: "boom"},
		{name: "context without id strips stale ids", c: withoutID, message: "boom (request id: stale)", want: "boom"},
		{name: "current id replaces stale ids", c: withID, message: "boom (request id: stale)", want: "boom (request id: current)"},
		{name: "current id appended", c: withID, message: "boom", want: "boom (request id: current)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, MessageWithCurrentRequestId(test.c, test.message))
		})
	}
}

func TestStreamPublicErrorSummaryPreservesUpstreamHTTPMessage(t *testing.T) {
	const bedrockMessage = "InvokeModel: ValidationException: tool type 'web_search_20260209' is not supported for this model"
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "bedrock missing code", body: `{"type":"error","error":{"type":"<nil>","message":"` + bedrockMessage + ` (request id: upstream)"}}`, want: bedrockMessage},
		{name: "openai", body: `{"error":{"code":"invalid_request","message":"unsupported tool"}}`, want: "unsupported tool"},
		{name: "numeric code", body: `{"error":{"code":400,"message":"unsupported tool"}}`, want: "unsupported tool"},
		{name: "message", body: `{"message":"unsupported tool","private":"body-only-secret"}`, want: "unsupported tool"},
		{name: "string error", body: `{"error":"unsupported tool"}`, want: "unsupported tool"},
		{name: "detail", body: `{"detail":"unsupported tool"}`, want: "unsupported tool"},
		{name: "mask credentials", body: `{"error":{"message":"unsupported tool api_key:message-secret"}}`, want: "unsupported tool api_key:***"},
		{name: "metadata excluded", body: `{"error":{"message":"unsupported tool","metadata":{"raw":"body-only-secret"}}}`, want: "unsupported tool"},
		{name: "empty message", body: `{"error":{"message":"","code":"bad_request"}}`},
		{name: "whitespace message", body: `{"error":{"message":" \t\n"}}`},
		{name: "only request id", body: `{"error":{"message":" (request id: upstream)"}}`},
		{name: "blank object falls through", body: `{"error":{"message":" \t\n"},"message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "request id object falls through", body: `{"error":{"message":" (request id: upstream)"},"message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "blank string falls through", body: `{"error":" \t\n","message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "request id string falls through", body: `{"error":" (request id: upstream)","message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "blank top message falls through", body: `{"message":" \t","msg":"unsupported tool"}`, want: "unsupported tool"},
		{name: "blank fields fall through to detail", body: `{"message":" (request id: upstream)","msg":" ","err":" \n","error_msg":" \t","detail":"unsupported tool"}`, want: "unsupported tool"},
		{name: "blank detail falls through to header", body: `{"detail":" ","header":{"message":"unsupported tool"}}`, want: "unsupported tool"},
		{name: "blank header falls through to response", body: `{"header":{"message":" (request id: upstream)"},"response":{"error":{"message":"unsupported tool"}}}`, want: "unsupported tool"},
		{name: "nested message keeps priority", body: `{"error":{"message":"nested error"},"message":"top error"}`, want: "nested error"},
		{name: "all fields blank", body: `{"error":" ","message":" (request id: upstream)","msg":" \t","detail":" \n"}`},
		{name: "no message", body: `{"error":{"type":"server_error"}}`},
		{name: "numeric error", body: `{"error":400}`},
		{name: "null error", body: `{"error":null}`},
		{name: "null error with message", body: `{"error":null,"message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "numeric error with message", body: `{"error":400,"message":"unsupported tool"}`, want: "unsupported tool"},
		{name: "array error", body: `{"error":["body-only-secret"]}`},
		{name: "empty body"},
		{name: "invalid json", body: `{"error":body-only-secret`},
		{name: "html", body: `<html>body-only-secret</html>`},
	} {
		for _, showBody := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/show_body=%t", test.name, showBody), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set(relaycommon.StreamResponseOnlyKey, true)
				apiErr := RelayErrorHandler(c, &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, showBody)
				originalError := apiErr.Error()
				originalResponse := apiErr.ToClaudeError()
				originalRetry := types.IsSkipRetryError(apiErr)
				want := fmt.Sprintf("upstream response failed (status=400, code=%s)", apiErr.GetErrorCode())
				if test.want != "" {
					want = "status_code=400, " + test.want
				}
				assert.Equal(t, want, StreamPublicErrorSummary(c, apiErr))
				assert.Equal(t, originalError, apiErr.Error())
				assert.Equal(t, originalResponse, apiErr.ToClaudeError())
				assert.Equal(t, originalRetry, types.IsSkipRetryError(apiErr))
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				c.Set(relaycommon.StreamResponseOnlyKey, false)
				assert.Equal(t, apiErr.MaskSensitiveErrorWithStatusCode(), StreamPublicErrorSummary(c, apiErr))
			})
		}
	}
}

func TestRelayErrorLogFallbackPreservesResponseAndRetry(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":" \t","code":"invalid_request"},"message":"quota exceeded"}`,
		`{"error":" (request id: upstream)","message":"quota exceeded"}`,
	} {
		for _, showBody := range []bool{false, true} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(relaycommon.StreamResponseOnlyKey, true)
			apiErr := RelayErrorHandler(c, &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body))}, showBody)
			require.Equal(t, "status_code=400, quota exceeded", StreamPublicErrorSummary(c, apiErr))
			// The old parser's empty response message and retry choice remain intact.
			require.False(t, types.IsSkipRetryError(apiErr))
			if showBody {
				require.Contains(t, apiErr.Error(), "body:")
			} else {
				require.Empty(t, apiErr.Error())
				require.Equal(t, "openai_error", apiErr.ToClaudeError().Message)
			}
		}
	}
}

func TestStreamPublicErrorSummaryProtocolAndLocalErrors(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	for _, test := range []struct {
		name string
		err  *types.NewAPIError
		want string
	}{
		{name: "claude protocol error", err: types.WithClaudeError(types.ClaudeError{Type: "invalid_request_error", Message: "unsupported tool"}, 400), want: "status_code=400, unsupported tool"},
		{name: "message without status", err: types.WithOpenAIError(types.OpenAIError{Message: "unsupported tool"}, 0), want: "unsupported tool"},
		{name: "local transport error", err: types.NewOpenAIError(errors.New("private transport cause"), types.ErrorCodeDoRequestFailed, 502), want: "upstream response failed (status=502, code=do_request_failed)"},
		{name: "hidden protocol message", err: types.WithOpenAIError(types.OpenAIError{Message: "private error"}, 400, types.ErrOptionWithHideErrMsg("hidden")), want: "upstream response failed (status=400, code=unknown_error)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, StreamPublicErrorSummary(c, test.err))
			c.Set(relaycommon.StreamResponseOnlyKey, false)
			assert.Equal(t, test.err.MaskSensitiveErrorWithStatusCode(), StreamPublicErrorSummary(c, test.err))
			c.Set(relaycommon.StreamResponseOnlyKey, true)
		})
	}
}

func TestViolationNormalizationPreservesLogMessageSource(t *testing.T) {
	for _, branch := range []string{"marker", "prefix"} {
		for _, scenario := range []string{"metadata", "empty source", "hidden", "fallback field"} {
			t.Run(branch+"/"+scenario, func(t *testing.T) {
				message := "upstream rejected request"
				code := "violation_fee.custom"
				if branch == "marker" {
					message = CSAMViolationMarker
					code = "bad_request"
				}
				input := types.WithOpenAIError(types.OpenAIError{
					Message: message, Code: code, Type: "provider_error",
					Metadata: []byte(`{"raw":"private-metadata"}`),
				}, http.StatusBadRequest)
				want := message
				switch scenario {
				case "empty source":
					types.ErrOptionWithUpstreamMessage("")(input)
					want = ""
				case "hidden":
					types.ErrOptionWithHideErrMsg("hidden")(input)
					want = ""
				case "fallback field":
					types.ErrOptionWithUpstreamMessage("selected fallback (request id: stale)")(input)
					want = "selected fallback"
				}
				// Reproduce the existing response contract, including its metadata formatting.
				legacyResponse := input.ToOpenAIError()
				if branch == "marker" {
					legacyResponse.Type = string(types.ErrorCodeViolationFeeGrokCSAM)
					legacyResponse.Code = string(types.ErrorCodeViolationFeeGrokCSAM)
				}
				legacy := types.WithOpenAIError(legacyResponse, input.StatusCode, types.ErrOptionWithSkipRetry())
				got := NormalizeViolationFeeError(input)
				require.Equal(t, legacy.ToOpenAIError(), got.ToOpenAIError())
				require.Equal(t, legacy.ToClaudeError(), got.ToClaudeError())
				require.Equal(t, legacy.GetErrorCode(), got.GetErrorCode())
				require.Equal(t, input.StatusCode, got.StatusCode)
				require.True(t, types.IsSkipRetryError(got))
				require.Equal(t, want, got.UpstreamErrorMessage())
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set(relaycommon.StreamResponseOnlyKey, true)
				wantSummary := fmt.Sprintf("upstream response failed (status=400, code=%s)", got.GetErrorCode())
				if want != "" {
					wantSummary = "status_code=400, " + want
				}
				require.Equal(t, wantSummary, StreamPublicErrorSummary(c, got))
				require.NotContains(t, StreamPublicErrorSummary(c, got), "private-metadata")
			})
		}
	}
}

func TestProcessChannelErrorResponseOnlyPreservesUpstreamMessage(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000104, "svc-upstream-message")
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	apiErr := RelayErrorHandler(c, &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"message":"ValidationException: unsupported web_search_20260209 api_key:message-secret (request id: upstream)"},"private":"body-only-secret"}`)),
	}, false)

	ProcessChannelError(c, types.ChannelError{ChannelId: 448}, apiErr, nil)

	logs := readLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, "status_code=400, ValidationException: unsupported web_search_20260209 api_key:*** (request id: svc-upstream-message)", logs[0].Content)
	assert.NotContains(t, logs[0].Content, "message-secret")
	assert.NotContains(t, logs[0].Content, "body-only-secret")
	assert.Equal(t, "svc-upstream-message", logs[0].RequestId)
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadRequest), other["status_code"])
	assert.Equal(t, "unknown_error", other["error_code"])
}

func TestStreamFailureMessagesReachSettlementLogs(t *testing.T) {
	previousConsumeLog := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	// Keep real log persistence; isolate unrelated asynchronous cost accounting.
	model.DrainRelayLogsSync(5 * time.Second)
	model.RegisterRelayLogAccountingHandler(func(model.RelayLogAccountingPayload, int) {})
	t.Cleanup(func() {
		model.DrainRelayLogsSync(5 * time.Second)
		common.LogConsumeEnabled = previousConsumeLog
		model.RegisterRelayLogAccountingHandler(func(p model.RelayLogAccountingPayload, logID int) {
			costCommissionSnapshot{
				UserID: p.UserID, ChannelID: p.ChannelID, ChannelName: p.ChannelName,
				UsingGroup: p.UsingGroup, OriginModelName: p.OriginModelName,
				GroupRatio: p.GroupRatio, Quota: p.Quota, SurchargeQuota: p.SurchargeQuota,
				BaseQuota: p.BaseQuota,
			}.record(logID)
		})
	})
	for i, tc := range []struct {
		name    string
		quota   int
		content string
	}{
		{"no output", 0, ""},
		{"partial charged output", 42, "billing note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenID := 91000110 + i
			c, _ := processChannelErrorLogFixture(t, tokenID, "stream-current")
			billing := &claudeSettlementFixture{}
			info := &relaycommon.RelayInfo{
				UserId: c.GetInt("id"), TokenId: tokenID, Billing: billing,
				UserQuota: 1_000_000_000,
				IsStream:  true, RelayFormat: types.RelayFormatOpenAI,
				ChannelMeta: &relaycommon.ChannelMeta{},
			}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			if tc.quota > 0 {
				content := []byte(`{"choices":[{"delta":{"content":"partial"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
				require.NoError(t, info.StreamSession.ObserveEvent("", content))
				info.StreamSession.CommitDelivery(content)
			}
			require.NoError(t, info.StreamSession.ObserveEvent("error", []byte(`{"error":{"message":"overloaded api_key:secret (request id: upstream)"},"private":"body-secret"}`)))
			FinalizeStreamUsage(c, info, nil)
			params := ConsumptionSettlementParams{Quota: tc.quota, Content: tc.content, IsStream: true, Other: map[string]interface{}{}}
			FinalizeConsumptionSettlement(c, info, params)
			FinalizeConsumptionSettlement(c, info, params)
			model.DrainRelayLogsSync(5 * time.Second)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("token_id = ?", tokenID).Find(&logs).Error)
			require.Len(t, logs, 1)
			want := "overloaded api_key:*** (request id: stream-current)"
			if tc.content != "" {
				want = tc.content + "; " + want
			}
			require.Equal(t, want, logs[0].Content)
			require.Equal(t, tc.quota, logs[0].Quota)
			wantType := model.LogTypeError
			if tc.quota > 0 {
				wantType = model.LogTypeConsume
			}
			require.Equal(t, wantType, logs[0].Type)
			require.Equal(t, "stream-current", logs[0].RequestId)
			require.Equal(t, 1, billing.calls)
			require.Equal(t, tc.quota, billing.actual)
			require.NotContains(t, logs[0].Content, "body-secret")
		})
	}
}

// processChannelErrorLogFixture 构造一个已鉴权中转请求的上下文并打开错误日志开关；token_id 取唯一值，
// 返回的查询函数只读取本用例写入的错误日志并在结束时删除它们。
func processChannelErrorLogFixture(t *testing.T, tokenID int, requestID string) (*gin.Context, func() []model.Log) {
	t.Helper()
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLog
		model.DrainRelayLogsSync(5 * time.Second)
		require.NoError(t, model.LOG_DB.Where("token_id = ?", tokenID).Delete(&model.Log{}).Error)
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, requestID)
	c.Set("id", 7)
	c.Set("token_name", "error-log-token")
	c.Set("token_id", tokenID)
	c.Set("original_model", "error-log-model")
	c.Set("group", "default")
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	return c, func() []model.Log {
		model.DrainRelayLogsSync(5 * time.Second)
		var logs []model.Log
		require.NoError(t, model.LOG_DB.Where("type = ? AND token_id = ?", model.LogTypeError, tokenID).Find(&logs).Error)
		return logs
	}
}

func TestProcessChannelErrorRecordsPublicSummaryWithCurrentRequestId(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000101, "svc-current-request")
	apiErr := types.NewOpenAIError(errors.New("upstream failed api_key:review-secret (request id: upstream-stale)"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	ProcessChannelError(c, types.ChannelError{ChannelId: 101, ChannelName: "snapshot"}, apiErr, nil)

	logs := readLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, 101, logs[0].ChannelId)
	assert.Equal(t, "svc-current-request", logs[0].RequestId)
	assert.True(t, logs[0].IsStream)
	assert.Equal(t, MessageWithCurrentRequestId(c, apiErr.MaskSensitiveErrorWithStatusCode()), logs[0].Content)
	assert.Contains(t, logs[0].Content, "(request id: svc-current-request)")
	assert.NotContains(t, logs[0].Content, "upstream-stale")
	assert.NotContains(t, logs[0].Content, "review-secret")
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, "/v1/chat/completions", other["request_path"])
	assert.Equal(t, float64(http.StatusBadGateway), other["status_code"])
	assert.Equal(t, string(types.ErrorCodeBadResponseStatusCode), other["error_code"])
	assert.NotContains(t, other, "stream_diagnostic")
}

func TestProcessChannelErrorResponseOnlyModeHidesCauseAndKeepsDiagnostic(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000102, "svc-response-only")
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "policy-blocked")
	apiErr := types.NewOpenAIError(errors.New("low-level private cause"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	ProcessChannelError(c, types.ChannelError{ChannelId: 102}, apiErr, nil)

	logs := readLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, "upstream response failed (status=502, code=bad_response_status_code) (request id: svc-response-only)", logs[0].Content)
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, logs[0].Other, "low-level private cause")
	// 策略原因触发流式诊断：日志保存诊断尝试编号与策略原因。
	assert.Contains(t, other, "stream_diagnostic_attempt", logs[0].Other)
	assert.Equal(t, "policy-blocked", other["reject_reason"], logs[0].Other)
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "policy-blocked", adminInfo["reject_reason"])
	assert.NotContains(t, other, "stream_diagnostic_available")
}

func TestProcessChannelErrorSkipsErrorLogWhenDisabledOrNotRecorded(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000103, "svc-skip")
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, nil, nil)
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, types.NewError(errors.New("not recorded"), types.ErrorCodeBadResponse, types.ErrOptionWithNoRecordErrorLog()), nil)
	constant.ErrorLogEnabled = false
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, types.NewError(errors.New("log disabled"), types.ErrorCodeBadResponse), nil)
	assert.Empty(t, readLogs())
}
