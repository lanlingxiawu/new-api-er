package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// prefill_group.go — full CRUD. PrefillGroup is not in the harness migration
// set, so each test migrates it (idempotent) before use. Covers create/update
// validation branches, duplicate-name rejection, and list filtering by type.
// ---------------------------------------------------------------------------

func migratePrefillGroup(t *testing.T) {
	t.Helper()
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PrefillGroup{}))
}

func cleanupPrefillGroup(t *testing.T, id int) {
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.PrefillGroup{}, id)
		}
	})
}

func createPrefillGroupViaHandler(t *testing.T, name, typ string) int {
	ctx, rec := newCtx(t, http.MethodPost, "/api/prefill_group", map[string]any{"name": name, "type": typ})
	asAdmin(ctx, nextTestID())
	CreatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var g model.PrefillGroup
	require.NoError(t, unmarshalData(resp.Data, &g))
	require.NotZero(t, g.Id)
	cleanupPrefillGroup(t, g.Id)
	return g.Id
}

func TestCreatePrefillGroup_BadJSON(t *testing.T) {
	migratePrefillGroup(t)
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/prefill_group", "{bad")
	asAdmin(ctx, nextTestID())
	CreatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestCreatePrefillGroup_MissingNameOrType(t *testing.T) {
	migratePrefillGroup(t)
	// missing type
	ctx, rec := newCtx(t, http.MethodPost, "/api/prefill_group", map[string]any{"name": uniq("pg")})
	asAdmin(ctx, nextTestID())
	CreatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "不能为空")

	// missing name
	ctx2, rec2 := newCtx(t, http.MethodPost, "/api/prefill_group", map[string]any{"type": "channel"})
	asAdmin(ctx2, nextTestID())
	CreatePrefillGroup(ctx2)
	resp2 := decodeResp(t, rec2)
	require.False(t, resp2.Success)
}

func TestCreatePrefillGroup_Success(t *testing.T) {
	migratePrefillGroup(t)
	name := uniq("pg")
	id := createPrefillGroupViaHandler(t, name, "channel")

	groups, err := model.GetAllPrefillGroups("channel")
	require.NoError(t, err)
	var found bool
	for _, g := range groups {
		if g.Id == id {
			found = true
			require.Equal(t, name, g.Name)
		}
	}
	require.True(t, found)
}

func TestCreatePrefillGroup_DuplicateName(t *testing.T) {
	migratePrefillGroup(t)
	name := uniq("pg")
	_ = createPrefillGroupViaHandler(t, name, "channel")

	ctx, rec := newCtx(t, http.MethodPost, "/api/prefill_group", map[string]any{"name": name, "type": "channel"})
	asAdmin(ctx, nextTestID())
	CreatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "已存在")
}

func TestUpdatePrefillGroup_MissingID(t *testing.T) {
	migratePrefillGroup(t)
	ctx, rec := newCtx(t, http.MethodPut, "/api/prefill_group", map[string]any{"name": uniq("pg"), "type": "channel"})
	asAdmin(ctx, nextTestID())
	UpdatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "组 ID")
}

func TestUpdatePrefillGroup_BadJSON(t *testing.T) {
	migratePrefillGroup(t)
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/prefill_group", "{bad")
	asAdmin(ctx, nextTestID())
	UpdatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestUpdatePrefillGroup_Success(t *testing.T) {
	migratePrefillGroup(t)
	id := createPrefillGroupViaHandler(t, uniq("pg"), "channel")
	newName := uniq("pg")

	ctx, rec := newCtx(t, http.MethodPut, "/api/prefill_group", map[string]any{"id": id, "name": newName, "type": "channel"})
	asAdmin(ctx, nextTestID())
	UpdatePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	groups, err := model.GetAllPrefillGroups("")
	require.NoError(t, err)
	for _, g := range groups {
		if g.Id == id {
			require.Equal(t, newName, g.Name)
		}
	}
}

func TestDeletePrefillGroup_BadID(t *testing.T) {
	migratePrefillGroup(t)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/prefill_group/nope", nil)
	ctx.AddParam("id", "nope")
	asAdmin(ctx, nextTestID())
	DeletePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestDeletePrefillGroup_Success(t *testing.T) {
	migratePrefillGroup(t)
	id := createPrefillGroupViaHandler(t, uniq("pg"), "channel")

	ctx, rec := newCtx(t, http.MethodDelete, fmt.Sprintf("/api/prefill_group/%d", id), nil)
	ctx.AddParam("id", fmt.Sprintf("%d", id))
	asAdmin(ctx, nextTestID())
	DeletePrefillGroup(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
}

func TestGetPrefillGroups_FilterByType(t *testing.T) {
	migratePrefillGroup(t)
	typ := uniq("pgt") // unique type value (fits the size:32 column)
	name := uniq("pg")
	id := createPrefillGroupViaHandler(t, name, typ)

	ctx, rec := newCtx(t, http.MethodGet, "/api/prefill_group?type="+typ, nil)
	asAdmin(ctx, nextTestID())
	GetPrefillGroups(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var groups []model.PrefillGroup
	require.NoError(t, unmarshalData(resp.Data, &groups))
	require.Len(t, groups, 1, "type filter must return exactly the one seeded row")
	require.Equal(t, id, groups[0].Id)
}
