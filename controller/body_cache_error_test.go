package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestBodyCacheCapacityDoesNotRetryOrDisableChannel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	err := service.NewBodyCacheCapacityError(c)
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() { common.AutomaticDisableChannelEnabled = old }()
	require.False(t, shouldRetry(c, err, 10))
	require.False(t, service.ShouldDisableChannel(err))
	require.NotPanics(t, func() { processChannelError(c, types.ChannelError{AutoBan: true}, err, nil) })
}
