package openrouter

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestReasoning_EffortRoundTrip(t *testing.T) {
	r := RequestReasoning{Enabled: true, Effort: "high", Exclude: true}
	b, err := common.Marshal(r)
	require.NoError(t, err)
	// effort present, max_tokens omitted (omitempty, zero value)
	assert.Contains(t, string(b), `"effort":"high"`)
	assert.NotContains(t, string(b), "max_tokens")
	assert.Contains(t, string(b), `"exclude":true`)

	var back RequestReasoning
	require.NoError(t, common.Unmarshal(b, &back))
	assert.Equal(t, r, back)
}

func TestRequestReasoning_MaxTokensRoundTrip(t *testing.T) {
	r := RequestReasoning{Enabled: true, MaxTokens: 2048}
	b, err := common.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"max_tokens":2048`)
	assert.NotContains(t, string(b), "effort")
	// exclude defaults false -> omitted
	assert.NotContains(t, string(b), "exclude")

	var back RequestReasoning
	require.NoError(t, common.Unmarshal(b, &back))
	assert.Equal(t, 2048, back.MaxTokens)
}

func TestOpenRouterEnterpriseResponse_RoundTrip(t *testing.T) {
	raw := []byte(`{"data":{"balance":10},"success":true}`)
	var resp OpenRouterEnterpriseResponse
	require.NoError(t, common.Unmarshal(raw, &resp))
	assert.True(t, resp.Success)
	assert.JSONEq(t, `{"balance":10}`, string(resp.Data))

	// marshal back
	out, err := common.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"success":true`)
}

func TestChannelMetadata(t *testing.T) {
	assert.Equal(t, "openrouter", ChannelName)
	assert.NotNil(t, ModelList) // empty but non-nil slice
}
