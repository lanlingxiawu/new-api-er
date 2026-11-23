package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrUserGroupRatiosConflict = errors.New("user group ratios changed concurrently")

type UserGroupRatiosChange struct {
	UserID   int
	Username string
	Before   string
	After    string
}

// UpdateUserGroupRatios writes only ratios and the metadata version. The
// expected value fences concurrent edits and background deleted-group cleanup.
func UpdateUserGroupRatios(ctx context.Context, userID, operatorRole int, expected, updated string) (*UserGroupRatiosChange, error) {
	if userID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if operatorRole < common.RoleAdminUser {
		return nil, ErrUserQuotaPermission
	}
	var change UserGroupRatiosChange
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "username", "role", "group_ratios").First(&user, userID).Error; err != nil {
			return err
		}
		if operatorRole != common.RoleRootUser && operatorRole <= user.Role {
			return ErrUserQuotaPermission
		}
		if user.GroupRatios != expected {
			return ErrUserGroupRatiosConflict
		}
		change = UserGroupRatiosChange{UserID: user.Id, Username: user.Username, Before: user.GroupRatios, After: updated}
		if user.GroupRatios == updated {
			return nil
		}
		return tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
			"group_ratios": updated, "profile_version": gorm.Expr("profile_version + ?", 1),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &change, nil
}
