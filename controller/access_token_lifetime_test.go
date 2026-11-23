package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessTokenLongLivedManagementGrantCanOnlyShrink(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, authz.SetUserPermissions(user.Id, authz.PermissionsMap{
		authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionChannelsOwned: true},
	}))
	router := newAccessTokenTestRouter()
	identity, browser := createAccessTokenTestSession(t, user.Id, "management-reduction-session")
	_, token := createScopedAccessToken(t, user.Id, 0, "channel:read", "channel:write")
	tokenPath := fmt.Sprintf("/api/user/access_tokens/%d", token.Id)

	expandedScopes := []string{"channel:read", "channel:write", "profile:read"}
	expansionProof := issueAccessTokenUpdateProof(t, identity, token.Id, expandedScopes...)
	response := accessTokenRequest(router, http.MethodPatch, tokenPath, browser, expansionProof, `{"name":"expanded","scopes":["channel:read","channel:write","profile:read"]}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"code":"ACCESS_TOKEN_EXPIRY_INVALID"`)
	var consumed int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("consumed_at IS NOT NULL").Count(&consumed).Error)
	assert.Zero(t, consumed, "an expired management grant must reject expansion before spending proof")
	stored, err := model.GetUserAccessToken(user.Id, token.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"channel:read", "channel:write"}, stored.GetScopes())
	assert.Equal(t, "test token", stored.Name)

	reductionProof := issueAccessTokenUpdateProof(t, identity, token.Id, "channel:read")
	response = accessTokenRequest(router, http.MethodPatch, tokenPath, browser, reductionProof, `{"name":"reduced","scopes":["channel:read"]}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"success":true`)
	stored, err = model.GetUserAccessToken(user.Id, token.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"channel:read"}, stored.GetScopes())
	assert.Equal(t, "reduced", stored.Name)
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("consumed_at IS NOT NULL").Count(&consumed).Error)
	assert.EqualValues(t, 1, consumed)
}
