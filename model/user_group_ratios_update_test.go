package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestUpdateUserGroupRatiosOnlyChangesRatiosAndChecksConcurrentState(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&User{}))
	user := User{Username: "ratio-patch-" + common.GetRandomString(10), DisplayName: "keep", Password: "keep-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 123, GroupRatios: "{}", ProfileVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	t.Cleanup(func() { db.Unscoped().Delete(&user) })
	_, err := UpdateUserGroupRatios(context.Background(), 0, common.RoleAdminUser, "{}", `{"default":2}`)
	require.Error(t, err, "zero must not select the first user")
	change, err := UpdateUserGroupRatios(context.Background(), user.Id, common.RoleAdminUser, "{}", `{"default":0}`)
	require.NoError(t, err)
	require.Equal(t, "{}", change.Before)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, user.Username, stored.Username)
	require.Equal(t, user.DisplayName, stored.DisplayName)
	require.Equal(t, user.Password, stored.Password)
	require.Equal(t, user.Quota, stored.Quota)
	require.Equal(t, user.Group, stored.Group)
	require.Equal(t, user.ProfileVersion+1, stored.ProfileVersion)
	_, err = UpdateUserGroupRatios(context.Background(), user.Id, common.RoleAdminUser, "{}", `{"default":2}`)
	require.ErrorIs(t, err, ErrUserGroupRatiosConflict)
	_, err = UpdateUserGroupRatios(context.Background(), user.Id, common.RoleAdminUser, stored.GroupRatios, stored.GroupRatios)
	require.NoError(t, err, "unchanged writes are successful on all dialects")
	require.NoError(t, db.Model(&user).Update("role", common.RoleAdminUser).Error)
	_, err = UpdateUserGroupRatios(context.Background(), user.Id, common.RoleAdminUser, stored.GroupRatios, "{}")
	require.ErrorIs(t, err, ErrUserQuotaPermission)
	require.NoError(t, db.Delete(&user).Error)
	_, err = UpdateUserGroupRatios(context.Background(), user.Id, common.RoleRootUser, stored.GroupRatios, "{}")
	require.Error(t, err)
}
