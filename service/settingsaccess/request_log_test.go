package settingsaccess

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRequestLogRestartOptionScope(t *testing.T) {
	require.True(t, AllowsOption("operations.request-log", "request_log_setting.clear_on_restart"))
	require.False(t, AllowsOption("operations.performance", "request_log_setting.clear_on_restart"))
}
