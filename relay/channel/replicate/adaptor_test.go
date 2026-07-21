package replicate

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	constant.StreamingTimeout = 300
	service.InitHttpClient()
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
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeReplicate}}
}

// newMultipartWithImage writes a single file part and returns the content type.
func newMultipartWithImage(b io.Writer, field, filename, content string) string {
	mw := multipart.NewWriter(b)
	part, _ := mw.CreateFormFile(field, filename)
	_, _ = part.Write([]byte(content))
	_ = mw.Close()
	return mw.FormDataContentType()
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info returns error", func(t *testing.T) {
		_, err := a.GetRequestURL(nil)
		require.Error(t, err)
	})

	t.Run("empty base url falls back to default", func(t *testing.T) {
		info := newInfo()
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		// empty request path => returns base url as-is
		assert.Equal(t, "https://api.replicate.com", got)
		assert.Equal(t, "https://api.replicate.com", info.ChannelBaseUrl)
	})

	t.Run("with request path builds full url", func(t *testing.T) {
		info := newInfo()
		info.ChannelBaseUrl = "https://api.replicate.com"
		info.RequestURLPath = "/v1/models/foo/predictions"
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://api.replicate.com/v1/models/foo/predictions", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info returns error", func(t *testing.T) {
		err := a.SetupRequestHeader(newTestContext(httptest.NewRecorder()), &http.Header{}, nil)
		require.Error(t, err)
	})

	t.Run("empty api key returns error", func(t *testing.T) {
		err := a.SetupRequestHeader(newTestContext(httptest.NewRecorder()), &http.Header{}, newInfo())
		require.Error(t, err)
	})

	t.Run("success sets auth and prefer headers", func(t *testing.T) {
		info := newInfo()
		info.ApiKey = "tok-123"
		h := http.Header{}
		err := a.SetupRequestHeader(newTestContext(httptest.NewRecorder()), &h, info)
		require.NoError(t, err)
		assert.Equal(t, "Bearer tok-123", h.Get("Authorization"))
		assert.Equal(t, "wait", h.Get("Prefer"))
		assert.Equal(t, "application/json", h.Get("Content-Type"))
		assert.Equal(t, "application/json", h.Get("Accept"))
	})
}

// ---- ConvertImageRequest ----

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info returns error", func(t *testing.T) {
		_, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), nil, dto.ImageRequest{Prompt: "x"})
		require.Error(t, err)
	})

	t.Run("empty prompt returns error", func(t *testing.T) {
		_, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{})
		require.Error(t, err)
	})

	t.Run("default model and path set", func(t *testing.T) {
		info := newInfo()
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), info, dto.ImageRequest{Prompt: "a cat"})
		require.NoError(t, err)
		assert.Equal(t, ModelFlux11Pro, info.UpstreamModelName)
		assert.Equal(t, "/v1/models/"+ModelFlux11Pro+"/predictions", info.RequestURLPath)
		m := out.(map[string]any)
		input := m["input"].(map[string]any)
		assert.Equal(t, "a cat", input["prompt"])
	})

	t.Run("model from request when upstream empty", func(t *testing.T) {
		info := newInfo()
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), info, dto.ImageRequest{Prompt: "p", Model: "acme/model"})
		require.NoError(t, err)
		_ = out
		assert.Equal(t, "acme/model", info.UpstreamModelName)
	})

	t.Run("prompt from postform fallback", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("prompt=formprompt"))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		out, err := a.ConvertImageRequest(c, newInfo(), dto.ImageRequest{})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "formprompt", input["prompt"])
	})

	t.Run("square size maps to aspect ratio", func(t *testing.T) {
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{Prompt: "p", Size: "1024x1024"})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "1:1", input["aspect_ratio"])
	})

	t.Run("custom size maps to custom aspect with dimensions", func(t *testing.T) {
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{Prompt: "p", Size: "1000x1300"})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "custom", input["aspect_ratio"])
		assert.Contains(t, input, "width")
		assert.Contains(t, input, "height")
	})

	t.Run("output_format quality N applied", func(t *testing.T) {
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{
			Prompt:       "p",
			OutputFormat: []byte(`"webp"`),
			Quality:      "hd",
			N:            lo.ToPtr(uint(3)),
		})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "webp", input["output_format"])
		assert.Equal(t, true, input["prompt_upsampling"])
		assert.Equal(t, 3, input["num_outputs"])
	})

	t.Run("extra_fields merged", func(t *testing.T) {
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{
			Prompt:      "p",
			ExtraFields: []byte(`{"seed":42}`),
		})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.EqualValues(t, 42, input["seed"])
	})

	t.Run("invalid extra_fields returns error", func(t *testing.T) {
		_, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.ImageRequest{
			Prompt:      "p",
			ExtraFields: []byte(`not-json`),
		})
		require.Error(t, err)
	})

	t.Run("extra input map merged", func(t *testing.T) {
		req := dto.ImageRequest{Prompt: "p"}
		req.Extra = map[string]json.RawMessage{"input": json.RawMessage(`{"guidance":7}`)}
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), req)
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.EqualValues(t, 7, input["guidance"])
	})

	t.Run("extra scalar field merged", func(t *testing.T) {
		req := dto.ImageRequest{Prompt: "p"}
		req.Extra = map[string]json.RawMessage{"steps": json.RawMessage(`28`)}
		out, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), newInfo(), req)
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.EqualValues(t, 28, input["steps"])
	})

	t.Run("edits mode without file returns error", func(t *testing.T) {
		info := newInfo()
		info.RelayMode = relayconstant.RelayModeImagesEdits
		_, err := a.ConvertImageRequest(newTestContext(httptest.NewRecorder()), info, dto.ImageRequest{Prompt: "p"})
		require.Error(t, err)
	})

	t.Run("edits mode uploads file and sets image_prompt", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/files", r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"urls":{"get":"https://cdn/uploaded.png"}}`))
		}))
		defer srv.Close()

		// Build a multipart request carrying an "image" file.
		var body strings.Builder
		mw := newMultipartWithImage(&body, "image", "pic.png", "fakebytes")
		c := newTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
		c.Request.Header.Set("Content-Type", mw)

		info := newInfo()
		info.RelayMode = relayconstant.RelayModeImagesEdits
		info.ApiKey = "k"
		info.ChannelBaseUrl = srv.URL
		out, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "edit me"})
		require.NoError(t, err)
		input := out.(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "https://cdn/uploaded.png", input["image_prompt"])
	})
}

// ---- DoResponse ----

func TestDoResponse(t *testing.T) {
	a := &Adaptor{}

	newDoCtx := func() (*httptest.ResponseRecorder, *gin.Context) {
		rec := httptest.NewRecorder()
		return rec, newTestContext(rec)
	}

	t.Run("nil response returns error", func(t *testing.T) {
		_, err := a.DoResponse(newTestContext(httptest.NewRecorder()), nil, newInfo())
		require.NotNil(t, err)
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		_, ctx := newDoCtx()
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, "not-json"), newInfo())
		require.NotNil(t, err)
	})

	t.Run("prediction error message surfaced", func(t *testing.T) {
		_, ctx := newDoCtx()
		body := `{"error":{"message":"boom"}}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "boom")
	})

	t.Run("prediction error falls back to detail", func(t *testing.T) {
		_, ctx := newDoCtx()
		body := `{"error":{"detail":"detailed"}}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "detailed")
	})

	t.Run("status not succeeded returns error", func(t *testing.T) {
		_, ctx := newDoCtx()
		body := `{"status":"failed"}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.NotNil(t, err)
	})

	t.Run("empty output returns error", func(t *testing.T) {
		_, ctx := newDoCtx()
		body := `{"status":"succeeded","output":null}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.NotNil(t, err)
	})

	t.Run("string output writes image url", func(t *testing.T) {
		rec, ctx := newDoCtx()
		body := `{"status":"succeeded","output":"https://cdn/x.png"}`
		usage, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "https://cdn/x.png")
	})

	t.Run("array output writes multiple urls", func(t *testing.T) {
		rec, ctx := newDoCtx()
		body := `{"status":"succeeded","output":["https://cdn/a.png","https://cdn/b.png"]}`
		usage, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "a.png")
		assert.Contains(t, out, "b.png")
	})

	t.Run("succeeded but no usable string in array returns error", func(t *testing.T) {
		_, ctx := newDoCtx()
		body := `{"status":"succeeded","output":[123,456]}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), newInfo())
		require.NotNil(t, err)
	})

	t.Run("b64 requested but download blocked returns error", func(t *testing.T) {
		_, ctx := newDoCtx()
		info := newInfo()
		info.Request = &dto.ImageRequest{ResponseFormat: "b64_json"}
		body := `{"status":"succeeded","output":"http://127.0.0.1:1/x.png"}`
		_, err := a.DoResponse(ctx, newResponse(http.StatusOK, body), info)
		require.NotNil(t, err)
	})
}

// ---- pure helpers ----

func TestMapOpenAISizeToFlux(t *testing.T) {
	cases := []struct {
		size   string
		aspect string
		ok     bool
	}{
		{"1024x1024", "1:1", true},
		{"1792x1024", "16:9", true},
		{"1024x1792", "9:16", true},
		{"1536x1024", "3:2", true},
		{"1024x1536", "2:3", true},
		{"800x600", "4:3", true},   // reduces to 4:3
		{"1000x1300", "custom", true},
		{"bad", "", false},
		{"0x100", "", false},
		{"axb", "", false},
	}
	for _, c := range cases {
		aspect, _, _, ok := mapOpenAISizeToFlux(c.size)
		assert.Equal(t, c.ok, ok, c.size)
		if c.ok {
			assert.Equal(t, c.aspect, aspect, c.size)
		}
	}
}

func TestNormalizeFluxDimension(t *testing.T) {
	assert.Equal(t, 256, normalizeFluxDimension(100))  // below min
	assert.Equal(t, 1440, normalizeFluxDimension(5000)) // above max
	assert.Equal(t, 0+512, normalizeFluxDimension(512)) // already aligned
	// round to nearest step of 32
	assert.Equal(t, 512, normalizeFluxDimension(500))
	assert.Equal(t, 544, normalizeFluxDimension(530))
}

func TestGcdAndReduceRatio(t *testing.T) {
	assert.Equal(t, 4, gcd(8, 12))
	assert.Equal(t, 5, gcd(5, 0))
	rw, rh := reduceRatio(1920, 1080)
	assert.Equal(t, 16, rw)
	assert.Equal(t, 9, rh)
}

// ---- getters & unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := newInfo()
	a.Init(info)

	_, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{})
	assert.Error(t, err)
	_, err = a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	_, err = a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{})
	assert.Error(t, err)
	_, err = a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
}

var _ = types.ErrorCodeBadResponse
