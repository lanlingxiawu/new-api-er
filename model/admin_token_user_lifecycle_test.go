package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHardDeleteRemovesAuthorizationAndChannelOwnership(t *testing.T) {
	for _, test := range []struct {
		name          string
		username      string
		injectFailure bool
	}{
		{name: "committed deletion", username: "hard-delete-owner-success"},
		{name: "cleanup failure rolls back", username: "hard-delete-owner-rollback", injectFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newAdminTokenModelDB(t)
			require.NoError(t, db.AutoMigrate(
				&User{}, &UserAccessToken{}, &UserChannelExclusion{}, &TwoFABackupCode{}, &TwoFA{},
				&UserSession{}, &UserLoginProtection{}, &AuthFlow{}, &PasskeyCredential{}, &Token{}, &UserOAuthBinding{},
				&ExternalIdentityClaim{}, &Channel{}, &Ability{}, &ChannelCreator{}, &CasbinRule{},
			))
			user := User{Username: test.username, Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
			require.NoError(t, db.Create(&user).Error)
			channels := []Channel{{Name: "owned-before-delete", Key: "secret", Models: "m", Group: "default"}}
			require.NoError(t, BatchInsertChannels(channels, user.Id))
			rule := CasbinRule{Ptype: "p", V0: fmt.Sprintf("user:%d", user.Id), V1: "admin_access_token", V2: "use", V3: "allow"}
			require.NoError(t, db.Create(&rule).Error)
			t.Cleanup(func() {
				db.Where("v0 = ?", rule.V0).Delete(&CasbinRule{})
				db.Where("channel_id = ?", channels[0].Id).Delete(&ChannelCostConfig{})
				db.Where("channel_id = ?", channels[0].Id).Delete(&Ability{})
				db.Where("channel_id = ?", channels[0].Id).Delete(&ChannelCreator{})
				db.Unscoped().Delete(&Channel{}, channels[0].Id)
				db.Unscoped().Delete(&User{}, user.Id)
			})

			if test.injectFailure {
				require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("test:hard_delete_creator_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "channel_creators" {
						tx.AddError(errors.New("injected creator cleanup failure"))
					}
				}))
			}
			_, err := user.HardDelete()
			if test.injectFailure {
				require.ErrorContains(t, err, "injected creator cleanup failure")
				var stored User
				require.NoError(t, db.First(&stored, user.Id).Error)
				var creators, rules int64
				require.NoError(t, db.Model(&ChannelCreator{}).Where("creator_user_id = ?", user.Id).Count(&creators).Error)
				require.NoError(t, db.Model(&CasbinRule{}).Where("v0 = ?", rule.V0).Count(&rules).Error)
				require.EqualValues(t, 1, creators)
				require.EqualValues(t, 1, rules)
				return
			}

			require.NoError(t, err)
			var creators, rules int64
			require.NoError(t, db.Model(&ChannelCreator{}).Where("creator_user_id = ?", user.Id).Count(&creators).Error)
			require.NoError(t, db.Model(&CasbinRule{}).Where("v0 = ?", rule.V0).Count(&rules).Error)
			require.Zero(t, creators)
			require.Zero(t, rules)
			var storedChannel Channel
			require.NoError(t, db.First(&storedChannel, channels[0].Id).Error, "deleting the user must not delete or transfer the channel")

			reused := User{Id: user.Id, Username: test.username + "-reused", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
			require.NoError(t, db.Create(&reused).Error)
			var inherited int64
			require.NoError(t, ChannelsCreatedBy(db, reused.Id).Count(&inherited).Error)
			require.Zero(t, inherited)
			require.NoError(t, db.Model(&CasbinRule{}).Where("v0 = ?", rule.V0).Count(&rules).Error)
			require.Zero(t, rules)
		})
	}
}
