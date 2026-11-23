package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutomaticCredentialCleanupAuthenticationIsolation(t *testing.T) {
	for _, kind := range []string{"clear_api_keys", "clear_access_tokens"} {
		t.Run(kind, func(t *testing.T) {
			_, _, target := setupAdminUserAPIKeyTest(t)
			ctx := context.Background()
			require.NoError(t, model.MigrateAuditSecurity(ctx, model.DB))
			require.NoError(t, model.DB.AutoMigrate(&model.EmployeeProfile{}))
			old := operation_setting.GetAuditSecuritySetting()
			t.Cleanup(func() {
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
					"enabled": fmt.Sprint(old.Enabled), "capture_enabled": fmt.Sprint(old.CaptureEnabled), "suspended": fmt.Sprint(old.Suspended),
					"auto_ban_enabled": fmt.Sprint(old.AutoBanEnabled), "auto_ban_effective_at": fmt.Sprint(old.AutoBanEffectiveAt),
					"auto_ban_include_staff": fmt.Sprint(old.AutoBanIncludeStaff), "auto_ban_excluded_user_ids_json": old.AutoBanExcludedUserIDsJSON, "auto_ban_rules_json": old.AutoBanRulesJSON,
				}))
			})
			rules, err := operation_setting.ParseAutoBanRules(operation_setting.DefaultAuditSecuritySetting().AutoBanRulesJSON)
			require.NoError(t, err)
			for i := range rules {
				rules[i].Selected = false
				rules[i].ClearAPIKeys, rules[i].ClearAccessTokens = kind == "clear_api_keys", kind == "clear_access_tokens"
			}
			raw, err := common.Marshal(rules)
			require.NoError(t, err)
			now := time.Now().Unix()
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
				"enabled": "true", "capture_enabled": "true", "suspended": "false", "auto_ban_enabled": "true", "auto_ban_effective_at": fmt.Sprint(now - 5),
				"auto_ban_include_staff": "false", "auto_ban_excluded_user_ids_json": "[]", "auto_ban_rules_json": string(raw),
			}))
			const pat, legacy = "nap_automatic-fixture", "automatic-legacy"
			createClearAccessTokenFixture(t, target.Id, pat)
			require.NoError(t, model.DB.Model(target).Updates(map[string]any{"access_token": legacy, "access_token_created_at": int64(123)}).Error)
			key := model.Token{UserId: target.Id, Key: "automatic-model-fixture", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			require.NoError(t, model.DB.Create(&key).Error)
			bundle, err := service.CreateLoginSession(target.Id, "password", "127.0.0.1", "cleanup-test")
			require.NoError(t, err)
			router := gin.New()
			router.GET("/api/user/self", middleware.UserAuth(), func(c *gin.Context) { common.ApiSuccess(c, nil) })
			request := func(credential string) int {
				r := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
				r.Header.Set("Authorization", "Bearer "+credential)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w.Code
			}
			for _, credential := range []string{pat, legacy, bundle.AccessToken} {
				require.Equal(t, http.StatusOK, request(credential))
			}
			_, err = model.ValidateUserToken(key.Key)
			require.NoError(t, err)
			alert := model.SecurityAlert{ActorID: target.Id, Version: 1}
			require.NoError(t, model.DB.Create(&alert).Error)
			result, err := common.Marshal(model.SecurityResponseResult{PolicyAt: now - 5, Rule: rules[3], TargetAuthVersion: target.AuthVersion, TargetRole: target.Role})
			require.NoError(t, err)
			action := model.SecurityAction{AlertID: alert.ID, TargetUserID: target.Id, Action: kind, Source: "automatic", Outcome: "pending", EventAt: now, Result: string(result)}
			require.NoError(t, model.DB.Create(&action).Error)
			updated, err := model.ProcessSecurityCredentialCleanup(ctx, model.DB, nil, action.ID)
			require.NoError(t, err)
			require.Equal(t, "succeeded", updated.Outcome)
			require.Equal(t, http.StatusOK, request(bundle.AccessToken), "independent cleanup must retain browser session")
			_, err = model.ValidateUserToken(key.Key)
			if kind == "clear_api_keys" {
				require.Error(t, err)
				require.Equal(t, http.StatusOK, request(pat))
				require.Equal(t, http.StatusOK, request(legacy))
			} else {
				require.NoError(t, err)
				require.Equal(t, http.StatusUnauthorized, request(pat))
				require.Equal(t, http.StatusUnauthorized, request(legacy))
			}
		})
	}
}
