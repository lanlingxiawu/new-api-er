package controller

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkRedemption(t *testing.T, mut func(r *model.Redemption)) *model.Redemption {
	t.Helper()
	requireDB(t)
	id := nextTestID()
	r := &model.Redemption{
		Id:          id,
		UserId:      1,
		Key:         common.GetUUID(),
		Name:        uniq("red"),
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       100,
		CreatedTime: time.Now().Unix(),
		ExpiredTime: 0,
	}
	if mut != nil {
		mut(r)
	}
	require.NoError(t, model.DB.Create(r).Error)
	deleteByID(t, &model.Redemption{}, r.Id)
	return r
}

// AddRedemption: blocked until payment compliance is confirmed.
func TestAddRedemption_RequiresPaymentCompliance(t *testing.T) {
	requireDB(t)
	// force compliance OFF, restore afterward
	ps := operation_setting.GetPaymentSetting()
	prev := ps.ComplianceConfirmed
	ps.ComplianceConfirmed = false
	t.Cleanup(func() { ps.ComplianceConfirmed = prev })

	body := map[string]any{"name": "promo", "count": 1, "quota": 100}
	ctx, rec := newCtx(t, http.MethodPost, "/api/redemption/", body)
	asAdmin(ctx, 1)
	AddRedemption(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

// AddRedemption: name length boundary (empty and >20 runes) rejected.
func TestAddRedemption_NameLengthBoundary(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)

	for _, name := range []string{"", strings.Repeat("n", 21)} {
		body := map[string]any{"name": name, "count": 1, "quota": 100}
		ctx, rec := newCtx(t, http.MethodPost, "/api/redemption/", body)
		asAdmin(ctx, 1)
		AddRedemption(ctx)
		assert.False(t, decodeResp(t, rec).Success, "name %q should be rejected", name)
	}
}

// AddRedemption: count boundary (<=0 and >100) rejected.
func TestAddRedemption_CountBoundary(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)

	for _, count := range []int{0, -1, 101} {
		body := map[string]any{"name": "promo", "count": count, "quota": 100}
		ctx, rec := newCtx(t, http.MethodPost, "/api/redemption/", body)
		asAdmin(ctx, 1)
		AddRedemption(ctx)
		assert.False(t, decodeResp(t, rec).Success, "count %d should be rejected", count)
	}
}

// AddRedemption: past expiry rejected.
func TestAddRedemption_PastExpiryRejected(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)

	body := map[string]any{"name": "promo", "count": 1, "quota": 100, "expired_time": time.Now().Unix() - 3600}
	ctx, rec := newCtx(t, http.MethodPost, "/api/redemption/", body)
	asAdmin(ctx, 1)
	AddRedemption(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

// AddRedemption: happy path returns generated keys and persists them.
func TestAddRedemption_Success(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)

	name := "p" + strconv.Itoa(nextTestID()) // ~10 runes, unique, <=20
	body := map[string]any{"name": name, "count": 3, "quota": 250}
	ctx, rec := newCtx(t, http.MethodPost, "/api/redemption/", body)
	asAdmin(ctx, 990001)
	AddRedemption(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var keys []string
	require.NoError(t, common.Unmarshal(resp.Data, &keys))
	require.Len(t, keys, 3)

	// cleanup created rows
	t.Cleanup(func() {
		model.DB.Unscoped().Where("name = ?", name).Delete(&model.Redemption{})
	})
	var count int64
	model.DB.Model(&model.Redemption{}).Where("name = ?", name).Count(&count)
	assert.EqualValues(t, 3, count)
}

// GetRedemption: invalid id -> ApiError.
func TestGetRedemption_InvalidID(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/redemption/abc", nil)
	asAdmin(ctx, 1)
	ctx.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetRedemption(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

// GetRedemption: existing id returns the record.
func TestGetRedemption_Success(t *testing.T) {
	requireDB(t)
	r := mkRedemption(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/redemption/"+strconv.Itoa(r.Id), nil)
	asAdmin(ctx, 1)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(r.Id)}}
	GetRedemption(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var got model.Redemption
	require.NoError(t, common.Unmarshal(resp.Data, &got))
	assert.Equal(t, r.Id, got.Id)
}

// UpdateRedemption status_only toggles status without touching other fields.
func TestUpdateRedemption_StatusOnly(t *testing.T) {
	requireDB(t)
	r := mkRedemption(t, func(r *model.Redemption) {
		r.Name = "keepname"
		r.Status = common.RedemptionCodeStatusEnabled
	})
	body := map[string]any{"id": r.Id, "status": common.RedemptionCodeStatusDisabled, "name": "ignored"}
	ctx, rec := newCtx(t, http.MethodPut, "/api/redemption/?status_only=true", body)
	asAdmin(ctx, 1)
	UpdateRedemption(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var reloaded model.Redemption
	require.NoError(t, model.DB.First(&reloaded, r.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusDisabled, reloaded.Status)
	assert.Equal(t, "keepname", reloaded.Name)
}

// UpdateRedemption full update rejects a past expiry.
func TestUpdateRedemption_PastExpiryRejected(t *testing.T) {
	requireDB(t)
	r := mkRedemption(t, nil)
	body := map[string]any{"id": r.Id, "name": "n", "quota": 100, "expired_time": time.Now().Unix() - 100}
	ctx, rec := newCtx(t, http.MethodPut, "/api/redemption/", body)
	asAdmin(ctx, 1)
	UpdateRedemption(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

// DeleteRedemption removes the row.
func TestDeleteRedemption_Success(t *testing.T) {
	requireDB(t)
	r := mkRedemption(t, nil)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/redemption/"+strconv.Itoa(r.Id), nil)
	asAdmin(ctx, 1)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(r.Id)}}
	DeleteRedemption(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var count int64
	model.DB.Model(&model.Redemption{}).Where("id = ?", r.Id).Count(&count)
	assert.EqualValues(t, 0, count)
}

// validateExpiredTime: boundary between 0 (never), past, and future.
func TestValidateExpiredTime(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodPost, "/api/redemption/", nil)
	ok, _ := validateExpiredTime(ctx, 0)
	assert.True(t, ok, "0 means never-expire and is valid")
	ok, msg := validateExpiredTime(ctx, time.Now().Unix()-100)
	assert.False(t, ok)
	assert.NotEmpty(t, msg)
	ok, _ = validateExpiredTime(ctx, time.Now().Unix()+3600)
	assert.True(t, ok)
}
