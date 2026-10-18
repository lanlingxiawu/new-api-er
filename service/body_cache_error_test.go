package service

import (
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestBodyCacheCapacityError(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	err := NewBodyCacheCapacityError(c)
	require.Equal(t, 503, err.StatusCode)
	require.True(t, types.IsSkipRetryError(err))
	require.Equal(t, BodyCacheCapacityErrorCode, err.GetErrorCode())
}
