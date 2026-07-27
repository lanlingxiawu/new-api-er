package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newModelMapContext(t *testing.T, mapping string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if mapping != "" {
		c.Set("model_mapping", mapping)
	}
	return c
}

func TestModelMappedHelper_NoMapping(t *testing.T) {
	c := newModelMapContext(t, "")
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-4o"}
	req := &dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	require.NoError(t, ModelMappedHelper(c, info, req))
	require.False(t, info.IsModelMapped)
	require.NotNil(t, info.ChannelMeta) // lazily allocated
}

func TestModelMappedHelper_EmptyMappingObject(t *testing.T) {
	c := newModelMapContext(t, "{}")
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-4o"}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.False(t, info.IsModelMapped)
}

func TestModelMappedHelper_SimpleMapping(t *testing.T) {
	c := newModelMapContext(t, `{"gpt-4o":"gpt-4o-real"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-4o"}
	req := &dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	require.NoError(t, ModelMappedHelper(c, info, req))
	require.True(t, info.IsModelMapped)
	require.Equal(t, "gpt-4o-real", info.UpstreamModelName)
	require.Equal(t, "gpt-4o-real", req.Model)
}

func TestModelMappedHelper_ChainedMapping(t *testing.T) {
	c := newModelMapContext(t, `{"a":"b","b":"c","c":"d"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "a"}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.True(t, info.IsModelMapped)
	require.Equal(t, "d", info.UpstreamModelName)
}

func TestModelMappedHelper_CycleDetected(t *testing.T) {
	// a -> b -> a forms a cycle between two distinct models.
	c := newModelMapContext(t, `{"a":"b","b":"a"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "a"}
	err := ModelMappedHelper(c, info, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
}

func TestModelMappedHelper_SelfMapOrigin(t *testing.T) {
	// origin maps to itself: not treated as mapped.
	c := newModelMapContext(t, `{"a":"a"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "a"}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.False(t, info.IsModelMapped)
}

func TestModelMappedHelper_SelfMapDownstream(t *testing.T) {
	// chain reaches a model that maps to itself (not the origin): mapped=true, break.
	c := newModelMapContext(t, `{"a":"b","b":"b"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "a"}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.True(t, info.IsModelMapped)
	require.Equal(t, "b", info.UpstreamModelName)
}

func TestModelMappedHelper_InvalidMappingJSON(t *testing.T) {
	c := newModelMapContext(t, `not json`)
	info := &relaycommon.RelayInfo{OriginModelName: "a"}
	err := ModelMappedHelper(c, info, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal_model_mapping_failed")
}

func TestModelMappedHelper_ResponsesCompact(t *testing.T) {
	suffix := ratio_setting.CompactModelSuffix
	origin := "gpt-4o" + suffix
	c := newModelMapContext(t, "")
	info := &relaycommon.RelayInfo{
		OriginModelName: origin,
		RelayMode:       relayconstant.RelayModeResponsesCompact,
	}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	// mapping trimmed to base then re-suffixed
	require.Equal(t, "gpt-4o", info.UpstreamModelName)
	require.Equal(t, ratio_setting.WithCompactModelSuffix("gpt-4o"), info.OriginModelName)
}

func TestModelMappedHelper_ResponsesCompactWithMapping(t *testing.T) {
	suffix := ratio_setting.CompactModelSuffix
	origin := "gpt-4o" + suffix
	c := newModelMapContext(t, `{"gpt-4o":"gpt-4o-upstream"}`)
	info := &relaycommon.RelayInfo{
		OriginModelName: origin,
		RelayMode:       relayconstant.RelayModeResponsesCompact,
	}
	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.Equal(t, "gpt-4o-upstream", info.UpstreamModelName)
}
