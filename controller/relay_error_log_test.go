package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The relay error log records the channel in the channel_id column only; the
// id/name/type no longer go into other, where every reader had to strip them.
// Admins keep the retry chain in admin_info.
func TestChannelErrorLogOther_ChannelOnlyInColumn(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("channel_id", 101)
	c.Set("channel_name", "Azure-JP-secret")
	c.Set("channel_type", 3)
	c.Set("use_channel", []string{"101"})
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	other := channelErrorLogOther(c, apiErr)

	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, other, key)
	}
	assert.Equal(t, "/v1/chat/completions", other["request_path"])
	assert.Equal(t, http.StatusBadGateway, other["status_code"])
	assert.Equal(t, types.ErrorCodeBadResponseStatusCode, other["error_code"])
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []string{"101"}, adminInfo["use_channel"])
	assert.NotContains(t, adminInfo, "is_multi_key")
}
