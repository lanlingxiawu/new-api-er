package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An image request the channel adaptor cannot express (Gemini generates images
// only with imagen models) fails before any upstream call with a 501 that stays
// retryable, so the next channel is tried; the client never sees adaptor text.
func TestImageHelper_UnsupportedByChannelIsRetryable(t *testing.T) {
	var upstreamCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	raw := `{"model":"gemini-2.5-flash-image","prompt":"a cat"}`
	req := &dto.ImageRequest{}
	require.NoError(t, common.UnmarshalJsonStr(raw, req))
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-2.5-flash-image")
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAIImage,
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		RequestURLPath:  "/v1/images/generations",
		OriginModelName: "gemini-2.5-flash-image",
		Request:         req,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}

	require.NoError(t, i18n.Init())
	apiErr := ImageHelper(c, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotImplemented, apiErr.StatusCode)
	assert.Equal(t, relaycommon.ErrorCodeImageRequestUnsupported, apiErr.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(apiErr), "another channel may support it")
	assert.Equal(t, "Model gemini-2.5-flash-image does not support this image request on the available channels.", apiErr.Error())
	assert.Zero(t, upstreamCalls.Load(), "the request must not reach upstream")
}

// Every adaptor ConvertImageRequest error, built the way the adaptor builds it,
// is sorted by who can fix it, and none of its text (Go errors, file paths,
// config keys) reaches the client.
func TestNewImageConvertError_Classification(t *testing.T) {
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)

	unsupported := "Model m does not support this image request on the available channels."
	invalid := i18n.Translate(i18n.LangEn, i18n.MsgRelayImageRequestInvalid)
	tooLarge := i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyTooLarge)
	prepare := i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestPrepareFailed)
	diskErr := &fs.PathError{Op: "open", Path: "/tmp/multipart-123", Err: errors.New("no space left on device")}
	jsonTypeErr := &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(0), Field: "seed"}
	uploadEOF := &url.Error{Op: "Post", URL: "https://api.replicate.com/v1/files", Err: io.EOF}

	type want struct {
		status    int
		code      types.ErrorCode
		skipRetry bool
		message   string
	}
	unsupportedWant := want{http.StatusNotImplemented, relaycommon.ErrorCodeImageRequestUnsupported, false, unsupported}
	invalidWant := want{http.StatusBadRequest, types.ErrorCodeInvalidRequest, true, invalid}
	serverWant := want{http.StatusInternalServerError, types.ErrorCodeConvertRequestFailed, false, prepare}
	upstreamWant := want{http.StatusBadGateway, types.ErrorCodeConvertRequestFailed, false, prepare}
	tooLargeWant := want{http.StatusRequestEntityTooLarge, types.ErrorCodeReadRequestBodyFailed, true, tooLarge}

	cases := []struct {
		name    string
		apiType int
		err     error
		want    want
	}{
		// the channel type cannot do it: next channel
		{"claude not implemented", constant.APITypeAnthropic, errors.New("not implemented"), unsupportedWant},
		{"aws not implemented", constant.APITypeAws, errors.New("not implemented"), unsupportedWant},
		{"codex", constant.APITypeCodex, errors.New("codex channel: endpoint not supported"), unsupportedWant},
		{"submodel", constant.APITypeSubmodel, errors.New("submodel channel: endpoint not supported"), unsupportedWant},
		{"gemini non-imagen", constant.APITypeGemini, errors.New("not supported model for image generation, only imagen models are supported"), unsupportedWant},
		{"ali relay mode", constant.APITypeAli, fmt.Errorf("unsupported image relay mode: %d", 9), unsupportedWant},
		{"minimax relay mode", constant.APITypeMiniMax, fmt.Errorf("unsupported image relay mode: %d", 9), unsupportedWant},
		{"advancedcustom converter", constant.APITypeAdvancedCustom, fmt.Errorf("converter %q does not support image requests", "openai_chat"), unsupportedWant},
		{"advancedcustom route", constant.APITypeAdvancedCustom, fmt.Errorf("advanced custom channel does not support request path %s for model %s", "/v1/images/edits", "m"), unsupportedWant},

		// advanced-custom channel configuration: not the client's fault, next channel
		{"advancedcustom missing config", constant.APITypeAdvancedCustom, errors.New("advanced_custom is required"), serverWant},
		{"advancedcustom route path", constant.APITypeAdvancedCustom, fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path is required", 0), serverWant},
		{"advancedcustom auth", constant.APITypeAdvancedCustom, errors.New("advanced_custom.auth.name is required"), serverWant},
		{"advancedcustom missing info", constant.APITypeAdvancedCustom, errors.New("missing relay info"), serverWant},

		// request content the client must fix
		{"openai image missing", constant.APITypeOpenAI, errors.New("image is required"), invalidWant},
		{"openai no form", constant.APITypeOpenAI, errors.New("no multipart form data found"), invalidWant},
		{"openai malformed form", constant.APITypeOpenAI, fmt.Errorf("failed to parse multipart form: %w", errors.New("multipart: NextPart: EOF")), invalidWant},
		{"ali image missing", constant.APITypeAli, fmt.Errorf("convert image edit form request failed: %w", fmt.Errorf("get image base64s from form failed: %w", errors.New("image is required"))), invalidWant},
		{"ali n out of range", constant.APITypeAli, fmt.Errorf("convert image request to async ali image request failed: %w", fmt.Errorf("parameters.n must be an integer between 1 and %d", 128)), invalidWant},
		{"ali parameters field", constant.APITypeAli, fmt.Errorf("convert image request to async ali image request failed: %w", fmt.Errorf("invalid parameters field: %w", jsonTypeErr)), invalidWant},
		{"ali malformed form", constant.APITypeAli, fmt.Errorf("convert image edit form request failed: %w", fmt.Errorf("get image base64s from form failed: %w", fmt.Errorf("failed to parse image edit form request: %w", errors.New("multipart: NextPart: bufio: buffer full")))), invalidWant},
		{"replicate prompt missing", constant.APITypeReplicate, errors.New("replicate adaptor: prompt is required"), invalidWant},
		{"replicate edit image missing", constant.APITypeReplicate, errors.New("replicate adaptor: image file is required for edits"), invalidWant},
		{"replicate malformed form", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: parse multipart form failed: %w", errors.New("multipart: boundary is empty")), invalidWant},
		{"replicate extra field", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: failed to decode extra field %s: %w", "seed", jsonTypeErr), invalidWant},
		{"jimeng extra fields", constant.APITypeJimeng, fmt.Errorf("failed to unmarshal extra fields: %w", &json.SyntaxError{Offset: 1}), invalidWant},

		// form over the size limits
		{"openai form too large", constant.APITypeOpenAI, fmt.Errorf("failed to parse multipart form: %w", multipart.ErrMessageTooLarge), tooLargeWant},

		// the gateway's own disk and form writing: server fault, next channel
		{"openai form temp file", constant.APITypeOpenAI, fmt.Errorf("failed to parse multipart form: %w", diskErr), serverWant},
		{"openai open uploaded file", constant.APITypeOpenAI, fmt.Errorf("failed to open image file %d: %w", 0, diskErr), serverWant},
		{"openai form part", constant.APITypeOpenAI, fmt.Errorf("create form part failed for image %d: %w", 0, errors.New("io: read/write on closed pipe")), serverWant},
		{"openai mask", constant.APITypeOpenAI, errors.New("failed to open mask file"), serverWant},
		{"ali read uploaded file", constant.APITypeAli, fmt.Errorf("convert image edit form request failed: %w", fmt.Errorf("get image base64s from form failed: %w", errors.New("failed to read image file"))), serverWant},
		{"replicate form temp file", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: parse multipart form failed: %w", diskErr), serverWant},
		{"replicate relay info nil", constant.APITypeReplicate, errors.New("replicate adaptor: relay info is nil"), serverWant},
		{"replicate upload form", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: create upload form failed: %w", errors.New("x")), serverWant},

		// the adaptor's own upstream call (Replicate file upload): 502, next channel
		{"replicate upload dropped", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: upload image failed: %w", uploadEOF), upstreamWant},
		{"replicate upload status", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: upload image failed with status %d: %s", 503, "busy"), upstreamWant},
		{"replicate upload response", constant.APITypeReplicate, fmt.Errorf("replicate adaptor: decode upload response failed: %w", &json.SyntaxError{Offset: 3}), upstreamWant},
		{"replicate upload url", constant.APITypeReplicate, errors.New("replicate adaptor: upload response missing url"), upstreamWant},
	}
	for _, tc := range cases {
		info := &relaycommon.RelayInfo{OriginModelName: "m", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiType: tc.apiType}}
		apiErr := newImageConvertError(c, info, tc.err)
		assert.Equal(t, tc.want.status, apiErr.StatusCode, tc.name)
		assert.Equal(t, tc.want.code, apiErr.GetErrorCode(), tc.name)
		assert.Equal(t, tc.want.skipRetry, types.IsSkipRetryError(apiErr), tc.name)
		assert.Equal(t, tc.want.message, apiErr.Error(), tc.name)
		if !tc.want.skipRetry {
			assert.True(t, relaycommon.IsLocalRequestBuildError(apiErr), "%s: retryable ones must not auto-disable", tc.name)
		}
		for _, internal := range []string{"/tmp", "advanced_custom", "replicate", "multipart:", "json:"} {
			assert.NotContains(t, apiErr.ToOpenAIError().Message, internal, tc.name)
		}
	}

	// No relay info (never the case in ImageHelper) still yields a message.
	assert.Equal(t, http.StatusInternalServerError, newImageConvertError(c, nil, errors.New("boom")).StatusCode)
}
