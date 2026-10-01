package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A request that fails parsing or validation is the client's error: HTTP 400
// in the entry format's error shape, with a message free of Go decoder text,
// and it never reaches the retry loop or an upstream.
func TestRelay_InvalidRequestIs400WithoutGoInternals(t *testing.T) {
	cases := []struct {
		name    string
		format  types.RelayFormat
		path    string
		body    string
		message string
	}{
		{"claude max_tokens string", types.RelayFormatClaude, "/v1/messages",
			`{"model":"m","max_tokens":"abc","messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'max_tokens': expected integer >= 0."},
		{"claude empty messages", types.RelayFormatClaude, "/v1/messages",
			`{"model":"m","messages":[]}`, "field messages is required"},
		{"openai max_tokens -1", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","max_tokens":-1,"messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'max_tokens': expected integer >= 0."},
		{"openai empty messages", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[]}`, "field messages is required"},
		{"gemini malformed json", types.RelayFormatGemini, "/v1beta/models/m:generateContent",
			`{"contents":`, "The request body could not be parsed. Send a valid JSON body in the format this endpoint expects."},
		{"gemini empty contents", types.RelayFormatGemini, "/v1beta/models/m:generateContent",
			`{"contents":[]}`, "contents is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(common.RequestIdKey, "req-invalid")

			Relay(c, tc.format)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			body := w.Body.String()
			if tc.format == types.RelayFormatClaude {
				// Anthropic shape: {"type":"error","error":{"type":..,"message":..}}
				assert.Equal(t, "error", gjson.Get(body, "type").String())
				// Claude clients classify by Anthropic error types; a 400 is invalid_request_error.
				assert.Equal(t, "invalid_request_error", gjson.Get(body, "error.type").String())
			} else {
				assert.Equal(t, string(types.ErrorCodeInvalidRequest), gjson.Get(body, "error.code").String())
			}
			// The message is followed by the request id suffix.
			msg := gjson.Get(body, "error.message").String()
			assert.True(t, strings.HasPrefix(msg, tc.message), msg)
			for _, internal := range []string{"json:", "Go struct", "cannot unmarshal", "ClaudeRequest", "GeneralOpenAIRequest", "unexpected end of JSON input"} {
				assert.NotContains(t, body, internal)
			}
		})
	}
}

// An image request one channel's adaptor cannot express moves on to the next
// channel; invalid request content does not.
func TestShouldRetry_ImageConvertFailures(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	unsupported := types.NewErrorWithStatusCode(errors.New("x"), relaycommon.ErrorCodeImageRequestUnsupported, http.StatusNotImplemented)
	assert.True(t, shouldRetry(c, unsupported, 1))
	serverFault := types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeConvertRequestFailed, http.StatusInternalServerError)
	assert.True(t, shouldRetry(c, serverFault, 1))
	invalid := types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	assert.False(t, shouldRetry(c, invalid, 1))
}

// Errors raised while building the upstream request never auto-disable the
// channel, even when an admin disables on every 5xx.
func TestShouldDisableChannel_IgnoresLocalRequestBuildErrors(t *testing.T) {
	prevEnabled, prevRanges := common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodeRanges
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = prevEnabled
		operation_setting.AutomaticDisableStatusCodeRanges = prevRanges
	})
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 500, End: 599}}

	for _, local := range []*types.NewAPIError{
		types.NewErrorWithStatusCode(errors.New("x"), relaycommon.ErrorCodeImageRequestUnsupported, http.StatusNotImplemented),
		types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeConvertRequestFailed, http.StatusInternalServerError),
		types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeConvertRequestFailed, http.StatusBadGateway),
	} {
		assert.False(t, service.ShouldDisableChannel(local), local.GetErrorCode())
	}
	upstream := types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	assert.True(t, service.ShouldDisableChannel(upstream), "an upstream 5xx still follows the admin's setting")
}
