package controller

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFullScanLogQueryTimeRange(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name, query string
		valid       bool
		span        int64
	}{
		{"omitted", "", true, 86400},
		{"explicit_zero", "?start_timestamp=0&end_timestamp=0", true, 86400},
		{"end_only", fmt.Sprintf("?end_timestamp=%d", now-86400), true, 86400},
		{"one_day", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now-86400, now), true, 86400},
		{"maximum", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now-30*86400, now), true, 30 * 86400},
		{"over_maximum", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now-30*86400-1, now), false, 0},
		{"negative", "?start_timestamp=-1", false, 0},
		{"malformed", "?start_timestamp=abc", false, 0},
		{"overflow", "?end_timestamp=9223372036854775808", false, 0},
		{"reversed", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now, now-1), false, 0},
		{"future_start", fmt.Sprintf("?start_timestamp=%d", now+3600), false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newCtx(t, http.MethodGet, "/api/log/"+tc.query, nil)
			start, end, ok := parseLogQueryTimeRange(c)
			require.Equal(t, tc.valid, ok)
			if ok {
				require.Positive(t, start)
				require.LessOrEqual(t, end, time.Now().Unix())
				require.Equal(t, tc.span, end-start)
			} else {
				require.False(t, decodeResp(t, rec).Success)
			}
		})
	}
}

func TestFullScanInteractiveLogWindows(t *testing.T) {
	user := mkUser(t, nil)
	employee := &model.EmployeeProfile{UserId: user.Id, Status: model.CustomerStatusEnabled}
	require.NoError(t, model.DB.Create(employee).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(employee).Error) })
	now := time.Now().Unix()
	for _, age := range []int64{60, 48 * 3600} {
		log := &model.Log{UserId: user.Id, Username: user.Username, CreatedAt: now - age, Quota: 7, Type: model.LogTypeConsume}
		require.NoError(t, model.LOG_DB.Create(log).Error)
		t.Cleanup(func() { require.NoError(t, model.LOG_DB.Delete(log).Error) })
		audit := &model.AuditLog{EventId: uniq("window-event"), UserId: user.Id, Username: user.Username, ActorRole: common.RoleCommonUser, CreatedAt: now - age}
		require.NoError(t, model.LOG_DB.Create(audit).Error)
		t.Cleanup(func() { require.NoError(t, model.LOG_DB.Delete(audit).Error) })
	}
	for _, endpoint := range []struct {
		name    string
		handler gin.HandlerFunc
		stat    bool
	}{
		{"admin", GetAllLogs, false}, {"self", GetUserLogs, false},
		{"employee", GetEmployeeCustomerLogs, false}, {"audit", GetAuditLogs, false},
		{"admin_stat", GetLogsStat, true}, {"self_stat", GetLogsSelfStat, true},
		{"employee_stat", GetEmployeeCustomerLogsStat, true},
	} {
		for _, window := range []struct {
			name, query string
			total       int
		}{
			{"default_day", "", 1},
			{"explicit_history", fmt.Sprintf("&start_timestamp=%d&end_timestamp=%d", now-3*86400, now), 2},
			{"excessive", fmt.Sprintf("&start_timestamp=%d&end_timestamp=%d", now-31*86400, now), -1},
		} {
			t.Run(endpoint.name+"/"+window.name, func(t *testing.T) {
				c, rec := newCtx(t, http.MethodGet, "/?username="+user.Username+window.query, nil)
				c.Set("id", user.Id)
				c.Set("username", user.Username)
				c.Set("role", common.RoleRootUser)
				endpoint.handler(c)
				response := decodeResp(t, rec)
				if window.total < 0 {
					require.False(t, response.Success)
					return
				}
				require.True(t, response.Success, response.Message)
				var data struct{ Total, Quota int }
				require.NoError(t, common.Unmarshal(response.Data, &data))
				if endpoint.stat {
					require.Equal(t, 7*window.total, data.Quota)
				} else {
					require.Equal(t, window.total, data.Total)
				}
			})
		}
	}
}
