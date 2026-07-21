package ai360

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ai360 exposes only channel metadata (no adaptor logic in this package).
func TestChannelName(t *testing.T) {
	assert.Equal(t, "ai360", ChannelName)
}

func TestModelList(t *testing.T) {
	// ModelList is intentionally empty for this channel.
	assert.Empty(t, ModelList)
}
