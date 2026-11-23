package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestAutomaticAccountResponseIsBoundedAndIdempotent(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	ctx := context.Background()
	old := operation_setting.GetAuditSecuritySetting()
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": fmt.Sprint(old.Enabled), "capture_enabled": fmt.Sprint(old.CaptureEnabled), "suspended": fmt.Sprint(old.Suspended), "auto_ban_enabled": fmt.Sprint(old.AutoBanEnabled), "auto_ban_effective_at": fmt.Sprint(old.AutoBanEffectiveAt), "auto_ban_include_staff": fmt.Sprint(old.AutoBanIncludeStaff), "auto_ban_exclude_root": fmt.Sprint(old.AutoBanExcludeRoot), "auto_ban_excluded_user_ids_json": old.AutoBanExcludedUserIDsJSON}))
	})
	now := time.Now().Unix()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "capture_enabled": "true", "suspended": "false", "auto_ban_enabled": "true", "auto_ban_effective_at": fmt.Sprint(now - 10), "auto_ban_excluded_user_ids_json": "[102]", "auto_ban_include_staff": "false", "auto_ban_exclude_root": "true"}))
	state, err := model.ClaimAuditSecurity(ctx, db, "response-fixture")
	require.NoError(t, err)
	for _, tc := range []struct {
		id, role        int
		at              int64
		outcome, reason string
	}{
		{100, common.RoleCommonUser, now, "succeeded", ""},
		{101, common.RoleRootUser, now, "skipped", "protected_account"},
		{102, common.RoleCommonUser, now, "skipped", "excluded_account"},
		{103, common.RoleAdminUser, now, "skipped", "excluded_account"},
		{104, common.RoleCommonUser, now - 121, "skipped", "expired"},
	} {
		u := model.User{Id: tc.id, Username: fmt.Sprint("response-", tc.id), AffCode: fmt.Sprint(tc.id), Role: tc.role, Status: common.UserStatusEnabled, Quota: 777, UsedQuota: 23, Group: "plus", AuthVersion: 1}
		require.NoError(t, db.Create(&u).Error)
		e := compromiseEvent(fmt.Sprint(tc.id), tc.id, tc.at, "8.8.8.8", "auth.session_refresh_reuse")
		e.ActorRole = 0 // Verified replay identifies the account but has no trusted caller role.
		require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e}, state, operation_setting.GetAuditSecuritySetting()))
	}
	w := worker{db: db}
	require.NoError(t, w.processAccountResponses(ctx))
	for _, id := range []int{100, 101, 102, 103, 104} {
		var user model.User
		require.NoError(t, db.First(&user, id).Error)
		var action model.SecurityAction
		require.NoError(t, db.Where("target_user_id = ?", id).Take(&action).Error)
		if id == 100 {
			require.Equal(t, common.UserStatusDisabled, user.Status)
			require.Equal(t, int64(2), user.AuthVersion)
			require.Equal(t, "succeeded", action.Outcome)
		} else {
			require.Equal(t, common.UserStatusEnabled, user.Status)
			require.Equal(t, "skipped", action.Outcome)
		}
		require.Equal(t, 777, user.Quota)
		require.Equal(t, 23, user.UsedQuota)
		require.Equal(t, "plus", user.Group)
	}
	// A user's manual recovery must not be undone by replaying the same episode.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 100).Updates(map[string]any{"status": common.UserStatusEnabled, "auth_version": 3}).Error)
	require.NoError(t, w.processAccountResponses(ctx))
	var recovered model.User
	require.NoError(t, db.First(&recovered, 100).Error)
	require.Equal(t, common.UserStatusEnabled, recovered.Status)
	require.Equal(t, int64(3), recovered.AuthVersion)
}

func TestAccountResponseRecoversCacheWithoutReplayingBan(t *testing.T) {
	address := os.Getenv("AUDIT_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set AUDIT_TEST_REDIS_ADDR to an isolated Redis instance")
	}
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	ctx := context.Background()
	oldEnabled, oldRDB := common.RedisEnabled, common.RDB
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	require.NoError(t, client.Ping(ctx).Err())
	common.RedisEnabled = true
	id := 1700000000 + int(time.Now().UnixNano()%1000000)
	cacheKey := fmt.Sprintf("user:%d", id)
	t.Cleanup(func() {
		require.NoError(t, client.Del(ctx, cacheKey, "auth:user:version:{"+cacheKey+"}", "auth:user:profile:{"+cacheKey+"}", "auth:user:fence:{"+cacheKey+"}").Err())
		_ = client.Close()
		common.RedisEnabled, common.RDB = oldEnabled, oldRDB
	})
	user := model.User{Id: id, Username: "cache-response", AffCode: fmt.Sprint(id), Status: common.UserStatusDisabled, Role: common.RoleCommonUser, Quota: 777, AuthVersion: 2, ProfileVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	alert := model.SecurityAlert{ActorID: id, Version: 1}
	require.NoError(t, db.Create(&alert).Error)
	raw, err := common.Marshal(model.SecurityResponseResult{AuthVersion: 2})
	require.NoError(t, err)
	action := model.SecurityAction{AlertID: alert.ID, TargetUserID: id, Action: "disable_account", Source: "automatic", Outcome: "committed", Result: string(raw)}
	require.NoError(t, db.Create(&action).Error)
	broken := redis.NewClient(&redis.Options{MaxRetries: -1, Dialer: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("fixture Redis outage")
	}})
	defer broken.Close()
	common.RDB = broken
	require.NoError(t, processAccountResponse(ctx, db, action))
	require.NoError(t, db.First(&action, action.ID).Error)
	require.Equal(t, "sync_pending", action.Outcome)
	var unchanged model.User
	require.NoError(t, db.First(&unchanged, id).Error)
	require.Equal(t, int64(2), unchanged.AuthVersion)
	// Simulate a newer manual recovery and concurrent quota/profile updates.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{"status": common.UserStatusEnabled, "auth_version": 3, "profile_version": 2, "quota": 999}).Error)
	common.RDB = client
	require.NoError(t, client.HSet(ctx, cacheKey, "Quota", 123).Err())
	require.NoError(t, processAccountResponse(ctx, db, action))
	require.NoError(t, db.First(&action, action.ID).Error)
	require.Equal(t, "superseded", action.Outcome)
	require.Equal(t, "3", client.HGet(ctx, cacheKey, "AuthVersion").Val())
	require.Equal(t, "1", client.HGet(ctx, cacheKey, "Status").Val())
	require.Equal(t, "123", client.HGet(ctx, cacheKey, "Quota").Val(), "must preserve concurrently maintained cached quota")
	require.NoError(t, db.First(&unchanged, id).Error)
	require.Equal(t, 999, unchanged.Quota)
	require.Equal(t, int64(3), unchanged.AuthVersion)
}

func TestManualAccountResponseChecksCurrentOperatorAndReturnsReceipt(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	ctx := context.Background()
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	for _, user := range []model.User{
		{Id: 201, Username: "response-root", AffCode: "201", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 5},
		{Id: 202, Username: "response-customer", AffCode: "202", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 900},
		{Id: 203, Username: "response-customer-two", AffCode: "203", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1},
	} {
		require.NoError(t, db.Create(&user).Error)
	}
	state, err := model.ClaimAuditSecurity(ctx, db, "manual-response-test")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	for _, id := range []int{202, 203} {
		e := compromiseEvent(fmt.Sprint("replay-", id), id, time.Now().Unix(), "8.8.8.8", "auth.session_refresh_reuse")
		require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e}, state, cfg))
	}
	identity := model.AuthSessionIdentity{UserID: 201, UserAuthVersion: 5}
	var alert model.SecurityAlert
	require.NoError(t, db.Where("actor_id = ? AND rule = ?", 202, "session_reuse").Take(&alert).Error)
	first, err := CreateManualAccountResponse(ctx, db, common.RoleRootUser, identity, alert.ID, alert.Version, "first-click")
	require.NoError(t, err)
	require.Equal(t, "succeeded", first.Outcome)
	again, err := CreateManualAccountResponse(ctx, db, common.RoleRootUser, identity, alert.ID, alert.Version, "first-click")
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	var user model.User
	require.NoError(t, db.First(&user, 202).Error)
	require.Equal(t, int64(2), user.AuthVersion)
	require.Equal(t, 900, user.Quota)
	alert = model.SecurityAlert{}
	require.NoError(t, db.Where("actor_id = ? AND rule = ?", 203, "session_reuse").Take(&alert).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 201).Updates(map[string]any{"auth_version": 6}).Error)
	stale, err := CreateManualAccountResponse(ctx, db, common.RoleRootUser, identity, alert.ID, alert.Version, "stale-proof")
	require.NoError(t, err)
	require.Equal(t, "skipped", stale.Outcome)
	require.Equal(t, "forbidden", stale.ReasonCode)
	user = model.User{}
	require.NoError(t, db.First(&user, 203).Error)
	require.Equal(t, common.UserStatusEnabled, user.Status)
}
