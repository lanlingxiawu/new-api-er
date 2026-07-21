package meta

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
)

// RelayInfoChannelType / RelayInfoUpstreamModelName safely read fields promoted
// from the embedded *ChannelMeta, returning zero values when info or ChannelMeta
// is nil (accessing promoted fields on a nil ChannelMeta would otherwise panic).

func TestRelayInfoChannelType_NilInfo(t *testing.T) {
	assert.Equal(t, 0, RelayInfoChannelType(nil))
}

func TestRelayInfoChannelType_NilChannelMeta(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	assert.Nil(t, info.ChannelMeta)
	assert.Equal(t, 0, RelayInfoChannelType(info))
}

func TestRelayInfoChannelType_Populated(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42}}
	assert.Equal(t, 42, RelayInfoChannelType(info))
}

func TestRelayInfoUpstreamModelName_NilInfo(t *testing.T) {
	assert.Equal(t, "", RelayInfoUpstreamModelName(nil))
}

func TestRelayInfoUpstreamModelName_NilChannelMeta(t *testing.T) {
	assert.Equal(t, "", RelayInfoUpstreamModelName(&relaycommon.RelayInfo{}))
}

func TestRelayInfoUpstreamModelName_Populated(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro"}}
	assert.Equal(t, "gemini-2.5-pro", RelayInfoUpstreamModelName(info))
}
