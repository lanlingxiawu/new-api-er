package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nativeGeminiInfo(baseURL, path, model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeGemini,
		RequestURLPath:  path,
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    baseURL,
			ApiKey:            "channel-key",
			UpstreamModelName: model,
		},
	}
}

// D-1: a native embed path used to be sent upstream as :generateContent
// whenever the model name did not start with an embedding prefix, while the
// body was an embed request and the response was parsed as an embedding.
func TestGetRequestURLNativeEmbedActionFollowsPath(t *testing.T) {
	a := &Adaptor{}
	for _, tc := range []struct {
		name, path, model, wantSuffix string
	}{
		{"embed on non-embedding model", "/v1beta/models/gemini-2.0-flash:embedContent", "gemini-2.0-flash", "/models/gemini-2.0-flash:embedContent"},
		{"batch embed on non-embedding model", "/v1beta/models/gemini-2.0-flash:batchEmbedContents", "gemini-2.0-flash", "/models/gemini-2.0-flash:batchEmbedContents"},
		{"embed with query key", "/v1beta/models/text-embedding-004:embedContent?key=x", "text-embedding-004", "/models/text-embedding-004:embedContent"},
		{"generate on embedding-named model", "/v1beta/models/gemini-embedding-001:generateContent", "gemini-embedding-001", "/models/gemini-embedding-001:embedContent"},
		{"generate", "/v1beta/models/gemini-2.5-flash:generateContent", "gemini-2.5-flash", "/models/gemini-2.5-flash:generateContent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, err := a.GetRequestURL(nativeGeminiInfo("https://upstream.test", tc.path, tc.model))
			require.NoError(t, err)
			assert.Contains(t, url, tc.wantSuffix)
		})
	}
}

// Non-native requests (OpenAI /v1/embeddings to a Gemini channel) keep the
// model-name rule, whatever their URL path says.
func TestGetRequestURLNonNativeEmbeddingUsesModelName(t *testing.T) {
	info := nativeGeminiInfo("https://upstream.test", "/v1/embeddings", "gemini-embedding-001")
	info.RelayMode = constant.RelayModeEmbeddings
	info.IsGeminiBatchEmbedding = true
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Contains(t, url, "/models/gemini-embedding-001:batchEmbedContents")

	info = nativeGeminiInfo("https://upstream.test", "/v1/chat/completions:embedContent", "gemini-2.5-flash")
	info.RelayMode = constant.RelayModeChatCompletions
	url, err = (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Contains(t, url, "/models/gemini-2.5-flash:generateContent")
	assert.Equal(t, "", nativeGeminiEmbedAction(info))
}

// Round trip against a mock upstream: the upstream sees :embedContent and the
// embedding response is relayed to the client as is.
func TestNativeEmbedRequestReachesUpstreamEmbedAction(t *testing.T) {
	var mu sync.Mutex
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":{"values":[0.25,0.5]}}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	path := "/v1beta/models/gemini-2.0-flash:embedContent"
	body := []byte(`{"content":{"parts":[{"text":"hi"}]}}`)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))

	a := &Adaptor{}
	info := nativeGeminiInfo(upstream.URL, path, "gemini-2.0-flash")
	resp, err := a.DoRequest(c, info, bytes.NewReader(body))
	require.NoError(t, err)
	httpResp, ok := resp.(*http.Response)
	require.True(t, ok)

	usage, apiErr := a.DoResponse(c, httpResp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	mu.Lock()
	assert.Equal(t, "/v1beta/models/gemini-2.0-flash:embedContent", upstreamPath)
	mu.Unlock()
	relayed, err := io.ReadAll(recorder.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"embedding":{"values":[0.25,0.5]}}`, string(relayed))
}
