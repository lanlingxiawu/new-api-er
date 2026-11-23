package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type UserAPIKeyDeletionResult struct {
	DeletedCount int64 `json:"deleted_count"`
	Completed    bool  `json:"completed"`
	UpperID      int   `json:"-"`
}

// AuthorizeUserCredentialDeletion reads current roles rather than cached menu state.
func AuthorizeUserCredentialDeletion(ctx context.Context, operatorID, targetID int) (*model.User, error) {
	var operator, target model.User
	if err := model.DB.WithContext(ctx).Select("id", "role", "status").First(&operator, operatorID).Error; err != nil {
		return nil, err
	}
	if err := model.DB.WithContext(ctx).Select("id", "role", "username").First(&target, targetID).Error; err != nil {
		return nil, err
	}
	if operator.Status != common.UserStatusEnabled || operator.Role < common.RoleAdminUser || operator.Role <= target.Role {
		return nil, ErrVerificationForbidden
	}
	return &target, nil
}

func ClearUserAPIKeys(ctx context.Context, operatorID, targetID int) (UserAPIKeyDeletionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result := UserAPIKeyDeletionResult{}
	var err error
	result.UpperID, err = model.UserTokenDeletionUpperBound(ctx, targetID)
	if err != nil {
		return result, err
	}
	for {
		if _, err := AuthorizeUserCredentialDeletion(ctx, operatorID, targetID); err != nil {
			return result, err
		}
		count, done, err := model.DeleteUserTokensBatch(ctx, operatorID, targetID, result.UpperID)
		result.DeletedCount += count
		if errors.Is(err, model.ErrUserAPIKeyClearForbidden) {
			return result, ErrVerificationForbidden
		}
		if err != nil {
			return result, err
		}
		if done {
			result.Completed = true
			return result, nil
		}
	}
}
