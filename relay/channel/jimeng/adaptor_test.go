package jimeng

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func newTestContext(rec http.ResponseWriter) *gin.Context {
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		StartTime:   time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com"},
	}
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	got, err := a.GetRequestURL(newInfo())
	require.NoError(t, err)
	assert.Equal(t, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", got)
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())

	t.Run("nil request returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, newInfo(), nil)
		require.Error(t, err)
	})

	t.Run("passthrough non-nil request", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "x"}
		out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
		require.NoError(t, err)
		assert.Equal(t, req, out)
	})
}

// ---- ConvertImageRequest ----

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())

	t.Run("default response format sets return_url", func(t *testing.T) {
		out, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{Model: "jimeng_high_aes_general_v21_L", Prompt: "a cat"})
		require.NoError(t, err)
		p := out.(imageRequestPayload)
		assert.Equal(t, "jimeng_high_aes_general_v21_L", p.ReqKey)
		assert.Equal(t, "a cat", p.Prompt)
		assert.True(t, p.ReturnURL)
	})

	t.Run("explicit url format sets return_url", func(t *testing.T) {
		out, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{Prompt: "p", ResponseFormat: "url"})
		require.NoError(t, err)
		assert.True(t, out.(imageRequestPayload).ReturnURL)
	})

	t.Run("b64_json format does not set return_url", func(t *testing.T) {
		out, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{Prompt: "p", ResponseFormat: "b64_json"})
		require.NoError(t, err)
		assert.False(t, out.(imageRequestPayload).ReturnURL)
	})

	t.Run("extra_fields override merged", func(t *testing.T) {
		out, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{
			Prompt:      "p",
			ExtraFields: []byte(`{"width":768,"height":512,"seed":7}`),
		})
		require.NoError(t, err)
		p := out.(imageRequestPayload)
		assert.Equal(t, 768, p.Width)
		assert.Equal(t, 512, p.Height)
		assert.EqualValues(t, 7, p.Seed)
	})

	t.Run("invalid extra_fields returns error", func(t *testing.T) {
		_, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{Prompt: "p", ExtraFields: []byte(`not-json`)})
		require.Error(t, err)
	})
}

// ---- responseJimeng2OpenAIImage ----

func TestResponseJimeng2OpenAIImage(t *testing.T) {
	info := newInfo()
	resp := &ImageResponse{}
	resp.Data.BinaryDataBase64 = []string{"b64a", "b64b"}
	resp.Data.ImageUrls = []string{"https://cdn/x.png"}
	out := responseJimeng2OpenAIImage(newTestContext(httptest.NewRecorder()), resp, info)
	require.Len(t, out.Data, 3)
	assert.Equal(t, "b64a", out.Data[0].B64Json)
	assert.Equal(t, "b64b", out.Data[1].B64Json)
	assert.Equal(t, "https://cdn/x.png", out.Data[2].Url)
	assert.Equal(t, info.StartTime.Unix(), out.Created)
}

// ---- jimengImageHandler ----

func TestJimengImageHandler(t *testing.T) {
	t.Run("success writes openai image response", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		body := `{"code":10000,"message":"ok","data":{"image_urls":["https://cdn/a.png"]}}`
		usage, err := jimengImageHandler(c, newResponse(http.StatusOK, body), newInfo())
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Contains(t, rec.Body.String(), "https://cdn/a.png")
	})

	t.Run("upstream error code surfaced", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		body := `{"code":50411,"message":"prompt blocked","data":{}}`
		usage, err := jimengImageHandler(c, newResponse(http.StatusOK, body), newInfo())
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "prompt blocked")
		assert.Equal(t, "50411", err.ToOpenAIError().Code)
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		usage, err := jimengImageHandler(c, newResponse(http.StatusOK, "not-json"), newInfo())
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- DoResponse routing ----

func TestDoResponse(t *testing.T) {
	a := &Adaptor{}

	t.Run("images generations routes to image handler", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := newInfo()
		info.RelayMode = relayconstant.RelayModeImagesGenerations
		body := `{"code":10000,"data":{"image_urls":["https://cdn/z.png"]}}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Contains(t, rec.Body.String(), "z.png")
	})

	t.Run("non-image malformed routes to openai handler and errors", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := newInfo()
		info.RelayMode = relayconstant.RelayModeChatCompletions
		_, err := a.DoResponse(c, newResponse(http.StatusOK, "not-json"), info)
		require.NotNil(t, err)
	})
}

// ---- Sign ----

func TestSign(t *testing.T) {
	t.Run("invalid key format returns error", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		req, _ := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", strings.NewReader(`{"a":1}`))
		c.Request = req
		err := Sign(c, req, "onlyonepart")
		require.Error(t, err)
	})

	t.Run("valid ak|sk sets signature headers", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		req, _ := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", strings.NewReader(`{"prompt":"hi"}`))
		c.Request = req
		err := Sign(c, req, "myak|mysk")
		require.NoError(t, err)
		assert.NotEmpty(t, req.Header.Get("Authorization"))
		assert.Contains(t, req.Header.Get("Authorization"), "HMAC-SHA256 Credential=myak/")
		assert.NotEmpty(t, req.Header.Get("X-Date"))
		assert.NotEmpty(t, req.Header.Get("X-Content-Sha256"))
		assert.Equal(t, "visual.volcengineapi.com", req.Header.Get("Host"))
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	})

	t.Run("nil body still signs", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		req, _ := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess", nil)
		c.Request = req
		err := Sign(c, req, "ak|sk")
		require.NoError(t, err)
		assert.NotEmpty(t, req.Header.Get("Authorization"))
	})
}

// ---- SetPayloadHash / getPayloadHash / hmacSHA256 ----

func TestPayloadHashAndHmac(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	require.Empty(t, getPayloadHash(c))
	err := SetPayloadHash(c, map[string]any{"prompt": "hi"})
	require.NoError(t, err)
	h := getPayloadHash(c)
	assert.Len(t, h, 64) // hex sha256

	// deterministic hmac
	a := hmacSHA256([]byte("k"), []byte("d"))
	b := hmacSHA256([]byte("k"), []byte("d"))
	assert.Equal(t, a, b)
	assert.NotEqual(t, hmacSHA256([]byte("k1"), []byte("d")), a)
}

// ---- getters & unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := newInfo()
	a.Init(info)

	require.Error(t, a.SetupRequestHeader(c, &http.Header{}, info))
	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{})
	assert.Error(t, err)
	_, err = a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
}
