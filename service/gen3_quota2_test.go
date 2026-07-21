package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// quota.go — PreWssConsumeQuota error paths + async quota-notify branches.
// ===========================================================================

func TestQuota2_PreWss_UserQuotaInsufficient(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 8501, 8501, 8501
	seedUser(t, uid, 1) // almost no wallet quota
	seedToken(t, tid, uid, "prewss-lowuser", 1_000_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-prewss-lowuser")
	info.TokenKey = "sk-prewss-lowuser"
	usage := &dto.RealtimeUsage{
		InputTokens: 100000, OutputTokens: 50000, TotalTokens: 150000,
		InputTokenDetails:  dto.InputTokenDetails{TextTokens: 90000, AudioTokens: 10000},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 50000},
	}
	err := PreWssConsumeQuota(finalizeCtx(t), info, usage)
	assert.Error(t, err, "wallet quota below computed cost")
}

func TestQuota2_PreWss_TokenQuotaInsufficient(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 8502, 8502, 8502
	seedUser(t, uid, 100_000_000) // plenty of wallet
	seedToken(t, tid, uid, "prewss-lowtok", 1)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-prewss-lowtok")
	info.TokenKey = "sk-prewss-lowtok"
	usage := &dto.RealtimeUsage{
		InputTokens: 100000, OutputTokens: 50000, TotalTokens: 150000,
		InputTokenDetails:  dto.InputTokenDetails{TextTokens: 90000, AudioTokens: 10000},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 50000},
	}
	err := PreWssConsumeQuota(finalizeCtx(t), info, usage)
	assert.Error(t, err, "token remain quota below computed cost")
}

// --- async quota notify (fire-and-forget; verified via completion, not assert) ---

func TestQuota2_CheckAndSendQuotaNotify(t *testing.T) {
	info := &relaycommon.RelayInfo{
		UserId:      8503,
		UserEmail:   "u@example.com",
		UserQuota:   100, // below any reasonable threshold
		UserSetting: dto.UserSetting{},
	}
	// quota+pre consumed pushes remaining under the warning threshold => notify path.
	checkAndSendQuotaNotify(info, 50, 40)
	// gopool.Go is async fire-and-forget (Rule 0); give it a moment to run.
	time.Sleep(150 * time.Millisecond)
}

func TestQuota2_CheckAndSendQuotaNotify_NotifyTypes(t *testing.T) {
	for _, nt := range []string{dto.NotifyTypeBark, dto.NotifyTypeGotify, dto.NotifyTypeWebhook} {
		info := &relaycommon.RelayInfo{
			UserId:      8510,
			UserEmail:   "u@example.com",
			UserQuota:   100,
			UserSetting: dto.UserSetting{NotifyType: nt},
		}
		checkAndSendQuotaNotify(info, 50, 40)
	}
	time.Sleep(150 * time.Millisecond)
}

func TestQuota2_CheckAndSendSubscriptionQuotaNotify(t *testing.T) {
	// Nil / no-subscription => early return.
	checkAndSendSubscriptionQuotaNotify(nil)
	checkAndSendSubscriptionQuotaNotify(&relaycommon.RelayInfo{})

	info := &relaycommon.RelayInfo{
		UserId:      8504,
		UserEmail:   "s@example.com",
		UserSetting: dto.UserSetting{},
	}
	info.SubscriptionId = 7
	info.SubscriptionAmountTotal = 1000
	info.SubscriptionAmountUsedAfterPreConsume = 995 // remaining 5 < threshold
	checkAndSendSubscriptionQuotaNotify(info)

	// Exercise the per-NotifyType content branches.
	for _, nt := range []string{dto.NotifyTypeBark, dto.NotifyTypeGotify, dto.NotifyTypeWebhook} {
		info2 := &relaycommon.RelayInfo{
			UserId: 8505, UserEmail: "s@example.com",
			UserSetting: dto.UserSetting{NotifyType: nt},
		}
		info2.SubscriptionId = 7
		info2.SubscriptionAmountTotal = 1000
		info2.SubscriptionAmountUsedAfterPreConsume = 998
		checkAndSendSubscriptionQuotaNotify(info2)
	}
	time.Sleep(200 * time.Millisecond)
}

var _ = require.NoError
var _ = common.RedisEnabled
var _ = types.RelayFormatOpenAI
