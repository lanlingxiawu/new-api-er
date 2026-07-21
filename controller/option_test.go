package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// isPaymentComplianceOptionKey: prefix match on payment_setting.compliance_*.
func TestIsPaymentComplianceOptionKey(t *testing.T) {
	assert.True(t, isPaymentComplianceOptionKey("payment_setting.compliance_confirmed"))
	assert.True(t, isPaymentComplianceOptionKey("payment_setting.compliance_terms_version"))
	assert.False(t, isPaymentComplianceOptionKey("payment_setting.amount_options"))
	assert.False(t, isPaymentComplianceOptionKey("compliance_confirmed"))
}

// isPositiveOptionValue: accepts positive ints and floats; rejects zero,
// negatives, and non-numeric strings (equivalence partitioning).
func TestIsPositiveOptionValue(t *testing.T) {
	assert.True(t, isPositiveOptionValue("1"))
	assert.True(t, isPositiveOptionValue("  42 "))
	assert.True(t, isPositiveOptionValue("0.5"))
	assert.False(t, isPositiveOptionValue("0"))
	assert.False(t, isPositiveOptionValue("-3"))
	assert.False(t, isPositiveOptionValue("-0.1"))
	assert.False(t, isPositiveOptionValue("abc"))
	assert.False(t, isPositiveOptionValue(""))
}

// isIntInRange: boundary value analysis at and just outside [min,max].
func TestIsIntInRange(t *testing.T) {
	assert.False(t, isIntInRange("-1", 0, 10)) // below min
	assert.True(t, isIntInRange("0", 0, 10))   // at min
	assert.True(t, isIntInRange("5", 0, 10))   // inside
	assert.True(t, isIntInRange("10", 0, 10))  // at max
	assert.False(t, isIntInRange("11", 0, 10)) // above max
	assert.False(t, isIntInRange("x", 0, 10))  // non-numeric
	assert.False(t, isIntInRange("3.5", 0, 10))
}

// collectModelNamesFromOptionValue: parses a JSON object's keys into the set;
// blank / invalid JSON is a no-op.
func TestCollectModelNamesFromOptionValue(t *testing.T) {
	set := map[string]struct{}{}
	collectModelNamesFromOptionValue(`{"gpt-4o":2,"claude":3}`, set)
	assert.Contains(t, set, "gpt-4o")
	assert.Contains(t, set, "claude")
	assert.Len(t, set, 2)

	// blank and invalid inputs do not mutate the set
	collectModelNamesFromOptionValue("", set)
	collectModelNamesFromOptionValue("  ", set)
	collectModelNamesFromOptionValue("not-json", set)
	assert.Len(t, set, 2)
}
