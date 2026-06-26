package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestThirdPartySD2ChannelMappings(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeThirdPartySD2)
	require.True(t, ok)
	require.Equal(t, constant.APITypeOpenAI, apiType)

	endpoints := GetEndpointTypesByChannelType(constant.ChannelTypeThirdPartySD2, "dreamina-seedance-2-0-260128")
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, endpoints)
}
