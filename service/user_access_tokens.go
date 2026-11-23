package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
)

func ClearUserAccessTokens(ctx context.Context, operatorID, targetID int) (model.UserAccessTokenClearResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := model.ClearUserAccessTokens(ctx, operatorID, targetID)
	if errors.Is(err, model.ErrUserAccessTokenClearForbidden) {
		return result, ErrVerificationForbidden
	}
	return result, err
}
