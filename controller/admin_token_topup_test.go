package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
)

func TestAdminTokenTopupReconcileRequiresProviderPayment(t *testing.T) {
	actor := setupAdminTokenAutomation(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionTopupsReconcile: true}}))
	raw, _ := createScopedAccessToken(t, actor.Id, common.GetTimestamp()+3600, "billing:topup_reconcile")
	user := model.User{Username: "topup-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "topup-customer"}
	require.NoError(t, db.Create(&user).Error)
	order := model.TopUp{UserId: user.Id, TradeNo: "fixture-paid-order", Amount: 1, Money: 1, PaymentProvider: model.PaymentProviderAlipay, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	stub := &stubAlipayTradeQuerier{response: &alipay.TradeQueryRsp{Error: alipay.Error{Code: alipay.CodeSuccess}, TradeStatus: alipay.TradeStatusWaitBuyerPay}}
	previous := getAlipayTradeQuerier
	getAlipayTradeQuerier = func() alipayTradeQuerier { return stub }
	t.Cleanup(func() { getAlipayTradeQuerier = previous })
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	group := router.Group("/api/user", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuUsersView))
	group.POST("/topup/reconcile", AdminReconcileTopUp)
	group.POST("/topup/complete", AdminCompleteTopUp)
	body := fmt.Sprintf(`{"trade_no":%q}`, order.TradeNo)
	response := accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
	require.Contains(t, response.Body.String(), `"success":false`)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
	stub.err = errors.New("injected provider failure")
	response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NotContains(t, response.Body.String(), "injected")
	stub.err = nil
	stub.response.TradeStatus = "UNKNOWN"
	response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, db.Model(&order).Update("payment_provider", model.PaymentProviderStripe).Error)
	response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota, "failed, unknown and unsupported provider queries never credit the wallet")
	require.NoError(t, db.Model(&order).Update("payment_provider", model.PaymentProviderAlipay).Error)
	stub.response.TradeStatus = alipay.TradeStatusSuccess
	for range 2 {
		response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	}
	require.Contains(t, response.Body.String(), `"already_completed":true`)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(common.QuotaPerUnit), stored.Quota)
	response = accessTokenRequest(router, "POST", "/api/user/topup/complete", raw, "", body)
	require.Equal(t, http.StatusForbidden, response.Code, "the unverified manual completion route must remain blocked")
	response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", `{"trade_no":"fixture-paid-order","paid":true}`)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionTopupsReconcile: false}}))
	response = accessTokenRequest(router, "POST", "/api/user/topup/reconcile", raw, "", body)
	require.Equal(t, http.StatusForbidden, response.Code)
}
