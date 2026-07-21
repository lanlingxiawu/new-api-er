package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetTrustQuota(t *testing.T) {
	assert.Equal(t, int(10*QuotaPerUnit), GetTrustQuota())
}

func TestQuotaToUSD(t *testing.T) {
	// Normal conversion with the configured unit.
	assert.InDelta(t, float64(500000)/QuotaPerUnit, QuotaToUSD(500000), 1e-9)
	assert.Equal(t, 0.0, QuotaToUSD(0))
}

func TestQuotaToUSD_ZeroUnitGuard(t *testing.T) {
	orig := QuotaPerUnit
	QuotaPerUnit = 0
	defer func() { QuotaPerUnit = orig }()
	assert.Equal(t, 0.0, QuotaToUSD(12345), "QuotaPerUnit==0 must not divide by zero")
}
