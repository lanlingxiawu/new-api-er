package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 13: the wallet quota rejections reach API clients, so they follow the
// request language instead of being hardcoded Chinese.

func quotaLangCtx(t *testing.T, acceptLanguage string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if acceptLanguage != "" {
		c.Request.Header.Set("Accept-Language", acceptLanguage)
	}
	return c
}

func TestQuotaRejectMessage_Language(t *testing.T) {
	require.NoError(t, i18n.Init())
	args := map[string]any{"Remaining": "$0.00"}
	en := "Insufficient account quota. Remaining quota: $0.00. Please top up and try again."

	assert.Equal(t, en, quotaRejectMessage(nil, i18n.MsgQuotaUserInsufficient, args), "nil context")
	noRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, en, quotaRejectMessage(noRequest, i18n.MsgQuotaUserInsufficient, args), "context without request")
	assert.Equal(t, en, quotaRejectMessage(quotaLangCtx(t, ""), i18n.MsgQuotaUserInsufficient, args), "no Accept-Language")
	assert.Equal(t, "用户额度不足，剩余额度：$0.00。请充值后重试。",
		quotaRejectMessage(quotaLangCtx(t, "zh-CN,zh;q=0.9"), i18n.MsgQuotaUserInsufficient, args))
	assert.Equal(t, "使用者額度不足，剩餘額度：$0.00。請儲值後重試。",
		quotaRejectMessage(quotaLangCtx(t, "zh-TW"), i18n.MsgQuotaUserInsufficient, args))
}

func TestBS_WalletRejections_AreTranslated(t *testing.T) {
	require.NoError(t, i18n.Init())

	const emptyUid = 3190
	seedUser(t, emptyUid, 0)
	_, apiErr := NewBillingSession(quotaLangCtx(t, ""), bsRelayInfo(emptyUid, 0, "", "wallet_only"), 100)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "Insufficient account quota. Remaining quota:")
	assert.NotContains(t, apiErr.Error(), "用户")

	_, apiErr = NewBillingSession(quotaLangCtx(t, "zh-CN"), bsRelayInfo(emptyUid, 0, "", "wallet_only"), 100)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "用户额度不足，剩余额度：")

	const shortUid = 3191
	seedUser(t, shortUid, 1500)
	_, apiErr = NewBillingSession(quotaLangCtx(t, "en"), bsRelayInfo(shortUid, 0, "", "wallet_only"), 2000)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Contains(t, apiErr.Error(), "Insufficient account quota for this request. Remaining quota:")
	assert.Contains(t, apiErr.Error(), ", required: ")
	assert.Equal(t, 1500, getUserQuota(t, shortUid), "nothing deducted on reject")
}
