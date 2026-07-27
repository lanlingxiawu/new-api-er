package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Extra coverage: file_service mime detection branches, employee commission
// side effect, billing_session subscription sync.
// ===========================================================================

// --- smartDetectMimeType: Content-Disposition filename branch --------------

func TestFileService_MimeFromContentDisposition(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	// Opaque content-type + no URL extension, but a filename in Content-Disposition.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="report.png"`)
		_, _ = w.Write(pngBytes(t, 4, 4))
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/download")
	_, mime, err := GetBase64Data(nil, src)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
}

// --- smartDetectMimeType: URL extension branch -----------------------------

func TestFileService_MimeFromURLExtension(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Opaque content-type; no Content-Disposition; extension is in the path.
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(pngBytes(t, 4, 4))
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/a.png?token=abc")
	_, mime, err := GetBase64Data(nil, src)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
}

// --- RecordCostAndSettleEmployeeCommission ---------------------------------

func TestEmployeeCommission_RecordCostAndSettle(t *testing.T) {
	truncate(t)
	const uid, chid = 8301, 8302
	seedUser(t, uid, 100000)

	// quota == 0 => early return.
	RecordCostAndSettleEmployeeCommission(&relaycommon.RelayInfo{UserId: uid}, 0, 0, 0)

	info := &relaycommon.RelayInfo{
		UserId:          uid,
		OriginModelName: "gpt-4o",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: chid, ChannelName: "c"},
		PriceData:       hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}
	// User has no inviter => cost-only ledger write (or circuit-breaker skip). No panic.
	RecordCostAndSettleEmployeeCommission(info, 1000, 0, 123)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM consumption_costs WHERE user_id = ?", uid)
	})
}

// --- billing_session syncRelayInfo subscription branch ---------------------

func TestBS_SyncRelayInfo_Subscription(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	sub := &SubscriptionFunding{
		subscriptionId:  55,
		preConsumed:     200,
		AmountTotal:     10000,
		AmountUsedAfter: 500,
		PlanId:          3,
		PlanTitle:       "Pro",
	}
	s := &BillingSession{
		relayInfo:     info,
		funding:       sub,
		extraReserved: 50,
	}
	s.syncRelayInfo()

	assert.Equal(t, 55, info.SubscriptionId)
	assert.EqualValues(t, 250, info.SubscriptionPreConsumed) // preConsumed + extraReserved
	assert.EqualValues(t, 10000, info.SubscriptionAmountTotal)
	assert.EqualValues(t, 550, info.SubscriptionAmountUsedAfterPreConsume)
	assert.Equal(t, 3, info.SubscriptionPlanId)
	assert.Equal(t, "Pro", info.SubscriptionPlanTitle)
	assert.Equal(t, BillingSourceSubscription, info.BillingSource)

	// Wallet funding resets subscription fields.
	info2 := &relaycommon.RelayInfo{}
	sWallet := &BillingSession{relayInfo: info2, funding: &WalletFunding{userId: 1}}
	sWallet.syncRelayInfo()
	assert.Equal(t, 0, info2.SubscriptionId)
	assert.EqualValues(t, 0, info2.SubscriptionPreConsumed)
}
