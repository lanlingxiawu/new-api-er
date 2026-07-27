package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// NormalizeCacheCreationSplit folds any unattributed cache-creation tokens
// (total minus the explicit 5m and 1h buckets) back into the 5m bucket, and
// clamps a negative remainder to zero.

func TestNormalizeCacheCreationSplit(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		tokens5m int
		tokens1h int
		want5m   int
		want1h   int
	}{
		{"remainder folds into 5m", 100, 20, 30, 70, 30},    // remainder 50 -> 20+50
		{"exact split, zero remainder", 50, 20, 30, 20, 30}, // boundary remainder == 0
		{"negative remainder clamped", 10, 8, 5, 8, 5},      // remainder -3 -> 0
		{"all zero", 0, 0, 0, 0, 0},
		{"only 1h known", 40, 0, 10, 30, 10},  // remainder 30 -> 0+30
		{"total equals 5m", 20, 20, 0, 20, 0}, // remainder 0
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got5m, got1h := NormalizeCacheCreationSplit(tt.total, tt.tokens5m, tt.tokens1h)
			assert.Equal(t, tt.want5m, got5m)
			assert.Equal(t, tt.want1h, got1h)
		})
	}
}
