package auditsecurity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestResponseGroupsShareActionsAndStopWithoutChangingReviewPermissions(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.Token{}, &model.UserAccessToken{}, &model.UserSession{}))
	now := time.Now().Unix()
	old := operation_setting.GetAuditSecuritySetting()
	oldRaw, err := common.Marshal(old)
	require.NoError(t, err)
	t.Cleanup(func() {
		var values map[string]any
		require.NoError(t, common.Unmarshal(oldRaw, &values))
		updates := map[string]string{}
		for k, v := range values {
			updates[k] = fmt.Sprint(v)
		}
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, updates))
	})
	cfg := old
	cfg.Enabled, cfg.CaptureEnabled, cfg.AutoBanEnabled, cfg.Suspended, cfg.AutoBanIncludeStaff = true, true, true, false, false
	cfg.AutoBanEffectiveAt = now - 10
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	require.NoError(t, err)
	for i := range rules {
		if rules[i].ID != "session_reuse" {
			continue
		}
		rules[i].ClearAPIKeys = true
		rules[i].ClearAccessTokens = true
	}
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	cfg.AutoBanRulesJSON = string(raw)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "capture_enabled": "true", "auto_ban_enabled": "true", "suspended": "false", "auto_ban_effective_at": fmt.Sprint(now - 10), "auto_ban_rules_json": string(raw)}))
	for _, u := range []model.User{{Id: 701, Username: "group-user", AffCode: "group-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 3}, {Id: 702, Username: "group-root", AffCode: "group-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 4}} {
		require.NoError(t, db.Create(&u).Error)
	}
	require.NoError(t, db.Create(&model.UserSession{SID: "root-stop-session", UserID: 702, UserAuthVersion: 4, Version: 1, Status: model.UserSessionStatusActive, ExpiresAt: now + 300}).Error)
	e := compromiseEvent("group-trigger", 701, now, "8.8.8.8", "auth.session_refresh_reuse")
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(e.Facts, &facts))
	facts.Identity = &model.AuditIdentity{ActorID: 701, Role: common.RoleCommonUser, AuthVersion: 3}
	facts.LivePolicyAt, facts.FirstObservedAt = cfg.AutoBanEffectiveAt, now
	raw, err = common.Marshal(facts)
	require.NoError(t, err)
	e.Facts = string(raw)
	require.NoError(t, db.Create(&e).Error)
	group, err := model.ClaimSecurityResponseGroup(db, e, cfg.AutoBanEffectiveAt, *facts.Identity, now)
	require.NoError(t, err)
	alerts := []model.SecurityAlert{{ActorID: 701, ActorRole: common.RoleCommonUser, Version: 1}, {ActorID: 701, ActorRole: common.RoleCommonUser, Version: 1}}
	for i := range alerts {
		require.NoError(t, db.Create(&alerts[i]).Error)
		require.NoError(t, queueAutomaticAccountResponse(db, alerts[i], model.SecurityFinding{Rule: "session_reuse"}, e, model.SecurityState{Name: "scanner"}, cfg, now, false, group))
	}
	var count int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
	_, err = model.StopSecurityResponseGroup(context.Background(), db, model.AuthSessionIdentity{UserID: 701, UserAuthVersion: 3}, alerts[1].ID, 1, group.ID, "stop-attempt")
	require.Error(t, err)
	_, err = model.StopSecurityResponseGroup(context.Background(), db, model.AuthSessionIdentity{UserID: 702, UserAuthVersion: 4, SessionID: "root-stop-session", SessionVersion: 1}, alerts[1].ID, 1, group.ID, "stop-authorized")
	require.NoError(t, err)
	var actions []model.SecurityAction
	require.NoError(t, db.Where("response_group_id = ?", group.ID).Find(&actions).Error)
	for _, action := range actions {
		if action.Action == "stop_automatic_response" {
			require.Equal(t, "succeeded", action.Outcome)
			continue
		}
		require.Equal(t, "skipped", action.Outcome)
		require.Equal(t, "stopped", action.ReasonCode)
	}
	var user model.User
	require.NoError(t, db.First(&user, 701).Error)
	require.Equal(t, common.UserStatusEnabled, user.Status)
	require.EqualValues(t, 3, user.AuthVersion)
}

func TestSharedResponseProjectionKeepsNewerHumanAndActiveLegacyReceipts(t *testing.T) {
	db := newWorkerTestDB(t)
	group := model.SecurityResponseGroup{TargetUserID: 701, TriggerKey: "projection-trigger", State: "active"}
	require.NoError(t, db.Create(&group).Error)
	alerts := []model.SecurityAlert{{ActorID: 701}, {ActorID: 701}, {ActorID: 702}}
	for i := range alerts {
		require.NoError(t, db.Create(&alerts[i]).Error)
		require.NoError(t, db.Create(&model.SecurityResponseLink{AlertID: alerts[i].ID, GroupID: group.ID}).Error)
	}
	oldBan := model.SecurityAction{AlertID: alerts[0].ID, TargetUserID: 701, Action: "disable_account", Outcome: "skipped"}
	legacyCleanup := model.SecurityAction{AlertID: alerts[0].ID, TargetUserID: 701, Action: "clear_api_keys", Outcome: "sync_pending"}
	require.NoError(t, db.Create(&oldBan).Error)
	require.NoError(t, db.Create(&legacyCleanup).Error)
	var grouped []model.SecurityAction
	for _, kind := range []string{"disable_account", "clear_access_tokens", "clear_api_keys"} {
		action := model.SecurityAction{AlertID: alerts[0].ID, ResponseGroupID: &group.ID, TargetUserID: 701, Action: kind, Source: "automatic", Outcome: "succeeded"}
		require.NoError(t, db.Create(&action).Error)
		grouped = append(grouped, action)
	}
	humanBan := model.SecurityAction{AlertID: alerts[0].ID, TargetUserID: 701, Action: "disable_account", Source: "human", Outcome: "succeeded"}
	require.NoError(t, db.Create(&humanBan).Error)
	require.NoError(t, loadAlertResponses(context.Background(), db, alerts))

	require.NotNil(t, alerts[0].Response)
	require.Equal(t, humanBan.ID, alerts[0].Response.ID)
	require.Equal(t, "human", alerts[0].Response.Source)
	var actualIDs []int64
	for _, response := range alerts[0].Responses {
		actualIDs = append(actualIDs, response.ID)
	}
	require.ElementsMatch(t, []int64{legacyCleanup.ID, grouped[0].ID, grouped[1].ID, grouped[2].ID, humanBan.ID}, actualIDs)
	require.Len(t, alerts[1].Responses, 3)
	require.Equal(t, grouped[0].ID, alerts[1].Response.ID)
	require.Equal(t, group.ID, *alerts[1].ResponseGroupID)
	require.True(t, alerts[1].CanStop)
	require.Empty(t, alerts[2].Responses, "another account must not receive the group's results")
	require.Nil(t, alerts[2].Response)
}
