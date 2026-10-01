package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	// ErrUserQuotaOutOfRange: the override does not fit users.quota, which is a
	// 32-bit int column on MySQL and PostgreSQL.
	ErrUserQuotaOutOfRange = errors.New("user quota out of range")
	// ErrUserQuotaPermission: the operator may not change this user's quota.
	ErrUserQuotaPermission = errors.New("cannot adjust quota for this user role")
)

// OverrideUserQuota sets a user's wallet balance to quota on behalf of an
// operator with operatorRole and returns the balance it replaced (the committed
// database value).
//
// The value is bounded to the column range, and the row is locked while the
// operator's permission over the user's current role is checked and the
// balance rewritten, so a concurrent promotion cannot slip in between the
// check and the write. The committed difference is then applied to the Redis
// user cache with the same atomic HINCRBY the add/subtract paths use, so the
// relay pre-consume (which reads the cached quota) sees the new balance at once
// instead of after the cache TTL. Applying a difference rather than writing the
// absolute value keeps relay deductions that are already in the cache but not
// yet flushed to the database (batch update) accounted for: once they flush,
// the database and the cache agree again.
func OverrideUserQuota(userId int, operatorRole int, quota int) (before int, err error) {
	if quota > math.MaxInt32 || quota < -math.MaxInt32 {
		return 0, ErrUserQuotaOutOfRange
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "quota", "role").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if operatorRole != common.RoleRootUser && operatorRole <= user.Role {
			return ErrUserQuotaPermission
		}
		before = user.Quota
		// An unchanged value is still a success; MySQL may report 0 affected
		// rows for it, so skip the write instead of checking RowsAffected.
		if quota == before {
			return nil
		}
		return tx.Model(&User{}).Where("id = ?", userId).Update("quota", quota).Error
	})
	if err != nil {
		return 0, err
	}
	if delta := int64(quota) - int64(before); delta != 0 {
		if err := cacheIncrUserQuota(userId, delta); err != nil {
			common.SysError(fmt.Sprintf("failed to sync quota override to user cache for user %d: %s", userId, err))
		}
	}
	return before, nil
}
