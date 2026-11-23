package model

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrUserAccessTokenClearForbidden = errors.New("access token clearing is forbidden")

type UserAccessTokenClearResult struct {
	RevokedCount  int64 `json:"revoked_count"`
	LegacyRevoked bool  `json:"-"`
}

// ClearUserAccessTokens serializes with token creation and commits scoped and
// legacy credential revocation together. It never changes browser sessions.
func ClearUserAccessTokens(ctx context.Context, operatorID, targetID int) (UserAccessTokenClearResult, error) {
	if operatorID <= 0 || targetID <= 0 || operatorID == targetID {
		return UserAccessTokenClearResult{}, ErrUserAccessTokenClearForbidden
	}
	var result UserAccessTokenClearResult
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use a deterministic order for the two users; all token operations lock
		// users before user_access_tokens. Do not hold these locks across Redis.
		ids := []int{min(operatorID, targetID), max(operatorID, targetID)}
		var operator, target User
		for _, id := range ids {
			user := &operator
			columns := []string{"id", "role", "status", "username"}
			if id == targetID {
				user = &target
				columns = append(columns, "access_token", "access_token_created_at")
			}
			if err := lockForUpdate(tx).Select(columns).First(user, id).Error; err != nil {
				return err
			}
		}
		if operator.Status != common.UserStatusEnabled || operator.Role < common.RoleAdminUser || operator.Role <= target.Role {
			return ErrUserAccessTokenClearForbidden
		}
		count, err := DeleteUserAccessTokensWithTx(tx, targetID)
		if err != nil {
			return err
		}
		result.RevokedCount = count
		result.LegacyRevoked = strings.TrimSpace(target.GetAccessToken()) != ""
		if target.AccessToken != nil || target.AccessTokenCreatedAt != nil {
			if err := tx.Model(&User{}).Where("id = ?", targetID).
				Updates(map[string]any{"access_token": nil, "access_token_created_at": nil}).Error; err != nil {
				return err
			}
		}
		if result.LegacyRevoked {
			result.RevokedCount++
		}
		return nil
	})
	if err != nil {
		// A rollback or unknown commit outcome is never a confirmed count.
		return UserAccessTokenClearResult{}, err
	}
	return result, nil
}
