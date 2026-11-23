package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestAdminChannelOperationRejectsInvalidTargets(t *testing.T) {
	for _, body := range []string{`{"ids":[]}`, `{"ids":[0]}`, `{"ids":[-1]}`, `{"ids":[1,2],"channel_id":3}`} {
		_, err := ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/batch", Body: []byte(body)})
		require.Error(t, err, body)
	}
	parsed, err := ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/batch", Body: []byte(`{"ids":[2,1,2]}`)})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, parsed.IDs)
	require.Equal(t, VerificationScopeAdminChannelDelete, parsed.Scope)

	duplicateIDs := make([]int, 101)
	for i := range duplicateIDs {
		duplicateIDs[i] = 1
	}
	duplicateBody, err := common.Marshal(map[string]any{"ids": duplicateIDs})
	require.NoError(t, err)
	parsed, err = ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/batch", Body: duplicateBody})
	require.NoError(t, err)
	require.Equal(t, []int{1}, parsed.IDs)
	uniqueIDs := make([]int, 101)
	for i := range uniqueIDs {
		uniqueIDs[i] = i + 1
	}
	uniqueBody, err := common.Marshal(map[string]any{"ids": uniqueIDs})
	require.NoError(t, err)
	_, err = ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/batch", Body: uniqueBody})
	require.ErrorIs(t, err, ErrVerificationContextInvalid)

	for _, name := range []string{"enable_all_keys", "disable_all_keys"} {
		action, err := ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/multi_key/manage", Body: []byte(fmt.Sprintf(`{"channel_id":1,"action":%q}`, name))})
		require.NoError(t, err)
		require.Equal(t, authz.ChannelOperate, action.Permission)
		require.Equal(t, VerificationScopeAdminChannelChange, action.Scope)
		require.False(t, action.Sensitive)
	}
	_, err = ParseAdminChannelOperation(AdminChannelOperation{Action: "POST /api/channel/fetch_models", Body: []byte(`{"channel_id":1}`)})
	require.Error(t, err)
}

func TestAdminChannelProofBindsTargetsChangesAndRevocation(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelCreator{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.UserAccessToken{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.ChannelCostConfig{}))
	useTestSessionSecret(t)
	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	require.NoError(t, authz.Init(model.DB))
	user := model.User{Username: "pat-proof-" + common.GetRandomString(10), Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	t.Cleanup(func() {
		model.DB.Where("v0 = ?", authz.UserSubject(user.Id)).Delete(&model.CasbinRule{})
		model.DB.Unscoped().Delete(&user)
	})
	require.NoError(t, authz.SetUserPermissionsInTx(model.DB, user.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionChannelsOwned: true}, authz.ResourceChannel: {authz.ActionSensitiveWrite: true}}))
	token := model.UserAccessToken{UserId: user.Id, Name: "test", TokenHash: common.GetRandomString(40), CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 3600}
	require.NoError(t, token.SetScopes([]string{"channel:write", "channel:operate", "channel:sensitive_write"}))
	require.NoError(t, model.DB.Create(&token).Error)
	t.Cleanup(func() { model.DB.Delete(&token) })
	identity := AuthIdentity{UserID: user.Id, SessionID: model.AccessTokenSessionID(token.Id), UserAuthVersion: 1, SessionVersion: model.AccessTokenSessionVersion}
	twoFA := model.TwoFA{UserId: user.Id, Secret: "test-only", IsEnabled: true}
	require.NoError(t, model.DB.Create(&twoFA).Error)
	t.Cleanup(func() {
		model.DB.Where("user_id = ?", user.Id).Delete(&model.AuthFlow{})
		model.DB.Unscoped().Delete(&twoFA)
	})
	channels := []model.Channel{{Name: "proof-channel", Key: "secret", Models: "m", Group: "default"}}
	require.NoError(t, model.BatchInsertChannels(channels, user.Id))
	id := channels[0].Id
	t.Cleanup(func() {
		model.DB.Where("channel_id = ?", id).Delete(&model.Ability{})
		model.DB.Where("channel_id = ?", id).Delete(&model.ChannelCreator{})
		model.DB.Delete(&channels[0])
	})
	operation := AdminChannelOperation{Action: "POST /api/channel/:id/status", ChannelID: id, Body: []byte(`{"status":2}`)}
	raw, err := common.Marshal(operation)
	require.NoError(t, err)
	verification := VerificationOperation{Scope: VerificationScopeAdminChannelChange, Context: raw}
	binding, err := BindAuthorizedVerificationOperation(identity, verification)
	require.NoError(t, err)
	require.NotContains(t, binding.ContextHash, "secret")
	operation.Query = map[string]string{}
	emptyQueryRaw, err := common.Marshal(operation)
	require.NoError(t, err)
	// Explicit {} in incoming JSON must bind just like omitted query parameters.
	emptyQueryRaw = []byte(strings.TrimSuffix(string(emptyQueryRaw), "}") + `,"query":{}}`)
	emptyQueryBinding, err := BindAuthorizedVerificationOperation(identity, VerificationOperation{Scope: VerificationScopeAdminChannelChange, Context: emptyQueryRaw})
	require.NoError(t, err)
	require.Equal(t, binding.ContextHash, emptyQueryBinding.ContextHash)
	proof, _, err := IssueSecurityProof(identity, VerificationMethodTwoFA, binding)
	require.NoError(t, err)
	wrongIdentity := identity
	wrongIdentity.SessionID = model.AccessTokenSessionID(token.Id + 1)
	_, err = verifySecurityProof(proof, wrongIdentity, binding)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)
	claims, err := parseAuthClaims(proof, securityProofTokenUse, authSigningKey(securityProofTokenUse))
	require.NoError(t, err)
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(authSigningKey(securityProofTokenUse))
	require.NoError(t, err)
	_, err = ConsumeOperationProof(expired, identity, verification)
	require.ErrorIs(t, err, ErrAuthTokenExpired)
	_, err = ConsumeOperationProof(proof, identity, verification)
	require.NoError(t, err)
	_, err = ConsumeOperationProof(proof, identity, verification)
	require.ErrorIs(t, err, ErrProofConsumed)
	proof, _, err = IssueSecurityProof(identity, VerificationMethodTwoFA, binding)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&channels[0]).Update("priority", 7).Error)
	_, err = ConsumeOperationProof(proof, identity, verification)
	require.ErrorIs(t, err, ErrProofContext, "changed configuration invalidates the old proof")
	require.NoError(t, model.DB.Model(&channels[0]).Update("priority", nil).Error)
	beforeOtherInfo, err := BindAuthorizedVerificationOperation(identity, verification)
	require.NoError(t, err)
	originalOtherInfo := channels[0].OtherInfo
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", id).Update("other_info", `{"status_reason":"manual operation"}`).Error)
	afterOtherInfo, err := BindAuthorizedVerificationOperation(identity, verification)
	require.NoError(t, err)
	require.NotEqual(t, beforeOtherInfo.ContextHash, afterOtherInfo.ContextHash, "runtime status inherited by copy and multi-key actions must be bound")
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", id).Update("other_info", originalOtherInfo).Error)
	operation.Body = []byte(`{"status":1}`)
	raw, err = common.Marshal(operation)
	require.NoError(t, err)
	changed, err := BindAuthorizedVerificationOperation(identity, VerificationOperation{Scope: VerificationScopeAdminChannelChange, Context: raw})
	require.NoError(t, err)
	require.NotEqual(t, binding.ContextHash, changed.ContextHash)
	operation.ChannelID = id + 10000
	_, err = AuthorizeAdminChannelOperation(context.Background(), identity, operation)
	require.ErrorIs(t, err, model.ErrChannelNotAccessible)
	operation = AdminChannelOperation{Action: "PUT /api/channel/", Body: []byte(fmt.Sprintf(`{"id":%d,"base_url":"https://changed.invalid"}`, id))}
	require.NoError(t, token.SetScopes([]string{"channel:write"}))
	require.NoError(t, model.DB.Model(&token).Update("scopes", token.Scopes).Error)
	_, err = AuthorizeAdminChannelOperation(context.Background(), identity, operation)
	require.ErrorIs(t, err, ErrVerificationForbidden)
	require.NoError(t, authz.SetUserPermissionsInTx(model.DB, user.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: false}}))
	_, err = BindAuthorizedVerificationOperation(identity, verification)
	require.ErrorIs(t, err, ErrAdminTokenDenied)
	_, err = ConsumeOperationProof(proof, identity, verification)
	require.ErrorIs(t, err, ErrAdminTokenDenied, "revocation rejects a previously issued proof")
	for _, method := range []string{VerificationMethodPassword, VerificationMethodSession, VerificationMethodOAuth} {
		options, err := securityVerificationPolicy(VerificationScopeAdminChannelChange, model.UserVerificationState{HasPassword: true})
		require.NoError(t, err)
		for _, option := range options {
			require.NotEqual(t, method, option.Method)
		}
	}
}
