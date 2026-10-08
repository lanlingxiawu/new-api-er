package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProbeRoutingAdminMetadata(t *testing.T) {
	require.Nil(t, GetRequestProbeRouting(nil))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	admin := map[string]interface{}{}
	AppendProbeRoutingAdminInfo(ctx, admin)
	require.Empty(t, admin)
	ctx.Set(ProbeRoutingContextKey, &RequestProbeRouting{InputChars: 12, Group: "test"})
	ctx.Set("channel_id", 42)
	AppendProbeRoutingAdminInfo(ctx, admin)
	require.Equal(t, map[string]interface{}{"rule": "short-text-v1", "input_chars": 12, "group": "test", "channel_id": 42}, admin["probe_routing"])
	channel, found := GetPreferredChannelByAffinity(ctx, "model", "test")
	require.False(t, found)
	require.Zero(t, channel)
}
