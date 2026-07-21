package constant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetChannelTypeName is the only behavioral function in the constant package;
// everything else is enum/table declarations (not unit-tested per Rule 15.6).

func TestGetChannelTypeName_KnownTypesMapToTheirName(t *testing.T) {
	require.NotEmpty(t, ChannelTypeNames, "the channel-type name table must be populated")
	for channelType, name := range ChannelTypeNames {
		assert.Equal(t, name, GetChannelTypeName(channelType),
			"registered channel type %d must resolve to its table name", channelType)
	}
}

func TestGetChannelTypeName_UnknownTypeReturnsUnknown(t *testing.T) {
	unknown := -1
	_, exists := ChannelTypeNames[unknown]
	require.False(t, exists, "-1 must not be a registered channel type")
	assert.Equal(t, "Unknown", GetChannelTypeName(unknown))
}
