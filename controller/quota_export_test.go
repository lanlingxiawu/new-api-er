package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseQuotaExportRequest(t *testing.T) {
	longName := strings.Repeat("名", 65)
	for _, tc := range []struct {
		name string
		body string
		role int
		ok   bool
	}{
		{"range", `{"filters":{"start_timestamp":10,"end_timestamp":20}}`, common.RoleAdminUser, true},
		{"channel zero", `{"filters":{"start_timestamp":10,"end_timestamp":20,"channel_id":0,"user_id":0}}`, common.RoleAdminUser, true},
		{"trimmed text", `{"filters":{"start_timestamp":10,"end_timestamp":20,"username":"  alice  ","model_name":"gpt","use_group":"vip"}}`, common.RoleAdminUser, true},
		{"blank text", `{"filters":{"start_timestamp":10,"end_timestamp":20,"username":"   "}}`, common.RoleAdminUser, true},
		{"root filters", `{"filters":{"start_timestamp":10,"end_timestamp":20,"token_id":7,"node_name":" node-a "},"options":{"header":false,"csv_bom":false,"timezone":"UTC"}}`, common.RoleRootUser, true},
		{"missing filters", `{}`, common.RoleAdminUser, false},
		{"null filters", `{"filters":null}`, common.RoleAdminUser, false},
		{"null field", `{"filters":{"start_timestamp":10,"end_timestamp":20,"username":null}}`, common.RoleAdminUser, false},
		{"unknown field", `{"filters":{"start_timestamp":10,"end_timestamp":20,"columns":["id"]}}`, common.RoleAdminUser, false},
		{"data source", `{"data_source":"quota","filters":{"start_timestamp":10,"end_timestamp":20}}`, common.RoleAdminUser, false},
		{"mode", `{"mode":"summary","filters":{"start_timestamp":10,"end_timestamp":20}}`, common.RoleAdminUser, false},
		{"admin token", `{"filters":{"start_timestamp":10,"end_timestamp":20,"token_id":1}}`, common.RoleAdminUser, false},
		{"admin node", `{"filters":{"start_timestamp":10,"end_timestamp":20,"node_name":"a"}}`, common.RoleAdminUser, false},
		{"long text", `{"filters":{"start_timestamp":10,"end_timestamp":20,"username":"` + longName + `"}}`, common.RoleAdminUser, false},
		{"negative", `{"filters":{"start_timestamp":10,"end_timestamp":20,"channel_id":-1}}`, common.RoleAdminUser, false},
		{"fraction", `{"filters":{"start_timestamp":10,"end_timestamp":20,"user_id":1.5}}`, common.RoleAdminUser, false},
		{"exponent", `{"filters":{"start_timestamp":10,"end_timestamp":20,"user_id":1e2}}`, common.RoleAdminUser, false},
		{"timezone only", `{"filters":{"start_timestamp":10,"end_timestamp":20},"options":{"timezone":"UTC"}}`, common.RoleAdminUser, true},
		{"bad timezone", `{"filters":{"start_timestamp":10,"end_timestamp":20},"options":{"timezone":"Not/AZone"}}`, common.RoleAdminUser, false},
		{"null option", `{"filters":{"start_timestamp":10,"end_timestamp":20},"options":{"header":null}}`, common.RoleAdminUser, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newRawCtx(t, http.MethodPost, "/api/data/quota/export/jobs", tc.body)
			filter, start, end, opts, err := parseQuotaExportRequest(c, tc.role)
			if !tc.ok {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(10), start)
			require.Equal(t, int64(20), end)
			require.NotNil(t, filter)
			switch tc.name {
			case "channel zero":
				require.NotNil(t, filter.ChannelID)
				require.NotNil(t, filter.UserID)
				require.Equal(t, 0, *filter.ChannelID)
				require.Equal(t, 0, *filter.UserID)
			case "trimmed text":
				require.Equal(t, "alice", filter.Username)
				require.Equal(t, "gpt", filter.ModelName)
				require.Equal(t, "vip", filter.UseGroup)
				require.Nil(t, filter.ChannelID)
			case "blank text":
				require.Empty(t, filter.Username)
			case "timezone only":
				require.Equal(t, "UTC", opts.Timezone)
				require.True(t, opts.Header)
				require.True(t, opts.CSVBOM)
			case "root filters":
				require.NotNil(t, filter.TokenID)
				require.Equal(t, 7, *filter.TokenID)
				require.Equal(t, "node-a", filter.NodeName)
				require.False(t, opts.Header)
				require.False(t, opts.CSVBOM)
				require.Equal(t, "UTC", opts.Timezone)
			default:
				require.True(t, opts.Header)
				require.True(t, opts.CSVBOM)
			}
		})
	}
}

func TestCreateQuotaExportJobRejectsBadRange(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.AdminMaxRangeSec = 31 * 86400
	})
	for _, tc := range []struct {
		name string
		body string
		key  string
	}{
		{"empty", `{"filters":{"start_timestamp":0,"end_timestamp":20}}`, i18n.MsgLogExportRangeRequired},
		{"reversed", `{"filters":{"start_timestamp":20,"end_timestamp":10}}`, i18n.MsgLogExportRangeInvalid},
		{"above cap", `{"filters":{"start_timestamp":1,"end_timestamp":253402300800}}`, i18n.MsgLogExportRangeInvalid},
		{"too long", `{"filters":{"start_timestamp":1,"end_timestamp":2764802}}`, i18n.MsgLogExportRangeTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRawCtx(t, http.MethodPost, "/api/data/quota/export/jobs", tc.body)
			asAdmin(c, 42)
			c.Set("log_export_access", model.LogExportAccess{UserID: 42, Role: common.RoleAdminUser, Scope: "quota_admin"})
			CreateQuotaExportJob(c)
			response := decodeResp(t, rec)
			require.False(t, response.Success)
			var args map[string]any
			if tc.key == i18n.MsgLogExportRangeTooLong {
				args = map[string]any{"Days": int64(31)}
			}
			require.Equal(t, common.TranslateMessage(c, tc.key, args), response.Message)
		})
	}
}

func TestQuotaExportScopeRequiresDashboardSession(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/data/quota/export/jobs", nil)
	c.Set("id", 42)
	c.Set("role", common.RoleRootUser)
	c.Set("use_access_token", true)
	QuotaExportScope()(c)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.True(t, c.IsAborted())

	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/data/quota/export/jobs", nil)
	withDashboardSession(c, 7, common.RoleCommonUser)
	QuotaExportScope()(c)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.True(t, c.IsAborted())

	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/data/quota/export/jobs", nil)
	withDashboardSession(c, 7, common.RoleAdminUser)
	QuotaExportScope()(c)
	require.False(t, c.IsAborted())
	access := c.MustGet("log_export_access").(model.LogExportAccess)
	require.Equal(t, "quota_admin", access.Scope)
	require.Equal(t, 7, access.UserID)
	require.Equal(t, common.RoleAdminUser, access.Role)
}

func TestUsageSelfAndEmployeeRejectQuotaFields(t *testing.T) {
	c, _ := newRawCtx(t, http.MethodPost, "/api/log/self/export/jobs", `{"quota_filters":{},"filters":{"start_timestamp":10,"end_timestamp":20}}`)
	_, err := parseSelfLogExportRequest(c)
	require.Error(t, err)
	c, _ = newRawCtx(t, http.MethodPost, "/api/log/self/export/jobs", `{"filters":{"start_timestamp":10,"end_timestamp":20,"node_name":"a"}}`)
	_, err = parseSelfLogExportRequest(c)
	require.Error(t, err)

	c, _ = newRawCtx(t, http.MethodPost, "/api/log/employee/export/jobs", `{"quota_filters":{},"start_timestamp":10,"end_timestamp":20}`)
	_, err = prepareEmployeeExport(c)
	require.ErrorIs(t, err, model.ErrEmployeeExportInvalid)

	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.Enabled = true })
	c, rec := newRawCtx(t, http.MethodPost, "/api/log/export/jobs", `{"start_timestamp":10,"end_timestamp":20,"quota_filters":{"username":"alice"}}`)
	asAdmin(c, 42)
	CreateLogExportJob(c)
	response := decodeResp(t, rec)
	require.False(t, response.Success)
	require.Equal(t, common.TranslateMessage(c, i18n.MsgInvalidParams), response.Message)
}

func withDashboardSession(c *gin.Context, id, role int) {
	c.Set("id", id)
	c.Set("role", role)
	c.Set("username", "quota-admin")
	c.Set("session_id", "dashboard-session")
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
}
