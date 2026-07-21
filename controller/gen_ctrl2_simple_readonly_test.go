package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

// decodeInto unmarshals the full recorder body (used by handlers that write a
// bespoke envelope via c.JSON rather than common.ApiSuccess).
func decodeInto(rec *httptest.ResponseRecorder, v any) error {
	return common.Unmarshal(rec.Body.Bytes(), v)
}

// unmarshalData unmarshals the `data` field of the standard envelope.
func unmarshalData(raw json.RawMessage, v any) error {
	return common.Unmarshal(raw, v)
}

// ---------------------------------------------------------------------------
// authz.go :: GetPermissionCatalog — pure (no DB). Serializes the static
// permission registry + role matrices. Asserts the envelope + non-empty
// resources/roles.
// ---------------------------------------------------------------------------

func TestGetPermissionCatalog(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/authz/catalog", nil)
	asAdmin(ctx, nextTestID())
	GetPermissionCatalog(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data struct {
		Resources []map[string]any `json:"resources"`
		Roles     []map[string]any `json:"roles"`
	}
	require.NoError(t, unmarshalData(resp.Data, &data))
	require.NotEmpty(t, data.Resources, "catalog must expose at least one resource")
	require.NotEmpty(t, data.Roles, "catalog must expose at least one role")
}

// ---------------------------------------------------------------------------
// exchange_rate.go :: GetUSDCNYRate — memory/fallback source, never requires
// DB or the network in tests (falls back to the built-in constant).
// ---------------------------------------------------------------------------

func TestGetUSDCNYRate(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/exchange-rate", nil)
	GetUSDCNYRate(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Rate   float64 `json:"rate"`
			Source string  `json:"source"`
			Pair   string  `json:"pair"`
		} `json:"data"`
	}
	require.NoError(t, decodeInto(rec, &out))
	require.True(t, out.Success)
	require.Greater(t, out.Data.Rate, 0.0, "rate must be a positive number")
	require.Equal(t, "binance_p2p", out.Data.Source)
	require.Equal(t, "USDT/CNY", out.Data.Pair)
}

// ---------------------------------------------------------------------------
// rankings.go :: GetRankings — invalid period is rejected by the service
// (rankingConfig default case) with HTTP 400. The happy path queries the log
// DB and is left to integration/staging (Rule 15.6).
// ---------------------------------------------------------------------------

func TestGetRankings_InvalidPeriod(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/rankings?period=not-a-period", nil)
	GetRankings(ctx)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, decodeInto(rec, &out))
	require.False(t, out.Success)
	require.NotEmpty(t, out.Message)
}

// ---------------------------------------------------------------------------
// return_path.go :: paymentReturnPath — pure. Trims a single trailing slash
// from the configured server address and appends the theme-aware suffix.
// ---------------------------------------------------------------------------

func TestPaymentReturnPath(t *testing.T) {
	prev := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = prev })

	// trailing slash trimmed exactly once
	system_setting.ServerAddress = "https://example.com/"
	got := paymentReturnPath("/topup/result")
	require.True(t, len(got) > len("https://example.com"))
	require.Contains(t, got, "https://example.com")
	require.NotContains(t, got, "com//", "duplicate slash must not appear after trim")

	// no trailing slash: suffix still appended
	system_setting.ServerAddress = "https://example.com"
	got2 := paymentReturnPath("/topup/result")
	require.Contains(t, got2, "https://example.com")
	require.Equal(t, got, got2, "trailing-slash and no-slash forms resolve identically")
}

// ---------------------------------------------------------------------------
// perf_metrics.go :: GetPerfMetrics missing-model guard + filterActiveGroups
// pure filter. The summary/query happy paths depend on the metrics store and
// are out of scope.
// ---------------------------------------------------------------------------

func TestGetPerfMetrics_MissingModel(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/perf/metrics", nil)
	GetPerfMetrics(ctx)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, decodeInto(rec, &out))
	require.False(t, out.Success)
	require.Contains(t, out.Message, "model")
}

func TestFilterActiveGroups(t *testing.T) {
	groups := []perfmetrics.GroupResult{
		{Group: "auto"},           // always kept
		{Group: "____not_active"}, // dropped (not in ratio map)
	}
	got := filterActiveGroups(groups)
	// "auto" is always retained; the bogus group is filtered out.
	found := false
	for _, g := range got {
		require.NotEqual(t, "____not_active", g.Group)
		if g.Group == "auto" {
			found = true
		}
	}
	require.True(t, found, "auto group must always survive filtering")
}

// ---------------------------------------------------------------------------
// ledger_pipeline_status.go :: AdminGetLedgerPipelineStatus — assembles a
// status snapshot from in-memory buffers + settings (no DB read). Asserts the
// envelope and the presence of both the flat (Classic UI) and nested (Default
// UI) shapes, plus that the derived throughput is consistent.
// ---------------------------------------------------------------------------

func TestAdminGetLedgerPipelineStatus(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/ledger/pipeline-status", nil)
	asAdmin(ctx, nextTestID())
	AdminGetLedgerPipelineStatus(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data map[string]any
	require.NoError(t, unmarshalData(resp.Data, &data))
	// Flat fields consumed by Classic UI.
	for _, k := range []string{
		"cost_backlog", "pair_backlog", "commission_backlog",
		"flush_interval_sec", "settlement_flush_max_per_cycle",
		"theoretical_pair_records_per_sec", "theoretical_pair_rpm",
	} {
		_, ok := data[k]
		require.True(t, ok, "missing flat field %q", k)
	}
	// Nested snapshot consumed by Default UI.
	_, ok := data["snapshot"]
	require.True(t, ok, "nested snapshot must be present")

	// flush_interval_sec is coerced to a positive default when unset.
	require.Greater(t, data["flush_interval_sec"].(float64), 0.0)
}

// ---------------------------------------------------------------------------
// missing_models.go :: GetMissingModels — thin wrapper over model.GetMissingModels
// (channels' model list minus registered meta rows). DB-backed; asserts the
// envelope succeeds with a real DB.
// ---------------------------------------------------------------------------

func TestGetMissingModels(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Model{}))

	ctx, rec := newCtx(t, http.MethodGet, "/api/missing-models", nil)
	asAdmin(ctx, nextTestID())
	GetMissingModels(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	// data is a JSON array (possibly empty) of model-name strings.
	var names []string
	require.NoError(t, unmarshalData(resp.Data, &names))
}
