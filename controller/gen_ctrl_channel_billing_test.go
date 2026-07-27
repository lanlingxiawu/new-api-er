package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// channel-billing.go: auth-header builders, account-balance configuration check,
// and the id-parsing guards of the balance-refresh handlers.

func TestGetAuthHeader(t *testing.T) {
	h := GetAuthHeader("sk-abc")
	assert.Equal(t, "Bearer sk-abc", h.Get("Authorization"))
}

func TestGetClaudeAuthHeader(t *testing.T) {
	h := GetClaudeAuthHeader("key-xyz")
	assert.Equal(t, "key-xyz", h.Get("x-api-key"))
	assert.Equal(t, "2023-06-01", h.Get("anthropic-version"))
}

func TestIsChannelAccountBalanceConfigured(t *testing.T) {
	assert.False(t, isChannelAccountBalanceConfigured(nil))

	ch := &model.Channel{}
	assert.False(t, isChannelAccountBalanceConfigured(ch), "empty settings -> not configured")

	// Only token set -> still not configured (needs both token and user id).
	ch.SetSetting(dto.ChannelSettings{AccountBalanceToken: "t"})
	assert.False(t, isChannelAccountBalanceConfigured(ch))

	ch.SetSetting(dto.ChannelSettings{AccountBalanceToken: "t", AccountBalanceUserID: "1"})
	assert.True(t, isChannelAccountBalanceConfigured(ch))
}

func TestUpdateChannelBalance_BadIdParam(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/channel/balance/abc", nil)
	ctx.Params = []gin.Param{{Key: "id", Value: "abc"}}
	UpdateChannelBalance(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestUpdateChannelAccountBalance_BadIdParam(t *testing.T) {
	ctx, rec := newCtx(t, "POST", "/api/channel/account-balance/abc", nil)
	ctx.Params = []gin.Param{{Key: "id", Value: "notint"}}
	UpdateChannelAccountBalance(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}
