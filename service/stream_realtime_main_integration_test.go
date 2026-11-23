package service_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// The service TestMain supplies real, isolated databases. Exercise the actual
// compatibility handler as well as final settlement: reservations alone can
// match the consume log while undercharging relative to official main.
func TestRealtimeMainActualDebitMatchesOfficial(t *testing.T) {
	const modelName = "realtime-main-debit-fixture"
	previous := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"realtime-main-debit-fixture":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previous)) })
	normal := `{"type":"response.done","response":{"id":"same","usage":{"total_tokens":118,"input_tokens":101,"output_tokens":17,"input_token_details":{"text_tokens":101},"output_token_details":{"text_tokens":17}}}}`
	zero := strings.Replace(normal, `"total_tokens":118`, `"total_tokens":0`, 1)
	audio := `{"type":"response.done","response":{"id":"audio","usage":{"total_tokens":12,"input_tokens":5,"output_tokens":7,"input_token_details":{"text_tokens":2,"audio_tokens":3},"output_token_details":{"text_tokens":3,"audio_tokens":4}}}}`
	for index, tc := range []struct {
		name          string
		frames        []string
		debit, logged int
		fixedPrice    bool
		limited       string
	}{
		{name: "normal", frames: []string{normal}, debit: 236, logged: 118},
		{name: "duplicate", frames: []string{normal, normal}, debit: 472, logged: 236},
		{name: "zero_total_positive_parts", frames: []string{zero}, debit: 118},
		{name: "missing_usage", frames: []string{`{"type":"response.done","response":{"id":"missing"}}`}, debit: 1},
		{name: "invalid_after_charge", frames: []string{normal, `{`}, debit: 236, logged: 118},
		{name: "audio", frames: []string{audio}, debit: 24, logged: 12},
		{name: "fixed_price", frames: []string{normal, normal}, debit: 500, logged: 500, fixedPrice: true},
		{name: "fixed_price_zero_total", frames: []string{zero}, fixedPrice: true},
		{name: "insufficient_wallet", frames: []string{normal}, debit: 236, logged: 236, limited: "wallet"},
		{name: "insufficient_token", frames: []string{normal}, debit: 236, logged: 236, limited: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const balance = 1_000_000
			id := 874310 + index
			key := fmt.Sprintf("realtime-main-debit-%d", id)
			user := model.User{Id: id, Username: key, AffCode: key, Quota: balance, Status: common.UserStatusEnabled}
			token := model.Token{Id: id, UserId: id, Key: key, Name: key, RemainQuota: balance, Status: common.TokenStatusEnabled}
			if tc.limited == "wallet" {
				user.Quota = 40
			}
			if tc.limited == "token" {
				token.RemainQuota = 40
			}
			walletBefore, tokenBefore := user.Quota, token.RemainQuota
			channel := model.Channel{Id: id, Name: key, Key: "fixture", Status: common.ChannelStatusEnabled}
			require.NoError(t, model.DB.Create(&user).Error)
			require.NoError(t, model.DB.Create(&token).Error)
			require.NoError(t, model.DB.Create(&channel).Error)
			t.Cleanup(func() {
				model.DrainRelayLogsSync(5 * time.Second)
				model.LOG_DB.Where("user_id = ?", id).Delete(&model.Log{})
				model.DB.Where("id = ?", id).Delete(&model.Token{})
				model.DB.Where("id = ?", id).Delete(&model.Channel{})
				model.DB.Where("id = ?", id).Delete(&model.User{})
			})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for _, frame := range tc.frames {
					if ws.WriteMessage(websocket.TextMessage, []byte(frame)) != nil {
						return
					}
				}
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""))
			}))
			defer upstream.Close()
			finished := make(chan error, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					finished <- err
					return
				}
				defer client.Close()
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					finished <- err
					return
				}
				defer target.Close()
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				info := &relaycommon.RelayInfo{UserId: id, TokenId: id, TokenKey: key, UserQuota: balance,
					OriginModelName: modelName, UsingGroup: "default", StartTime: time.Now(), ForcePreConsume: true,
					BillingSource: service.BillingSourceWallet, RelayFormat: types.RelayFormatOpenAIRealtime, IsStream: true,
					ClientWs: client, TargetWs: target, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(false)},
					ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, UpstreamModelName: modelName},
					PriceData:   hosttypes.PriceData{ModelRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
				if tc.fixedPrice {
					info.UsePrice = true
					info.PriceData.UsePrice = true
					info.PriceData.ModelPrice = .001
				}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				billing, apiErr := service.NewBillingSession(c, info, 10)
				if apiErr != nil {
					finished <- fmt.Errorf("preconsume: %v", apiErr)
					return
				}
				info.Billing = billing
				apiErr, usage := openai.OpenaiRealtimeHandler(c, info)
				if apiErr != nil {
					finished <- fmt.Errorf("handler: %v", apiErr)
					return
				}
				service.PostWssConsumeQuota(c, info, modelName, usage, nil)
				service.PostWssConsumeQuota(c, info, modelName, usage, nil)
				finished <- nil
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
			for {
				if _, _, err := client.ReadMessage(); err != nil {
					break
				}
			}
			require.NoError(t, <-finished)
			model.DrainRelayLogsSync(5 * time.Second)
			require.NoError(t, model.DB.First(&user, id).Error)
			require.NoError(t, model.DB.First(&token, id).Error)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", id, 2).Find(&logs).Error)
			require.Len(t, logs, 1, "one final consume log despite per-round debits")
			require.Equal(t, tc.logged, logs[0].Quota)
			require.Equal(t, tc.debit, walletBefore-user.Quota, "wallet must include official main's round debit")
			require.Equal(t, tc.debit, tokenBefore-token.RemainQuota, "token debit must match official main too")
		})
	}
}
