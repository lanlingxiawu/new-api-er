package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestExportGroupRatioOverrideRestrictedRequests(t *testing.T) {
	for _, body := range []string{
		`{"filters":{"start_timestamp":1,"end_timestamp":2},"group_ratio_override":"1.5"}`,
		`{"filters":{"start_timestamp":1,"end_timestamp":2,"group_ratio_override":"1.5"}}`,
	} {
		c, _ := newRawCtx(t, http.MethodPost, "/api/log/self/export/jobs", body)
		_, err := parseSelfLogExportRequest(c)
		require.Error(t, err)
	}
	c, _ := newRawCtx(t, http.MethodPost, "/api/user/employee/export/jobs", `{"group_ratio_override":"1.5"}`)
	_, err := prepareEmployeeExport(c)
	require.Error(t, err)
}

func TestExportGroupRatioOverrideControllerValidation(t *testing.T) {
	server := miniredis.RunT(t)
	previousRDB, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousRDB, previousEnabled
	})
	s := operation_setting.GetLogExportSetting()
	previous := *s
	s.Enabled = true
	t.Cleanup(func() { *s = previous })
	for _, tc := range []struct {
		role   int
		value  string
		status int
	}{
		{common.RoleCommonUser, "1.5", http.StatusForbidden},
		{common.RoleAdminUser, "-1", http.StatusOK},
		{common.RoleRootUser, "10001", http.StatusOK},
	} {
		c, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", map[string]any{"group_ratio_override": tc.value})
		c.Set("id", 42)
		c.Set("role", tc.role)
		CreateLogExportJob(c)
		require.Equal(t, tc.status, rec.Code)
		var result struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &result))
		require.False(t, result.Success)
		if tc.status == http.StatusOK {
			require.NotContains(t, result.Message, "log_export.")
		}
	}
}
