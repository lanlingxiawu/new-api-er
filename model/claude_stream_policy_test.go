package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClaudeStreamValidationConfigGroup(t *testing.T) {
	useConfigGroupDB(t)
	previous := operation_setting.ClaudeStreamValidationEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_validation_setting", map[string]string{"enabled": strconv.FormatBool(previous)}))
	})
	for _, value := range []string{"false", "true"} {
		applied, err := SaveConfigGroup("claude_stream_validation_setting", map[string]string{"enabled": value})
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, value == "true", operation_setting.ClaudeStreamValidationEnabled())
	}
	for _, values := range []map[string]string{{"enabled": "invalid"}, {"unknown": "true"}, {"enabled": "false", "unknown": "true"}} {
		_, err := SaveConfigGroup("claude_stream_validation_setting", values)
		require.Error(t, err)
		require.True(t, operation_setting.ClaudeStreamValidationEnabled())
	}
}

func TestUserStreamValidationSQLite(t *testing.T) {
	db := useConfigGroupDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&User{}, &Channel{}))
	require.NoError(t, db.AutoMigrate(&User{}, &Channel{}))
	require.False(t, db.Migrator().HasColumn(&User{}, "claude_stream_validation"))
	require.False(t, db.Migrator().HasColumn(&User{}, "stream_protocol_validation"))
	t.Run("persistence", TestUserStreamValidationPersistence)
	t.Run("protocol", TestUserProtocolValidationPersistence)
	t.Run("channel", TestChannelStreamValidationPersistence)
	t.Run("concurrent", TestUserStreamValidationConcurrentPreferences)
}

func TestProtocolValidationConfiguredDatabase(t *testing.T) {
	requireDB(t)
	original := DB
	transaction := DB.Begin()
	require.NoError(t, transaction.Error)
	previous := operation_setting.GetStreamValidationDefaults()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		_ = transaction.Rollback().Error
		DB = original
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		var explicit *bool
		if previous.Explicit {
			explicit = common.GetPointer(previous.Enabled)
		}
		operation_setting.ReplaceStreamValidationDefaults(explicit, previous.ClaudeEnabled)
	})
	DB = transaction
	// Only these option rows are touched and the surrounding transaction rolls
	// back. Exercise the configured dialect, including nested transaction errors.
	require.NoError(t, DB.Where(commonKeyCol+" IN ?", []string{streamValidationOption, legacyStreamValidationOption}).Delete(&Option{}).Error)
	require.NoError(t, UpdateOptionsBulk(map[string]string{streamValidationOption: "false", legacyStreamValidationOption: "true"}))
	var stored Option
	require.NoError(t, DB.Where(&Option{Key: streamValidationOption}).First(&stored).Error)
	require.Equal(t, "false", stored.Value)
	require.False(t, operation_setting.GetStreamValidationDefaults().Enabled)
	require.ErrorIs(t, UpdateOption(legacyStreamValidationOption, "true"), ErrLegacyStreamValidationConflict)
}

func TestUserStreamValidationConcurrentPreferences(t *testing.T) {
	user := newTestUser(t, func(u *User) { u.Setting = `{"claude_stream_validation":true,"language":"en"}` })
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		for range 6 {
			if err := DB.Transaction(func(tx *gorm.DB) error {
				return UpdateUserClaudeStreamValidation(tx, user.Id, common.GetPointer(false))
			}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		<-start
		for range 6 {
			if err := UpdateUserSetting(user.Id, dto.UserSetting{Language: "fr", ClaudeStreamValidation: common.GetPointer(true)}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	close(start)
	first, second := <-done, <-done
	require.NoError(t, first)
	require.NoError(t, second)
	stored, err := GetUserById(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, common.GetPointer(false), stored.GetSetting().ClaudeStreamValidation)
	require.Equal(t, "fr", stored.GetSetting().Language)
}

func TestChannelStreamValidationPersistence(t *testing.T) {
	requireDB(t)
	channel := mkChannel(t, nil)
	for _, value := range []*bool{common.GetPointer(false), common.GetPointer(true), nil} {
		channel.SetSetting(dto.ChannelSettings{ClaudeStreamValidation: value, Proxy: "http://localhost:8080"})
		require.NoError(t, DB.Model(channel).Update("setting", channel.Setting).Error)
		var loaded Channel
		require.NoError(t, DB.First(&loaded, channel.Id).Error)
		require.Equal(t, value, loaded.GetSetting().ClaudeStreamValidation)
		require.Equal(t, "http://localhost:8080", loaded.GetSetting().Proxy)
	}
}

func TestUserStreamValidationPreservedAcrossPreferenceEdits(t *testing.T) {
	for _, stored := range []string{`{}`, `{"claude_stream_validation":true}`, `{"claude_stream_validation":false}`} {
		for _, submitted := range []string{`{"language":"fr"}`, `{"language":"fr","claude_stream_validation":false}`, `{"language":"fr","claude_stream_validation":true}`} {
			merged, err := preserveUserStreamValidation(submitted, stored)
			require.NoError(t, err)
			var want, got dto.UserSetting
			require.NoError(t, common.UnmarshalJsonStr(stored, &want))
			require.NoError(t, common.UnmarshalJsonStr(merged, &got))
			require.Equal(t, want.ClaudeStreamValidation, got.ClaudeStreamValidation)
			require.Equal(t, "fr", got.Language)
		}
	}
}

func TestProtocolPolicyPreservesAdminOverrides(t *testing.T) {
	merged, err := preserveUserStreamValidation(`{"language":"fr","stream_protocol_validation":true}`, `{"stream_protocol_validation":false,"claude_stream_validation":true}`)
	require.NoError(t, err)
	var got dto.UserSetting
	require.NoError(t, common.UnmarshalJsonStr(merged, &got))
	require.Equal(t, common.GetPointer(false), got.StreamProtocolValidation)
	require.Equal(t, common.GetPointer(true), got.ClaudeStreamValidation)
	require.Equal(t, "fr", got.Language)
}

func TestProtocolChannelPolicyPatch(t *testing.T) {
	old := &Channel{Setting: common.GetPointer(`{"stream_protocol_validation":false,"claude_stream_validation":true}`)}
	for _, tc := range []struct {
		input    string
		want     *bool
		conflict bool
	}{
		{`{"proxy":"new"}`, common.GetPointer(false), false},
		{`{"stream_protocol_validation":null}`, nil, false},
		{`{"stream_protocol_validation":true,"claude_stream_validation":false}`, common.GetPointer(true), false},
		{`{"claude_stream_validation":false}`, nil, true},
	} {
		channel := &Channel{Setting: &tc.input}
		err := channel.PreserveStreamValidationSetting(old)
		if tc.conflict {
			require.ErrorIs(t, err, ErrLegacyStreamValidationConflict)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.want, channel.GetSetting().StreamProtocolValidation)
		if tc.input != `{"proxy":"new"}` {
			require.Nil(t, channel.GetSetting().ClaudeStreamValidation)
		}
	}
}

func TestProtocolValidationConfigGroup(t *testing.T) {
	useConfigGroupDB(t)
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_protocol_validation_setting", map[string]string{"enabled": "null"}))
	})
	for _, value := range []string{"false", "true"} {
		applied, err := SaveConfigGroup("stream_protocol_validation_setting", map[string]string{"enabled": value})
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, value == "true", operation_setting.GetStreamValidationDefaults().Enabled)
		require.True(t, operation_setting.GetStreamValidationDefaults().Explicit)
	}
	for _, value := range []string{"null", "0", "1", "FALSE", "invalid"} {
		_, err := SaveConfigGroup("stream_protocol_validation_setting", map[string]string{"enabled": value})
		require.Error(t, err)
	}
	_, err := SaveConfigGroup("claude_stream_validation_setting", map[string]string{"enabled": "false"})
	require.ErrorIs(t, err, ErrLegacyStreamValidationConflict)
}

func TestProtocolValidationReloadAndBulk(t *testing.T) {
	useConfigGroupDB(t)
	t.Cleanup(func() { operation_setting.ReplaceStreamValidationDefaults(nil, true) })
	require.NoError(t, UpdateOptionsBulk(map[string]string{streamValidationOption: "false", legacyStreamValidationOption: "true"}))
	require.False(t, operation_setting.GetStreamValidationDefaults().Enabled)
	require.ErrorIs(t, UpdateOptionsBulk(map[string]string{legacyStreamValidationOption: "true"}), ErrLegacyStreamValidationConflict)
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: legacyStreamValidationOption}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, publishStreamValidationOptions(map[string]string{streamValidationOption: "null", legacyStreamValidationOption: "false"}))
	defaults := operation_setting.GetStreamValidationDefaults()
	require.False(t, defaults.Explicit)
	require.True(t, defaults.Enabled)
	require.False(t, defaults.ClaudeEnabled)
}

func TestUserStreamValidationCorruptSettingInherits(t *testing.T) {
	for _, raw := range []string{`{"claude_stream_validation":"off"}`, `{"claude_stream_validation":0}`} {
		user := &User{Setting: raw}
		cached := &UserBase{Setting: raw}
		require.Nil(t, user.GetSetting().ClaudeStreamValidation)
		require.Nil(t, cached.GetSetting().ClaudeStreamValidation)
		channel := &Channel{Setting: &raw}
		require.Error(t, channel.ValidateSettings(), "admin writes must reject invalid types")
	}
}

func TestChannelStreamValidationPatch(t *testing.T) {
	old := &Channel{Setting: common.GetPointer(`{"claude_stream_validation":false,"proxy":"old"}`)}
	for _, tc := range []struct {
		input string
		want  *bool
	}{
		{`{"proxy":"new"}`, common.GetPointer(false)},
		{`{"claude_stream_validation":null}`, nil},
		{`{"claude_stream_validation":true}`, common.GetPointer(true)},
		{`{"claude_stream_validation":false}`, common.GetPointer(false)},
	} {
		channel := &Channel{Setting: &tc.input}
		require.NoError(t, channel.PreserveStreamValidationSetting(old))
		require.Equal(t, tc.want, channel.GetSetting().ClaudeStreamValidation)
	}
}

func TestUserStreamValidationPersistence(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Setting = `{"language":"en","future_option":7}` })
	for _, value := range []*bool{common.GetPointer(false), common.GetPointer(true), nil} {
		before, err := GetUserById(u.Id, false)
		require.NoError(t, err)
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateUserClaudeStreamValidation(tx, u.Id, value) }))
		after, err := GetUserById(u.Id, false)
		require.NoError(t, err)
		require.Equal(t, value, after.GetSetting().ClaudeStreamValidation)
		require.Equal(t, before.ProfileVersion+1, after.ProfileVersion)
		require.Contains(t, after.Setting, `"future_option":7`)
		// An old self-settings snapshot must not restore a prior admin policy.
		require.NoError(t, UpdateUserSetting(u.Id, before.GetSetting()))
		after, err = GetUserById(u.Id, false)
		require.NoError(t, err)
		require.Equal(t, value, after.GetSetting().ClaudeStreamValidation)
		// Restore the unknown field for the next round, without altering policy.
		before.Setting = `{"language":"en","future_option":7}`
		require.NoError(t, before.Update(false))
	}
}

func TestUserProtocolValidationPersistence(t *testing.T) {
	u := newTestUser(t, func(u *User) { u.Setting = `{"language":"en","claude_stream_validation":false,"future_option":7}` })
	for _, value := range []*bool{common.GetPointer(false), common.GetPointer(true), nil} {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateUserStreamProtocolValidation(tx, u.Id, value) }))
		stored, err := GetUserById(u.Id, false)
		require.NoError(t, err)
		require.Equal(t, value, stored.GetSetting().StreamProtocolValidation)
		require.Nil(t, stored.GetSetting().ClaudeStreamValidation)
		require.Contains(t, stored.Setting, `"future_option":7`)
		if value != nil {
			require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return UpdateUserClaudeStreamValidation(tx, u.Id, nil) }), ErrLegacyStreamValidationConflict)
		}
		stale := stored.GetSetting()
		stale.StreamProtocolValidation = common.GetPointer(true)
		require.NoError(t, UpdateUserSetting(u.Id, stale))
		after, err := GetUserById(u.Id, false)
		require.NoError(t, err)
		require.Equal(t, value, after.GetSetting().StreamProtocolValidation)
		// Restore the unrelated future field using the admin-preserving writer.
		stored.Setting = `{"language":"en","future_option":7}`
		require.NoError(t, stored.Update(false))
	}
}
