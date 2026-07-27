package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestUser wraps the shared mkUser factory and additionally assigns a unique
// aff_code. The users table has a UNIQUE index on aff_code, and the bare factory
// leaves it empty (""), so creating more than one factory user inside a single
// test would collide on the empty value. Assigning a unique code here keeps
// multi-user tests independent without touching the shared harness.
func newTestUser(t *testing.T, mut func(u *User)) *User {
	t.Helper()
	return mkUser(t, func(u *User) {
		code := strings.ReplaceAll(uniq("aff"), "_", "")
		if len(code) > 32 {
			code = code[:32]
		}
		u.AffCode = code
		if mut != nil {
			mut(u)
		}
	})
}

// ===========================================================================
// Pure logic: sort options, base user, setting/token accessors
// ===========================================================================

func TestNewUserSortOptions(t *testing.T) {
	// unknown column -> id/desc
	o := NewUserSortOptions("bogus", "asc")
	assert.Equal(t, "id", o.SortBy)
	assert.Equal(t, "desc", o.SortOrder)

	// valid column, asc preserved
	o = NewUserSortOptions("Quota", "ASC")
	assert.Equal(t, "quota", o.SortBy)
	assert.Equal(t, "asc", o.SortOrder)

	// valid column, non-asc order normalised to desc
	o = NewUserSortOptions("username", "weird")
	assert.Equal(t, "username", o.SortBy)
	assert.Equal(t, "desc", o.SortOrder)

	// valid column, explicit desc
	o = NewUserSortOptions("group", "desc")
	assert.Equal(t, "group", o.SortBy)
	assert.Equal(t, "desc", o.SortOrder)
}

func TestUserSortOptions_Apply(t *testing.T) {
	requireDB(t)
	// id column: single ordering clause; non-id column: secondary id ordering.
	for _, sb := range []string{"id", "username", "quota", "created_at", "last_login_at", "group"} {
		o := NewUserSortOptions(sb, "asc")
		var users []*User
		// Just ensure the produced SQL executes without error.
		err := o.Apply(DB.Model(&User{})).Limit(1).Find(&users).Error
		require.NoErrorf(t, err, "apply sort %s", sb)
	}

	// SortBy set to a value absent from the map (via struct literal) falls back to id.
	bad := UserSortOptions{SortBy: "not_a_col", SortOrder: "asc"}
	var users []*User
	require.NoError(t, bad.Apply(DB.Model(&User{})).Limit(1).Find(&users).Error)
}

func TestUser_ToBaseUserAndAccessors(t *testing.T) {
	u := &User{
		Id: 5, Group: "g", GroupRatios: `{"g":1}`, Quota: 9, Status: common.UserStatusEnabled,
		Username: "bob", Setting: `{"language":"en"}`, Email: "b@c.com",
	}
	base := u.ToBaseUser()
	assert.Equal(t, 5, base.Id)
	assert.Equal(t, "g", base.Group)
	assert.Equal(t, 9, base.Quota)
	assert.Equal(t, "bob", base.Username)

	// access token accessors
	assert.Equal(t, "", u.GetAccessToken())
	u.SetAccessToken("tok123")
	assert.Equal(t, "tok123", u.GetAccessToken())
}

func TestUser_GetSetSetting(t *testing.T) {
	u := &User{}
	// empty -> zero value
	assert.Equal(t, dto.UserSetting{}, u.GetSetting())

	u.SetSetting(dto.UserSetting{Language: "zh"})
	assert.Contains(t, u.Setting, "zh")
	assert.Equal(t, "zh", u.GetSetting().Language)

	// invalid JSON degrades silently
	u.Setting = `{bad`
	assert.Equal(t, dto.UserSetting{}, u.GetSetting())
}

func TestNormalizeEmail(t *testing.T) {
	assert.Equal(t, "a@b.com", NormalizeEmail("  A@B.CoM "))
	assert.Equal(t, "", NormalizeEmail("   "))
}

// ===========================================================================
// Email availability / counting
// ===========================================================================

func TestEmailAvailabilityFuncs(t *testing.T) {
	email := uniq("mail") + "@ex.com"
	u := newTestUser(t, func(u *User) { u.Email = email })

	// CountUsersByEmail: empty -> 0; case-insensitive match -> 1
	n, err := CountUsersByEmail("")
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	n, err = CountUsersByEmail(strings.ToUpper(email))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	// IsEmailAvailable: empty -> true; taken -> false; exclude owner -> true
	ok, err := IsEmailAvailable("", 0)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = IsEmailAvailable(email, 0)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = IsEmailAvailable(email, u.Id)
	require.NoError(t, err)
	assert.True(t, ok)

	// EnsureEmailAvailable: taken -> ErrEmailAlreadyTaken; excluding owner -> nil
	assert.ErrorIs(t, EnsureEmailAvailable(email, 0), ErrEmailAlreadyTaken)
	require.NoError(t, EnsureEmailAvailable(email, u.Id))

	// IsEmailAlreadyTaken helper
	assert.True(t, IsEmailAlreadyTaken(email))
	assert.False(t, IsEmailAlreadyTaken(uniq("nope")+"@x.com"))
}

func TestCheckUserExistOrDeleted(t *testing.T) {
	email := uniq("cx") + "@ex.com"
	u := newTestUser(t, func(u *User) { u.Email = email })

	// existing username -> true
	exist, err := CheckUserExistOrDeleted(u.Username, "")
	require.NoError(t, err)
	assert.True(t, exist)

	// existing by email (username mismatch)
	exist, err = CheckUserExistOrDeleted(uniq("other"), email)
	require.NoError(t, err)
	assert.True(t, exist)

	// neither exists -> false
	exist, err = CheckUserExistOrDeleted(uniq("ghost"), uniq("ghost")+"@x.com")
	require.NoError(t, err)
	assert.False(t, exist)
}

func TestGetUniqueUserByEmail(t *testing.T) {
	// empty -> ErrEmailNotFound
	_, err := GetUniqueUserByEmail("")
	assert.ErrorIs(t, err, ErrEmailNotFound)

	// not found -> ErrEmailNotFound
	_, err = GetUniqueUserByEmail(uniq("missing") + "@x.com")
	assert.ErrorIs(t, err, ErrEmailNotFound)

	// single match -> user
	email := uniq("uniq") + "@ex.com"
	u := newTestUser(t, func(u *User) { u.Email = email })
	got, err := GetUniqueUserByEmail(strings.ToUpper(email))
	require.NoError(t, err)
	assert.Equal(t, u.Id, got.Id)

	// ambiguous -> ErrEmailAmbiguous
	dupEmail := uniq("dup") + "@ex.com"
	newTestUser(t, func(u *User) { u.Email = dupEmail })
	newTestUser(t, func(u *User) { u.Email = dupEmail })
	_, err = GetUniqueUserByEmail(dupEmail)
	assert.ErrorIs(t, err, ErrEmailAmbiguous)
}

// ===========================================================================
// Insert / finish / OAuth insert
// ===========================================================================

func TestUser_Insert(t *testing.T) {
	requireDB(t)
	u := &User{
		Username: uniq("ins"),
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, u.Insert(0))
	require.NotZero(t, u.Id)
	deleteByID(t, &User{}, u.Id)

	// Quota initialised to QuotaForNewUser; aff code generated (4 chars)
	assert.Equal(t, common.QuotaForNewUser, u.Quota)
	assert.Len(t, u.AffCode, 4)

	// password stored hashed (not plaintext)
	reloaded, err := GetUserById(u.Id, true)
	require.NoError(t, err)
	assert.NotEqual(t, "password123", reloaded.Password)
	assert.True(t, common.ValidatePasswordAndHash("password123", reloaded.Password))

	// finishInsert seeded a role-based sidebar config into the setting
	assert.NotEmpty(t, reloaded.GetSetting().SidebarModules)

	// FinishInsert is an idempotent wrapper (QuotaForNewUser==0 -> no extra effect)
	u.FinishInsert(0)

	// duplicate email is rejected by Insert
	email := uniq("dupins") + "@ex.com"
	newTestUser(t, func(x *User) { x.Email = email })
	dup := &User{Username: uniq("ins2"), Password: "password123", Email: email}
	assert.ErrorIs(t, dup.Insert(0), ErrEmailAlreadyTaken)
}

func TestUser_InsertWithTxAndFinalize(t *testing.T) {
	requireDB(t)
	u := &User{
		Username: uniq("oauth"),
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		return u.InsertWithTx(tx, 0)
	})
	require.NoError(t, err)
	require.NotZero(t, u.Id)
	deleteByID(t, &User{}, u.Id)
	assert.Equal(t, common.QuotaForNewUser, u.Quota)

	u.FinalizeOAuthUserCreation(0)
	reloaded, err := GetUserById(u.Id, true)
	require.NoError(t, err)
	assert.NotEmpty(t, reloaded.GetSetting().SidebarModules)
}

// ===========================================================================
// Lookups by id / affcode / batch
// ===========================================================================

func TestGetUserById(t *testing.T) {
	u := newTestUser(t, nil)

	// id 0 -> error
	_, err := GetUserById(0, false)
	assert.Error(t, err)

	// selectAll false omits password
	got, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, u.Id, got.Id)
	assert.Equal(t, "", got.Password)

	// selectAll true includes password
	got, err = GetUserById(u.Id, true)
	require.NoError(t, err)
	assert.NotEqual(t, "", got.Password)

	// missing id -> record not found
	_, err = GetUserById(999_999_999, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetUsersByIds(t *testing.T) {
	a := newTestUser(t, nil)
	b := newTestUser(t, nil)

	// empty ids -> empty map
	m, err := GetUsersByIds(nil)
	require.NoError(t, err)
	assert.Empty(t, m)

	m, err = GetUsersByIds([]int{a.Id, b.Id, 999_999_999})
	require.NoError(t, err)
	require.Len(t, m, 2)
	assert.Equal(t, a.Username, m[a.Id].Username)

	// unscoped variant (only id/username/display_name)
	m2, err := GetUsersByIdsUnscoped([]int{a.Id, b.Id})
	require.NoError(t, err)
	assert.Len(t, m2, 2)

	m3, err := GetUsersByIdsUnscopedWithContext(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, m3)
}

func TestGetUserIdByAffCode(t *testing.T) {
	// empty -> error
	_, err := GetUserIdByAffCode("")
	assert.Error(t, err)

	code := strings.ReplaceAll(uniq("aff"), "_", "")[:8]
	u := newTestUser(t, func(u *User) { u.AffCode = code })
	id, err := GetUserIdByAffCode(code)
	require.NoError(t, err)
	assert.Equal(t, u.Id, id)
}

func TestGetMaxUserId(t *testing.T) {
	u := newTestUser(t, nil)
	// our test ids live in the 800M+ range; max must be >= our fresh id
	assert.GreaterOrEqual(t, GetMaxUserId(), u.Id)
}

// ===========================================================================
// Update / Edit / ClearBinding
// ===========================================================================

func TestUser_UpdatePreservesQuota(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Quota = 100; u.DisplayName = "orig" })

	// Update omits quota/used_quota/request_count -> quota change is ignored
	u.DisplayName = "changed"
	u.Quota = 999
	require.NoError(t, u.Update(false))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, "changed", reloaded.DisplayName)
	assert.Equal(t, 100, reloaded.Quota) // unchanged
}

func TestUser_UpdateWithPassword(t *testing.T) {
	u := newTestUser(t, nil)
	u.Password = "newpassword1"
	require.NoError(t, u.Update(true))
	reloaded, _ := GetUserById(u.Id, true)
	assert.True(t, common.ValidatePasswordAndHash("newpassword1", reloaded.Password))
}

func TestUser_Edit(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Group = "default" })
	newGroup := uniq("eg")
	u.Group = newGroup
	u.DisplayName = "edited"
	require.NoError(t, u.Edit(false))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, newGroup, reloaded.Group)
	assert.Equal(t, "edited", reloaded.DisplayName)

	// Edit with password update
	u.Password = "editedpass12"
	require.NoError(t, u.Edit(true))
	reloaded, _ = GetUserById(u.Id, true)
	assert.True(t, common.ValidatePasswordAndHash("editedpass12", reloaded.Password))
}

func TestUser_ClearBinding(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.GitHubId = uniq("gh") })

	// id 0 -> error
	assert.Error(t, (&User{Id: 0}).ClearBinding("github"))
	// invalid binding type -> error
	assert.Error(t, u.ClearBinding("bogus"))

	// clear github binding
	require.NoError(t, u.ClearBinding("github"))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, "", reloaded.GitHubId)
}

func TestUser_UpdateGitHubId(t *testing.T) {
	assert.Error(t, (&User{Id: 0}).UpdateGitHubId("x"))
	u := newTestUser(t, func(u *User) { u.GitHubId = uniq("gh") })
	newId := uniq("gh2")
	require.NoError(t, u.UpdateGitHubId(newId))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, newId, reloaded.GitHubId)
}

func TestUpdateUserRemark(t *testing.T) {
	u := newTestUser(t, nil)
	require.NoError(t, UpdateUserRemark(u.Id, "vip customer"))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, "vip customer", reloaded.Remark)
}

func TestUpdateUserSetting(t *testing.T) {
	// id 0 -> error
	assert.Error(t, UpdateUserSetting(0, dto.UserSetting{}))

	u := newTestUser(t, nil)
	require.NoError(t, UpdateUserSetting(u.Id, dto.UserSetting{Language: "ru"}))
	got, err := GetUserSetting(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "ru", got.Language)
}

// ===========================================================================
// Delete (soft) / HardDelete
// ===========================================================================

func TestUser_DeleteSoft(t *testing.T) {
	// id 0 -> error
	assert.Error(t, (&User{Id: 0}).Delete())
	assert.Error(t, DeleteUserById(0))

	u := newTestUser(t, nil)
	require.NoError(t, DeleteUserById(u.Id))

	// default scope hides soft-deleted rows
	_, err := GetUserById(u.Id, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	// but the row still exists unscoped
	var cnt int64
	DB.Unscoped().Model(&User{}).Where("id = ?", u.Id).Count(&cnt)
	assert.EqualValues(t, 1, cnt)
}

func TestUser_HardDelete(t *testing.T) {
	assert.Error(t, (&User{Id: 0}).HardDelete())
	assert.Error(t, HardDeleteUserById(0))

	u := newTestUser(t, nil)
	mkToken(t, u.Id, nil) // ensure auth data is cleaned too
	require.NoError(t, HardDeleteUserById(u.Id))

	var cnt int64
	DB.Unscoped().Model(&User{}).Where("id = ?", u.Id).Count(&cnt)
	assert.EqualValues(t, 0, cnt)
	// tokens for the user are gone as well
	var tcnt int64
	DB.Unscoped().Model(&Token{}).Where("user_id = ?", u.Id).Count(&tcnt)
	assert.EqualValues(t, 0, tcnt)
}

// ===========================================================================
// Bind email (locked)
// ===========================================================================

func TestBindEmailToUser(t *testing.T) {
	u := newTestUser(t, nil)
	email := uniq("bind") + "@ex.com"
	require.NoError(t, BindEmailToUser(u, email))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, email, reloaded.Email)

	// same email on a different user is rejected
	other := newTestUser(t, nil)
	assert.ErrorIs(t, BindEmailToUser(other, strings.ToUpper(email)), ErrEmailAlreadyTaken)
}

// ===========================================================================
// TransferAffQuotaToQuota
// ===========================================================================

func TestTransferAffQuotaToQuota(t *testing.T) {
	unit := int(common.QuotaPerUnit)

	// below minimum unit -> error
	u := newTestUser(t, func(u *User) { u.AffQuota = 10 * unit })
	assert.Error(t, u.TransferAffQuotaToQuota(unit-1))

	// insufficient aff quota -> error
	poor := newTestUser(t, func(u *User) { u.AffQuota = unit })
	assert.Error(t, poor.TransferAffQuotaToQuota(2*unit))

	// successful transfer moves quota from aff to main
	u2 := newTestUser(t, func(u *User) { u.AffQuota = 3 * unit; u.Quota = 0 })
	require.NoError(t, u2.TransferAffQuotaToQuota(unit))
	reloaded, _ := GetUserById(u2.Id, true)
	assert.Equal(t, 2*unit, reloaded.AffQuota) // 3u - 1u
	assert.Equal(t, unit, reloaded.Quota)      // 0 + 1u
}

// ===========================================================================
// ValidateAndFill + credential helpers
// ===========================================================================

func mkHashedUser(t *testing.T, password string, mut func(u *User)) *User {
	t.Helper()
	hash, err := common.Password2Hash(password)
	require.NoError(t, err)
	return newTestUser(t, func(u *User) {
		u.Password = hash
		if mut != nil {
			mut(u)
		}
	})
}

func TestUser_ValidateAndFill(t *testing.T) {
	// empty credentials
	err := (&User{}).ValidateAndFill()
	assert.ErrorIs(t, err, ErrUserEmptyCredentials)

	u := mkHashedUser(t, "password123", nil)

	// correct password + enabled -> ok
	probe := &User{Username: u.Username, Password: "password123"}
	require.NoError(t, probe.ValidateAndFill())
	assert.Equal(t, u.Id, probe.Id)

	// wrong password -> invalid credentials
	bad := &User{Username: u.Username, Password: "wrongpass1"}
	assert.ErrorIs(t, bad.ValidateAndFill(), ErrInvalidCredentials)

	// not found -> invalid credentials
	missing := &User{Username: uniq("nouser"), Password: "password123"}
	assert.ErrorIs(t, missing.ValidateAndFill(), ErrInvalidCredentials)

	// disabled user -> invalid credentials
	dis := mkHashedUser(t, "password123", func(u *User) { u.Status = common.UserStatusDisabled })
	probe2 := &User{Username: dis.Username, Password: "password123"}
	assert.ErrorIs(t, probe2.ValidateAndFill(), ErrInvalidCredentials)
}

func TestResetUserPasswordByEmail(t *testing.T) {
	// empty args -> error
	assert.Error(t, ResetUserPasswordByEmail("", "x"))
	assert.Error(t, ResetUserPasswordByEmail("a@b.com", ""))

	email := uniq("rst") + "@ex.com"
	u := mkHashedUser(t, "password123", func(u *User) { u.Email = email })
	require.NoError(t, ResetUserPasswordByEmail(email, "brandnew12"))
	reloaded, _ := GetUserById(u.Id, true)
	assert.True(t, common.ValidatePasswordAndHash("brandnew12", reloaded.Password))
}

// ===========================================================================
// Fill-by-* and IsXxxAlreadyTaken helpers
// ===========================================================================

func TestFillUserByIdentifiers(t *testing.T) {
	gh := uniq("gh")
	dc := uniq("dc")
	oi := uniq("oi")
	wc := uniq("wc")
	tg := uniq("tg")
	ld := uniq("ld")
	email := uniq("fill") + "@ex.com"
	u := newTestUser(t, func(u *User) {
		u.GitHubId = gh
		u.DiscordId = dc
		u.OidcId = oi
		u.WeChatId = wc
		u.TelegramId = tg
		u.LinuxDOId = ld
		u.Email = email
	})

	// FillUserById
	fu := &User{Id: u.Id}
	require.NoError(t, fu.FillUserById())
	assert.Equal(t, u.Username, fu.Username)
	assert.Error(t, (&User{Id: 0}).FillUserById())

	// FillUserByEmail
	fe := &User{Email: email}
	require.NoError(t, fe.FillUserByEmail())
	assert.Equal(t, u.Id, fe.Id)
	assert.Error(t, (&User{}).FillUserByEmail())

	// FillUserByGitHubId
	fg := &User{GitHubId: gh}
	require.NoError(t, fg.FillUserByGitHubId())
	assert.Equal(t, u.Id, fg.Id)
	assert.Error(t, (&User{}).FillUserByGitHubId())

	// FillUserByDiscordId
	fd := &User{DiscordId: dc}
	require.NoError(t, fd.FillUserByDiscordId())
	assert.Equal(t, u.Id, fd.Id)
	assert.Error(t, (&User{}).FillUserByDiscordId())

	// FillUserByOidcId
	fo := &User{OidcId: oi}
	require.NoError(t, fo.FillUserByOidcId())
	assert.Equal(t, u.Id, fo.Id)
	assert.Error(t, (&User{}).FillUserByOidcId())

	// FillUserByWeChatId
	fw := &User{WeChatId: wc}
	require.NoError(t, fw.FillUserByWeChatId())
	assert.Equal(t, u.Id, fw.Id)
	assert.Error(t, (&User{}).FillUserByWeChatId())

	// FillUserByTelegramId (found + not-found + empty)
	ft := &User{TelegramId: tg}
	require.NoError(t, ft.FillUserByTelegramId())
	assert.Equal(t, u.Id, ft.Id)
	assert.Error(t, (&User{}).FillUserByTelegramId())
	assert.Error(t, (&User{TelegramId: uniq("nope")}).FillUserByTelegramId())

	// FillUserByLinuxDOId (found + not-found + empty)
	fl := &User{LinuxDOId: ld}
	require.NoError(t, fl.FillUserByLinuxDOId())
	assert.Equal(t, u.Id, fl.Id)
	assert.Error(t, (&User{}).FillUserByLinuxDOId())
	assert.Error(t, (&User{LinuxDOId: uniq("nope")}).FillUserByLinuxDOId())
}

func TestIsIdentifierAlreadyTaken(t *testing.T) {
	gh := uniq("gh")
	dc := uniq("dc")
	oi := uniq("oi")
	wc := uniq("wc")
	tg := uniq("tg")
	ld := uniq("ld")
	newTestUser(t, func(u *User) {
		u.GitHubId = gh
		u.DiscordId = dc
		u.OidcId = oi
		u.WeChatId = wc
		u.TelegramId = tg
		u.LinuxDOId = ld
	})

	assert.True(t, IsGitHubIdAlreadyTaken(gh))
	assert.False(t, IsGitHubIdAlreadyTaken(uniq("x")))
	assert.True(t, IsDiscordIdAlreadyTaken(dc))
	assert.False(t, IsDiscordIdAlreadyTaken(uniq("x")))
	assert.True(t, IsOidcIdAlreadyTaken(oi))
	assert.False(t, IsOidcIdAlreadyTaken(uniq("x")))
	assert.True(t, IsWeChatIdAlreadyTaken(wc))
	assert.False(t, IsWeChatIdAlreadyTaken(uniq("x")))
	assert.True(t, IsTelegramIdAlreadyTaken(tg))
	assert.False(t, IsTelegramIdAlreadyTaken(uniq("x")))
	assert.True(t, IsLinuxDOIdAlreadyTaken(ld))
	assert.False(t, IsLinuxDOIdAlreadyTaken(uniq("x")))
}

// ===========================================================================
// Role / access token / root helpers
// ===========================================================================

func TestIsAdmin(t *testing.T) {
	assert.False(t, IsAdmin(0))
	admin := newTestUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	common1 := newTestUser(t, func(u *User) { u.Role = common.RoleCommonUser })
	assert.True(t, IsAdmin(admin.Id))
	assert.False(t, IsAdmin(common1.Id))
}

func TestValidateAccessToken(t *testing.T) {
	// empty -> nil, nil
	u1, err := ValidateAccessToken("")
	require.NoError(t, err)
	assert.Nil(t, u1)

	token := strings.ReplaceAll(uniq("at"), "_", "")
	if len(token) > 32 {
		token = token[:32]
	}
	u := newTestUser(t, func(u *User) { u.SetAccessToken(token) })

	// Bearer prefix is stripped
	got, err := ValidateAccessToken("Bearer " + token)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, u.Id, got.Id)

	// unknown token -> nil, nil
	got, err = ValidateAccessToken(uniq("unknown"))
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetRootUserAndExists(t *testing.T) {
	root := newTestUser(t, func(u *User) { u.Role = common.RoleRootUser })
	_ = root
	assert.True(t, RootUserExists())
	got := GetRootUser()
	require.NotNil(t, got)
	assert.Equal(t, common.RoleRootUser, got.Role)
}

// ===========================================================================
// Quota getters / increase / decrease / delta / used quota
// ===========================================================================

func TestGetUserQuotaAndUsedQuota(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Quota = 4242; u.UsedQuota = 77 })

	// fromDB true
	q, err := GetUserQuota(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 4242, q)

	// fromDB false with Redis disabled falls through to DB
	q, err = GetUserQuota(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 4242, q)

	used, err := GetUserUsedQuota(u.Id)
	require.NoError(t, err)
	assert.Equal(t, 77, used)

	email, err := GetUserEmail(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Email, email)
}

func TestIncreaseDecreaseUserQuota(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Quota = 1000 })

	// negative rejected
	assert.Error(t, IncreaseUserQuota(u.Id, -1, true))
	assert.Error(t, DecreaseUserQuota(u.Id, -1, true))

	require.NoError(t, IncreaseUserQuota(u.Id, 250, true))
	q, _ := GetUserQuota(u.Id, true)
	assert.Equal(t, 1250, q)

	require.NoError(t, DecreaseUserQuota(u.Id, 300, true))
	q, _ = GetUserQuota(u.Id, true)
	assert.Equal(t, 950, q)

	// Delta: 0 -> no-op, >0 -> increase, <0 -> decrease
	require.NoError(t, DeltaUpdateUserQuota(u.Id, 0))
	q, _ = GetUserQuota(u.Id, true)
	assert.Equal(t, 950, q)
	require.NoError(t, DeltaUpdateUserQuota(u.Id, 50))
	q, _ = GetUserQuota(u.Id, true)
	assert.Equal(t, 1000, q)
	require.NoError(t, DeltaUpdateUserQuota(u.Id, -200))
	q, _ = GetUserQuota(u.Id, true)
	assert.Equal(t, 800, q)
}

func TestUpdateUserUsedQuotaAndRequestCount(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.UsedQuota = 0; u.RequestCount = 0 })

	// public entry point (BatchUpdateEnabled is false in tests -> synchronous)
	UpdateUserUsedQuotaAndRequestCount(u.Id, 30)
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, 30, reloaded.UsedQuota)
	assert.Equal(t, 1, reloaded.RequestCount)

	// unexported helpers add on top
	updateUserUsedQuota(u.Id, 5)
	updateUserRequestCount(u.Id, 2)
	updateUserUsedQuotaAndRequestCount(u.Id, 10, 3)
	reloaded, _ = GetUserById(u.Id, true)
	assert.Equal(t, 45, reloaded.UsedQuota)   // 30 + 5 + 10
	assert.Equal(t, 6, reloaded.RequestCount) // 1 + 2 + 3

	// combined quota/used/request; zero-all early-returns without change
	updateUserQuotaUsedQuotaAndRequestCount(u.Id, 0, 0, 0)
	before, _ := GetUserById(u.Id, true)
	updateUserQuotaUsedQuotaAndRequestCount(u.Id, 100, 7, 1)
	after, _ := GetUserById(u.Id, true)
	assert.Equal(t, before.Quota+100, after.Quota)
	assert.Equal(t, before.UsedQuota+7, after.UsedQuota)
	assert.Equal(t, before.RequestCount+1, after.RequestCount)
}

func TestUpdateUserLastLoginAt(t *testing.T) {
	u := newTestUser(t, nil)
	UpdateUserLastLoginAt(u.Id)
	reloaded, _ := GetUserById(u.Id, true)
	assert.Greater(t, reloaded.LastLoginAt, int64(0))
}

// ===========================================================================
// Group / Setting / Username getters (DB path)
// ===========================================================================

func TestGetUserGroupSettingUsername(t *testing.T) {
	grp := uniq("gg")
	u := newTestUser(t, func(u *User) {
		u.Group = grp
		u.Setting = `{"language":"ja"}`
	})

	g, err := GetUserGroup(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, grp, g)
	g, err = GetUserGroup(u.Id, false) // redis disabled -> DB
	require.NoError(t, err)
	assert.Equal(t, grp, g)

	s, err := GetUserSetting(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "ja", s.Language)
	s, err = GetUserSetting(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "ja", s.Language)

	name, err := GetUsernameById(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, u.Username, name)
	name, err = GetUsernameById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, u.Username, name)
}

// ===========================================================================
// Inviter id cache + mutual invitation
// ===========================================================================

func TestUserInviterIdCache(t *testing.T) {
	inviter := newTestUser(t, nil)
	u := newTestUser(t, func(u *User) { u.InviterId = inviter.Id })

	// first call reads DB and caches
	got, err := GetUserInviterIdWithError(u.Id)
	require.NoError(t, err)
	assert.Equal(t, inviter.Id, got)

	// GetUserInviterId (swallows error) returns cached value
	assert.Equal(t, inviter.Id, GetUserInviterId(u.Id))

	// update changes value + invalidates cache
	newInviter := newTestUser(t, nil)
	require.NoError(t, UpdateUserInviterId(u.Id, newInviter.Id))
	assert.Equal(t, newInviter.Id, GetUserInviterId(u.Id))

	// explicit invalidation is safe
	InvalidateInviterIdCache(u.Id)
	assert.Equal(t, newInviter.Id, GetUserInviterId(u.Id))
}

func TestIsMutualInvitation(t *testing.T) {
	// guard branch: non-positive / equal ids -> false, nil
	ok, err := IsMutualInvitation(0, 5)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = IsMutualInvitation(5, 5)
	require.NoError(t, err)
	assert.False(t, ok)

	a := newTestUser(t, nil)
	b := newTestUser(t, nil)
	require.NoError(t, UpdateUserInviterId(a.Id, b.Id))
	require.NoError(t, UpdateUserInviterId(b.Id, a.Id))

	ok, err = IsMutualInvitationWithContext(context.Background(), a.Id, b.Id)
	require.NoError(t, err)
	assert.True(t, ok)

	// non-mutual: c invites a, a does not invite c
	c := newTestUser(t, nil)
	require.NoError(t, UpdateUserInviterId(c.Id, a.Id))
	ok, err = IsMutualInvitation(c.Id, a.Id)
	require.NoError(t, err)
	assert.False(t, ok) // a's inviter is b, not c
}

// ===========================================================================
// Listing / searching (admin, non-relay paths)
// ===========================================================================

func TestGetAllUsers(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, nil)
	pageInfo := &common.PageInfo{Page: 1, PageSize: 10}
	users, total, err := GetAllUsers(pageInfo)
	require.NoError(t, err)
	assert.Greater(t, total, int64(0))
	_ = users
	_ = u

	// with explicit sort options (exercises resolveUserSortOptions non-empty path)
	users, _, err = GetAllUsers(pageInfo, NewUserSortOptions("quota", "asc"))
	require.NoError(t, err)
	_ = users
}

func TestSearchUsers(t *testing.T) {
	requireDB(t)
	grp := uniq("sgrp")
	admin := newTestUser(t, func(u *User) { u.Group = grp; u.Role = common.RoleAdminUser })
	member := newTestUser(t, func(u *User) { u.Group = grp; u.Role = common.RoleCommonUser })

	// scope by unique group, empty keyword -> both users
	users, total, err := SearchUsers("", grp, nil, nil, false, false, false, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, users, 2)

	// excludeAdmins removes the admin
	users, total, err = SearchUsers("", grp, nil, nil, false, true, false, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, member.Id, users[0].Id)

	// role filter
	role := common.RoleAdminUser
	users, total, err = SearchUsers("", grp, &role, nil, false, false, false, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, admin.Id, users[0].Id)

	// status filter (enabled) within group
	status := common.UserStatusEnabled
	_, total, err = SearchUsers("", grp, nil, &status, false, false, false, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// status == -1 -> only soft-deleted; none in our group
	deletedStatus := -1
	_, total, err = SearchUsers("", grp, nil, &deletedStatus, false, false, false, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	// numeric keyword hits the id branch
	users, total, err = SearchUsers(strconv.Itoa(member.Id), "", nil, nil, false, false, false, 0, 50)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
	found := false
	for _, uu := range users {
		if uu.Id == member.Id {
			found = true
		}
	}
	assert.True(t, found)

	// excludeEmployees / excludeAssignedCustomers branches (no employees in group -> unchanged)
	_, total, err = SearchUsers("", grp, nil, nil, true, false, true, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
}

// TestSearchUsers_FillAssignmentAndInviter exercises the post-query enrichment
// helpers fillUserAssignmentInfo (customer_profiles + inviter/employee join) and
// fillUserInviterRemarks, plus the excludeAssignedCustomers filter branch.
func TestSearchUsers_FillAssignmentAndInviter(t *testing.T) {
	requireDB(t)
	grp := uniq("agrp")

	employee := newTestUser(t, func(u *User) { u.DisplayName = "Empy" })
	require.NoError(t, DB.Create(&EmployeeProfile{UserId: employee.Id, Status: 1}).Error)
	t.Cleanup(func() { DB.Unscoped().Where("user_id = ?", employee.Id).Delete(&EmployeeProfile{}) })

	// inviter has a remark that must be surfaced onto the customer row
	inviter := newTestUser(t, func(u *User) { u.Remark = "vip-inviter" })

	customer := newTestUser(t, func(u *User) {
		u.Group = grp
		u.InviterId = inviter.Id
	})
	require.NoError(t, DB.Create(&CustomerProfile{
		EmployeeUserId: employee.Id,
		CustomerUserId: customer.Id,
		Status:         1,
	}).Error)
	t.Cleanup(func() { DB.Unscoped().Where("customer_user_id = ?", customer.Id).Delete(&CustomerProfile{}) })

	users, total, err := SearchUsers("", grp, nil, nil, false, false, false, 0, 50)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	got := users[0]
	assert.True(t, got.IsAssignedCustomer)
	assert.Equal(t, employee.Id, got.AssignedEmployeeUserId)
	assert.Equal(t, "Empy", got.AssignedEmployeeName) // display name preferred
	assert.Equal(t, "vip-inviter", got.InviterRemark)

	// excludeAssignedCustomers removes the assigned customer -> empty result
	_, total, err = SearchUsers("", grp, nil, nil, false, false, true, 0, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

// ===========================================================================
// Redis-first getters (cache hit path for quota/group/setting/username)
// ===========================================================================

func TestUserGetters_RedisFirst(t *testing.T) {
	enableRedis(t)
	grp := uniq("rg")
	u := newTestUser(t, func(u *User) {
		u.Quota = 3131
		u.Group = grp
		u.Setting = `{"language":"fr"}`
	})
	t.Cleanup(func() { _ = invalidateUserCache(u.Id) })
	require.NoError(t, populateUserCache(*u))

	// fromDB=false -> served from Redis hash
	q, err := GetUserQuota(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 3131, q)

	g, err := GetUserGroup(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, grp, g)

	s, err := GetUserSetting(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "fr", s.Language)

	name, err := GetUsernameById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, u.Username, name)
}

// inviteUser is normally only reached from the registration reward path (gated
// on QuotaForInviter>0 + payment compliance). Exercise it directly: it bumps
// AffCount by 1 and adds QuotaForInviter to AffQuota / AffHistoryQuota.
func TestInviteUser(t *testing.T) {
	requireDB(t)
	inviter := newTestUser(t, func(u *User) { u.AffCount = 0; u.AffQuota = 0 })
	require.NoError(t, inviteUser(inviter.Id))
	reloaded, _ := GetUserById(inviter.Id, true)
	assert.Equal(t, 1, reloaded.AffCount)
	assert.Equal(t, common.QuotaForInviter, reloaded.AffQuota)
	assert.Equal(t, common.QuotaForInviter, reloaded.AffHistoryQuota)

	// missing inviter -> error surfaces from GetUserById
	assert.Error(t, inviteUser(0))
}

// sentinel distinctness
func TestUserSentinelErrors(t *testing.T) {
	assert.False(t, errors.Is(ErrEmailAlreadyTaken, ErrEmailNotFound))
	assert.False(t, errors.Is(ErrInvalidCredentials, ErrUserEmptyCredentials))
	assert.False(t, errors.Is(ErrEmailAmbiguous, ErrDatabase))
}
