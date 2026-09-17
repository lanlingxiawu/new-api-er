package constant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetChannelTypeName / GetChannelBaseURL are the behavioral functions in the
// constant package; everything else is enum/table declarations (not unit-tested
// per Rule 15.6), except the persisted channel-type numbers pinned below.

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

func TestGetChannelBaseURLIsBoundsSafe(t *testing.T) {
	assert.Empty(t, GetChannelBaseURL(ChannelTypeTaskPlugin))
	assert.Empty(t, GetChannelBaseURL(9999))
	assert.Empty(t, GetChannelBaseURL(-1))
}

// Channel type numbers are persisted in channels.type; this fork's numbering
// diverges from upstream from 58 onward and must never shift.
func TestChannelTypeNumbersArePinned(t *testing.T) {
	assert.Equal(t, 58, ChannelTypeThirdPartySD2)
	assert.Equal(t, 59, ChannelTypeAdvancedCustom)
	assert.Equal(t, 60, ChannelTypeSub2API)
	assert.Equal(t, 61, ChannelTypeNewAPI)
	assert.Equal(t, 62, ChannelTypeTaskPlugin)
	assert.Equal(t, 63, ChannelTypeVLLM)
	assert.Equal(t, 64, ChannelTypeSGLang)
	assert.Equal(t, ChannelTypeSGLang, ChannelTypeDummy, "Dummy repeats the last type; callers iterate with <=")
}

func TestChannelBaseURLsCoverEveryChannelType(t *testing.T) {
	assert.Len(t, ChannelBaseURLs, ChannelTypeDummy+1,
		"ChannelBaseURLs must have exactly one entry per channel type index")
	assert.Equal(t, "https://model.service-inference.ai", GetChannelBaseURL(ChannelTypeThirdPartySD2))
}
