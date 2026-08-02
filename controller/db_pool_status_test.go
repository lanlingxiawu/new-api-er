package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDBPoolRuntimeStatusReturnsStableEnvelope(t *testing.T) {
	requireDB(t)
	previousLogDB := model.LOG_DB
	model.LOG_DB = model.DB
	t.Cleanup(func() { model.LOG_DB = previousLogDB })

	ctx, rec := newRawCtx(t, http.MethodGet, "/api/option/db-pool/stats", "")
	GetDBPoolRuntimeStatus(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	response := decodeResp(t, rec)
	require.True(t, response.Success)
	data := decodeStatusData(t, response.Data)
	assert.Contains(t, data, "sampled_at")
	assert.Contains(t, data, "main")
	assert.Nil(t, data["log"])
	assert.Equal(t, true, data["log_reuses_main"])
}
