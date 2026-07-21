package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// custom_oauth_provider.go — CRUD for custom OAuth providers, slug validation,
// field-mapping defaults, and access-policy JSON validation.

func TestCustomOAuthProvider_TableName(t *testing.T) {
	assert.Equal(t, "custom_oauth_providers", CustomOAuthProvider{}.TableName())
}

// validProvider builds a minimal provider that passes validateCustomOAuthProvider.
func validProvider(slug string) *CustomOAuthProvider {
	return &CustomOAuthProvider{
		Name:                  "Test Provider",
		Slug:                  slug,
		ClientId:              "client-id",
		AuthorizationEndpoint: "https://example.com/authorize",
		TokenEndpoint:         "https://example.com/token",
		UserInfoEndpoint:      "https://example.com/userinfo",
	}
}

func cleanupProviderBySlug(t *testing.T, slug string) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("slug = ?", slug).Delete(&CustomOAuthProvider{})
		}
	})
}

func TestCreateCustomOAuthProvider_DefaultsAndSlugLowercase(t *testing.T) {
	requireDB(t)
	// uniqCode yields lowercase alphanumerics; hyphen is a valid slug char.
	slug := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slug)

	p := validProvider(slug)
	p.Scopes = ""
	require.NoError(t, CreateCustomOAuthProvider(p))
	assert.NotZero(t, p.Id)

	// field-mapping + scope defaults filled by validation
	assert.Equal(t, "sub", p.UserIdField)
	assert.Equal(t, "preferred_username", p.UsernameField)
	assert.Equal(t, "name", p.DisplayNameField)
	assert.Equal(t, "email", p.EmailField)
	assert.Equal(t, "openid profile email", p.Scopes)

	// re-read from DB to confirm persistence
	got, err := GetCustomOAuthProviderById(p.Id)
	require.NoError(t, err)
	assert.Equal(t, slug, got.Slug)
	assert.False(t, got.Enabled) // default false
}

func TestCreateCustomOAuthProvider_SlugUppercasedIsNormalized(t *testing.T) {
	requireDB(t)
	suffix := uniqCode() // lowercase alphanumerics
	normalized := "prov-" + suffix
	cleanupProviderBySlug(t, normalized)
	p := validProvider("PROV-" + suffix) // mixed case input
	require.NoError(t, CreateCustomOAuthProvider(p))
	assert.Equal(t, normalized, p.Slug, "slug must be lowercased in place")
}

func TestValidateCustomOAuthProvider_RequiredFields(t *testing.T) {
	requireDB(t)
	cases := []struct {
		name    string
		mutate  func(p *CustomOAuthProvider)
		wantSub string
	}{
		{"empty name", func(p *CustomOAuthProvider) { p.Name = "" }, "provider name is required"},
		{"empty slug", func(p *CustomOAuthProvider) { p.Slug = "" }, "provider slug is required"},
		{"bad slug chars", func(p *CustomOAuthProvider) { p.Slug = "Bad Slug!" }, "only lowercase letters"},
		{"empty client id", func(p *CustomOAuthProvider) { p.ClientId = "" }, "client ID is required"},
		{"empty authorize", func(p *CustomOAuthProvider) { p.AuthorizationEndpoint = "" }, "authorization endpoint is required"},
		{"empty token", func(p *CustomOAuthProvider) { p.TokenEndpoint = "" }, "token endpoint is required"},
		{"empty userinfo", func(p *CustomOAuthProvider) { p.UserInfoEndpoint = "" }, "user info endpoint is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validProvider("prov-" + uniqCode())
			c.mutate(p)
			err := CreateCustomOAuthProvider(p)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantSub)
		})
	}
}

func TestCreateCustomOAuthProvider_AccessPolicyValidation(t *testing.T) {
	requireDB(t)

	// invalid JSON
	p := validProvider("prov-" + uniqCode())
	p.AccessPolicy = "{not json"
	err := CreateCustomOAuthProvider(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid JSON")

	// valid JSON but semantically invalid (no conditions/groups)
	p = validProvider("prov-" + uniqCode())
	p.AccessPolicy = `{"logic":"and","conditions":[]}`
	err = CreateCustomOAuthProvider(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access_policy is invalid")

	// valid policy accepted and persisted
	slug := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slug)
	p = validProvider(slug)
	p.AccessPolicy = `{"logic":"or","conditions":[{"field":"email","op":"contains","value":"@corp.com"}]}`
	require.NoError(t, CreateCustomOAuthProvider(p))
}

func TestUpdateCustomOAuthProvider(t *testing.T) {
	requireDB(t)
	slug := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slug)
	p := validProvider(slug)
	require.NoError(t, CreateCustomOAuthProvider(p))

	p.Name = "Renamed"
	p.Enabled = true
	require.NoError(t, UpdateCustomOAuthProvider(p))

	got, err := GetCustomOAuthProviderById(p.Id)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", got.Name)
	assert.True(t, got.Enabled)

	// update that fails validation is rejected
	p.Name = ""
	err = UpdateCustomOAuthProvider(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider name is required")
}

func TestGetProviders_ByIdSlugAllEnabled(t *testing.T) {
	requireDB(t)
	slugOn := "prov-" + uniqCode()
	slugOff := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slugOn)
	cleanupProviderBySlug(t, slugOff)

	on := validProvider(slugOn)
	on.Enabled = true
	require.NoError(t, CreateCustomOAuthProvider(on))
	off := validProvider(slugOff)
	off.Enabled = false
	require.NoError(t, CreateCustomOAuthProvider(off))

	// by slug
	gotBySlug, err := GetCustomOAuthProviderBySlug(slugOn)
	require.NoError(t, err)
	assert.Equal(t, on.Id, gotBySlug.Id)

	// missing slug/id -> not found
	_, err = GetCustomOAuthProviderBySlug(uniq("missing"))
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetCustomOAuthProviderById(999_000_000 + nextTestID())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// all providers contains both
	all, err := GetAllCustomOAuthProviders()
	require.NoError(t, err)
	ids := map[int]bool{}
	for _, pr := range all {
		ids[pr.Id] = true
	}
	assert.True(t, ids[on.Id])
	assert.True(t, ids[off.Id])

	// enabled-only excludes disabled
	enabled, err := GetEnabledCustomOAuthProviders()
	require.NoError(t, err)
	enIds := map[int]bool{}
	for _, pr := range enabled {
		enIds[pr.Id] = true
		assert.True(t, pr.Enabled)
	}
	assert.True(t, enIds[on.Id])
	assert.False(t, enIds[off.Id])
}

func TestIsSlugTaken(t *testing.T) {
	requireDB(t)
	slug := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slug)

	assert.False(t, IsSlugTaken(slug, 0))
	p := validProvider(slug)
	require.NoError(t, CreateCustomOAuthProvider(p))

	assert.True(t, IsSlugTaken(slug, 0))
	// excluding the owning id -> not taken (allows self-update)
	assert.False(t, IsSlugTaken(slug, p.Id))
	// excluding a different id -> still taken
	assert.True(t, IsSlugTaken(slug, p.Id+1))
}

func TestDeleteCustomOAuthProvider_CascadesBindings(t *testing.T) {
	requireDB(t)
	slug := "prov-" + uniqCode()
	cleanupProviderBySlug(t, slug)
	p := validProvider(slug)
	require.NoError(t, CreateCustomOAuthProvider(p))

	user := mkUser(t, nil)
	cleanupBindingsByProvider(t, p.Id)
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: p.Id, ProviderUserId: uniq("puid")}))

	require.NoError(t, DeleteCustomOAuthProvider(p.Id))

	// provider gone
	_, err := GetCustomOAuthProviderById(p.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	// binding removed by cascade
	c, err := GetBindingCountByProviderId(p.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), c)
}

// ---------------------------------------------------------------------------
// validateAccessPolicyPayload — exercised directly for full branch coverage.
// ---------------------------------------------------------------------------

func TestValidateAccessPolicyPayload(t *testing.T) {
	// nil policy
	assert.Error(t, validateAccessPolicyPayload(nil))

	// empty logic defaults to "and"; requires at least one condition/group
	assert.Error(t, validateAccessPolicyPayload(&accessPolicyPayload{}))

	// unsupported logic
	err := validateAccessPolicyPayload(&accessPolicyPayload{
		Logic:      "xor",
		Conditions: []accessConditionItem{{Field: "email", Op: "eq", Value: "a"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported logic")

	// condition missing field
	err = validateAccessPolicyPayload(&accessPolicyPayload{
		Conditions: []accessConditionItem{{Field: "  ", Op: "eq"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field is required")

	// unsupported op
	err = validateAccessPolicyPayload(&accessPolicyPayload{
		Conditions: []accessConditionItem{{Field: "email", Op: "regex"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")

	// in/not_in requires array value
	err = validateAccessPolicyPayload(&accessPolicyPayload{
		Conditions: []accessConditionItem{{Field: "role", Op: "in", Value: "admin"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an array")

	// valid in with array value
	require.NoError(t, validateAccessPolicyPayload(&accessPolicyPayload{
		Logic:      "AND", // case-insensitive
		Conditions: []accessConditionItem{{Field: "role", Op: "in", Value: []any{"admin", "user"}}},
	}))

	// nested groups: valid outer, invalid inner propagates
	err = validateAccessPolicyPayload(&accessPolicyPayload{
		Logic: "or",
		Groups: []accessPolicyPayload{
			{Conditions: []accessConditionItem{{Field: "", Op: "eq"}}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group[0]")

	// nested groups all valid
	require.NoError(t, validateAccessPolicyPayload(&accessPolicyPayload{
		Logic: "or",
		Groups: []accessPolicyPayload{
			{Conditions: []accessConditionItem{{Field: "email", Op: "exists"}}},
		},
	}))
}
