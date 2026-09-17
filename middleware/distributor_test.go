package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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
		Id:    777,
		Type:  constant.ChannelTypeAzure,
		Name:  "azure-chan",
		Key:   "secret-key",
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

// pinTokenChannel mirrors what token auth does for "sk-xxx-<channelId>" admin
// tokens: it adds a single-attempt token pin to the request's channel constraints.
func pinTokenChannel(c *gin.Context, channelID int) {
	service.GetChannelConstraints(c).AddPin(taskdto.ChannelPin{
		ChannelId: channelID,
		Source:    taskdto.PinSourceToken,
		Rank:      taskdto.PinRankToken,
		RetryMode: taskdto.PinRetrySingleAttempt,
	})
}

func TestDistribute_SpecificChannelValid(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	ran := false
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { pinTokenChannel(c, ch.Id) },
		Distribute(),
		func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, ran)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDistribute_SpecificChannelNotFound(t *testing.T) {
	requireDB(t)
	r := gin.New()
	r.POST("/v1/chat/completions",
		func(c *gin.Context) { pinTokenChannel(c, -987654) },
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
		func(c *gin.Context) { pinTokenChannel(c, ch.Id) },
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

func TestChannelMatchesExpectedTaskPluginUsesGenericChannelSetting(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "generic-alpha"})

	assert.True(t, channelMatchesExpectedTaskPlugin(nil, channel, "generic-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, channel, "generic-beta"))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, channel, ""))
}

func TestChannelMatchesExpectedTaskPluginUsesPinnedLegacyIndex(t *testing.T) {
	registry := jsplugin.NewRegistry()
	alpha, err := registry.Register(distributorTaskPluginSource("legacy-alpha", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)
	pinnedGeneration := registry.Generation()

	require.NoError(t, registry.Unregister("legacy-alpha"))
	_, err = registry.Register(distributorTaskPluginSource("legacy-beta", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{
		Generation: pinnedGeneration,
		Plugin:     alpha,
	})
	channel := &model.Channel{Type: constant.ChannelTypeKling}

	assert.True(t, channelMatchesExpectedTaskPlugin(c, channel, "legacy-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, channel, "legacy-beta"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "legacy-alpha"))
}

func TestChannelMatchesExpectedTaskPluginRejectsUnindexedLegacyChannel(t *testing.T) {
	registry := jsplugin.NewRegistry()
	plugin, err := registry.Register(distributorTaskPluginSource("legacy-alpha", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{
		Generation: registry.Generation(),
		Plugin:     plugin,
	})

	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "legacy-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: 0}, "legacy-alpha"))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, ""))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, &model.Channel{Type: constant.ChannelTypeKling}, "legacy-alpha"))

	c.Set("expected_task_plugin_key", "legacy-alpha")
	setupErr := SetupContextForSelectedChannel(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "task-model")
	require.NotNil(t, setupErr)
	assert.Contains(t, setupErr.Error(), "does not match")
}

func TestSharedEndpointRebindsToSelectedLegacyProvider(t *testing.T) {
	registry := jsplugin.NewRegistry()
	_, err := registry.Register(distributorEndpointPluginSource("gemini-shared", constant.ChannelTypeGemini), jsplugin.Options{})
	require.NoError(t, err)
	_, err = registry.Register(distributorEndpointPluginSource("vertex-shared", constant.ChannelTypeVertexAi), jsplugin.Options{})
	require.NoError(t, err)
	candidates := registry.Generation().LookupEndpointCandidates("POST", "/v1/responses", "task-model")
	require.Len(t, candidates, 2)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: registry.Generation(), Plugin: candidates[0].Plugin})
	c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{
		Generation: registry.Generation(),
		Plugin:     candidates[0].Plugin,
		Protocol:   candidates[0].Protocol,
		Operation:  candidates[0].Operation,
		Model:      "task-model",
		Candidates: candidates,
	})
	c.Set("expected_task_plugin_key", candidates[0].Plugin.Meta.Key)

	geminiChannel := &model.Channel{Id: 1, Type: constant.ChannelTypeGemini}
	vertexChannel := &model.Channel{Id: 2, Type: constant.ChannelTypeVertexAi}
	assert.True(t, channelMatchesExpectedTaskPlugin(c, geminiChannel, candidates[0].Plugin.Meta.Key))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, vertexChannel, candidates[0].Plugin.Meta.Key))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeKling}, candidates[0].Plugin.Meta.Key))

	require.Nil(t, SetupContextForSelectedChannel(c, vertexChannel, "task-model"))
	pinnedValue, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	require.True(t, exists)
	pinned, ok := pinnedValue.(jsplugin.PinnedEndpoint)
	require.True(t, ok)
	assert.Equal(t, "vertex-shared", pinned.Plugin.Meta.Key)
	assert.Equal(t, "vertex-shared", c.GetString("expected_task_plugin_key"))
	assert.Equal(t, "vertex-shared", c.GetString("task_plugin_key"))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, geminiChannel, "vertex-shared"), "a retry may select another declared provider")
}

func distributorTaskPluginSource(key string, channelType int) string {
	return fmt.Sprintf(`
export const meta = {
  apiVersion: 1,
  key: %q,
  name: %q,
  version: "1.0.0",
  author: {name: "Test"},
  channelTypes: [%d],
  models: ["task-model"],
  fetchMode: "per_task",
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {taskId: "task"}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
`, key, key, channelType)
}

func distributorEndpointPluginSource(key string, channelType int) string {
	return fmt.Sprintf(`
export const meta = {
  apiVersion: 1,
  key: %q,
  name: %q,
  version: "1.0.0",
  author: {name: "Test"},
  channelTypes: [%d],
  models: ["task-model"],
  fetchMode: "per_task",
  protocols: [{name: "openai_responses", supports: ["stream", "sync", "background"]}],
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {taskId: "task"}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export const protocols = {openai_responses: {
  decodeRequest: function(ctx) { return {kind: "submit", model: "task-model", requestBody: ctx.body.value}; },
  renderEvents: function() { return {events: [], state: null, done: false}; },
  renderFinal: function() { return {output: []}; },
}};
`, key, key, channelType)
}

func TestTokenModelLimitAllowsLegacyAliasAndModifierVariant(t *testing.T) {
	aliasOnly := map[string]bool{"claude-3-7-sonnet-thinking": true}
	assert.True(t, tokenModelLimitAllows(aliasOnly, "claude-3-7-sonnet-thinking"))
	assert.False(t, tokenModelLimitAllows(aliasOnly, "claude-3-7-sonnet"))

	baseOnly := map[string]bool{"claude-3-7-sonnet": true}
	assert.True(t, tokenModelLimitAllows(baseOnly, "claude-3-7-sonnet@thinking:on"))
	assert.True(t, tokenModelLimitAllows(baseOnly, "claude-3-7-sonnet-thinking"))

	wildcard := map[string]bool{"gemini-2.5-flash-thinking-*": true}
	assert.True(t, tokenModelLimitAllows(wildcard, "gemini-2.5-flash-thinking-8192"))
}

func TestTokenModelLimitAllowsExemptAtNameByFullName(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := append([]string(nil), settings.ThinkingModelBlacklist...)
	t.Cleanup(func() { settings.ThinkingModelBlacklist = original })
	settings.ThinkingModelBlacklist = append(original, "re:.*@sha256:.*")

	fullOnly := map[string]bool{"opaque@sha256:deadbeef": true}
	assert.True(t, tokenModelLimitAllows(fullOnly, "opaque@sha256:deadbeef"))

	baseOnly := map[string]bool{"opaque": true}
	assert.False(t, tokenModelLimitAllows(baseOnly, "opaque@sha256:deadbeef"))
}

func TestNoAvailableChannelMessageNamesClaimingTaskPlugin(t *testing.T) {
	require.NoError(t, i18n.Init())
	registry := jsplugin.NewRegistry()
	plugin, err := registry.Register(distributorTaskPluginSource("claimer", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)

	pinned, _ := gin.CreateTestContext(nil)
	pinned.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	pinned.Request.Header.Set("Accept-Language", "en")
	pinned.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
	message := noAvailableChannelMessage(pinned, "default", "kling-v1")
	assert.Contains(t, message, `"claimer"`)
	assert.Contains(t, message, "disable or override")
	assert.Contains(t, message, "kling-v1")

	plain, _ := gin.CreateTestContext(nil)
	plain.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	plain.Request.Header.Set("Accept-Language", "en")
	generic := noAvailableChannelMessage(plain, "default", "gpt-4o")
	assert.NotContains(t, generic, "task plugin")
	assert.Contains(t, generic, "gpt-4o")
}

func TestSharedEndpointRebindsToSelectedType61Plugin(t *testing.T) {
	registry := jsplugin.NewRegistry()
	for _, key := range []string{"alpha", "beta"} {
		source := strings.Replace(distributorEndpointPluginSource(key, 0), "channelTypes: [0],", "", 1)
		_, err := registry.Register(source, jsplugin.Options{})
		require.NoError(t, err)
	}
	generation := registry.Generation()
	candidates := generation.LookupEndpointCandidates("POST", "/v1/responses", "task-model")
	require.Len(t, candidates, 2)
	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: candidates[0].Plugin})
	c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Generation: generation, Plugin: candidates[0].Plugin, Protocol: candidates[0].Protocol, Operation: candidates[0].Operation, Model: "task-model", Candidates: candidates})
	c.Set("expected_task_plugin_key", "alpha")
	channel := &model.Channel{Id: 2, Type: constant.ChannelTypeTaskPlugin}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "unrelated"})
	assert.False(t, channelMatchesExpectedTaskPlugin(c, channel, "alpha"))
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "beta"})
	require.Nil(t, SetupContextForSelectedChannel(c, channel, "task-model"))
	assert.Equal(t, "beta", c.GetString("task_plugin_key"))
	assert.Equal(t, "beta", c.GetString("expected_task_plugin_key"))
	assert.Equal(t, "beta", c.MustGet(jsplugin.ContextKeyPinnedEndpoint).(jsplugin.PinnedEndpoint).Plugin.Meta.Key)
	require.NoError(t, i18n.Init())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	assert.Contains(t, noAvailableChannelMessage(c, "default", "task-model"), "alpha, beta")
}
