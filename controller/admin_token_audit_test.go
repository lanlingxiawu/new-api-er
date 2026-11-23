package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminTokenManageAuditRecordsVerificationMethod(t *testing.T) {
	tests := []struct {
		name           string
		contextMethod  string
		explicitMethod string
		expected       string
	}{
		{name: "proof method inherited", contextMethod: "2fa", expected: "2fa"},
		{name: "explicit method retained", contextMethod: "2fa", explicitMethod: "passkey", expected: "passkey"},
		{name: "read without proof has no method"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user, _ := setupAccessTokenAudit(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/", nil)
			c.Set("id", user.Id)
			c.Set("role", common.RoleAdminUser)
			c.Set("use_access_token", true)
			if test.contextMethod != "" {
				c.Set("admin_token_verification_method", test.contextMethod)
			}
			params := map[string]any{"name": "safe-name"}
			if test.explicitMethod != "" {
				params["verification_method"] = test.explicitMethod
			}
			recordManageAudit(c, "channel.create", params)

			var entry model.AuditLog
			require.NoError(t, model.LOG_DB.Where("action = ?", "channel.create").First(&entry).Error)
			require.NotNil(t, entry.Other.Op)
			encoded, err := common.Marshal(entry.Other.Op.Params)
			require.NoError(t, err)
			if test.expected == "" {
				assert.NotContains(t, string(encoded), "verification_method")
				return
			}
			assert.Contains(t, string(encoded), `"verification_method":"`+test.expected+`"`)
		})
	}
}

func TestAdminTokenChannelAuditUsesCommittedChanges(t *testing.T) {
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	for _, tc := range []struct {
		name, route, body, field string
		initialStatus, changed   int
		risk                     bool
	}{
		{"status", "POST /api/channel/status/batch", `{"status":2}`, "status", 1, 2, true},
		{"unchanged status", "POST /api/channel/status/batch", `{"status":1}`, "", 1, 0, false},
		{"daily amount", "POST /api/channel/batch/daily_limit", fmt.Sprintf(`{"daily_quota_limit":%d}`, model.MinDailyQuotaLimit()), "daily_limit", 1, 2, true},
		{"daily recovery", "POST /api/channel/batch/daily_limit", `{"daily_limit_recover_minutes":15}`, "daily_limit", 1, 2, true},
		{"empty daily edit", "POST /api/channel/batch/daily_limit", `{}`, "", 1, 0, false},
		{"tag", "POST /api/channel/batch/tag", `{"tag":"changed"}`, "tag", 1, 2, true},
		{"display", "PUT /api/channel/", `{"name":"display-change"}`, "name", 1, 2, false},
		{"group", "PUT /api/channel/tag", `{"groups":"maintenance"}`, "group", 1, 2, false},
		{"priority", "PUT /api/channel/tag", `{"priority":7}`, "priority", 1, 2, false},
		{"weight", "PUT /api/channel/tag", `{"weight":17}`, "weight", 1, 2, false},
		{"add model", "PUT /api/channel/tag", `{"models":"fixture,added"}`, "models", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
			require.NoError(t, model.DB.AutoMigrate(&model.ChannelCreator{}, &model.Ability{}, &model.ChannelCostConfig{}))
			channels := []model.Channel{{Name: "first", Key: "fixture-secret", Models: "fixture", Group: "default", Status: tc.initialStatus}, {Name: "second", Key: "fixture-secret", Models: "fixture", Group: "default", Status: tc.initialStatus}}
			require.NoError(t, model.BatchInsertChannels(channels, u.Id))
			ids := []int{channels[0].Id, channels[1].Id}
			t.Cleanup(func() {
				model.DB.Where("channel_id IN ?", ids).Delete(&model.ChannelCreator{})
				model.DB.Where("channel_id IN ?", ids).Delete(&model.Ability{})
				model.DB.Where("channel_id IN ?", ids).Delete(&model.ChannelCostConfig{})
				model.DB.Where("id IN ?", ids).Delete(&model.Channel{})
				model.LOG_DB.Where("user_id = ?", u.Id).Delete(&model.AuditLog{})
			})
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest("POST", "/api/channel/", nil)
			c.Set("id", u.Id)
			c.Set("role", common.RoleAdminUser)
			c.Set("use_access_token", true)
			action := &service.AdminChannelAction{IDs: ids, Operation: service.AdminChannelOperation{Action: tc.route, Body: []byte(tc.body)}}
			fullChannelReads := 0
			require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register("test:channel-cache-refresh", func(tx *gorm.DB) {
				if tx.Statement.Table == "channels" && !strings.Contains(strings.ToUpper(tx.Statement.SQL.String()), "WHERE") {
					fullChannelReads++
				}
			}))
			t.Cleanup(func() { model.DB.Callback().Query().Remove("test:channel-cache-refresh") })
			require.True(t, applyAdminTokenChannelBatch(c, action))
			if tc.changed == 0 {
				require.Zero(t, fullChannelReads, "unchanged request must not reload the complete channel cache")
			}
			require.Contains(t, r.Body.String(), `"success":true`)
			var row model.AuditLog
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND action = ?", u.Id, "channel.token_update").Last(&row).Error)
			var facts struct {
				Changed bool     `json:"changed"`
				Count   int      `json:"count"`
				IDs     []int    `json:"ids"`
				Fields  []string `json:"changed_fields"`
			}
			encoded, err := common.Marshal(row.Other.Op.Params)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(encoded, &facts))
			require.Equal(t, tc.changed > 0, facts.Changed)
			require.Equal(t, tc.changed, facts.Count)
			require.Len(t, facts.IDs, tc.changed)
			if tc.field != "" {
				require.Equal(t, []string{tc.field}, facts.Fields)
			} else {
				require.Empty(t, facts.Fields)
			}
			require.NotContains(t, string(encoded), "fixture-secret")
			event := auditsecurity.Normalize(row)
			require.Equal(t, tc.risk, len(auditsecurity.Evaluate(event, nil, operation_setting.DefaultAuditSecuritySetting())) > 0)
		})
	}
}

func TestChannelMaintenanceAuditPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		byTag   bool
		changes model.AuditFields
		risk    bool
	}{
		{"group", false, model.AuditFields{"group": "maintenance"}, false},
		{"priority", false, model.AuditFields{"priority": 7}, false},
		{"weight", false, model.AuditFields{"weight": 17}, false},
		{"add model", false, model.AuditFields{"models": "gpt-4o,fixture-added"}, false},
		{"remove model", false, model.AuditFields{"models": "fixture-added"}, false},
		{"address", false, model.AuditFields{"base_url": "https://receiver.invalid"}, true},
		{"address and routine", false, model.AuditFields{"base_url": "https://receiver.invalid", "models": "fixture-added", "group": "maintenance", "priority": 7, "weight": 17}, true},
		{"tag group", true, model.AuditFields{"groups": "maintenance"}, false},
		{"tag priority", true, model.AuditFields{"priority": 7}, false},
		{"tag weight", true, model.AuditFields{"weight": 17}, false},
		{"tag add model", true, model.AuditFields{"models": "gpt-4o,fixture-added"}, false},
		{"tag remove model", true, model.AuditFields{"models": "fixture-added"}, false},
		{"tag no fields", true, model.AuditFields{}, false},
		{"tag rename", true, model.AuditFields{"new_tag": "maintenance-renamed"}, true},
		{"tag mapping", true, model.AuditFields{"model_mapping": `{"gpt-4o":"fixture-alias"}`}, true},
		{"tag parameters", true, model.AuditFields{"param_override": `{"temperature":0.5}`}, true},
		{"tag daily limit", true, model.AuditFields{"daily_quota_limit": model.MinDailyQuotaLimit()}, true},
		{"tag routine and headers", true, model.AuditFields{"models": "fixture-added", "header_override": `{"X-Fixture":"private-header-value"}`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := mkUser(t, func(u *model.User) { u.Role = common.RoleRootUser })
			tag := uniq("maintenance")
			ch := mkChannel(t, func(ch *model.Channel) { ch.Tag = &tag })
			t.Cleanup(func() {
				model.DB.Where("channel_id = ?", ch.Id).Delete(&model.Ability{})
				model.DB.Where("channel_id = ?", ch.Id).Delete(&model.ChannelCostConfig{})
				model.LOG_DB.Where("user_id = ?", u.Id).Delete(&model.AuditLog{})
			})
			body := model.AuditFields{"id": ch.Id, "name": ch.Name, "type": ch.Type, "key": ch.Key, "models": ch.Models, "group": ch.Group}
			path, action, handler := "/api/channel/", "channel.update", UpdateChannel
			if tc.byTag {
				body = model.AuditFields{"tag": tag}
				path, action, handler = "/api/channel/tag", "channel.tag_edit", EditTagChannels
			}
			for field, value := range tc.changes {
				body[field] = value
			}
			encoded, err := common.Marshal(body)
			require.NoError(t, err)
			c, rec := newRawCtx(t, http.MethodPut, path, string(encoded))
			asRoot(c, u.Id)
			handler(c)
			require.True(t, decodeResp(t, rec).Success, "%s", rec.Body.String())
			stored, err := model.GetChannelById(ch.Id, false)
			require.NoError(t, err)
			storedJSON, err := common.Marshal(stored)
			require.NoError(t, err)
			var values map[string]any
			require.NoError(t, common.Unmarshal(storedJSON, &values))
			for field, value := range tc.changes {
				if field == "groups" {
					field = "group"
				} else if field == "new_tag" {
					field = "tag"
				}
				require.EqualValues(t, value, values[field], "requested configuration must actually be committed")
			}
			var row model.AuditLog
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND action = ?", u.Id, action).Last(&row).Error)
			event := auditsecurity.Normalize(row)
			findings := auditsecurity.Evaluate(event, nil, operation_setting.DefaultAuditSecuritySetting())
			require.Equal(t, tc.risk, len(findings) > 0)
			require.NotContains(t, event.Facts, "receiver.invalid")
			require.NotContains(t, event.Facts, "private-header-value")
			require.NotContains(t, event.Facts, ch.Key)
		})
	}
}
