package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func ctxWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// ---------------------------------------------------------------------------
// model_meta.go — model metadata CRUD.
// ---------------------------------------------------------------------------

func mkModelMeta(t *testing.T) *model.Model {
	t.Helper()
	requireDB(t)
	m := &model.Model{ModelName: uniq("mdl"), NameRule: model.NameRuleExact, Status: 1}
	require.NoError(t, m.Insert())
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.Model{}, m.Id)
		}
	})
	return m
}

func TestGetAllModelsMeta_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/models_meta?p=1&page_size=10", nil)
	asAdmin(ctx, 1)
	GetAllModelsMeta(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestSearchModelsMeta_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/models_meta/search?keyword=gpt", nil)
	asAdmin(ctx, 1)
	SearchModelsMeta(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestGetModelMeta_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/models_meta/x", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	GetModelMeta(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetModelMeta_NotFound(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/models_meta/999999991", nil)
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	GetModelMeta(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetModelMeta_OK(t *testing.T) {
	requireDB(t)
	m := mkModelMeta(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/models_meta/"+strconv.Itoa(m.Id), nil)
	idParam(ctx, "id", strconv.Itoa(m.Id))
	asAdmin(ctx, 1)
	GetModelMeta(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestCreateModelMeta_EmptyName(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/models_meta", model.Model{})
	asAdmin(ctx, 1)
	CreateModelMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "模型名称")
}

func TestCreateModelMeta_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/models_meta", "{bad")
	asAdmin(ctx, 1)
	CreateModelMeta(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestCreateModelMeta_Success(t *testing.T) {
	requireDB(t)
	name := uniq("mdl")
	ctx, rec := newCtx(t, http.MethodPost, "/api/models_meta", model.Model{ModelName: name, NameRule: model.NameRuleExact})
	asAdmin(ctx, 1)
	CreateModelMeta(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var created model.Model
	require.NoError(t, unmarshalData(resp.Data, &created))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.Model{}, created.Id)
		}
	})
}

func TestCreateModelMeta_Duplicate(t *testing.T) {
	requireDB(t)
	m := mkModelMeta(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/models_meta", model.Model{ModelName: m.ModelName, NameRule: model.NameRuleExact})
	asAdmin(ctx, 1)
	CreateModelMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "已存在")
}

func TestUpdateModelMeta_MissingId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPut, "/api/models_meta", model.Model{ModelName: "x"})
	asAdmin(ctx, 1)
	UpdateModelMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "缺少模型 ID")
}

func TestUpdateModelMeta_StatusOnly(t *testing.T) {
	requireDB(t)
	m := mkModelMeta(t)
	ctx, rec := newCtx(t, http.MethodPut, "/api/models_meta?status_only=true", model.Model{Id: m.Id, Status: 2})
	asAdmin(ctx, 1)
	UpdateModelMeta(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestDeleteModelMeta_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodDelete, "/api/models_meta/x", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	DeleteModelMeta(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestDeleteModelMeta_Success(t *testing.T) {
	requireDB(t)
	m := mkModelMeta(t)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/models_meta/"+strconv.Itoa(m.Id), nil)
	idParam(ctx, "id", strconv.Itoa(m.Id))
	asAdmin(ctx, 1)
	DeleteModelMeta(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestEnrichModels_Empty(t *testing.T) {
	// nil/empty slice returns quickly with no panic.
	enrichModels(nil)
	enrichModels([]*model.Model{})
}

// NOTE: console_migrate.go :: MigrateConsoleSetting is intentionally NOT tested.
// Two reasons documented during batch 4:
//   1. It calls model.InitOptionMap() which reloads ALL in-memory options from
//      the DB, clobbering process-global state (e.g. seeded group ratios) and
//      polluting unrelated tests such as TestGetUserGroups in the shared binary.
//   2. Its final `DELETE FROM options WHERE key IN (...)` uses an unquoted
//      reserved word `key`; on MySQL/PostgreSQL this raises a syntax error
//      (Error 1064) that the handler swallows (return value unchecked). See the
//      batch-4 report / docs for the bug note. Should use commonKeyCol (Rule 2).

// ---------------------------------------------------------------------------
// codex_usage.go — channel validation paths (no upstream network needed).
// ---------------------------------------------------------------------------

func TestGetCodexChannelUsage_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/x/codex/usage", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	GetCodexChannelUsage(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetCodexChannelUsage_NotFound(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/999999991/codex/usage", nil)
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	GetCodexChannelUsage(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetCodexChannelUsage_WrongType(t *testing.T) {
	requireDB(t)
	// Default channel type is 1 (OpenAI), not Codex.
	ch := mkChannel(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/"+strconv.Itoa(ch.Id)+"/codex/usage", nil)
	idParam(ctx, "id", strconv.Itoa(ch.Id))
	asAdmin(ctx, 1)
	GetCodexChannelUsage(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "Codex")
}

func TestResetCodexChannelUsage_WrongType(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/"+strconv.Itoa(ch.Id)+"/codex/reset", nil)
	idParam(ctx, "id", strconv.Itoa(ch.Id))
	asAdmin(ctx, 1)
	ResetCodexChannelUsage(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetCodexChannelRateLimitResetCredits_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/x/codex/reset-credits", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	GetCodexChannelRateLimitResetCredits(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

// ---------------------------------------------------------------------------
// uptime_kuma.go — status fetch + helpers (upstream mocked via httptest).
// ---------------------------------------------------------------------------

func TestGetUptimeKumaStatus_NoGroups(t *testing.T) {
	// No groups configured -> empty data, success.
	ctx, rec := newCtx(t, http.MethodGet, "/api/uptime/status", nil)
	GetUptimeKumaStatus(ctx)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, decodeInto(rec, &body))
	require.Equal(t, true, body["success"])
}

func TestGetAndDecode_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var dest struct {
		OK bool `json:"ok"`
	}
	ctx, cancel := ctxWithTimeout(2 * time.Second)
	defer cancel()
	err := getAndDecode(ctx, srv.Client(), srv.URL, &dest)
	require.NoError(t, err)
	require.True(t, dest.OK)
}

func TestGetAndDecode_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	var dest map[string]any
	ctx, cancel := ctxWithTimeout(2 * time.Second)
	defer cancel()
	err := getAndDecode(ctx, srv.Client(), srv.URL, &dest)
	require.Error(t, err)
}

func TestFetchGroupData_EmptyConfig(t *testing.T) {
	ctx, cancel := ctxWithTimeout(2 * time.Second)
	defer cancel()
	res := fetchGroupData(ctx, &http.Client{}, map[string]interface{}{"categoryName": "c"})
	require.Equal(t, "c", res.CategoryName)
	require.Empty(t, res.Monitors)
}

func TestFetchGroupData_MockedUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if len(r.URL.Path) >= len(apiHeartbeatPath) && r.URL.Path[:len(apiHeartbeatPath)] == apiHeartbeatPath {
			_, _ = w.Write([]byte(`{"heartbeatList":{"1":[{"status":1}]},"uptimeList":{"1_24":0.99}}`))
			return
		}
		_, _ = w.Write([]byte(`{"publicGroupList":[{"id":1,"name":"g","monitorList":[{"id":1,"name":"m"}]}]}`))
	}))
	defer srv.Close()
	ctx, cancel := ctxWithTimeout(3 * time.Second)
	defer cancel()
	res := fetchGroupData(ctx, srv.Client(), map[string]interface{}{
		"url": srv.URL, "slug": "s", "categoryName": "c",
	})
	require.Len(t, res.Monitors, 1)
	require.Equal(t, "m", res.Monitors[0].Name)
	require.Equal(t, 1, res.Monitors[0].Status)
	require.InDelta(t, 0.99, res.Monitors[0].Uptime, 0.001)
}

// ---------------------------------------------------------------------------
// system_task_handlers.go — handler metadata (Type / Interval / NewPayload).
// ---------------------------------------------------------------------------

func TestSystemTaskHandlers_Metadata(t *testing.T) {
	require.Equal(t, model.SystemTaskTypeChannelTest, channelTestHandler{}.Type())
	require.Equal(t, model.SystemTaskTypeModelUpdate, modelUpdateHandler{}.Type())
	require.Equal(t, model.SystemTaskTypeMidjourneyPoll, midjourneyPollHandler{}.Type())
	require.Equal(t, model.SystemTaskTypeAsyncTaskPoll, asyncTaskPollHandler{}.Type())

	require.Greater(t, int64(channelTestHandler{}.Interval()), int64(0))
	require.Greater(t, int64(modelUpdateHandler{}.Interval()), int64(0))
	require.Equal(t, 15*time.Second, midjourneyPollHandler{}.Interval())
	require.Equal(t, 15*time.Second, asyncTaskPollHandler{}.Interval())

	require.Nil(t, channelTestHandler{}.NewPayload())
	require.Nil(t, modelUpdateHandler{}.NewPayload())
	require.Nil(t, midjourneyPollHandler{}.NewPayload())
	require.Nil(t, asyncTaskPollHandler{}.NewPayload())

	// Enabled() must not panic; result depends on settings.
	_ = channelTestHandler{}.Enabled()
	_ = modelUpdateHandler{}.Enabled()
	_ = midjourneyPollHandler{}.Enabled()
	_ = asyncTaskPollHandler{}.Enabled()
}

func TestRegisterScheduledSystemTasks_NoPanic(t *testing.T) {
	require.NotPanics(t, func() { RegisterScheduledSystemTasks() })
}

// ---------------------------------------------------------------------------
// deployment.go — io.net gating (disabled by default) + settings read.
// ---------------------------------------------------------------------------

func withIoNetDisabled(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	prevEnabled, hadEnabled := common.OptionMap["model_deployment.ionet.enabled"]
	prevKey, hadKey := common.OptionMap["model_deployment.ionet.api_key"]
	common.OptionMap["model_deployment.ionet.enabled"] = "false"
	common.OptionMap["model_deployment.ionet.api_key"] = ""
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadEnabled {
			common.OptionMap["model_deployment.ionet.enabled"] = prevEnabled
		} else {
			delete(common.OptionMap, "model_deployment.ionet.enabled")
		}
		if hadKey {
			common.OptionMap["model_deployment.ionet.api_key"] = prevKey
		} else {
			delete(common.OptionMap, "model_deployment.ionet.api_key")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func TestGetModelDeploymentSettings_OK(t *testing.T) {
	withIoNetDisabled(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/deployment/settings", nil)
	asAdmin(ctx, 1)
	GetModelDeploymentSettings(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)
	var data map[string]any
	require.NoError(t, unmarshalData(resp.Data, &data))
	require.Equal(t, false, data["enabled"])
}

func TestDeploymentHandlers_IoNetDisabled(t *testing.T) {
	withIoNetDisabled(t)
	handlers := map[string]gin.HandlerFunc{
		"GetAllDeployments":            GetAllDeployments,
		"SearchDeployments":            SearchDeployments,
		"GetHardwareTypes":             GetHardwareTypes,
		"GetLocations":                 GetLocations,
		"GetAvailableReplicas":         GetAvailableReplicas,
		"CheckClusterNameAvailability": CheckClusterNameAvailability,
		"ListDeploymentContainers":     ListDeploymentContainers,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, "/x", nil)
			idParam(ctx, "id", "dep1")
			asAdmin(ctx, 1)
			h(ctx)
			resp := decodeResp(t, rec)
			require.False(t, resp.Success)
			require.Contains(t, resp.Message, "not enabled")
		})
	}
}

func TestTestIoNetConnection_ApiKeyRequired(t *testing.T) {
	withIoNetDisabled(t)
	// Empty body + no stored key -> "api_key is required".
	ctx, rec := newCtx(t, http.MethodPost, "/x", nil)
	asAdmin(ctx, 1)
	TestIoNetConnection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "api_key is required")
}

func TestTestIoNetConnection_InvalidPayload(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/x", "{bad")
	asAdmin(ctx, 1)
	TestIoNetConnection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid request payload")
}

func TestGetDeployment_MissingId_IoNetDisabled(t *testing.T) {
	// getIoEnterpriseClient runs before requireDeploymentID, so with io.net
	// disabled the response is the not-enabled error.
	withIoNetDisabled(t)
	ctx, rec := newCtx(t, http.MethodGet, "/x", nil)
	idParam(ctx, "id", "")
	asAdmin(ctx, 1)
	GetDeployment(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

// ---------------------------------------------------------------------------
// ratio_sync.go — upstream ratio fetch (no real network) + syncable channels.
// ---------------------------------------------------------------------------

func TestFetchUpstreamRatios_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/x", "{bad")
	asAdmin(ctx, 1)
	FetchUpstreamRatios(ctx)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFetchUpstreamRatios_NoUpstreams(t *testing.T) {
	// Empty request -> "无有效上游渠道", no network.
	ctx, rec := newCtx(t, http.MethodPost, "/x", map[string]any{})
	asAdmin(ctx, 1)
	FetchUpstreamRatios(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestGetSyncableChannels_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/ratio/syncable", nil)
	asAdmin(ctx, 1)
	GetSyncableChannels(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	// Always includes the two built-in presets.
	var chans []map[string]any
	require.NoError(t, unmarshalData(resp.Data, &chans))
	require.GreaterOrEqual(t, len(chans), 2)
}
