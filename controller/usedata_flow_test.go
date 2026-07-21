package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseFlowQuotaTimeRange: boundary/equivalence over start/end validity.
func TestParseFlowQuotaTimeRange(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		wantOK  bool
		wantMsg string
	}{
		{"valid", "start_timestamp=1000&end_timestamp=2000", true, ""},
		{"bad start", "start_timestamp=x&end_timestamp=2000", false, "invalid start_timestamp"},
		{"zero start", "start_timestamp=0&end_timestamp=2000", false, "invalid start_timestamp"},
		{"bad end", "start_timestamp=1000&end_timestamp=x", false, "invalid end_timestamp"},
		{"end before start", "start_timestamp=2000&end_timestamp=1000", false, "invalid time range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, "/api/data/flow?"+tc.query, nil)
			start, end, ok := parseFlowQuotaTimeRange(ctx)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.EqualValues(t, 1000, start)
				assert.EqualValues(t, 2000, end)
			} else {
				resp := decodeResp(t, rec)
				assert.False(t, resp.Success)
				assert.Equal(t, tc.wantMsg, resp.Message)
			}
		})
	}
}

// GetUserFlowQuotaDates: invalid start_timestamp short-circuits before any DB.
func TestGetUserFlowQuotaDates_InvalidTimeRange(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/data/flow/self?start_timestamp=bad&end_timestamp=2000", nil)
	asUser(ctx, 1)
	GetUserFlowQuotaDates(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, "invalid start_timestamp", resp.Message)
}

// GetUserFlowQuotaDates: span > 31 days is rejected.
func TestGetUserFlowQuotaDates_SpanTooLong(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/data/flow/self?start_timestamp=1&end_timestamp=2592002", nil)
	asUser(ctx, 1)
	GetUserFlowQuotaDates(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

type flowData struct {
	Success bool                  `json:"success"`
	Message string                `json:"message"`
	Data    []model.FlowQuotaData `json:"data"`
}

// GetUserFlowQuotaDates: self view is scoped to the caller and resolves the
// token name + use group (DB-backed).
func TestGetUserFlowQuotaDates_SelfScopedWithTokenName(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *model.Token) { tk.Name = "primary-tok" })
	seedQuotaData(t, &model.QuotaData{
		UserID:    u.Id,
		Username:  u.Username,
		TokenID:   tk.Id,
		UseGroup:  "default",
		ModelName: "gpt-flow",
		CreatedAt: 1500,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/data/flow/self?start_timestamp=1000&end_timestamp=2000", nil)
	asUser(ctx, u.Id)
	GetUserFlowQuotaDates(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var out flowData
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	assert.Equal(t, "primary-tok", out.Data[0].TokenName)
	assert.Equal(t, "default", out.Data[0].UseGroup)
	assert.Empty(t, out.Data[0].Username, "self view must not expose username")
	assert.Empty(t, out.Data[0].ChannelName, "self view has no channel dimension")
}

// GetAllFlowQuotaDates: admin view is scoped by username and resolves channel
// name (DB-backed).
func TestGetAllFlowQuotaDates_AdminScopedWithChannelName(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ch := mkChannel(t, func(ch *model.Channel) { ch.Name = "east-1" })
	seedQuotaData(t, &model.QuotaData{
		UserID:    u.Id,
		Username:  u.Username,
		ChannelID: ch.Id,
		UseGroup:  "vip",
		ModelName: "gpt-flow",
		CreatedAt: 1500,
		Count:     1,
		Quota:     70,
		TokenUsed: 30,
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/data/flow?start_timestamp=1000&end_timestamp=2000&username="+u.Username, nil)
	asAdmin(ctx, 1)
	GetAllFlowQuotaDates(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var out flowData
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	assert.Equal(t, u.Username, out.Data[0].Username)
	assert.Equal(t, "vip", out.Data[0].UseGroup)
	assert.Equal(t, "east-1", out.Data[0].ChannelName)
	assert.Empty(t, out.Data[0].NodeName, "admin view has no node dimension")
}

func seedQuotaData(t *testing.T, q *model.QuotaData) {
	t.Helper()
	requireDB(t)
	q.Id = nextTestID()
	require.NoError(t, model.DB.Create(q).Error)
	deleteByID(t, &model.QuotaData{}, q.Id)
}
