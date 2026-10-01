package settingsaccess

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProbeRoutingScope(t *testing.T) {
	require.True(t, AllowsOption("models.routing-reliability", "probe_routing_setting.max_input_chars"))
	require.True(t, AllowsGroup("models.routing-reliability", "probe_routing_setting", map[string]string{"max_input_chars": "128"}))
	require.False(t, AllowsGroup("models.routing-reliability", "probe_routing_setting", map[string]string{"enabled": "true"}))
	require.False(t, AllowsOption("site.notice", "probe_routing_setting.max_input_chars"))
}
