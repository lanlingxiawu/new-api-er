package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// system_info.go — system instance registry. SystemInstance is not in the
// harness migration set, so tests migrate it first (idempotent).
// ---------------------------------------------------------------------------

func migrateSystemInstance(t *testing.T) {
	t.Helper()
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemInstance{}))
}

func TestDeleteStaleSystemInstance_EmptyNodeName(t *testing.T) {
	migrateSystemInstance(t)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/system/instance/", nil)
	ctx.AddParam("node_name", "   ")
	asAdmin(ctx, nextTestID())
	DeleteStaleSystemInstance(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "node name is required")
}

func TestDeleteStaleSystemInstance_NotStale(t *testing.T) {
	migrateSystemInstance(t)
	node := uniq("node")
	// Register a fresh instance (last_seen_at = now): not yet stale.
	require.NoError(t, model.UpsertSystemInstance(node, map[string]any{"v": 1}, common.GetTimestamp(), common.GetTimestamp()))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Where("node_name = ?", node).Delete(&model.SystemInstance{})
		}
	})

	ctx, rec := newCtx(t, http.MethodDelete, "/api/system/instance/"+node, nil)
	ctx.AddParam("node_name", node)
	asAdmin(ctx, nextTestID())
	DeleteStaleSystemInstance(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success, "fresh instance must not be deletable as stale")
	require.Contains(t, resp.Message, "not stale")
}

func TestListSystemInstances_Success(t *testing.T) {
	migrateSystemInstance(t)
	node := uniq("node")
	require.NoError(t, model.UpsertSystemInstance(node, map[string]any{"v": 1}, common.GetTimestamp(), common.GetTimestamp()))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Where("node_name = ?", node).Delete(&model.SystemInstance{})
		}
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/system/instances", nil)
	asAdmin(ctx, nextTestID())
	ListSystemInstances(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	var instances []model.SystemInstanceResponse
	require.NoError(t, unmarshalData(resp.Data, &instances))
	var found bool
	for _, inst := range instances {
		if inst.NodeName == node {
			found = true
			require.Equal(t, model.SystemInstanceStatusOnline, inst.Status)
		}
	}
	require.True(t, found, "seeded instance must appear in the listing")
}

func TestDeleteStaleSystemInstances_Success(t *testing.T) {
	migrateSystemInstance(t)
	node := uniq("node")
	// Seed a stale instance (last_seen well beyond the stale threshold).
	staleTs := common.GetTimestamp() - model.SystemInstanceStaleAfterSeconds - 100
	require.NoError(t, model.UpsertSystemInstance(node, nil, staleTs, staleTs))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Where("node_name = ?", node).Delete(&model.SystemInstance{})
		}
	})

	ctx, rec := newCtx(t, http.MethodDelete, "/api/system/instances/stale", nil)
	asAdmin(ctx, nextTestID())
	DeleteStaleSystemInstances(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	var data struct {
		DeletedCount int64 `json:"deleted_count"`
	}
	require.NoError(t, unmarshalData(resp.Data, &data))
	require.GreaterOrEqual(t, data.DeletedCount, int64(1))
}
