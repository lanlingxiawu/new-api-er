package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func saveSensitive(t *testing.T) {
	t.Helper()
	origWords := SensitiveWords
	origCheck := CheckSensitiveEnabled
	origPrompt := CheckSensitiveOnPromptEnabled
	t.Cleanup(func() {
		SensitiveWords = origWords
		CheckSensitiveEnabled = origCheck
		CheckSensitiveOnPromptEnabled = origPrompt
	})
}

func TestSensitiveWordsToString(t *testing.T) {
	saveSensitive(t)
	SensitiveWords = []string{"a", "b", "c"}
	assert.Equal(t, "a\nb\nc", SensitiveWordsToString())
}

func TestSensitiveWordsToString_Single(t *testing.T) {
	saveSensitive(t)
	SensitiveWords = []string{"only"}
	assert.Equal(t, "only", SensitiveWordsToString())
}

func TestSensitiveWordsFromString(t *testing.T) {
	saveSensitive(t)
	SensitiveWordsFromString("a\nb\nc")
	assert.Equal(t, []string{"a", "b", "c"}, SensitiveWords)
}

func TestSensitiveWordsFromString_TrimsAndSkipsEmpty(t *testing.T) {
	saveSensitive(t)
	SensitiveWordsFromString("  a  \n\n   \n b \n")
	// Whitespace-only and empty lines dropped; surviving words trimmed.
	assert.Equal(t, []string{"a", "b"}, SensitiveWords)
}

func TestSensitiveWordsFromString_AllEmpty(t *testing.T) {
	saveSensitive(t)
	SensitiveWordsFromString("\n   \n\t\n")
	assert.Empty(t, SensitiveWords)
}

func TestSensitiveWordsRoundTrip(t *testing.T) {
	saveSensitive(t)
	SensitiveWords = []string{"foo", "bar"}
	SensitiveWordsFromString(SensitiveWordsToString())
	assert.Equal(t, []string{"foo", "bar"}, SensitiveWords)
}

func TestShouldCheckPromptSensitive_ConditionMatrix(t *testing.T) {
	saveSensitive(t)
	cases := []struct {
		enabled bool
		prompt  bool
		want    bool
	}{
		{true, true, true},   // both on => true
		{true, false, false}, // prompt off => false
		{false, true, false}, // global off => false
		{false, false, false},
	}
	for _, c := range cases {
		CheckSensitiveEnabled = c.enabled
		CheckSensitiveOnPromptEnabled = c.prompt
		assert.Equal(t, c.want, ShouldCheckPromptSensitive(),
			"enabled=%v prompt=%v", c.enabled, c.prompt)
	}
}
