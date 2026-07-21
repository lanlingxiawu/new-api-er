package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGrokSettings_ReturnsPointerToGlobal(t *testing.T) {
	require.Same(t, &grokSettings, GetGrokSettings())
}

func TestGetGrokSettings_Defaults(t *testing.T) {
	got := GetGrokSettings()
	assert.True(t, got.ViolationDeductionEnabled)
	assert.InDelta(t, 0.05, got.ViolationDeductionAmount, 1e-9)
}
