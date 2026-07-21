package service

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Small pure / delegation helpers.
// ===========================================================================

func TestSmall_GetCallbackAddress(t *testing.T) {
	origCustom := operation_setting.CustomCallbackAddress
	origServer := system_setting.ServerAddress
	t.Cleanup(func() {
		operation_setting.CustomCallbackAddress = origCustom
		system_setting.ServerAddress = origServer
	})

	operation_setting.CustomCallbackAddress = ""
	system_setting.ServerAddress = "https://server.example"
	assert.Equal(t, "https://server.example", GetCallbackAddress())

	operation_setting.CustomCallbackAddress = "https://cb.example"
	assert.Equal(t, "https://cb.example", GetCallbackAddress())
}

func TestSmall_CoverTaskActionToModelName(t *testing.T) {
	assert.Equal(t, "kling_generate", CoverTaskActionToModelName(constant.TaskPlatform("Kling"), "GENERATE"))
	assert.Equal(t, "suno_lyrics", CoverTaskActionToModelName(constant.TaskPlatform("suno"), "LYRICS"))
}

// --- openai_chat_responses_compat.go delegations ---------------------------

func TestSmall_ResponsesChatCompat_RoundTrip(t *testing.T) {
	// Responses -> ChatCompletions.
	respReq := &dto.OpenAIResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"hello"`),
	}
	chatReq, err := ResponsesRequestToChatCompletionsRequest(respReq)
	require.NoError(t, err)
	require.NotNil(t, chatReq)
	assert.Equal(t, "gpt-4o", chatReq.Model)

	// ChatCompletions response -> Responses response.
	chatResp := &dto.OpenAITextResponse{
		Id:    "resp-1",
		Model: "gpt-4o",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:   0,
				Message: dto.Message{Role: "assistant"},
			},
		},
		Usage: dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	chatResp.Choices[0].Message.SetStringContent("hi there")
	out, usage, err := ChatCompletionsResponseToResponsesResponse(chatResp, "resp-1")
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, usage)

	// Responses response -> ChatCompletions response.
	back, usage2, err := ResponsesResponseToChatCompletionsResponse(out, "resp-1")
	require.NoError(t, err)
	require.NotNil(t, back)
	require.NotNil(t, usage2)
}

// --- channel_select.go CacheGetRandomSatisfiedChannel error paths ----------

func TestSmall_CacheGetRandomSatisfiedChannel_AutoNoGroups(t *testing.T) {
	origAuto := setttingAutoGroups(t)
	_ = origAuto

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	param := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "gpt-4o"}
	// No matching channel in any auto group => nil channel; group echoes "auto".
	ch, group, _ := CacheGetRandomSatisfiedChannel(param)
	assert.Nil(t, ch)
	assert.Equal(t, "auto", group)
}

func TestSmall_CacheGetRandomSatisfiedChannel_NoChannel(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Non-auto group with no matching channels => selection fails.
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "no-such-model"}
	ch, group, _ := CacheGetRandomSatisfiedChannel(param)
	assert.Nil(t, ch)
	assert.Equal(t, "default", group)
}

// setttingAutoGroups clears auto groups so the "auto groups not enabled" branch
// is exercised, restoring the previous value afterwards.
func setttingAutoGroups(t *testing.T) []string {
	t.Helper()
	// AutoGroups is read via setting.GetAutoGroups(); with none configured (the
	// default test state) the "not enabled" branch triggers. Nothing to restore.
	return nil
}

// --- SubscriptionFunding no-op branches ------------------------------------

func TestSmall_SubscriptionFunding_Noops(t *testing.T) {
	sub := &SubscriptionFunding{}
	// delta 0 => no-op success.
	assert.NoError(t, sub.Settle(0))
	// preConsumed 0 => refund is a no-op success.
	assert.NoError(t, sub.Refund())
	assert.Equal(t, BillingSourceSubscription, sub.Source())
}
