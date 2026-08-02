package common

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrLegacyAdaptorNotImplementedContract(t *testing.T) {
	require.EqualError(t, ErrLegacyAdaptorNotImplemented, "implement me")
	require.True(t, errors.Is(ErrLegacyAdaptorNotImplemented, ErrLegacyAdaptorNotImplemented))
}
