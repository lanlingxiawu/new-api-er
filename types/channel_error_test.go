package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewChannelError_MapsAllFields(t *testing.T) {
	// Note the constructor parameter order: (id, type, name, isMultiKey, usingKey, autoBan).
	// The usingKey argument comes BEFORE autoBan, but the struct field order is AutoBan then UsingKey.
	ce := NewChannelError(42, 7, "my-channel", true, "sk-secret", true)

	require.NotNil(t, ce)
	require.Equal(t, 42, ce.ChannelId)
	require.Equal(t, 7, ce.ChannelType)
	require.Equal(t, "my-channel", ce.ChannelName)
	require.True(t, ce.IsMultiKey)
	require.Equal(t, "sk-secret", ce.UsingKey)
	require.True(t, ce.AutoBan)
}

func TestNewChannelError_FalseBooleans(t *testing.T) {
	ce := NewChannelError(0, 0, "", false, "", false)

	require.Equal(t, 0, ce.ChannelId)
	require.Equal(t, 0, ce.ChannelType)
	require.Equal(t, "", ce.ChannelName)
	require.False(t, ce.IsMultiKey)
	require.Equal(t, "", ce.UsingKey)
	require.False(t, ce.AutoBan)
}

// Guards against a swapped isMultiKey/autoBan wiring: the two bool args must
// land in the correct struct fields.
func TestNewChannelError_BooleanArgWiring(t *testing.T) {
	ce := NewChannelError(1, 2, "c", true, "k", false)
	require.True(t, ce.IsMultiKey, "4th arg maps to IsMultiKey")
	require.False(t, ce.AutoBan, "6th arg maps to AutoBan")

	ce2 := NewChannelError(1, 2, "c", false, "k", true)
	require.False(t, ce2.IsMultiKey)
	require.True(t, ce2.AutoBan)
}
