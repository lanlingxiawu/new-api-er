package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithCompactModelSuffix(t *testing.T) {
	assert.Equal(t, "gpt-4o"+CompactModelSuffix, WithCompactModelSuffix("gpt-4o"))
	// Already suffixed -> unchanged (no double suffix).
	already := "gpt-4o" + CompactModelSuffix
	assert.Equal(t, already, WithCompactModelSuffix(already))
	// Empty string still gets the suffix.
	assert.Equal(t, CompactModelSuffix, WithCompactModelSuffix(""))
}

func TestWithCompactModelVariants_Basic(t *testing.T) {
	got := WithCompactModelVariants([]string{"a", "b"})
	assert.Equal(t, []string{
		"a", "b",
		"a" + CompactModelSuffix, "b" + CompactModelSuffix,
	}, got, "originals first (dedup), then compact variants")
}

func TestWithCompactModelVariants_DedupesOriginals(t *testing.T) {
	got := WithCompactModelVariants([]string{"a", "a", "b"})
	assert.Equal(t, []string{
		"a", "b",
		"a" + CompactModelSuffix, "b" + CompactModelSuffix,
	}, got)
}

func TestWithCompactModelVariants_AlreadyCompactNotDuplicated(t *testing.T) {
	compact := "a" + CompactModelSuffix
	// "a" produces compact "a-openai-compact"; the already-compact input dedups.
	got := WithCompactModelVariants([]string{"a", compact})
	assert.Equal(t, []string{"a", compact}, got,
		"compact variant of 'a' equals the already-present compact input -> no dup")
}

func TestWithCompactModelVariants_Empty(t *testing.T) {
	got := WithCompactModelVariants(nil)
	assert.Empty(t, got)
}

func TestCompactConstants(t *testing.T) {
	assert.Equal(t, "-openai-compact", CompactModelSuffix)
	assert.Equal(t, "*-openai-compact", CompactWildcardModelKey)
}
