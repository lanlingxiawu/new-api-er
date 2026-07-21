package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// user_oauth_binding.go — per-user/per-provider OAuth binding CRUD with
// two uniqueness constraints (one binding per user+provider; one OAuth account
// per provider).

func TestUserOAuthBinding_TableName(t *testing.T) {
	assert.Equal(t, "user_oauth_bindings", UserOAuthBinding{}.TableName())
}

// cleanupBindingsByProvider removes every binding for a provider id at teardown.
func cleanupBindingsByProvider(t *testing.T, providerId int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("provider_id = ?", providerId).Delete(&UserOAuthBinding{})
		}
	})
}

func TestCreateUserOAuthBinding_Validation(t *testing.T) {
	requireDB(t)
	providerId := nextTestID()
	cleanupBindingsByProvider(t, providerId)

	// missing user id
	err := CreateUserOAuthBinding(&UserOAuthBinding{ProviderId: providerId, ProviderUserId: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user ID is required")

	// missing provider id
	err = CreateUserOAuthBinding(&UserOAuthBinding{UserId: 1, ProviderUserId: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider ID is required")

	// missing provider user id
	err = CreateUserOAuthBinding(&UserOAuthBinding{UserId: 1, ProviderId: providerId})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider user ID is required")
}

func TestCreateUserOAuthBinding_SuccessAndDuplicate(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	user2 := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	b := &UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid}
	require.NoError(t, CreateUserOAuthBinding(b))
	assert.NotZero(t, b.Id)
	assert.False(t, b.CreatedAt.IsZero(), "CreatedAt should be stamped")

	// same provider account bound to another user -> rejected (fail before insert)
	err := CreateUserOAuthBinding(&UserOAuthBinding{UserId: user2.Id, ProviderId: providerId, ProviderUserId: puid})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already bound")
}

func TestGetUserOAuthBinding_And_ByUserId(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	p1 := nextTestID()
	p2 := nextTestID()
	cleanupBindingsByProvider(t, p1)
	cleanupBindingsByProvider(t, p2)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: p1, ProviderUserId: uniq("a")}))
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: p2, ProviderUserId: uniq("b")}))

	// specific binding
	got, err := GetUserOAuthBinding(user.Id, p1)
	require.NoError(t, err)
	assert.Equal(t, p1, got.ProviderId)

	// missing binding -> record-not-found error
	_, err = GetUserOAuthBinding(user.Id, nextTestID())
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// all bindings for the user
	list, err := GetUserOAuthBindingsByUserId(user.Id)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestGetUserByOAuthBinding(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid}))

	gotUser, err := GetUserByOAuthBinding(providerId, puid)
	require.NoError(t, err)
	assert.Equal(t, user.Id, gotUser.Id)

	// no binding matches -> error (binding lookup fails)
	_, err = GetUserByOAuthBinding(providerId, uniq("nope"))
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetUserByOAuthBinding_DanglingUser(t *testing.T) {
	requireDB(t)
	// Binding points at a user id that does not exist -> user lookup errors.
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{
		UserId: 999_000_000 + nextTestID(), ProviderId: providerId, ProviderUserId: puid,
	}))
	_, err := GetUserByOAuthBinding(providerId, puid)
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestIsProviderUserIdTaken(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	assert.False(t, IsProviderUserIdTaken(providerId, puid))
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid}))
	assert.True(t, IsProviderUserIdTaken(providerId, puid))
	// same puid but different provider is independent
	assert.False(t, IsProviderUserIdTaken(nextTestID(), puid))
}

func TestCreateUserOAuthBindingWithTx(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	// validation still enforced inside tx (all three required-field branches)
	err := DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{ProviderId: providerId, ProviderUserId: puid})
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user ID is required")

	err = DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{UserId: user.Id, ProviderUserId: puid})
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider ID is required")

	err = DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{UserId: user.Id, ProviderId: providerId})
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider user ID is required")

	// successful commit
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid})
	}))
	assert.True(t, IsProviderUserIdTaken(providerId, puid))

	// duplicate within tx -> rejected
	err = DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{UserId: mkUser(t, nil).Id, ProviderId: providerId, ProviderUserId: puid})
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already bound")
}

func TestUpdateUserOAuthBinding_CreatesWhenAbsent(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	// no existing binding -> creates a new one
	require.NoError(t, UpdateUserOAuthBinding(user.Id, providerId, puid))
	got, err := GetUserOAuthBinding(user.Id, providerId)
	require.NoError(t, err)
	assert.Equal(t, puid, got.ProviderUserId)
}

func TestUpdateUserOAuthBinding_UpdatesExisting(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	oldPuid := uniq("old")
	newPuid := uniq("new")
	cleanupBindingsByProvider(t, providerId)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: oldPuid}))
	require.NoError(t, UpdateUserOAuthBinding(user.Id, providerId, newPuid))

	got, err := GetUserOAuthBinding(user.Id, providerId)
	require.NoError(t, err)
	assert.Equal(t, newPuid, got.ProviderUserId)
}

func TestUpdateUserOAuthBinding_RejectsTakenByAnother(t *testing.T) {
	requireDB(t)
	userA := mkUser(t, nil)
	userB := mkUser(t, nil)
	providerId := nextTestID()
	puidB := uniq("puidB")
	cleanupBindingsByProvider(t, providerId)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: userB.Id, ProviderId: providerId, ProviderUserId: puidB}))

	// userA trying to rebind to userB's OAuth account -> rejected
	err := UpdateUserOAuthBinding(userA.Id, providerId, puidB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already bound")
}

func TestUpdateUserOAuthBinding_SamePuidSameUserIsNoConflict(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid}))
	// re-applying the same puid for the same user must not error
	require.NoError(t, UpdateUserOAuthBinding(user.Id, providerId, puid))
}

func TestDeleteUserOAuthBinding(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	providerId := nextTestID()
	puid := uniq("puid")
	cleanupBindingsByProvider(t, providerId)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: providerId, ProviderUserId: puid}))
	require.NoError(t, DeleteUserOAuthBinding(user.Id, providerId))

	_, err := GetUserOAuthBinding(user.Id, providerId)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// deleting a non-existent binding is not an error (0 rows affected)
	require.NoError(t, DeleteUserOAuthBinding(user.Id, providerId))
}

func TestDeleteUserOAuthBindingsByUserId(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	p1 := nextTestID()
	p2 := nextTestID()
	cleanupBindingsByProvider(t, p1)
	cleanupBindingsByProvider(t, p2)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: p1, ProviderUserId: uniq("a")}))
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: user.Id, ProviderId: p2, ProviderUserId: uniq("b")}))

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return deleteUserOAuthBindingsByUserId(tx, user.Id)
	}))

	list, err := GetUserOAuthBindingsByUserId(user.Id)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestGetBindingCountByProviderId(t *testing.T) {
	requireDB(t)
	providerId := nextTestID()
	cleanupBindingsByProvider(t, providerId)

	c, err := GetBindingCountByProviderId(providerId)
	require.NoError(t, err)
	assert.Equal(t, int64(0), c)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: mkUser(t, nil).Id, ProviderId: providerId, ProviderUserId: uniq("a")}))
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{UserId: mkUser(t, nil).Id, ProviderId: providerId, ProviderUserId: uniq("b")}))

	c, err = GetBindingCountByProviderId(providerId)
	require.NoError(t, err)
	assert.Equal(t, int64(2), c)
}
