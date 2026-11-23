package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamPolicyErrorLogPreservesCapturedCause(t *testing.T) {
	for _, source := range []string{"explicit", "read", "missing"} {
		t.Run(source, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/fixture", nil)
			info := &relaycommon.RelayInfo{IsStream: true, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(false)}}
			BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Body: io.NopCloser(&iotestErrorReader{})}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			switch source {
			case "explicit":
				info.StreamDiagnostic.SetError(io.ErrUnexpectedEOF)
			case "read":
				_, err := io.ReadAll(resp.Body)
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
			other := model.NewLogOther()
			AppendStreamErrorDiagnostic(c, other, errors.New("generic relay failure"))
			diagnostic := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			expected := "unexpected EOF"
			if source == "missing" {
				expected = "generic relay failure"
			}
			require.Equal(t, expected, diagnostic.Error)
			AppendStreamErrorDiagnostic(c, other, errors.New("later wrapper"))
			require.Equal(t, expected, other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic).Error)
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.Empty(t, info.StreamDiagnostic.Snapshot().Error, "a retry must have independent evidence")
		})
	}
}

type iotestErrorReader struct{}

func (*iotestErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRealtimeMainRevenueMatchesFinalCharge(t *testing.T) {
	for i, total := range []int{0, 1500} {
		t.Run(map[int]string{0: "zero total", 1500: "normal total"}[total], func(t *testing.T) {
			uid, tid, chid := 874120+i, 874120+i, 874190+i
			seedUser(t, uid, 1_000_000)
			seedToken(t, tid, uid, "main-revenue-fixture", 1_000_000)
			seedChannel(t, chid)
			t.Cleanup(func() {
				model.LOG_DB.Where("user_id = ?", uid).Delete(&model.Log{})
				model.DB.Where("user_id = ?", uid).Delete(&model.ConsumptionCost{})
			})
			info := finalizeRelayInfo(uid, tid, chid, "main-revenue-fixture")
			info.IsStream = true
			info.RelayFormat = types.RelayFormatOpenAIRealtime
			info.UserSetting.StreamProtocolValidation = common.GetPointer(false)
			info.FreezeStreamValidation(dto.ChannelSettings{})
			billing := &claudeSettlementFixture{}
			info.Billing = billing
			c := finalizeCtx(t)
			c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
			usage := &dto.RealtimeUsage{InputTokens: 1000, OutputTokens: 500, TotalTokens: total,
				InputTokenDetails: dto.InputTokenDetails{TextTokens: 1000}, OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 500}}
			PostWssConsumeQuota(c, info, "gpt-4o-realtime", usage, nil)
			PostWssConsumeQuota(c, info, "gpt-4o-realtime", usage, nil)
			drainRelayLogs()
			model.FlushBusinessStatBuffers()
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ?", uid).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, 1, billing.calls)
			require.Equal(t, billing.actual, logs[0].Quota)
			var costs []model.ConsumptionCost
			require.NoError(t, model.DB.Where("user_id = ?", uid).Find(&costs).Error)
			if total == 0 {
				require.Zero(t, logs[0].Quota, "preserve main's zero-total charge")
				require.Never(t, func() bool {
					model.FlushBusinessStatBuffers()
					require.NoError(t, model.DB.Where("user_id = ?", uid).Find(&costs).Error)
					return len(costs) > 0
				}, time.Second, 20*time.Millisecond, "zero revenue follows the existing no-cost/commission-entry rule")
			} else {
				require.Positive(t, logs[0].Quota)
				require.Eventually(t, func() bool {
					model.FlushBusinessStatBuffers()
					return model.DB.Where("user_id = ?", uid).Find(&costs).Error == nil && len(costs) > 0
				}, 5*time.Second, 20*time.Millisecond)
				require.Len(t, costs, 1)
				require.EqualValues(t, logs[0].Quota, costs[0].RevenueQuota)
			}
		})
	}
}
