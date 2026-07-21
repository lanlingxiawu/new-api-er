package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// channelOwnerName resolves a channel type to a display owner string, falling
// back to the lower-cased channel type name when no adaptor name is available.
func TestChannelOwnerName_UsesAdaptorOrFallback(t *testing.T) {
	// OpenAI (type 1) resolves through the adaptor and yields a non-empty owner.
	owner := channelOwnerName(1)
	assert.NotEmpty(t, owner)

	// An unknown/invalid channel type falls back to the lower-cased type name.
	fallback := channelOwnerName(-999)
	assert.NotEmpty(t, fallback)
	assert.Equal(t, fallback, channelOwnerName(-999), "fallback must be deterministic")
}

// buildOpenAIModel: a known static model keeps its metadata; the owner override
// map wins when present and non-empty.
func TestBuildOpenAIModel_OwnerOverride(t *testing.T) {
	oaModel := buildOpenAIModel("gpt-4o", map[string]string{"gpt-4o": "acme"})
	assert.Equal(t, "gpt-4o", oaModel.Id)
	assert.Equal(t, "acme", oaModel.OwnedBy)
	assert.Equal(t, "model", oaModel.Object)
}

// buildOpenAIModel: an unknown model with no override falls back to "custom".
func TestBuildOpenAIModel_UnknownFallsBackToCustom(t *testing.T) {
	oaModel := buildOpenAIModel("totally-unknown-model-xyz", map[string]string{})
	assert.Equal(t, "totally-unknown-model-xyz", oaModel.Id)
	assert.Equal(t, "custom", oaModel.OwnedBy)
}

// buildOpenAIModel: an empty override value does not clobber the default owner.
func TestBuildOpenAIModel_EmptyOverrideIgnored(t *testing.T) {
	oaModel := buildOpenAIModel("unknown-model-abc", map[string]string{"unknown-model-abc": ""})
	assert.Equal(t, "custom", oaModel.OwnedBy)
}

func newGroupsCtx() *gin.Context {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	return ctx
}

// getModelListGroups: when the token group is empty, the resolved user group is
// used as the single owner group (no DB lookup because user_group is set).
func TestGetModelListGroups_UsesUserGroupWhenTokenGroupEmpty(t *testing.T) {
	ctx := newGroupsCtx()
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "svip")
	// token group unset

	groups, err := getModelListGroups(ctx)
	require.NoError(t, err)
	assert.Equal(t, "svip", groups.userGroup)
	assert.Empty(t, groups.tokenGroup)
	assert.Equal(t, []string{"svip"}, groups.ownerGroups)
}

// getModelListGroups: an explicit token group overrides the user group for the
// owner group set.
func TestGetModelListGroups_UsesExplicitTokenGroup(t *testing.T) {
	ctx := newGroupsCtx()
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "svip")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "vip")

	groups, err := getModelListGroups(ctx)
	require.NoError(t, err)
	assert.Equal(t, "svip", groups.userGroup)
	assert.Equal(t, "vip", groups.tokenGroup)
	assert.Equal(t, []string{"vip"}, groups.ownerGroups)
}
