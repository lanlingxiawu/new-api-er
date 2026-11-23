package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestSecurityCleanupFencesLateCacheRefillAndQuotaSettlement(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	ctx := context.Background()
	require.NoError(t, MigrateAuditSecurity(ctx, DB))
	user := mkUser(t, nil)
	key := mkToken(t, user.Id, func(token *Token) { token.ExpiredTime = -1; token.UnlimitedQuota = true })
	other := mkToken(t, mkUser(t, nil).Id, func(token *Token) { token.ExpiredTime = -1; token.UnlimitedQuota = true })
	primeTokenCache(t, key)
	primeTokenCache(t, other)
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
	rules[3].Selected, rules[3].ClearAPIKeys = false, true
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	now := time.Now().Unix()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
		"enabled": "true", "capture_enabled": "true", "suspended": "false", "auto_ban_enabled": "true", "auto_ban_effective_at": fmt.Sprint(now - 5),
		"auto_ban_include_staff": "false", "auto_ban_excluded_user_ids_json": "[]", "auto_ban_rules_json": string(raw),
	}))
	alert := SecurityAlert{ActorID: user.Id, Version: 1}
	require.NoError(t, DB.Create(&alert).Error)
	t.Cleanup(func() { require.NoError(t, DB.Where("id = ?", alert.ID).Delete(&SecurityAlert{}).Error) })
	raw, err = common.Marshal(SecurityResponseResult{PolicyAt: now - 5, Rule: rules[3], TargetAuthVersion: user.AuthVersion, TargetRole: user.Role})
	require.NoError(t, err)
	action := SecurityAction{AlertID: alert.ID, TargetUserID: user.Id, Action: "clear_api_keys", Source: "automatic", Outcome: "pending", EventAt: now, Result: string(raw)}
	require.NoError(t, DB.Create(&action).Error)
	t.Cleanup(func() { require.NoError(t, DB.Where("id = ?", action.ID).Delete(&SecurityAction{}).Error) })
	options := *common.RDB.Options()
	options.PoolSize, options.MaxRetries = 1, -1
	cache := redis.NewClient(&options)
	t.Cleanup(func() { _ = cache.Close() })
	result, err := ProcessSecurityCredentialCleanup(ctx, DB, cache, action.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Outcome)
	code, err := cacheInitToken(*key)
	require.NoError(t, err)
	require.Zero(t, code, "stale enabled snapshot must be fenced")
	_, err = cacheApplyTokenQuotaDelta(key.Id, key.Key, 1)
	require.NoError(t, err)
	require.NoError(t, key.Update())
	require.NoError(t, key.SelectUpdate())
	_, err = ValidateUserToken(key.Key)
	require.Error(t, err)
	_, err = ValidateUserToken(other.Key)
	require.NoError(t, err)
}
