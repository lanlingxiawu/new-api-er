package lingyiwanwu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// lingyiwanwu exposes only channel metadata (no adaptor logic in this package).
func TestChannelName(t *testing.T) {
	assert.Equal(t, "lingyiwanwu", ChannelName)
}

func TestModelList(t *testing.T) {
	assert.NotEmpty(t, ModelList)
	assert.Contains(t, ModelList, "yi-large")
}
