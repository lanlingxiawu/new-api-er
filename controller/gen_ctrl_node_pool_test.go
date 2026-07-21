package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// node_pool.go: base-url resolution and the reverse-proxy service-unavailable guards.

func TestGetNodeControlBaseUrl_TrimsTrailingSlash(t *testing.T) {
	prev := system_setting.NodeControlServiceUrl
	t.Cleanup(func() { system_setting.NodeControlServiceUrl = prev })

	system_setting.NodeControlServiceUrl = "https://ctl.example.test/"
	assert.Equal(t, "https://ctl.example.test", getNodeControlBaseUrl())

	system_setting.NodeControlServiceUrl = ""
	assert.Equal(t, "", getNodeControlBaseUrl())
}

func TestProxyNodePoolRequest_UnconfiguredBaseUrl(t *testing.T) {
	prev := system_setting.NodeControlServiceUrl
	t.Cleanup(func() { system_setting.NodeControlServiceUrl = prev })
	system_setting.NodeControlServiceUrl = ""

	ctx, rec := newCtx(t, "GET", "/api/node-pool/nodes", nil)
	GetNodePoolNodes(ctx)
	assert.Equal(t, 503, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, false, out["success"])
}

func TestProxyNodePoolRequest_MtlsNotConfigured(t *testing.T) {
	// Base URL set but the mTLS client cannot be built (no cert env) -> 503.
	prev := system_setting.NodeControlServiceUrl
	t.Cleanup(func() { system_setting.NodeControlServiceUrl = prev })
	system_setting.NodeControlServiceUrl = "https://ctl.example.test"

	// Only exercise this when the process-wide client was not already built
	// with valid certs; getNodePoolClient memoizes via sync.Once.
	if _, err := getNodePoolClient(); err == nil {
		t.Skip("node pool mTLS client is configured in this environment; skipping unconfigured-path assertion")
	}

	ctx, rec := newCtx(t, "GET", "/api/node-pool/nodes", nil)
	GetNodePoolNodes(ctx)
	assert.Equal(t, 503, rec.Code)
}
