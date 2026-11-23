package controller

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCommissionLogCursorValidation(t *testing.T) {
	for _, query := range []string{
		"start_time=100", "end_time=110", "start_time=0&end_time=0", "page_size=0", "page_size=101", "page_size=x",
		"start_time=100&end_time=99", "start_time=100&end_time=2678501", "start_time=-1", "employee_user_id=-1", "customer_user_id=x", "channel_id=-1", "loss_status=invalid",
		"cursor_id=1", "cursor_created_at=100", "cursor_created_at=100&cursor_id=0", "cursor_created_at=100&cursor_id=-1", "cursor_created_at=x&cursor_id=1",
		"start_time=100&end_time=110&cursor_created_at=111&cursor_id=1",
	} {
		t.Run(query, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission?pagination=cursor&"+query, nil)
			AdminListCommissionLogs(ctx)
			require.Equal(t, http.StatusOK, rec.Code)
			resp := decodeResp(t, rec)
			require.False(t, resp.Success)
			require.Contains(t, resp.Message, "31")
		})
	}
	ctx, _ := newCtx(t, http.MethodGet, "/?page_size=1", nil)
	before := time.Now().Unix()
	filter, err := parseCommissionLogPageFilter(ctx, 0)
	require.NoError(t, err)
	require.InDelta(t, before, filter.EndTime, 2)
	require.EqualValues(t, 86400, filter.EndTime-filter.StartTime)
	require.Equal(t, 1, filter.PageSize)
	ctx, _ = newCtx(t, http.MethodGet, "/?start_time=100&end_time=2678500&page_size=100&cursor_created_at=100&cursor_id=1", nil)
	_, err = parseCommissionLogPageFilter(ctx, 0)
	require.NoError(t, err)
}

func TestCommissionLogCursorEndpoints(t *testing.T) {
	requireDB(t)
	user, emp := mkEmployee(t, nil)
	other, otherEmp := mkEmployee(t, nil)
	now := time.Now().Unix()
	rows := []model.EmployeeCommissionLog{
		{EmployeeId: emp.Id, EmployeeUserId: user.Id, CustomerUserId: other.Id, ModelName: "cursor-older", CreatedAt: now - 1},
		{EmployeeId: emp.Id, EmployeeUserId: user.Id, CustomerUserId: other.Id, ModelName: "cursor-own", CreatedAt: now},
		{EmployeeId: otherEmp.Id, EmployeeUserId: other.Id, CustomerUserId: user.Id, ModelName: "cursor-other", CreatedAt: now},
	}
	require.NoError(t, model.DB.Create(&rows).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(&rows).Error) })
	for _, self := range []bool{false, true} {
		query := "/api/admin/employee/commission?pagination=cursor&page_size=1&employee_user_id=" + strconv.Itoa(user.Id)
		if self {
			query = "/api/user/employee/commission?pagination=cursor&page_size=1&employee_user_id=" + strconv.Itoa(other.Id)
		}
		ctx, rec := newCtx(t, http.MethodGet, query, nil)
		ctx.Set("id", user.Id)
		if self {
			GetMyCommissionLogs(ctx)
		} else {
			AdminListCommissionLogs(ctx)
		}
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Items      []map[string]any           `json:"items"`
				HasMore    bool                       `json:"has_more"`
				NextCursor *model.CommissionLogCursor `json:"next_cursor"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.True(t, response.Success, rec.Body.String())
		require.Len(t, response.Data.Items, 1)
		require.Equal(t, "cursor-own", response.Data.Items[0]["model_name"])
		require.True(t, response.Data.HasMore)
		require.Equal(t, &model.CommissionLogCursor{CreatedAt: now, Id: rows[1].Id}, response.Data.NextCursor)
		require.NotContains(t, rec.Body.String(), `"total"`)
		if self {
			require.NotContains(t, response.Data.Items[0], "channel_name")
		}
		ctx, rec = newCtx(t, http.MethodGet, query+"&cursor_created_at="+strconv.FormatInt(now, 10)+"&cursor_id="+strconv.Itoa(rows[1].Id), nil)
		ctx.Set("id", user.Id)
		if self {
			GetMyCommissionLogs(ctx)
		} else {
			AdminListCommissionLogs(ctx)
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.True(t, response.Success)
		require.Len(t, response.Data.Items, 1)
		require.Equal(t, "cursor-older", response.Data.Items[0]["model_name"])
		require.False(t, response.Data.HasMore)
		require.Nil(t, response.Data.NextCursor)
	}
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission?pagination=cursor", nil)
	canceled, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(canceled)
	AdminListCommissionLogs(ctx)
	require.False(t, decodeResp(t, rec).Success)
	require.NotContains(t, rec.Body.String(), "context canceled")
	require.NotContains(t, rec.Body.String(), "SELECT")

	ctx, rec = newCtx(t, http.MethodGet, "/api/admin/employee/commission?page=1&page_size=10&employee_user_id="+strconv.Itoa(user.Id), nil)
	AdminListCommissionLogs(ctx)
	require.True(t, decodeResp(t, rec).Success)
	require.Contains(t, rec.Body.String(), `"total"`)
	require.Contains(t, rec.Body.String(), `"page_size":10`)
}
