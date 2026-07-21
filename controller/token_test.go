package controller

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokenItemsPage struct {
	Items []model.Token `json:"items"`
	Total int           `json:"total"`
}

func decodeTokenItems(t *testing.T, data []byte) tokenItemsPage {
	t.Helper()
	var page tokenItemsPage
	require.NoError(t, common.Unmarshal(data, &page))
	return page
}

// GetAllTokens: caller-scoped listing + key masking + no raw-key leak.
func TestGetAllTokens_ScopesToCallerAndMasksKey(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	other := mkUser(t, nil)
	tk := mkToken(t, owner.Id, func(tk *model.Token) { tk.Name = "list-token" })
	mkToken(t, other.Id, func(tk *model.Token) { tk.Name = "other-user-token" })

	ctx, rec := newCtx(t, http.MethodGet, "/api/token/?p=1&size=100", nil)
	asUser(ctx, owner.Id)
	GetAllTokens(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	page := decodeTokenItems(t, resp.Data)

	// only the owner's token is returned in this caller's scope
	var found *model.Token
	for i := range page.Items {
		if page.Items[i].Id == tk.Id {
			found = &page.Items[i]
		}
		assert.NotEqual(t, other.Id, page.Items[i].UserId, "listing leaked another user's token")
	}
	require.NotNil(t, found, "owner token not present in listing")
	assert.Equal(t, tk.GetMaskedKey(), found.Key)
	assert.NotContains(t, rec.Body.String(), tk.Key, "raw key leaked in list response")
}

// SearchTokens: keyword search masks keys and never leaks the raw key.
func TestSearchTokens_MasksKey(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	name := uniq("searchable")
	tk := mkToken(t, owner.Id, func(tk *model.Token) { tk.Name = name })

	ctx, rec := newCtx(t, http.MethodGet, "/api/token/search?keyword="+name+"&p=1&size=10", nil)
	asUser(ctx, owner.Id)
	SearchTokens(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	page := decodeTokenItems(t, resp.Data)
	require.Len(t, page.Items, 1)
	assert.Equal(t, tk.GetMaskedKey(), page.Items[0].Key)
	assert.NotContains(t, rec.Body.String(), tk.Key)
}

// GetToken: detail masks key.
func TestGetToken_MasksKey(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, nil)

	ctx, rec := newCtx(t, http.MethodGet, "/api/token/"+strconv.Itoa(tk.Id), nil)
	asUser(ctx, owner.Id)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tk.Id)}}
	GetToken(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var got model.Token
	require.NoError(t, common.Unmarshal(resp.Data, &got))
	assert.Equal(t, tk.GetMaskedKey(), got.Key)
	assert.NotContains(t, rec.Body.String(), tk.Key)
}

// GetToken: non-numeric id -> ApiError (success false).
func TestGetToken_InvalidIDReturnsError(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/token/abc", nil)
	asUser(ctx, owner.Id)
	ctx.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetToken(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// GetTokenKey: owner gets full key; a different user is denied.
func TestGetTokenKey_OwnershipEnforced(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	stranger := mkUser(t, nil)
	tk := mkToken(t, owner.Id, nil)

	// authorized
	okCtx, okRec := newCtx(t, http.MethodPost, "/api/token/"+strconv.Itoa(tk.Id)+"/key", nil)
	asUser(okCtx, owner.Id)
	okCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tk.Id)}}
	GetTokenKey(okCtx)
	okResp := decodeResp(t, okRec)
	require.True(t, okResp.Success, okResp.Message)
	var keyData struct {
		Key string `json:"key"`
	}
	require.NoError(t, common.Unmarshal(okResp.Data, &keyData))
	assert.Equal(t, tk.GetFullKey(), keyData.Key)

	// unauthorized: model.GetTokenByIds is scoped by userId, so stranger fails.
	denyCtx, denyRec := newCtx(t, http.MethodPost, "/api/token/"+strconv.Itoa(tk.Id)+"/key", nil)
	asUser(denyCtx, stranger.Id)
	denyCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tk.Id)}}
	GetTokenKey(denyCtx)
	denyResp := decodeResp(t, denyRec)
	assert.False(t, denyResp.Success)
	assert.NotContains(t, denyRec.Body.String(), tk.Key)
}

// GetTokenStatus: expired_time -1 (never) surfaces as expires_at 0.
func TestGetTokenStatus_NeverExpiresMapsToZero(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, func(tk *model.Token) {
		tk.ExpiredTime = -1
		tk.RemainQuota = 4200
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/token/status", nil)
	asUser(ctx, owner.Id)
	ctx.Set("token_id", tk.Id)
	GetTokenStatus(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Object         string `json:"object"`
		TotalGranted   int    `json:"total_granted"`
		TotalAvailable int    `json:"total_available"`
		ExpiresAt      int64  `json:"expires_at"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "credit_summary", body.Object)
	assert.Equal(t, 4200, body.TotalGranted)
	assert.Equal(t, 4200, body.TotalAvailable)
	assert.EqualValues(t, 0, body.ExpiresAt)
}

// AddToken: name length boundary (>50) is rejected.
func TestAddToken_NameTooLong(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	body := map[string]any{
		"name":            strings.Repeat("x", 51),
		"unlimited_quota": true,
		"expired_time":    -1,
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/token/", body)
	asUser(ctx, owner.Id)
	AddToken(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// AddToken: negative quota rejected when not unlimited.
func TestAddToken_NegativeQuotaRejected(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	body := map[string]any{
		"name":            "neg",
		"unlimited_quota": false,
		"remain_quota":    -1,
		"expired_time":    -1,
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/token/", body)
	asUser(ctx, owner.Id)
	AddToken(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// AddToken: happy path inserts a token owned by the caller with a generated key.
func TestAddToken_Success(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	name := uniq("added")
	body := map[string]any{
		"name":            name,
		"unlimited_quota": true,
		"expired_time":    -1,
		"group":           "default",
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/token/", body)
	asUser(ctx, owner.Id)
	AddToken(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created model.Token
	require.NoError(t, model.DB.Where("user_id = ? AND name = ?", owner.Id, name).First(&created).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(&model.Token{}, created.Id) })
	assert.NotEmpty(t, created.Key)
	assert.Equal(t, owner.Id, created.UserId)
}

// AddToken: malformed JSON body -> ApiError.
func TestAddToken_MalformedBody(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/token/", `{"name":`)
	asUser(ctx, owner.Id)
	AddToken(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// UpdateToken: full field update masks key in response and persists changes.
func TestUpdateToken_UpdatesAndMasksKey(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, func(tk *model.Token) { tk.Name = "before" })

	body := map[string]any{
		"id":                   tk.Id,
		"name":                 "after",
		"expired_time":         -1,
		"remain_quota":         100,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                "default",
		"cross_group_retry":    false,
	}
	ctx, rec := newCtx(t, http.MethodPut, "/api/token/", body)
	asUser(ctx, owner.Id)
	UpdateToken(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var got model.Token
	require.NoError(t, common.Unmarshal(resp.Data, &got))
	assert.Equal(t, "after", got.Name)
	assert.Equal(t, tk.GetMaskedKey(), got.Key)
	assert.NotContains(t, rec.Body.String(), tk.Key)

	var reloaded model.Token
	require.NoError(t, model.DB.First(&reloaded, tk.Id).Error)
	assert.Equal(t, "after", reloaded.Name)
}

// UpdateToken: status_only updates status without touching other fields.
func TestUpdateToken_StatusOnly(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, func(tk *model.Token) {
		tk.Name = "keep"
		tk.Status = common.TokenStatusEnabled
	})

	body := map[string]any{
		"id":     tk.Id,
		"status": common.TokenStatusDisabled,
		"name":   "should-be-ignored",
	}
	ctx, rec := newCtx(t, http.MethodPut, "/api/token/?status_only=true", body)
	asUser(ctx, owner.Id)
	UpdateToken(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var reloaded model.Token
	require.NoError(t, model.DB.First(&reloaded, tk.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, reloaded.Status)
	assert.Equal(t, "keep", reloaded.Name, "status_only must not rename the token")
}

// UpdateToken: re-enabling an exhausted, out-of-quota token is rejected.
func TestUpdateToken_ExhaustedCannotEnable(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, func(tk *model.Token) {
		tk.Status = common.TokenStatusExhausted
		tk.RemainQuota = 0
		tk.UnlimitedQuota = false
	})

	body := map[string]any{
		"id":              tk.Id,
		"status":          common.TokenStatusEnabled,
		"name":            "x",
		"expired_time":    -1,
		"remain_quota":    0,
		"unlimited_quota": false,
	}
	ctx, rec := newCtx(t, http.MethodPut, "/api/token/", body)
	asUser(ctx, owner.Id)
	UpdateToken(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// DeleteToken: owner can delete; row is gone afterward.
func TestDeleteToken_Success(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk := mkToken(t, owner.Id, nil)

	ctx, rec := newCtx(t, http.MethodDelete, "/api/token/"+strconv.Itoa(tk.Id), nil)
	asUser(ctx, owner.Id)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tk.Id)}}
	DeleteToken(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var count int64
	model.DB.Unscoped().Model(&model.Token{}).Where("id = ? AND deleted_at IS NULL", tk.Id).Count(&count)
	assert.EqualValues(t, 0, count)
}

// DeleteTokenBatch: empty id list -> invalid params.
func TestDeleteTokenBatch_EmptyIdsRejected(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/token/batch", map[string]any{"ids": []int{}})
	asUser(ctx, owner.Id)
	DeleteTokenBatch(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// DeleteTokenBatch: deletes only the caller's tokens, returns the count.
func TestDeleteTokenBatch_Success(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)
	tk1 := mkToken(t, owner.Id, nil)
	tk2 := mkToken(t, owner.Id, nil)

	ctx, rec := newCtx(t, http.MethodPost, "/api/token/batch", map[string]any{"ids": []int{tk1.Id, tk2.Id}})
	asUser(ctx, owner.Id)
	DeleteTokenBatch(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var count int
	require.NoError(t, common.Unmarshal(resp.Data, &count))
	assert.Equal(t, 2, count)
}

// GetTokenKeysBatch: empty ids rejected; >100 ids rejected; happy path returns
// a keyed map of full keys for the caller's tokens.
func TestGetTokenKeysBatch_Validation(t *testing.T) {
	requireDB(t)
	owner := mkUser(t, nil)

	t.Run("empty", func(t *testing.T) {
		ctx, rec := newCtx(t, http.MethodPost, "/api/token/keys", map[string]any{"ids": []int{}})
		asUser(ctx, owner.Id)
		GetTokenKeysBatch(ctx)
		assert.False(t, decodeResp(t, rec).Success)
	})

	t.Run("too many", func(t *testing.T) {
		ids := make([]int, 101)
		for i := range ids {
			ids[i] = i + 1
		}
		ctx, rec := newCtx(t, http.MethodPost, "/api/token/keys", map[string]any{"ids": ids})
		asUser(ctx, owner.Id)
		GetTokenKeysBatch(ctx)
		assert.False(t, decodeResp(t, rec).Success)
	})

	t.Run("success", func(t *testing.T) {
		tk := mkToken(t, owner.Id, nil)
		ctx, rec := newCtx(t, http.MethodPost, "/api/token/keys", map[string]any{"ids": []int{tk.Id}})
		asUser(ctx, owner.Id)
		GetTokenKeysBatch(ctx)
		resp := decodeResp(t, rec)
		require.True(t, resp.Success, resp.Message)
		var payload struct {
			Keys map[int]string `json:"keys"`
		}
		require.NoError(t, common.Unmarshal(resp.Data, &payload))
		assert.Equal(t, tk.GetFullKey(), payload.Keys[tk.Id])
	})
}
