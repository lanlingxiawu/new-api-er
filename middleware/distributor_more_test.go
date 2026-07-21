package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Additional getModelRequest path branches to exercise the routing switch.

func TestGetModelRequest_EmbeddingsDefaultFromParam(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/embeddings", "application/json", `{"model":"text-embedding-3-small"}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "text-embedding-3-small", req.Model)
}

func TestGetModelRequest_AudioTranslationsDefault(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/audio/translations", "application/json", `{}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "whisper-1", req.Model)
	require.Equal(t, relayconstant.RelayModeAudioTranslation, ctx.MustGet("relay_mode"))
}

func TestGetModelRequest_AudioTranscriptionsMultipartDefault(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/audio/transcriptions", "multipart/form-data; boundary=x", "")
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "whisper-1", req.Model)
	require.Equal(t, relayconstant.RelayModeAudioTranscription, ctx.MustGet("relay_mode"))
}

func TestGetModelRequest_VideoGenerationsPost(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/video/generations", "application/json", `{"model":"sora-2"}`)
	req, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.True(t, shouldSelect)
	require.Equal(t, "sora-2", req.Model)
	require.Equal(t, relayconstant.RelayModeVideoSubmit, ctx.MustGet("relay_mode"))
}

func TestGetModelRequest_VideoGenerationsGetFetch(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodGet, "/v1/video/generations/task-1", "", "")
	_, shouldSelect, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.False(t, shouldSelect)
	require.Equal(t, relayconstant.RelayModeVideoFetchByID, ctx.MustGet("relay_mode"))
}

func TestGetModelRequest_VideosPost(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/videos", "application/json", `{"model":"sora-2"}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "sora-2", req.Model)
}

func TestGetModelRequest_SunoSubmit(t *testing.T) {
	r := gin.New()
	var model string
	var shouldSelect bool
	r.POST("/suno/submit/:action", func(c *gin.Context) {
		req, ss, err := getModelRequest(c)
		require.NoError(t, err)
		model = req.Model
		shouldSelect = ss
		require.Equal(t, string(constant.TaskPlatformSuno), c.GetString("platform"))
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/suno/submit/music", strReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, shouldSelect)
	require.NotEmpty(t, model)
}

func TestGetModelRequest_SunoFetchNoSelect(t *testing.T) {
	r := gin.New()
	var shouldSelect bool
	r.GET("/suno/fetch/:id", func(c *gin.Context) {
		_, ss, err := getModelRequest(c)
		require.NoError(t, err)
		shouldSelect = ss
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/suno/fetch/abc", nil))
	require.False(t, shouldSelect)
}

func TestGetModelRequest_ResponsesCompactSuffix(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/responses/compact", "application/json", `{"model":"gpt-4o"}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Contains(t, req.Model, "gpt-4o") // compact suffix appended
}

func TestGetModelRequest_PlaygroundChat(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/pg/chat/completions", "application/json", `{"model":"gpt-4o","group":"vip"}`)
	req, _, err := getModelRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", req.Model)
	require.Equal(t, "vip", req.Group)
	require.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyTokenGroup))
}

// getModelFromRequest: form-encoded body path.
func TestGetModelFromRequest_FormEncoded(t *testing.T) {
	ctx := prepModelReqCtx(t, http.MethodPost, "/v1/images/edits", "application/x-www-form-urlencoded", "model=gpt-image-1")
	req, err := getModelFromRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-1", req.Model)
}

// ---------------------------------------------------------------------------
// Distribute: playground group override
// ---------------------------------------------------------------------------

func TestDistribute_PlaygroundGroupDenied(t *testing.T) {
	requireDB(t)
	r := gin.New()
	r.POST("/pg/chat/completions",
		func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		},
		Distribute(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	// request a group the user cannot use -> forbidden
	body := `{"model":"gpt-4o","group":"` + uniq("forbidden") + `"}`
	req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// SetupContextForSelectedChannel: non-Azure switch cases
// ---------------------------------------------------------------------------

func TestSetupContextForSelectedChannel_GeminiApiVersion(t *testing.T) {
	ctx, _ := newCtx(http.MethodPost, "/v1beta/models/gemini:generateContent", "")
	ch := &model.Channel{Id: 1, Type: constant.ChannelTypeGemini, Name: "g", Key: "k", Other: "v1beta"}
	require.Nil(t, SetupContextForSelectedChannel(ctx, ch, "gemini-2.0-flash"))
	require.Equal(t, "v1beta", ctx.GetString("api_version"))
}

func TestSetupContextForSelectedChannel_VertexRegion(t *testing.T) {
	ctx, _ := newCtx(http.MethodPost, "/v1/chat/completions", "")
	ch := &model.Channel{Id: 1, Type: constant.ChannelTypeVertexAi, Name: "v", Key: "k", Other: "us-central1"}
	require.Nil(t, SetupContextForSelectedChannel(ctx, ch, "gemini"))
	require.Equal(t, "us-central1", ctx.GetString("region"))
}

// ---------------------------------------------------------------------------
// detectLanguage: user setting takes priority
// ---------------------------------------------------------------------------

func TestDetectLanguage_UserSettingPriority(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{Language: "zh"})
	require.Equal(t, "zh", detectLanguage(ctx))
}

func TestGetTaskOriginModelName_LimitDisabled(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/v1/video/generations/task-1", "")
	// token model-limit not enabled -> early empty return.
	require.Equal(t, "", getTaskOriginModelName(ctx))
}

func TestGetTaskOriginModelName_NoTaskId(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/v1/video/generations/", "")
	ctx.Set(string(constant.ContextKeyTokenModelLimitEnabled), true)
	// limit enabled but no task_id param -> empty.
	require.Equal(t, "", getTaskOriginModelName(ctx))
}
