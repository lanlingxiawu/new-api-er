package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamCacheOnlyBillingScope 用 t 验证只缓存收费资格仅在受管结算生效，普通 token 的既有缓存计价保持。
func TestStreamCacheOnlyBillingScope(t *testing.T) {
	for _, mode := range []string{"nonstream", "legacy-stream", "managed", "native-claude"} {
		for _, field := range []string{"read", "create", "5m", "1h"} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				info := &relaycommon.RelayInfo{IsStream: mode != "nonstream", RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
				managed := mode == "managed" || mode == "native-claude"
				if managed {
					info.StreamResult = &relaycommon.StreamOutcome{UsageSource: "upstream"}
				}
				if mode == "native-claude" {
					info.StreamSession = relaycommon.NewStreamSession(types.RelayFormatClaude)
					info.StreamSession.Disable()
				}
				info.PriceData.ModelRatio = 1
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				info.PriceData.CacheRatio = 0.1
				info.PriceData.CacheCreationRatio = 1
				info.PriceData.CacheCreation5mRatio = 1
				info.PriceData.CacheCreation1hRatio = 2
				usage := &dto.Usage{UsageSemantic: "anthropic"}
				expected := 100
				switch field {
				case "read":
					usage.PromptTokensDetails.CachedTokens = 100
					expected = 10
				case "create":
					usage.PromptTokensDetails.CachedCreationTokens = 100
				case "5m":
					usage.ClaudeCacheCreation5mTokens = 100
				case "1h":
					usage.ClaudeCacheCreation1hTokens = 100
					expected = 200
				}
				summary := calculateTextQuotaSummary(c, info, usage)
				require.Equal(t, managed, summary.hasBillableUsage())
				if managed {
					require.Equal(t, expected, summary.Quota)
				} else {
					require.Zero(t, summary.Quota)
				}
				require.Equal(t, summary.Quota, summary.LedgerQuota)
				usage.PromptTokens = 5
				summary = calculateTextQuotaSummary(c, info, usage)
				require.True(t, summary.hasBillableUsage())
				require.Equal(t, expected+5, summary.Quota)
			})
		}
	}
}

// TestStreamCacheOnlyTransportGate 用 t 验证原始成功响应才创建扩展计费资格，透传不改变判断。
func TestStreamCacheOnlyTransportGate(t *testing.T) {
	for _, status := range []int{0, 200, 201, 401, 429, 500} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/pass=%v", status, passthrough), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
				info.ChannelSetting.PassThroughBodyEnabled = passthrough
				BeginStreamAttempt(c, info)
				if status == 0 {
					info.StreamSession.ObserveTransport(nil, context.DeadlineExceeded)
				} else {
					info.StreamSession.ObserveTransport(&http.Response{StatusCode: status}, nil)
				}
				info.PriceData.ModelRatio = 1
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				info.PriceData.CacheRatio = 0.1
				usage := &dto.Usage{UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 100}}
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"message_start","message":{"usage":{"cache_read_input_tokens":100}}}`)))
				info.StreamSession.CommitDelivery([]byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`))
				info.StreamSession.EndRead(io.EOF)
				selected := FinalizeStreamUsage(c, info, usage)
				summary := calculateTextQuotaSummary(c, info, selected)
				require.Equal(t, status == 200, summary.hasBillableUsage())
				if status == 200 {
					require.Equal(t, 10, summary.Quota)
					require.NotNil(t, info.StreamResult)
				} else {
					require.Nil(t, info.StreamResult)
					require.Same(t, usage, selected)
					require.Zero(t, summary.Quota)
				}
			})
		}
	}
}
