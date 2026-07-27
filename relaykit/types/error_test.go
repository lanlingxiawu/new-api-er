package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// nil-receiver safety on every method
// ---------------------------------------------------------------------------

func TestNewAPIError_NilReceiverMethods(t *testing.T) {
	var e *NewAPIError

	require.Nil(t, e.Unwrap())
	require.Equal(t, ErrorCode(""), e.GetErrorCode())
	require.Equal(t, ErrorType(""), e.GetErrorType())
	require.Equal(t, "", e.Error())
	require.Equal(t, "", e.ErrorWithStatusCode())
	require.Equal(t, "", e.MaskSensitiveError())
	require.Equal(t, "", e.MaskSensitiveErrorWithStatusCode())

	require.False(t, IsChannelError(nil))
	require.False(t, IsSkipRetryError(nil))
	require.False(t, IsRecordErrorLog(nil))
}

// ---------------------------------------------------------------------------
// Error() / Unwrap()
// ---------------------------------------------------------------------------

func TestError_WithUnderlyingErrorStripsRequestIds(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("boom (request id: abc)"), ErrorCodeBadResponse, http.StatusBadGateway)
	require.Equal(t, "boom", e.Error())
}

func TestError_NilUnderlyingFallsBackToErrorCode(t *testing.T) {
	e := &NewAPIError{errorCode: ErrorCodeModelNotFound}
	require.Equal(t, "model_not_found", e.Error(), "Err==nil falls back to the errorCode string")
}

func TestUnwrap_ExposesUnderlying(t *testing.T) {
	inner := errors.New("inner")
	e := NewError(inner, ErrorCodeBadRequestBody)
	require.Equal(t, inner, e.Unwrap())
	require.True(t, errors.Is(e, inner))
}

// ---------------------------------------------------------------------------
// GetErrorCode / GetErrorType
// ---------------------------------------------------------------------------

func TestGetErrorCodeAndType(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest)
	require.Equal(t, ErrorCodeInvalidRequest, e.GetErrorCode())
	require.Equal(t, ErrorTypeNewAPIError, e.GetErrorType())
}

// ---------------------------------------------------------------------------
// ErrorWithStatusCode
// ---------------------------------------------------------------------------

func TestErrorWithStatusCode_ZeroStatusReturnsBareMessage(t *testing.T) {
	e := &NewAPIError{Err: errors.New("msg"), StatusCode: 0}
	require.Equal(t, "msg", e.ErrorWithStatusCode())
}

func TestErrorWithStatusCode_WithStatusAndMessage(t *testing.T) {
	e := &NewAPIError{Err: errors.New("msg"), StatusCode: 503}
	require.Equal(t, "status_code=503, msg", e.ErrorWithStatusCode())
}

func TestErrorWithStatusCode_WithStatusEmptyMessage(t *testing.T) {
	// Err nil + empty errorCode => Error()=="" ; status set => bare status_code.
	e := &NewAPIError{StatusCode: 500}
	require.Equal(t, "status_code=500", e.ErrorWithStatusCode())
}

// ---------------------------------------------------------------------------
// MaskSensitiveError / MaskSensitiveErrorWithStatusCode
// ---------------------------------------------------------------------------

func TestMaskSensitiveError_MasksIP(t *testing.T) {
	e := NewError(errors.New("failed to reach 192.168.1.1 now"), ErrorCodeDoRequestFailed)
	got := e.MaskSensitiveError()
	require.Contains(t, got, "***.***.***.***")
	require.NotContains(t, got, "192.168.1.1")
}

func TestMaskSensitiveError_CountTokenFailedSkipsMasking(t *testing.T) {
	e := NewError(errors.New("failed 192.168.1.1"), ErrorCodeCountTokenFailed)
	got := e.MaskSensitiveError()
	require.Contains(t, got, "192.168.1.1", "count_token_failed bypasses masking")
}

func TestMaskSensitiveErrorWithStatusCode_ZeroStatus(t *testing.T) {
	e := NewError(errors.New("192.168.1.1 bad"), ErrorCodeDoRequestFailed)
	e.StatusCode = 0
	got := e.MaskSensitiveErrorWithStatusCode()
	require.NotContains(t, got, "status_code")
	require.Contains(t, got, "***.***.***.***")
}

func TestMaskSensitiveErrorWithStatusCode_WithStatus(t *testing.T) {
	e := NewError(errors.New("192.168.1.1 bad"), ErrorCodeDoRequestFailed)
	e.StatusCode = 502
	got := e.MaskSensitiveErrorWithStatusCode()
	require.Contains(t, got, "status_code=502, ")
	require.Contains(t, got, "***.***.***.***")
}

func TestMaskSensitiveErrorWithStatusCode_EmptyMessageWithStatus(t *testing.T) {
	e := &NewAPIError{StatusCode: 500}
	require.Equal(t, "status_code=500", e.MaskSensitiveErrorWithStatusCode())
}

// ---------------------------------------------------------------------------
// NewError — fresh vs deep-unwrap reuse
// ---------------------------------------------------------------------------

func TestNewError_FreshDefaults(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest)
	require.Equal(t, ErrorTypeNewAPIError, e.errorType)
	require.Equal(t, http.StatusInternalServerError, e.StatusCode)
	require.Equal(t, ErrorCodeInvalidRequest, e.errorCode)
	require.Nil(t, e.RelayError)
}

func TestNewError_ReusesExistingNewAPIError(t *testing.T) {
	inner := NewError(errors.New("inner"), ErrorCodeBadRequestBody)
	outer := NewError(inner, ErrorCodeAccessDenied, ErrOptionWithSkipRetry())

	require.Same(t, inner, outer, "existing *NewAPIError is reused, not re-wrapped")
	require.Equal(t, ErrorCodeBadRequestBody, outer.errorCode, "original errorCode preserved")
	require.True(t, outer.skipRetry, "options still applied to the reused error")
}

func TestNewError_UnwrapsWrappedNewAPIError(t *testing.T) {
	inner := NewError(errors.New("inner"), ErrorCodeBadRequestBody)
	wrapped := fmt.Errorf("context: %w", inner)
	outer := NewError(wrapped, ErrorCodeAccessDenied)
	require.Same(t, inner, outer, "errors.As reaches through a wrapping error")
}

// ---------------------------------------------------------------------------
// NewErrorWithStatusCode
// ---------------------------------------------------------------------------

func TestNewErrorWithStatusCode_Fields(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("boom (request id: x)"), ErrorCodeBadResponse, http.StatusTeapot)
	require.Equal(t, ErrorTypeNewAPIError, e.errorType)
	require.Equal(t, http.StatusTeapot, e.StatusCode)
	require.Equal(t, ErrorCodeBadResponse, e.errorCode)

	rel, ok := e.RelayError.(OpenAIError)
	require.True(t, ok)
	require.Equal(t, "boom", rel.Message, "errorMessage strips request ids")
	require.Equal(t, string(ErrorCodeBadResponse), rel.Type)
}

func TestNewErrorWithStatusCode_NilError(t *testing.T) {
	e := NewErrorWithStatusCode(nil, ErrorCodeEmptyResponse, http.StatusBadGateway)
	rel := e.RelayError.(OpenAIError)
	require.Equal(t, "", rel.Message, "errorMessage(nil) is empty")
}

func TestNewErrorWithStatusCode_AppliesOptions(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("x"), ErrorCodeBadResponse, http.StatusBadGateway,
		ErrOptionWithSkipRetry(), ErrOptionWithStatusCode(http.StatusTeapot))
	require.True(t, e.skipRetry)
	require.Equal(t, http.StatusTeapot, e.StatusCode, "later option overrides constructor status")
}

// ---------------------------------------------------------------------------
// WithOpenAIError — code coercion, type default, metadata branch
// ---------------------------------------------------------------------------

func TestWithOpenAIError_StringCodeUsedDirectly(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Type: "t", Code: "my_code"}, http.StatusBadRequest)
	require.Equal(t, ErrorCode("my_code"), e.errorCode)
	require.Equal(t, ErrorTypeOpenAIError, e.errorType)
}

func TestWithOpenAIError_NilCodeBecomesUnknown(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Type: "t", Code: nil}, http.StatusBadRequest)
	require.Equal(t, ErrorCode("unknown_error"), e.errorCode)
}

func TestWithOpenAIError_NonStringCodeStringified(t *testing.T) {
	// Code as ErrorCode (a distinct type, not `string`) or int -> fmt %v.
	e := WithOpenAIError(OpenAIError{Message: "m", Code: ErrorCode("bad_response")}, http.StatusBadRequest)
	require.Equal(t, ErrorCode("bad_response"), e.errorCode)

	e2 := WithOpenAIError(OpenAIError{Message: "m", Code: 42}, http.StatusBadRequest)
	require.Equal(t, ErrorCode("42"), e2.errorCode)
}

func TestWithOpenAIError_EmptyTypeDefaultsToUpstream(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Code: "c"}, http.StatusBadRequest)
	rel := e.RelayError.(OpenAIError)
	require.Equal(t, "upstream_error", rel.Type)
}

func TestWithOpenAIError_AppliesOptions(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Code: "c"}, http.StatusBadRequest, ErrOptionWithSkipRetry())
	require.True(t, e.skipRetry)
}

func TestWithOpenAIError_MetadataOpenRouterBranch(t *testing.T) {
	meta := json.RawMessage(`{"provider":"x"}`)
	e := WithOpenAIError(OpenAIError{
		Message:  "rate limited",
		Type:     "upstream_error",
		Code:     "bad_response",
		Metadata: meta,
	}, http.StatusTooManyRequests)

	require.Equal(t, meta, e.Metadata, "metadata copied onto the NewAPIError")
	require.Contains(t, e.Error(), "rate limited")
	require.Contains(t, e.Error(), `{"provider":"x"}`, "message augmented with metadata")
}

// ---------------------------------------------------------------------------
// WithClaudeError
// ---------------------------------------------------------------------------

func TestWithClaudeError_EmptyTypeDefaults(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "m (request id: r)"}, http.StatusBadRequest)
	rel := e.RelayError.(ClaudeError)
	require.Equal(t, "upstream_error", rel.Type)
	require.Equal(t, "m", rel.Message)
	require.Equal(t, ErrorCode("upstream_error"), e.errorCode)
	require.Equal(t, ErrorTypeClaudeError, e.errorType)
}

func TestWithClaudeError_ExplicitType(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "m", Type: "overloaded_error"}, http.StatusServiceUnavailable)
	require.Equal(t, ErrorCode("overloaded_error"), e.errorCode)
}

func TestWithClaudeError_AppliesOptions(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "m", Type: "overloaded_error"}, http.StatusServiceUnavailable,
		ErrOptionWithSkipRetry())
	require.True(t, e.skipRetry)
}

// ---------------------------------------------------------------------------
// NewOpenAIError
// ---------------------------------------------------------------------------

func TestNewOpenAIError_FreshBuildsOpenAIRelay(t *testing.T) {
	e := NewOpenAIError(errors.New("boom (request id: x)"), ErrorCodeBadResponse, http.StatusBadGateway)
	require.Equal(t, ErrorTypeOpenAIError, e.errorType)
	require.Equal(t, ErrorCodeBadResponse, e.errorCode)
	rel := e.RelayError.(OpenAIError)
	require.Equal(t, "boom", rel.Message)
	require.Equal(t, string(ErrorCodeBadResponse), rel.Type)
}

func TestNewOpenAIError_ReuseWithNilRelayPopulatesRelay(t *testing.T) {
	inner := NewError(errors.New("inner 192.168.1.1"), ErrorCodeBadRequestBody) // RelayError nil
	out := NewOpenAIError(inner, ErrorCodeBadResponse, http.StatusBadGateway)

	require.Same(t, inner, out)
	rel, ok := out.RelayError.(OpenAIError)
	require.True(t, ok, "reuse path with nil RelayError fills in an OpenAIError")
	require.Equal(t, string(ErrorCodeBadResponse), rel.Type)
}

func TestNewOpenAIError_ReuseKeepsExistingRelay(t *testing.T) {
	inner := WithOpenAIError(OpenAIError{Message: "orig", Type: "t", Code: "c"}, http.StatusBadRequest)
	out := NewOpenAIError(inner, ErrorCodeBadResponse, http.StatusBadGateway, ErrOptionWithSkipRetry())

	require.Same(t, inner, out)
	rel := out.RelayError.(OpenAIError)
	require.Equal(t, "orig", rel.Message, "existing RelayError not overwritten")
	require.True(t, out.skipRetry, "options still applied")
}

// ---------------------------------------------------------------------------
// InitOpenAIError
// ---------------------------------------------------------------------------

func TestInitOpenAIError(t *testing.T) {
	e := InitOpenAIError(ErrorCodeModelNotFound, http.StatusNotFound)
	require.Equal(t, ErrorTypeOpenAIError, e.errorType)
	require.Equal(t, ErrorCode(string(ErrorCodeModelNotFound)), e.errorCode)
	rel := e.RelayError.(OpenAIError)
	require.Equal(t, "", rel.Message)
	require.Equal(t, string(ErrorCodeModelNotFound), rel.Type)
}

// ---------------------------------------------------------------------------
// ToOpenAIError — every errorType branch
// ---------------------------------------------------------------------------

func TestToOpenAIError_OpenAITypeReturnsRelay(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "hello", Type: "t", Code: "invalid_request"}, http.StatusBadRequest)
	got := e.ToOpenAIError()
	require.Equal(t, "hello", got.Message)
	require.Equal(t, "t", got.Type)
}

func TestToOpenAIError_OpenAITypeWrongRelayFallsBack(t *testing.T) {
	// errorType says OpenAI but RelayError isn't an OpenAIError -> zero result,
	// empty message -> fallback to string(errorType).
	e := &NewAPIError{errorType: ErrorTypeOpenAIError, RelayError: "not-a-struct"}
	got := e.ToOpenAIError()
	require.Equal(t, string(ErrorTypeOpenAIError), got.Message)
}

func TestToOpenAIError_ClaudeTypeConverts(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "claude says no", Type: "overloaded_error"}, http.StatusServiceUnavailable)
	got := e.ToOpenAIError()
	require.Equal(t, "claude says no", got.Message)
	require.Equal(t, "overloaded_error", got.Type, "claude Type carried into OpenAI Type")
	require.Equal(t, e.errorCode, got.Code)
}

func TestToOpenAIError_DefaultType(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("plain fail"), ErrorCodeBadResponse, http.StatusBadGateway)
	got := e.ToOpenAIError()
	require.Equal(t, "plain fail", got.Message)
	require.Equal(t, string(ErrorTypeNewAPIError), got.Type)
	require.Equal(t, ErrorCodeBadResponse, got.Code)
}

func TestToOpenAIError_MasksSensitiveInfo(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("fail 192.168.1.1"), ErrorCodeBadResponse, http.StatusBadGateway)
	got := e.ToOpenAIError()
	require.Contains(t, got.Message, "***.***.***.***")
}

func TestToOpenAIError_CountTokenFailedSkipsMasking(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("fail 192.168.1.1"), ErrorCodeCountTokenFailed, http.StatusBadGateway)
	got := e.ToOpenAIError()
	require.Contains(t, got.Message, "192.168.1.1")
}

func TestToOpenAIError_EmptyMessageFallsBackToType(t *testing.T) {
	e := &NewAPIError{errorType: "custom_type"}
	got := e.ToOpenAIError()
	require.Equal(t, "custom_type", got.Message)
}

func TestToOpenAIError_RelayMessageFallbackToError(t *testing.T) {
	// default type, RelayError is OpenAIError with EMPTY message ->
	// relayMessage() falls through to e.Error().
	e := &NewAPIError{
		errorType:  ErrorTypeNewAPIError,
		errorCode:  ErrorCodeBadResponse,
		Err:        errors.New("underlying msg"),
		RelayError: OpenAIError{Message: ""},
	}
	got := e.ToOpenAIError()
	require.Equal(t, "underlying msg", got.Message)
}

// ---------------------------------------------------------------------------
// ToClaudeError — every errorType branch
// ---------------------------------------------------------------------------

func TestToClaudeError_OpenAITypeConverts(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "boom", Type: "t", Code: "invalid_request"}, http.StatusBadRequest)
	got := e.ToClaudeError()
	require.Equal(t, "boom", got.Message)
	require.Equal(t, "invalid_request", got.Type, "openAIError.Code stringified into claude Type")
}

func TestToClaudeError_OpenAITypeWrongRelayFallsBack(t *testing.T) {
	e := &NewAPIError{errorType: ErrorTypeOpenAIError, RelayError: 123}
	got := e.ToClaudeError()
	require.Equal(t, string(ErrorTypeOpenAIError), got.Message)
}

func TestToClaudeError_ClaudeTypeReturnsRelay(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "claude msg", Type: "overloaded_error"}, http.StatusServiceUnavailable)
	got := e.ToClaudeError()
	require.Equal(t, "claude msg", got.Message)
	require.Equal(t, "overloaded_error", got.Type)
}

func TestToClaudeError_DefaultType(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("plain fail"), ErrorCodeBadResponse, http.StatusBadGateway)
	got := e.ToClaudeError()
	require.Equal(t, "plain fail", got.Message)
	require.Equal(t, string(ErrorTypeNewAPIError), got.Type)
}

func TestToClaudeError_MasksSensitiveInfo(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("fail 192.168.1.1"), ErrorCodeBadResponse, http.StatusBadGateway)
	got := e.ToClaudeError()
	require.Contains(t, got.Message, "***.***.***.***")
}

func TestToClaudeError_CountTokenFailedSkipsMasking(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("fail 192.168.1.1"), ErrorCodeCountTokenFailed, http.StatusBadGateway)
	got := e.ToClaudeError()
	require.Contains(t, got.Message, "192.168.1.1")
}

func TestToClaudeError_EmptyMessageFallsBackToType(t *testing.T) {
	e := &NewAPIError{errorType: "custom_type"}
	got := e.ToClaudeError()
	require.Equal(t, "custom_type", got.Message)
}

// ---------------------------------------------------------------------------
// SetMessage — every RelayError branch
// ---------------------------------------------------------------------------

func TestSetMessage_OpenAIRelay(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "old", Type: "t", Code: "c"}, http.StatusBadRequest)
	e.SetMessage("new message")
	require.Equal(t, "new message", e.RelayError.(OpenAIError).Message)
	require.Equal(t, "new message", e.Error())
}

func TestSetMessage_ClaudeRelay(t *testing.T) {
	e := WithClaudeError(ClaudeError{Message: "old", Type: "overloaded_error"}, http.StatusServiceUnavailable)
	e.SetMessage("new message")
	rel := e.RelayError.(ClaudeError)
	require.Equal(t, "new message", rel.Message)
	require.Equal(t, "overloaded_error", rel.Type, "type preserved")
}

func TestSetMessage_DefaultBuildsOpenAIRelay(t *testing.T) {
	e := NewError(errors.New("orig"), ErrorCodeInvalidRequest) // RelayError nil
	e.SetMessage("new message")
	rel, ok := e.RelayError.(OpenAIError)
	require.True(t, ok, "nil RelayError becomes a fresh OpenAIError")
	require.Equal(t, "new message", rel.Message)
	require.Equal(t, string(ErrorTypeNewAPIError), rel.Type)
	require.Equal(t, ErrorCodeInvalidRequest, rel.Code)
}

// ---------------------------------------------------------------------------
// ErrOption* options
// ---------------------------------------------------------------------------

func TestErrOptionWithSkipRetry(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest, ErrOptionWithSkipRetry())
	require.True(t, IsSkipRetryError(e))
}

func TestErrOptionWithNoRecordErrorLog(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest, ErrOptionWithNoRecordErrorLog())
	require.False(t, IsRecordErrorLog(e))
}

func TestErrOptionWithStatusCode(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest, ErrOptionWithStatusCode(http.StatusTeapot))
	require.Equal(t, http.StatusTeapot, e.StatusCode)
}

func TestErrOptionWithHideErrMsg_DebugDisabled(t *testing.T) {
	prev := kitutil.Debug.Load()
	kitutil.Debug.Store(false)
	defer func() { kitutil.Debug.Store(prev) }()

	e := NewError(errors.New("secret internal detail"), ErrorCodeInvalidRequest, ErrOptionWithHideErrMsg("hidden"))
	require.Equal(t, "hidden", e.Error())
}

func TestErrOptionWithHideErrMsg_DebugEnabled(t *testing.T) {
	prev := kitutil.Debug.Load()
	kitutil.Debug.Store(true)
	defer func() { kitutil.Debug.Store(prev) }()

	// Just exercises the DebugEnabled print branch; message still replaced.
	e := NewError(errors.New("secret"), ErrorCodeInvalidRequest, ErrOptionWithHideErrMsg("hidden"))
	require.Equal(t, "hidden", e.Error())
}

// ---------------------------------------------------------------------------
// IsChannelError
// ---------------------------------------------------------------------------

func TestIsChannelError(t *testing.T) {
	ch := NewError(errors.New("x"), ErrorCodeChannelNoAvailableKey)
	require.True(t, IsChannelError(ch))

	notCh := NewError(errors.New("x"), ErrorCodeInvalidRequest)
	require.False(t, IsChannelError(notCh))
}

// ---------------------------------------------------------------------------
// IsRecordErrorLog
// ---------------------------------------------------------------------------

func TestIsRecordErrorLog_DefaultTrue(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest)
	require.True(t, IsRecordErrorLog(e), "unset recordErrorLog defaults to true")
}

func TestIsRecordErrorLog_ExplicitFalse(t *testing.T) {
	e := NewError(errors.New("x"), ErrorCodeInvalidRequest, ErrOptionWithNoRecordErrorLog())
	require.False(t, IsRecordErrorLog(e))
}

// ---------------------------------------------------------------------------
// errorMessage helper
// ---------------------------------------------------------------------------

func TestErrorMessage(t *testing.T) {
	require.Equal(t, "", errorMessage(nil))
	require.Equal(t, "clean", errorMessage(errors.New("clean (request id: r)")))
}

// ---------------------------------------------------------------------------
// relayMessage helper (via exported paths already covered) — direct nuance
// ---------------------------------------------------------------------------

func TestRelayMessage_ClaudeNonEmpty(t *testing.T) {
	e := &NewAPIError{
		errorType:  ErrorTypeClaudeError,
		RelayError: ClaudeError{Message: "claude direct"},
		Err:        errors.New("underlying"),
	}
	require.Equal(t, "claude direct", e.relayMessage())
}

func TestRelayMessage_FallbackWhenNoRelay(t *testing.T) {
	e := &NewAPIError{Err: errors.New("just underlying")}
	require.Equal(t, "just underlying", e.relayMessage())
}
