package auditsecurity

import (
	"context"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLegacyCloudProvidersNeverMakeRequests(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, kind := range []string{operation_setting.IPProviderIPAPI, operation_setting.IPProviderIPInfoLite, operation_setting.IPProviderProxycheck} {
		p := operation_setting.IPIntelligenceProvider{Type: kind, Endpoint: server.URL, AllowedPrivateHosts: []string{"127.0.0.1"}}
		_, failure, _ := callIPProvider(context.Background(), p, "8.8.8.8", 2)
		require.Equal(t, "invalid_provider", failure)
	}
	require.Zero(t, calls.Load())
}

func TestEmptyProviderResponseIsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	p := operation_setting.IPIntelligenceProvider{Type: operation_setting.IPProviderCustomIPAPI, Endpoint: server.URL, AllowedPrivateHosts: []string{"127.0.0.1"}}
	_, failure, _ := callIPProvider(context.Background(), p, "8.8.8.8", 2)
	require.Equal(t, "invalid_response", failure)
}
