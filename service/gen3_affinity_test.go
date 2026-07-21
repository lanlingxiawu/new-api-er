package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// channel_affinity.go — cache management + record/mark helpers.
// In-memory hot cache + gin context; no relay hot-path involvement.
// ===========================================================================

func withAffinitySetting(t *testing.T, mutate func(s *operation_setting.ChannelAffinitySetting)) *operation_setting.ChannelAffinitySetting {
	t.Helper()
	s := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, s)
	orig := *s
	mutate(s)
	t.Cleanup(func() { *s = orig })
	return s
}

func affinityCtx(t *testing.T, cacheKey, ruleName string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setChannelAffinityContext(c, channelAffinityMeta{
		CacheKey:   cacheKey,
		TTLSeconds: 600,
		RuleName:   ruleName,
		UsingGroup: "default",
		ModelName:  "gpt-4o",
		SkipRetry:  true,
	})
	return c
}

func TestAffinity_MatchAnyIncludeFold(t *testing.T) {
	assert.False(t, matchAnyIncludeFold(nil, "x"))
	assert.False(t, matchAnyIncludeFold([]string{"a"}, ""))
	assert.True(t, matchAnyIncludeFold([]string{"Foo"}, "a-FOO-bar"))
	assert.False(t, matchAnyIncludeFold([]string{"zzz"}, "abc"))
	// blank patterns skipped
	assert.False(t, matchAnyIncludeFold([]string{"  "}, "abc"))
}

func TestAffinity_ShouldKeepOnChannelDisabled(t *testing.T) {
	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.KeepOnChannelDisabled = true
	})
	assert.True(t, ShouldKeepChannelAffinityOnChannelDisabled())

	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.KeepOnChannelDisabled = false
	})
	assert.False(t, ShouldKeepChannelAffinityOnChannelDisabled())
}

func TestAffinity_RecordAndClearCurrent(t *testing.T) {
	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.Enabled = true
		s.DefaultTTLSeconds = 600
		s.SwitchOnSuccess = false
	})
	// Clear everything first for a clean count.
	ClearChannelAffinityCacheAll()

	c := affinityCtx(t, "chaff:test_rule:default:fp1", "test_rule")
	RecordChannelAffinity(c, 4242)

	// Clearing the current entry removes exactly it.
	assert.True(t, ClearCurrentChannelAffinityCache(c))
	// Second clear: nothing left for this context.
	assert.False(t, ClearCurrentChannelAffinityCache(c))
}

func TestAffinity_RecordDisabledNoop(t *testing.T) {
	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.Enabled = false
	})
	c := affinityCtx(t, "chaff:x:default:fp", "x")
	RecordChannelAffinity(c, 1) // disabled => no-op, no panic
	// channelID <= 0 => no-op.
	RecordChannelAffinity(c, 0)
}

func TestAffinity_MarkUsedAndAdminInfo(t *testing.T) {
	c := affinityCtx(t, "chaff:r:default:fp", "r")
	// channelID <= 0 => no-op.
	MarkChannelAffinityUsed(c, "default", 0)
	MarkChannelAffinityUsed(c, "vip", 77)

	admin := map[string]interface{}{}
	AppendChannelAffinityAdminInfo(c, admin)
	require.Contains(t, admin, "channel_affinity")
	info := admin["channel_affinity"].(map[string]interface{})
	assert.EqualValues(t, 77, info["channel_id"])
	assert.Equal(t, "vip", info["selected_group"])

	// nil map / nil context => safe no-ops.
	AppendChannelAffinityAdminInfo(c, nil)
	AppendChannelAffinityAdminInfo(nil, admin)
	MarkChannelAffinityUsed(nil, "g", 1)
}

func TestAffinity_ClearByRuleNameValidation(t *testing.T) {
	// Empty rule name => error.
	_, err := ClearChannelAffinityCacheByRuleName("  ")
	assert.Error(t, err)

	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.Enabled = true
		s.Rules = []operation_setting.ChannelAffinityRule{{Name: "known_rule", IncludeRuleName: true}}
	})
	// Unknown rule name => error (not found).
	_, err = ClearChannelAffinityCacheByRuleName("no_such_rule")
	assert.Error(t, err)
}

func TestAffinity_CacheStats(t *testing.T) {
	withAffinitySetting(t, func(s *operation_setting.ChannelAffinitySetting) {
		s.Enabled = true
		s.Rules = []operation_setting.ChannelAffinityRule{{Name: "stat_rule", IncludeRuleName: true}}
	})
	stats := GetChannelAffinityCacheStats()
	assert.True(t, stats.Enabled)
	assert.Contains(t, stats.ByRuleName, "stat_rule")
	assert.GreaterOrEqual(t, stats.Total, 0)

	// ClearAll returns the number of removed keys (>= 0).
	assert.GreaterOrEqual(t, ClearChannelAffinityCacheAll(), 0)
}
