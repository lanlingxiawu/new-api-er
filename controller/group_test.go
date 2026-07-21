package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetGroups returns the configured group ratio names as a string slice.
func TestGetGroups(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/group/", nil)
	asAdmin(ctx, 1)
	GetGroups(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var names []string
	require.NoError(t, common.Unmarshal(resp.Data, &names))
	// the shipped default group set always includes "default"
	assert.Contains(t, names, "default")
}

// GetUserGroups returns the usable groups for the authenticated user, keyed by
// group name, each with a ratio and description.
func TestGetUserGroups(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *model.User) { u.Group = "default" })

	ctx, rec := newCtx(t, http.MethodGet, "/api/user/groups", nil)
	asUser(ctx, u.Id)
	GetUserGroups(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var groups map[string]map[string]any
	require.NoError(t, common.Unmarshal(resp.Data, &groups))
	// the user's own default group should be usable
	assert.Contains(t, groups, "default")
}
