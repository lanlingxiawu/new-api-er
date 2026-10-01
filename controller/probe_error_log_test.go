package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelErrorLogRetainsProbeRouting(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set("channel_id", 42)
	ctx.Set(service.ProbeRoutingContextKey, &service.RequestProbeRouting{InputChars: 14, Group: "probe-test"})
	err := types.NewOpenAIError(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
	other := channelErrorLogOther(ctx, err)
	admin, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, map[string]interface{}{
		"rule": "short-text-v1", "input_chars": 14, "group": "probe-test", "channel_id": 42,
	}, admin["probe_routing"])
}
