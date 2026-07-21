package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// errors.go declares only sentinel errors. Verify they are non-nil, carry the
// documented message, are mutually distinct, and interoperate with
// errors.Is / %w wrapping as callers rely on.

func TestErrors_SentinelMessages(t *testing.T) {
	cases := []struct {
		err error
		msg string
	}{
		{ErrDatabase, "database error"},
		{ErrInvalidCredentials, "invalid credentials"},
		{ErrUserEmptyCredentials, "empty credentials"},
		{ErrEmailAlreadyTaken, "email already taken"},
		{ErrEmailNotFound, "email not found"},
		{ErrEmailAmbiguous, "email matches multiple users"},
		{ErrTokenNotProvided, "token not provided"},
		{ErrTokenInvalid, "token invalid"},
		{ErrRedeemFailed, "redeem.failed"},
		{ErrTwoFANotEnabled, "2fa not enabled"},
	}
	for _, c := range cases {
		assert.Error(t, c.err)
		assert.Equal(t, c.msg, c.err.Error())
	}
}

func TestErrors_MutuallyDistinct(t *testing.T) {
	all := []error{
		ErrDatabase,
		ErrInvalidCredentials,
		ErrUserEmptyCredentials,
		ErrEmailAlreadyTaken,
		ErrEmailNotFound,
		ErrEmailAmbiguous,
		ErrTokenNotProvided,
		ErrTokenInvalid,
		ErrRedeemFailed,
		ErrTwoFANotEnabled,
	}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			assert.Falsef(t, errors.Is(all[i], all[j]),
				"sentinel %d and %d must be distinct", i, j)
		}
	}
}

func TestErrors_WrappingRoundTrip(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrEmailNotFound)
	assert.True(t, errors.Is(wrapped, ErrEmailNotFound))
	assert.False(t, errors.Is(wrapped, ErrEmailAmbiguous))
	// identity holds against itself
	assert.True(t, errors.Is(ErrTokenInvalid, ErrTokenInvalid))
}
