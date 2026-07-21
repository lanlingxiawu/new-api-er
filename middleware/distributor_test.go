package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// extractModelNameFromGeminiPath — pure logic
// ---------------------------------------------------------------------------

func TestExtractModelNameFromGeminiPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1beta/models/gemini-2.0-flash:generateContent", "gemini-2.0-flash"},
		{"/v1beta/models/gemini-1.5-pro", "gemini-1.5-pro"}, // no colon -> to end
		{"/v1/chat/completions", ""},                        // no /models/
		{"/v1beta/models/", ""},                             // nothing after prefix
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.want, extractModelNameFromGeminiPath(tc.path))
		})
	}
}

// ---------------------------------------------------------------------------
// getJSONStringValue — via getModelFromJSONBody
// ---------------------------------------------------------------------------

func newJSONCtx(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, rec := newCtx(http.MethodPost, "/v1/chat/completions", body)
	ctx.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
	return ctx, rec
}

func TestGetModelFromJSONBody_Valid(t *testing.T) {
	ctx, _ := newJSONCtx(t, `{"model":"gpt-4o","group":"vip"}`)
	req, err := getModelFromJSONBody(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", req.Model)
	require.Equal(t, "vip", req.Group)
}

func TestGetModelFromJSONBody_MissingFields(t *testing.T) {
	ctx, _ := newJSONCtx(t, `{"messages":[]}`)
	req, err := getModelFromJSONBody(ctx)
	require.NoError(t, err)
	require.Equal(t, "", req.Model)
	require.Equal(t, "", req.Group)
}

func TestGetModelFromJSONBody_NullModel(t *testing.T) {
	ctx, _ := newJSONCtx(t, `{"model":null}`)
	req, err := getModelFromJSONBody(ctx)
	require.NoError(t, err)
	require.Equal(t, "", req.Model)
}

func TestGetModelFromJSONBody_WrongType(t *testing.T) {
	ctx, _ := newJSONCtx(t, `{"model":123}`)
	_, err := getModelFromJSONBody(ctx)
	require.Error(t, err) // model must be a string
}

func TestGetModelFromJSONBody_InvalidJSON(t *testing.T) {
	ctx, _ := newJSONCtx(t, `{not json`)
	_, err := getModelFromJSONBody(ctx)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// getModelRequest — path/method routing
// ---------------------------------------------------------------------------

func prepModelReqCtx(t *testing.T, method, target, contentType, body string) *gin.Context {
	t.Helper()
	ctx, _ := newCtx(method, target, body)
	if contentType != "" {
		ctx.Request.Header.Set("Content-Type", contentType)
	}
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
	return ctx
}

func TestGetModelRequest_ChatCompletionsJSON(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/chat/completions", "application/json", `{"model":"gpt-4o"}`)
	req, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.True(t, shouldSelect)
	require.Equal(t, "gpt-4o", req.Model)
}

func TestGetModelRequest_GeminiPath(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", "application/json", `{}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "gemini-2.0-flash", req.Model)
}

func TestGetModelRequest_ModerationsDefault(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/moderations", "application/json", `{}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "text-moderation-stable", req.Model)
}

func TestGetModelRequest_ImagesGenerationsDefault(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/images/generations", "application/json", `{}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "dall-e", req.Model)
}

func TestGetModelRequest_AudioSpeechDefault(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/audio/speech", "application/json", `{}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "tts-1", req.Model)
	relayMode, ok := ctx.Get("relay_mode")
	require.True(t, ok)
	require.NotNil(t, relayMode)
}

func TestGetModelRequest_RealtimeQueryModel(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodGet, "/v1/realtime?model=gpt-4o-realtime", "", "")
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o-realtime", req.Model)
}

func TestGetModelRequest_VideosGetFetchDoesNotSelect(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodGet, "/v1/videos/task_123", "", "")
	_, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.False(t, shouldSelect) // GET fetch by id -> no channel selection
}

func TestGetModelRequest_RemixDoesNotSelect(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/videos/abc/remix", "application/json", `{}`)
	_, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.False(t, shouldSelect)
}

func TestGetModelRequest_InvalidJSONReturnsError(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/chat/completions", "application/json", `{bad`)
	_, _, err := getModelRequest(ctx)
	require.Error(t, err)
}

func TestGetModelRequest_MjFetchNoSelect(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodGet, "/mj/task/123/fetch", "", "")
	_, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.False(t, shouldSelect)
	require.NotNil(t, ctx.MustGet("relay_mode"))
}

// ---------------------------------------------------------------------------
// channelSupportsRequestPath
// ---------------------------------------------------------------------------

func TestChannelSupportsRequestPath_NilChannel(t *testing.T) {
	require.False(t, channelSupportsRequestPath(nil, "/v1/chat/completions", "gpt-4o"))
}

func TestChannelSupportsRequestPath_NonAdvancedAlwaysTrue(t *testing.T) {
	ch := &model.Channel{Type: 1} // not AdvancedCustom
	require.True(t, channelSupportsRequestPath(ch, "/v1/chat/completions", "gpt-4o"))
}

func TestChannelSupportsRequestPath_AdvancedCustomNoConfig(t *testing.T) {
	ch := &model.Channel{Type: constant.ChannelTypeAdvancedCustom}
	// No AdvancedCustom config -> not usable.
	require.False(t, channelSupportsRequestPath(ch, "/v1/chat/completions", "gpt-4o"))
}

// ---------------------------------------------------------------------------
// SetupContextForSelectedChannel
// ---------------------------------------------------------------------------

func TestSetupContextForSelectedChannel_NilChannel(t *testing.T) {
	ctx, _ := newCtx(http.MethodPost, "/v1/chat/completions", "")
	apiErr := SetupContextForSelectedChannel(ctx, nil, "gpt-4o")
	require.NotNil(t, apiErr)
	require.Equal(t, "gpt-4o", ctx.GetString("original_model"))
}

func TestSetupContextForSelectedChannel_SetsContextKeys(t *testing.T) {
	ctx, _ := newCtx(http.MethodPost, "/v1/chat/completions", "")
	ch := &model.Channel{
		Id:   777,
		Type: constant.ChannelTypeAzure,
		Name: "azure-chan",
		Key:  "secret-key",
		Other: "2024-01-01",
	}
	apiErr := SetupContextForSelectedChannel(ctx, ch, "gpt-4o")
	require.Nil(t, apiErr)
	require.Equal(t, 777, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
	require.Equal(t, "azure-chan", common.GetContextKeyString(ctx, constant.ContextKeyChannelName))
	require.Equal(t, "secret-key", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
	// Azure sets api_version from Other.
	require.Equal(t, "2024-01-01", ctx.GetString("api_version"))
}

// ---------------------------------------------------------------------------
// Distribute — full middleware flow
// ---------------------------------------------------------------------------

// strReader wraps a string as an io.Reader for request bodies.
func strReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestDistribute_SpecificChannelValid(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	ran := false
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { c.Set(string(constant.ContextKeyTokenSpecificChannelId), strconv.Itoa(ch.Id)) },
		Distribute(),
		func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, ran)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDistribute_SpecificChannelNotNumeric(t *testing.T) {
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { c.Set(string(constant.ContextKeyTokenSpecificChannelId), "not-a-number") },
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDistribute_SpecificChannelDisabled(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(ch *model.Channel) { ch.Status = common.ChannelStatusManuallyDisabled })
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { c.Set(string(constant.ContextKeyTokenSpecificChannelId), strconv.Itoa(ch.Id)) },
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDistribute_ModelNameRequired(t *testing.T) {
	r := gin.New()
	r.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code) // empty model -> required
}

func TestDistribute_NoAvailableChannel(t *testing.T) {
	requireDB(t)
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUsingGroup, uniq("nogroup"))
		},
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"nonexistent-model-xyz"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestDistribute_TokenModelLimitEmptyForbidden(t *testing.T) {
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) {
			c.Set(string(constant.ContextKeyTokenModelLimitEnabled), true)
			// No ContextKeyTokenModelLimit set -> empty -> all models forbidden.
		},
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDistribute_TokenModelForbidden(t *testing.T) {
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) {
			c.Set(string(constant.ContextKeyTokenModelLimitEnabled), true)
			c.Set(string(constant.ContextKeyTokenModelLimit), map[string]bool{"gpt-3.5": true})
		},
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDistribute_SelectionHappyPath(t *testing.T) {
	requireDB(t)
	group := uniq("g")
	modelName := "test-model-" + uniq("m")

	ch := mkChannel(t, func(ch *model.Channel) {
		ch.Group = group
		ch.Models = modelName
	})
	// Seed an ability so InitChannelCache maps group+model -> channel.
	prio := int64(0)
	ability := &model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: ch.Id,
		Enabled:   true,
		Priority:  &prio,
		Weight:    1,
	}
	require.NoError(t, model.DB.Create(ability).Error)
	t.Cleanup(func() {
		model.DB.Where("channel_id = ?", ch.Id).Delete(&model.Ability{})
	})

	prevMem := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = prevMem
		model.InitChannelCache() // restore cache to real data
	})
	model.InitChannelCache()

	ran := false
	selectedChannelId := 0
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { common.SetContextKey(c, constant.ContextKeyUsingGroup, group) },
		Distribute(),
		func(c *gin.Context) {
			ran = true
			selectedChannelId = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
			c.Status(http.StatusOK)
		})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"`+modelName+`"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, ran, "terminal handler should run; body=%s", rec.Body.String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, ch.Id, selectedChannelId)
}
