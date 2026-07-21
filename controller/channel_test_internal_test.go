package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseStatusFilter: equivalence classes over the status query param.
func TestParseStatusFilter(t *testing.T) {
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("enabled"))
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("1"))
	assert.Equal(t, 0, parseStatusFilter("disabled"))
	assert.Equal(t, 0, parseStatusFilter("0"))
	assert.Equal(t, -1, parseStatusFilter(""))
	assert.Equal(t, -1, parseStatusFilter("garbage"))
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("ENABLED"), "case-insensitive")
}

// selectChannelsForAutomaticTest: manual-disabled channels are always skipped;
// passive-recovery mode restricts to auto-disabled channels only.
func TestSelectChannelsForAutomaticTest(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusManuallyDisabled},
		{Id: 3, Status: common.ChannelStatusAutoDisabled},
	}

	// scheduled-all mode: keep enabled + auto-disabled, skip manual-disabled
	all := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll)
	ids := channelIDs(all)
	assert.ElementsMatch(t, []int{1, 3}, ids)

	// passive-recovery mode: only auto-disabled
	passive := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModePassiveRecovery)
	assert.Equal(t, []int{3}, channelIDs(passive))
}

func channelIDs(channels []*model.Channel) []int {
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	return ids
}

// resolveChannelTestUserID: an authenticated context uses its own user id
// without touching the DB.
func TestResolveChannelTestUserID_UsesRequestUser(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodPost, "/api/channel/test", nil)
	asUser(ctx, 4242)
	id, err := resolveChannelTestUserID(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4242, id)
}

// sanitizeChannelSensitiveSettingsForDisplay masks the account-balance token so
// it never leaks in list/detail responses.
func TestSanitizeChannelSensitiveSettingsForDisplay(t *testing.T) {
	ch := &model.Channel{}
	s := ch.GetSetting()
	s.AccountBalanceToken = "raw-secret-token"
	ch.SetSetting(s)

	sanitizeChannelSensitiveSettingsForDisplay(ch)
	assert.Equal(t, maskedChannelSensitiveToken, ch.GetSetting().AccountBalanceToken)

	// no token -> unchanged (no mask injected)
	empty := &model.Channel{}
	sanitizeChannelSensitiveSettingsForDisplay(empty)
	assert.Empty(t, empty.GetSetting().AccountBalanceToken)
}

// preserveSensitiveChannelSettingsForUpdate: the masked placeholder restores the
// original token; an explicit empty string clears it (so the credential can be
// removed).
func TestPreserveSensitiveChannelSettingsForUpdate(t *testing.T) {
	origin := &model.Channel{}
	os := origin.GetSetting()
	os.AccountBalanceToken = "original-token"
	origin.SetSetting(os)

	// incoming submits the masked placeholder -> original preserved
	masked := &model.Channel{}
	ms := masked.GetSetting()
	ms.AccountBalanceToken = maskedChannelSensitiveToken
	masked.SetSetting(ms)
	preserveSensitiveChannelSettingsForUpdate(masked, origin)
	assert.Equal(t, "original-token", masked.GetSetting().AccountBalanceToken)

	// incoming submits empty string -> token cleared (not restored)
	cleared := &model.Channel{}
	cs := cleared.GetSetting()
	cs.AccountBalanceToken = ""
	cleared.SetSetting(cs)
	preserveSensitiveChannelSettingsForUpdate(cleared, origin)
	assert.Empty(t, cleared.GetSetting().AccountBalanceToken)
}
