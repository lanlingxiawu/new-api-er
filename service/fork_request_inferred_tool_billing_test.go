package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForkUndeliveredRequestInferredToolRefund(t *testing.T) {
	forkZeroDiagnosticAccounting(t)
	const toolKey = "fork_request_inferred_search"
	operation_setting.SetToolPriceForTest(toolKey, 2)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(toolKey) })
	for _, tc := range []struct {
		name      string
		reported  *int
		protocol  bool
		delivered bool
		wantQuota int64
	}{
		{name: "request alone"},
		{name: "reported zero", reported: new(int)},
		{name: "reported one", reported: common.GetPointer(1)},
		{name: "reported two", reported: common.GetPointer(2)},
		{name: "one protocol item", protocol: true},
		{name: "delivered keeps request inference", delivered: true, wantQuota: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			BeginStreamAttempt(c, info)
			// The Ali forced-search and GLM forced-search encoders use this entry
			// point before receiving any upstream tool or token usage.
			info.RecordRequestInferredToolCall(toolKey)
			if tc.reported != nil {
				info.SetBillableToolCount(toolKey, *tc.reported)
			}
			if tc.protocol {
				info.WebSearchBillingKey = toolKey
				info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			}
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":10}}`)))
			if tc.delivered {
				_, err := c.Writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
				require.NoError(t, err)
			}
			info.StreamSession.EndRead(io.EOF)
			FinalizeStreamUsage(c, info, nil)
			require.Equal(t, tc.delivered, info.StreamResult.EffectiveContent)
			if tc.delivered {
				summary := textQuotaSummary{ModelName: "gpt-4o", GroupRatio: 1}
				require.Equal(t, tc.wantQuota, calculateTextToolCallSurcharge(c, info, &summary).IntPart())
				return
			}
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			funding := &claudeSettlementFixture{}
			info.Billing = funding
			PostTextConsumeQuota(c, info, info.StreamFinalUsage, nil)
			require.EqualValues(t, tc.wantQuota, funding.actual)
		})
	}
}
