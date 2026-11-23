package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogQueryHandlersPropagateCancellation(t *testing.T) {
	for name, handler := range map[string]gin.HandlerFunc{
		"admin_list": GetAllLogs,
		"user_list":  GetUserLogs,
		"admin_stat": GetLogsStat,
		"user_stat":  GetLogsSelfStat,
		"token_list": GetLogByKey,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/log/", nil).WithContext(ctx)
			c.Set("id", 1)
			c.Set("token_id", 1)
			c.Set("username", "cancelled-query-test")
			handler(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.False(t, decodeResp(t, recorder).Success)
			require.NotContains(t, recorder.Body.String(), "context canceled")
		})
	}
}

func TestLogStatsPreserveExactSearchFilters(t *testing.T) {
	if model.LOG_DB == nil {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&model.Log{}))
		model.LOG_DB = db
		t.Cleanup(func() {
			model.LOG_DB = nil
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
		})
	}
	settings := operation_setting.GetLogSetting()
	previousRange := settings.MaxRangeDays
	settings.MaxRangeDays = 90
	t.Cleanup(func() { settings.MaxRangeDays = previousRange })

	now := time.Now().Unix()
	userID, username := nextTestID(), uniq("stat-filter")
	target := model.Log{
		UserId: userID, Username: username, Type: model.LogTypeConsume,
		RequestId: uniq("stat-request"), UpstreamRequestId: uniq("stat-upstream"),
		CreatedAt: now, Quota: 7, PromptTokens: 3, CompletionTokens: 2,
	}
	unrelated := target
	unrelated.RequestId, unrelated.UpstreamRequestId = uniq("other-request"), uniq("other-upstream")
	unrelated.Quota, unrelated.PromptTokens, unrelated.CompletionTokens = 1000, 100, 100
	for _, row := range []*model.Log{&target, &unrelated} {
		require.NoError(t, model.LOG_DB.Create(row).Error)
		t.Cleanup(func() { require.NoError(t, model.LOG_DB.Delete(&model.Log{}, row.Id).Error) })
	}

	for _, scope := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"admin", GetLogsStat},
		{"self", GetLogsSelfStat},
	} {
		for _, span := range []struct {
			name    string
			seconds int64
		}{
			{"within_hour_cache_range", 3 * 3600},
			{"large_time_range", 29 * 24 * 3600},
		} {
			for _, tc := range []struct {
				name, query string
				want        model.Stat
			}{
				{"request_id", "request_id=" + target.RequestId, model.Stat{Quota: 7, Rpm: 1, Tpm: 5}},
				{"upstream_request_id", "upstream_request_id=" + target.UpstreamRequestId, model.Stat{Quota: 7, Rpm: 1, Tpm: 5}},
				{"log_id", fmt.Sprintf("log_id=%d", target.Id), model.Stat{Quota: 7, Rpm: 1, Tpm: 5}},
				{"consume_only", "request_id=" + target.RequestId + "&type=5", model.Stat{Quota: 7, Rpm: 1, Tpm: 5}},
				{"missing", "request_id=" + uniq("missing-request"), model.Stat{}},
				{"conflicting_ids", "request_id=" + target.RequestId + "&upstream_request_id=" + unrelated.UpstreamRequestId, model.Stat{}},
				{"combined_filters", "request_id=" + target.RequestId + "&token_name=" + uniq("missing-token"), model.Stat{}},
			} {
				t.Run(scope.name+"/"+span.name+"/"+tc.name, func(t *testing.T) {
					query := fmt.Sprintf("/api/log/stat?start_timestamp=%d&end_timestamp=%d&%s", now-span.seconds, now+3600, tc.query)
					ctx, rec := newCtx(t, http.MethodGet, query, nil)
					ctx.Set("id", userID)
					ctx.Set("username", username)
					scope.handler(ctx)
					response := decodeResp(t, rec)
					require.True(t, response.Success, response.Message)
					var stat model.Stat
					require.NoError(t, common.Unmarshal(response.Data, &stat))
					require.Equal(t, tc.want, stat)
				})
			}
		}
		t.Run(scope.name+"/historical_quota_live_rates", func(t *testing.T) {
			query := fmt.Sprintf("/api/log/stat?start_timestamp=%d&end_timestamp=%d&request_id=%s", now-48*3600, now-24*3600, target.RequestId)
			ctx, rec := newCtx(t, http.MethodGet, query, nil)
			ctx.Set("id", userID)
			ctx.Set("username", username)
			scope.handler(ctx)
			response := decodeResp(t, rec)
			require.True(t, response.Success, response.Message)
			var stat model.Stat
			require.NoError(t, common.Unmarshal(response.Data, &stat))
			require.Equal(t, model.Stat{Quota: 0, Rpm: 1, Tpm: 5}, stat)
		})
	}

	ctx, rec := newCtx(t, http.MethodGet, "/api/log/self/stat?request_id="+target.RequestId+"&username="+username, nil)
	ctx.Set("id", nextTestID())
	ctx.Set("username", uniq("another-user"))
	GetLogsSelfStat(ctx)
	response := decodeResp(t, rec)
	require.True(t, response.Success, response.Message)
	var stat model.Stat
	require.NoError(t, common.Unmarshal(response.Data, &stat))
	require.Equal(t, model.Stat{}, stat, "request filters must not bypass the self scope")
}
