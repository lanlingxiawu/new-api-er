package authz

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestAdminTokenPermissionsRequireExplicitGrantAndReadCurrentDatabase(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	p := Permission{Resource: ResourceAdminAccessToken, Action: ActionUse}
	read := func() bool {
		grants, err := ReadCurrentPermissions(context.Background(), 42, common.RoleAdminUser, []Permission{p, ChannelRead})
		require.NoError(t, err)
		require.True(t, grants[ChannelRead])
		return grants[p]
	}
	require.False(t, read())
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: p.Resource, V2: p.Action, V3: EffectAllow}).Error)
	require.False(t, read(), "role-wide grants must not enable management tokens")
	grant := model.CasbinRule{Ptype: "p", V0: UserSubject(42), V1: p.Resource, V2: p.Action, V3: EffectAllow}
	require.NoError(t, db.Create(&grant).Error)
	require.True(t, read(), "must not depend on the local enforcer snapshot")
	require.NoError(t, db.Model(&grant).Update("v3", EffectDeny).Error)
	require.False(t, read(), "revocation must be visible without reloading Casbin")
	require.False(t, Can(42, common.RoleRootUser, p))
	require.False(t, Can(42, common.RoleCommonUser, p))
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: UserSubject(42), V1: ChannelRead.Resource, V2: ChannelRead.Action, V3: EffectAllow, V4: "own"}).Error)
	grants, err := ReadCurrentPermissions(context.Background(), 42, common.RoleAdminUser, []Permission{ChannelRead})
	require.NoError(t, err)
	require.False(t, grants[ChannelRead], "legacy constraints cannot turn into unrestricted primary-database grants")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadCurrentPermissions(ctx, 42, common.RoleAdminUser, []Permission{p})
	require.Error(t, err)
}

func TestAdminTokenPartialPermissionUpdatePreservesOtherActions(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	require.NoError(t, SetUserPermissionsInTx(db, 42, PermissionsMap{ResourceAdminAccessToken: {ActionUse: true, ActionUsersList: true, ActionChannelsOwned: true}}))
	require.NoError(t, SetUserPermissionsInTx(db, 42, PermissionsMap{ResourceAdminAccessToken: {ActionUsersList: false}}))
	require.NoError(t, ReloadPolicy())
	require.True(t, Can(42, common.RoleAdminUser, Permission{Resource: ResourceAdminAccessToken, Action: ActionUse}))
	require.False(t, Can(42, common.RoleAdminUser, Permission{Resource: ResourceAdminAccessToken, Action: ActionUsersList}))
	require.True(t, Can(42, common.RoleAdminUser, Permission{Resource: ResourceAdminAccessToken, Action: ActionChannelsOwned}))
	_, err := UpdateUserPermissionsInTx(db, 42, PermissionsMap{ResourceAdminAccessToken: {"unknown": true}})
	require.Error(t, err)
}

func TestCurrentPermissionsMatchSnapshotForLegacyRules(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	for index, tc := range []struct {
		name, effect, scope, extra string
		denyConflict, want         bool
	}{
		{name: "allow", effect: EffectAllow, want: true},
		{name: "deny", effect: EffectDeny},
		{name: "legacy_empty", want: true},
		{name: "legacy_all", scope: "all", want: true},
		{name: "legacy_own", effect: EffectAllow, scope: "own"},
		{name: "legacy_other", effect: EffectAllow, scope: "other"},
		{name: "legacy_extra", effect: EffectAllow, scope: "all", extra: "restricted"},
		{name: "conflict", effect: EffectAllow, denyConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := 700 + index
			rule := model.CasbinRule{Ptype: "p", V0: UserSubject(userID), V1: ChannelRead.Resource,
				V2: ChannelRead.Action, V3: tc.effect, V4: tc.scope, V5: tc.extra}
			require.NoError(t, db.Create(&rule).Error)
			if tc.denyConflict {
				require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: rule.V0,
					V1: rule.V1, V2: rule.V2, V3: EffectDeny}).Error)
			}
			require.NoError(t, ReloadPolicy())
			for _, role := range []int{common.RoleAdminUser, common.RoleRootUser, common.RoleCommonUser} {
				grants, err := ReadCurrentPermissions(context.Background(), userID, role, []Permission{ChannelRead})
				require.NoError(t, err)
				want := role == common.RoleRootUser || (role == common.RoleAdminUser && tc.want)
				require.Equal(t, want, grants[ChannelRead])
				require.Equal(t, want, Can(userID, role, ChannelRead))
			}
		})
	}
}
