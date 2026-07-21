package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func jsonBodyContext(t *testing.T, body []byte) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Set(common.KeyRequestBody, body)
	return c
}

// ---------------------------------------------------------------------------
// ResolveIncomingBillingExprRequestInput
// ---------------------------------------------------------------------------

func TestResolveIncomingBillingExprRequestInput_ReadsBody(t *testing.T) {
	body := []byte(`{"service_tier":"fast"}`)
	c := jsonBodyContext(t, body)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "application/json"}}

	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	require.Equal(t, body, input.Body)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
}

func TestResolveIncomingBillingExprRequestInput_PreloadedMergesHeaders(t *testing.T) {
	c := jsonBodyContext(t, []byte(`{"ignored":true}`))
	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"X-From-Info": "info", "Shared": "info-wins-no"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Shared": "preloaded-wins", "X-From-Input": "input"},
			Body:    []byte(`{"preloaded":true}`),
		},
	}
	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	// Preloaded body wins; request body is not re-read.
	require.Equal(t, []byte(`{"preloaded":true}`), input.Body)
	// RequestHeaders form the base, preloaded input.Headers override on conflict.
	require.Equal(t, "info", input.Headers["X-From-Info"])
	require.Equal(t, "input", input.Headers["X-From-Input"])
	require.Equal(t, "preloaded-wins", input.Headers["Shared"])
}

func TestResolveIncomingBillingExprRequestInput_NilInfo(t *testing.T) {
	c := jsonBodyContext(t, []byte(`{"a":1}`))
	input, err := ResolveIncomingBillingExprRequestInput(c, nil)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"a":1}`), input.Body)
	require.Empty(t, input.Headers)
}

func TestResolveIncomingBillingExprRequestInput_NonJSONContentType(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("plain"))
	c.Request.Header.Set("Content-Type", "text/plain")
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "text/plain"}}

	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	require.Nil(t, input.Body, "non-JSON body is not read for billing expr")
}

// ---------------------------------------------------------------------------
// BuildBillingExprRequestInputFromRequest
// ---------------------------------------------------------------------------

func TestBuildBillingExprRequestInputFromRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:     "gemini-3.1-pro-preview",
		Stream:    lo.ToPtr(true),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens: lo.ToPtr(uint(3000)),
	}
	input, err := BuildBillingExprRequestInputFromRequest(request, map[string]string{
		"Content-Type": "application/json",
		"X-Test":       "1",
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, "1", input.Headers["X-Test"])
	require.True(t, gjson.GetBytes(input.Body, "stream").Bool())
	require.Equal(t, "user", gjson.GetBytes(input.Body, "messages.0.role").String())
	require.Equal(t, float64(3000), gjson.GetBytes(input.Body, "max_tokens").Float())
}

func TestBuildBillingExprRequestInputFromRequest_NilRequest(t *testing.T) {
	input, err := BuildBillingExprRequestInputFromRequest(nil, map[string]string{"X": "y"})
	require.NoError(t, err)
	require.Equal(t, "y", input.Headers["X"])
	require.Nil(t, input.Body)
}

// ---------------------------------------------------------------------------
// isJSONContentType / cloneStringMap / cloneRequestInput
// ---------------------------------------------------------------------------

func TestIsJSONContentType(t *testing.T) {
	require.True(t, isJSONContentType("application/json"))
	require.True(t, isJSONContentType("  APPLICATION/JSON  "))
	require.True(t, isJSONContentType("application/json; charset=utf-8"))
	require.False(t, isJSONContentType("text/plain"))
	require.False(t, isJSONContentType(""))
}

func TestCloneStringMap(t *testing.T) {
	require.Equal(t, map[string]string{}, cloneStringMap(nil))
	require.Equal(t, map[string]string{}, cloneStringMap(map[string]string{}))

	src := map[string]string{"a": "1", "  ": "blank-key-dropped", "": "empty-key-dropped", "b": "2"}
	got := cloneStringMap(src)
	require.Equal(t, map[string]string{"a": "1", "b": "2"}, got)

	// It is a copy, not the same reference.
	got["a"] = "changed"
	require.Equal(t, "1", src["a"])
}

func TestCloneRequestInput(t *testing.T) {
	src := billingexpr.RequestInput{
		Headers: map[string]string{"a": "1"},
		Body:    []byte("body"),
	}
	dst := cloneRequestInput(src)
	require.Equal(t, src.Headers, dst.Headers)
	require.Equal(t, src.Body, dst.Body)

	// Deep copy: mutating dst does not touch src.
	dst.Body[0] = 'X'
	require.Equal(t, byte('b'), src.Body[0])

	// Empty body -> nil body clone.
	empty := cloneRequestInput(billingexpr.RequestInput{Headers: map[string]string{}})
	require.Nil(t, empty.Body)
}
