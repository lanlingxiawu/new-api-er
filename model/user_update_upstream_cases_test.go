package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Cases from upstream/main:model/user_update_test.go, run on the fork's shared
// DB harness. Upstream empties the users table and relies on fixed ids,
// usernames and e-mail addresses; here users come from mkUser (unique id,
// username and aff_code), e-mail addresses carry a per-test unique local part,
// and rows created through User.Insert are removed by id.

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	requireDB(t)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

// userUpdateCasesEmail returns a unique e-mail address with the given
// mixed-case local-part prefix and domain.
func userUpdateCasesEmail(prefix string, domain string) string {
	return uniq(prefix) + "@" + domain
}

// cleanupInsertedUserByUsername removes a user created through User.Insert
// (which assigns its own id) together with any registration log it wrote.
func cleanupInsertedUserByUsername(t *testing.T, username string) {
	t.Helper()
	t.Cleanup(func() {
		var ids []int
		DB.Unscoped().Model(&User{}).Where("username = ?", username).Pluck("id", &ids)
		for _, id := range ids {
			DB.Unscoped().Delete(&User{}, id)
			if LOG_DB != nil {
				LOG_DB.Where("user_id = ?", id).Delete(&Log{})
			}
		}
	})
}

func createUserBindTestUser(t *testing.T) User {
	t.Helper()
	user := mkUser(t, func(u *User) {
		u.Password = "unused-password-hash"
		u.AuthVersion = 1
	})
	return *user
}

func TestUserUpdateDoesNotOverwriteConcurrentAccountingOrTokenChanges(t *testing.T) {
	setupUserUpdateTestState(t)

	rotatedToken := uniq("rot")
	user := mkUser(t, func(u *User) {
		u.Password = "password"
		u.DisplayName = "before"
		u.Quota = 1000
		u.UsedQuota = 20
		u.RequestCount = 3
		u.AffCount = 2
		u.AffQuota = 800
		u.AffHistoryQuota = 1200
		u.SetAccessToken(uniq("old"))
	})

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
		"aff_count":     gorm.Expr("aff_count + ?", 1),
		"aff_quota":     gorm.Expr("aff_quota - ?", 500),
		"aff_history":   gorm.Expr("aff_history + ?", 500),
		"access_token":  rotatedToken,
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, 3, got.AffCount)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1700, got.AffHistoryQuota)
	assert.Equal(t, rotatedToken, got.GetAccessToken())
}

func TestUsageAccountingSupportsSignedDirectAndBatchDeltas(t *testing.T) {
	setupUserUpdateTestState(t)
	resetBatchUpdateTestState(t)

	user := mkUser(t, func(u *User) {
		u.Password = "password"
		u.UsedQuota = 1000
		u.RequestCount = 3
	})
	channel := mkChannel(t, func(ch *Channel) {
		ch.Name = "usage-adjustment-channel"
		ch.UsedQuota = 1000
	})

	UpdateUserUsedQuota(user.Id, -200)
	UpdateUserUsedQuota(user.Id, 50)
	UpdateChannelUsedQuota(channel.Id, -200)
	UpdateChannelUsedQuota(channel.Id, 50)

	var got User
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	var gotChannel Channel
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota)

	common.BatchUpdateEnabled = true
	UpdateUserUsedQuota(user.Id, 400)
	UpdateUserUsedQuota(user.Id, -100)
	UpdateChannelUsedQuota(channel.Id, 400)
	UpdateChannelUsedQuota(channel.Id, -100)

	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota, "batch deltas must remain queued until flush")
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota, "batch deltas must remain queued until flush")

	batchUpdate()
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 1150, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(1150), gotChannel.UsedQuota)
}

func TestUpdateUserAccessTokenOnlyUpdatesAccessToken(t *testing.T) {
	setupUserUpdateTestState(t)

	user := mkUser(t, func(u *User) {
		u.Password = "password"
		u.DisplayName = "before"
		u.Quota = 1000
		u.AffQuota = 800
		u.AffHistoryQuota = 1200
	})

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":        gorm.Expr("quota + ?", 500),
		"aff_quota":    gorm.Expr("aff_quota - ?", 500),
		"display_name": "concurrent-update",
	}).Error)

	rotatedToken := uniq("rot")
	require.NoError(t, UpdateUserAccessToken(user.Id, rotatedToken))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, rotatedToken, got.GetAccessToken())
	assert.Equal(t, "concurrent-update", got.DisplayName)
	assert.Equal(t, 1500, got.Quota)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1200, got.AffHistoryQuota)
}

func TestUpdateUserAccessTokenRejectsSoftDeletedUser(t *testing.T) {
	setupUserUpdateTestState(t)

	oldToken := uniq("old")
	user := mkUser(t, func(u *User) {
		u.Password = "password"
		u.SetAccessToken(oldToken)
	})
	require.NoError(t, DB.Delete(user).Error)

	err := UpdateUserAccessToken(user.Id, uniq("orphan"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var got User
	require.NoError(t, DB.Unscoped().First(&got, user.Id).Error)
	assert.Equal(t, oldToken, got.GetAccessToken())
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := mkUser(t, func(u *User) {
		u.Password = "password"
		u.Quota = 1000
		u.UsedQuota = 20
		u.RequestCount = 3
	})

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	email := userUpdateCasesEmail("Taken", "Example.com")
	existing := mkUser(t, func(u *User) {
		u.Password = "old-password"
		u.Email = email
	})

	err := EnsureEmailAvailable(" "+strings.ToLower(strings.Split(email, "@")[0])+"@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail(strings.ToUpper(email))
	require.NoError(t, err)
	assert.Equal(t, existing.Username, user.Username)

	require.NoError(t, EnsureEmailAvailable(strings.ToLower(email), user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	email := userUpdateCasesEmail("taken", "example.com")
	mkUser(t, func(u *User) {
		u.Password = "old-password"
		u.Email = email
	})

	username := uniq("oauth-user")
	cleanupInsertedUserByUsername(t, username)
	user := &User{
		Username: username,
		Email:    strings.ToUpper(email),
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", username).Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	username := uniq("passwordless-user")
	cleanupInsertedUserByUsername(t, username)
	user := &User{
		Username: username,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestUpdateUserBindColumnOnlyTouchesTheBindingColumn(t *testing.T) {
	requireDB(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"role":   common.RoleAdminUser,
		"status": common.UserStatusEnabled,
		"group":  "vip",
	}).Error)

	githubID := uniq("gh")
	require.NoError(t, UpdateUserBindColumn(user.Id, "github_id", githubID))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, githubID, reloaded.GitHubId)
	assert.Equal(t, common.RoleAdminUser, reloaded.Role)
	assert.Equal(t, common.UserStatusEnabled, reloaded.Status)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestUpdateUserBindColumnPreservesRestrictiveChange(t *testing.T) {
	requireDB(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("status", common.UserStatusDisabled).Error)
	wechatID := uniq("wx-open-id")
	require.NoError(t, UpdateUserBindColumn(user.Id, "wechat_id", wechatID))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, wechatID, reloaded.WeChatId)
	assert.Equal(t, common.UserStatusDisabled, reloaded.Status)
}

func TestUpdateUserBindColumnRejectsNonWhitelistedColumns(t *testing.T) {
	requireDB(t)

	user := createUserBindTestUser(t)
	for _, column := range []string{"role", "status", "group", "quota", "username", "password", "id"} {
		assert.Error(t, UpdateUserBindColumn(user.Id, column, "1"), "column %s must be rejected", column)
	}
	assert.Error(t, UpdateUserBindColumn(user.Id, "github_id; DROP TABLE users", "x"))
	assert.Error(t, UpdateUserBindColumn(0, "github_id", "x"))
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := mkUser(t, func(u *User) { u.Password = "" })

	loginUser := User{
		Username: user.Username,
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	legacyEmail := userUpdateCasesEmail("legacy", "example.com")
	first := mkUser(t, func(u *User) {
		u.Password = "old-1"
		u.Email = legacyEmail
	})
	second := mkUser(t, func(u *User) {
		u.Password = "old-2"
		u.Email = strings.ToUpper(legacyEmail)
	})

	err := ResetUserPasswordByEmail(legacyEmail, "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("id IN ?", []int{first.Id, second.Id}).Order("id asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	uniqueEmail := userUpdateCasesEmail("unique", "example.com")
	unique := mkUser(t, func(u *User) {
		u.Password = "old"
		u.Email = uniqueEmail
	})

	require.NoError(t, ResetUserPasswordByEmail(strings.ToUpper(uniqueEmail), "NewPassword123"))

	var reloaded User
	require.NoError(t, DB.First(&reloaded, unique.Id).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", reloaded.Password))

	err = ResetUserPasswordByEmail(userUpdateCasesEmail("missing", "example.com"), "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}
