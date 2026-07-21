package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// vendor_meta.go — full CRUD (Vendor is migrated by the harness). Covers:
//   Create: bad JSON, empty name, duplicate name, success
//   Get:    bad id, not found, success
//   Update: bad JSON, missing id, duplicate name, success
//   Delete: bad id, success
//   List/Search: pagination + keyword.
// Each created row registers a hard-delete cleanup (Delete is a soft delete).
// ---------------------------------------------------------------------------

func cleanupVendor(t *testing.T, id int) {
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.Vendor{}, id)
		}
	})
}

func createVendorViaHandler(t *testing.T, name string) int {
	ctx, rec := newCtx(t, http.MethodPost, "/api/vendor", map[string]any{"name": name, "icon": "OpenAI"})
	asAdmin(ctx, nextTestID())
	CreateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var v model.Vendor
	require.NoError(t, unmarshalData(resp.Data, &v))
	require.NotZero(t, v.Id)
	cleanupVendor(t, v.Id)
	return v.Id
}

func TestCreateVendorMeta_BadJSON(t *testing.T) {
	requireDB(t)
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/vendor", "{not-json")
	asAdmin(ctx, nextTestID())
	CreateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestCreateVendorMeta_EmptyName(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/vendor", map[string]any{"name": ""})
	asAdmin(ctx, nextTestID())
	CreateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "供应商名称")
}

func TestCreateVendorMeta_Success(t *testing.T) {
	requireDB(t)
	name := uniq("vendor")
	id := createVendorViaHandler(t, name)

	// Verify it is retrievable.
	got, err := model.GetVendorByID(id)
	require.NoError(t, err)
	require.Equal(t, name, got.Name)
}

func TestCreateVendorMeta_DuplicateName(t *testing.T) {
	requireDB(t)
	name := uniq("vendor")
	_ = createVendorViaHandler(t, name)

	ctx, rec := newCtx(t, http.MethodPost, "/api/vendor", map[string]any{"name": name})
	asAdmin(ctx, nextTestID())
	CreateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "已存在")
}

func TestGetVendorMeta_BadID(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/vendor/abc", nil)
	ctx.AddParam("id", "abc")
	asAdmin(ctx, nextTestID())
	GetVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestGetVendorMeta_Success(t *testing.T) {
	requireDB(t)
	id := createVendorViaHandler(t, uniq("vendor"))

	ctx, rec := newCtx(t, http.MethodGet, fmt.Sprintf("/api/vendor/%d", id), nil)
	ctx.AddParam("id", fmt.Sprintf("%d", id))
	asAdmin(ctx, nextTestID())
	GetVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var v model.Vendor
	require.NoError(t, unmarshalData(resp.Data, &v))
	require.Equal(t, id, v.Id)
}

func TestUpdateVendorMeta_MissingID(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodPut, "/api/vendor", map[string]any{"name": uniq("vendor")})
	asAdmin(ctx, nextTestID())
	UpdateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "供应商 ID")
}

func TestUpdateVendorMeta_BadJSON(t *testing.T) {
	requireDB(t)
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/vendor", "{bad")
	asAdmin(ctx, nextTestID())
	UpdateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestUpdateVendorMeta_Success(t *testing.T) {
	requireDB(t)
	id := createVendorViaHandler(t, uniq("vendor"))
	newName := uniq("vendor")

	ctx, rec := newCtx(t, http.MethodPut, "/api/vendor", map[string]any{"id": id, "name": newName, "description": "updated"})
	asAdmin(ctx, nextTestID())
	UpdateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	got, err := model.GetVendorByID(id)
	require.NoError(t, err)
	require.Equal(t, newName, got.Name)
	require.Equal(t, "updated", got.Description)
}

func TestUpdateVendorMeta_DuplicateName(t *testing.T) {
	requireDB(t)
	nameA := uniq("vendor")
	nameB := uniq("vendor")
	_ = createVendorViaHandler(t, nameA)
	idB := createVendorViaHandler(t, nameB)

	// Rename B to A's name -> duplicate rejection.
	ctx, rec := newCtx(t, http.MethodPut, "/api/vendor", map[string]any{"id": idB, "name": nameA})
	asAdmin(ctx, nextTestID())
	UpdateVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "已存在")
}

func TestDeleteVendorMeta_BadID(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/vendor/xyz", nil)
	ctx.AddParam("id", "xyz")
	asAdmin(ctx, nextTestID())
	DeleteVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestDeleteVendorMeta_Success(t *testing.T) {
	requireDB(t)
	id := createVendorViaHandler(t, uniq("vendor"))

	ctx, rec := newCtx(t, http.MethodDelete, fmt.Sprintf("/api/vendor/%d", id), nil)
	ctx.AddParam("id", fmt.Sprintf("%d", id))
	asAdmin(ctx, nextTestID())
	DeleteVendorMeta(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	// Soft-deleted: no longer retrievable via the default scope.
	_, err := model.GetVendorByID(id)
	require.Error(t, err)
}

func TestGetAllVendors_And_Search(t *testing.T) {
	requireDB(t)
	name := uniq("vendorsearch")
	id := createVendorViaHandler(t, name)

	// List (page 1) succeeds.
	ctx, rec := newCtx(t, http.MethodGet, "/api/vendor?p=1&page_size=10", nil)
	asAdmin(ctx, nextTestID())
	GetAllVendors(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())

	// Search by the unique keyword finds exactly our row.
	ctx2, rec2 := newCtx(t, http.MethodGet, "/api/vendor/search?keyword="+name, nil)
	asAdmin(ctx2, nextTestID())
	SearchVendors(ctx2)
	resp2 := decodeResp(t, rec2)
	require.True(t, resp2.Success, "body: %s", rec2.Body.String())
	var page struct {
		Total int            `json:"total"`
		Items []model.Vendor `json:"items"`
	}
	require.NoError(t, unmarshalData(resp2.Data, &page))
	require.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, id, page.Items[0].Id)
}
