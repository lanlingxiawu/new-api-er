package common_handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// errReadCloser fails on Read to exercise the io.ReadAll error branch.
type errReadCloser struct{}

func (errReadCloser) Read(_ []byte) (int, error) { return 0, errors.New("boom") }
func (errReadCloser) Close() error               { return nil }

// newResp builds an *http.Response whose body is the given string.
func newResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// newCtx returns a gin test context backed by a recorder.
func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return c, rec
}

// newInfo builds a RelayInfo with the pointer-embedded ChannelMeta/RerankerInfo
// initialized (both are embedded as pointers on RelayInfo).
func newInfo(channelType int, returnDocs bool, docs []any) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: channelType},
		RerankerInfo: &relaycommon.RerankerInfo{ReturnDocuments: returnDocs, Documents: docs},
	}
}

// --- read-body error path ---------------------------------------------------

func TestRerankHandler_ReadBodyError(t *testing.T) {
	c, _ := newCtx()
	info := newInfo(constant.ChannelTypeOpenAI, false, nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}, Header: make(http.Header)}

	usage, apiErr := RerankHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, apiErr.GetErrorCode())
}

// --- non-Xinference (Jina-style) path --------------------------------------

func TestRerankHandler_Jina_Success(t *testing.T) {
	c, rec := newCtx()
	info := newInfo(constant.ChannelTypeOpenAI, false, nil)
	body := `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":42}}`

	usage, apiErr := RerankHandler(c, info, newResp(body))

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	// Jina branch normalizes PromptTokens := TotalTokens
	assert.Equal(t, 42, usage.TotalTokens)
	assert.Equal(t, 42, usage.PromptTokens)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "relevance_score")
}

func TestRerankHandler_Jina_BadJSON(t *testing.T) {
	c, _ := newCtx()
	info := newInfo(constant.ChannelTypeOpenAI, false, nil)

	usage, apiErr := RerankHandler(c, info, newResp("not-json"))

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
}

// --- Xinference path --------------------------------------------------------

func TestRerankHandler_Xinference_BadJSON(t *testing.T) {
	c, _ := newCtx()
	info := newInfo(constant.ChannelTypeXinference, false, nil)

	usage, apiErr := RerankHandler(c, info, newResp("not-json"))

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
}

// ReturnDocuments=false: Document field must be dropped from every result and
// usage is derived from the estimate prompt tokens.
func TestRerankHandler_Xinference_NoReturnDocuments(t *testing.T) {
	c, _ := newCtx()
	info := newInfo(constant.ChannelTypeXinference, false, nil)
	info.SetEstimatePromptTokens(7)
	body := `{"results":[{"index":0,"relevance_score":0.5,"document":"hello"}]}`

	usage, apiErr := RerankHandler(c, info, newResp(body))

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 7, usage.TotalTokens)
}

// ReturnDocuments=true with the full document-branch matrix in a single
// response: non-empty string kept, empty string -> fallback to info.Documents,
// non-string kept as-is, nil document -> Document stays nil.
func TestRerankHandler_Xinference_ReturnDocuments_AllBranches(t *testing.T) {
	c, rec := newCtx()
	// index-aligned fallback documents
	info := newInfo(constant.ChannelTypeXinference, true,
		[]any{"fallback-0", "fallback-1", "fallback-2", "fallback-3"})
	info.SetEstimatePromptTokens(11)
	body := `{"results":[
		{"index":0,"relevance_score":0.9,"document":"kept-string"},
		{"index":1,"relevance_score":0.8,"document":""},
		{"index":2,"relevance_score":0.7,"document":{"nested":"obj"}},
		{"index":3,"relevance_score":0.6}
	]}`

	usage, apiErr := RerankHandler(c, info, newResp(body))

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)

	// decode what was written to the client to inspect Document handling
	var out dto.RerankResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Results, 4)

	// index 0: non-empty string kept verbatim
	assert.Equal(t, "kept-string", out.Results[0].Document)
	// index 1: empty string -> fallback to info.Documents[1]
	assert.Equal(t, "fallback-1", out.Results[1].Document)
	// index 2: non-string object kept as-is (map after JSON round-trip)
	m, ok := out.Results[2].Document.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "obj", m["nested"])
	// index 3: document absent -> nil, omitempty drops it
	assert.Nil(t, out.Results[3].Document)

	// index / relevance carried through
	assert.Equal(t, 0, out.Results[0].Index)
	assert.InDelta(t, 0.9, out.Results[0].RelevanceScore, 1e-9)
	assert.Equal(t, http.StatusOK, rec.Code)
}
