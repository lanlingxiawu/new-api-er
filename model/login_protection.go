package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrLoginLocked          = errors.New("account login is restricted")
	ErrPasswordMismatch     = fmt.Errorf("%w: password mismatch", ErrInvalidCredentials)
	errLoginAttemptObsolete = errors.New("login attempt no longer matches an enabled account")
)

// UserLoginProtection belongs only to dashboard login, never relay authorization.
type UserLoginProtection struct {
	UserID         int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	FailedAttempts int   `json:"failed_attempts" gorm:"not null;default:0"`
	LockedAt       int64 `json:"locked_at" gorm:"type:bigint;not null;default:0"`
	LastFailedAt   int64 `json:"-" gorm:"type:bigint;not null;default:0"`
	UpdatedAt      int64 `json:"-" gorm:"autoUpdateTime"`
	Locked         bool  `json:"locked" gorm:"-:all"`
}

// withLoginProtectionTransaction bounds contention and retries only SQLite busy
// errors. All actions are database-only and roll back before a retry.
func withLoginProtectionTransaction(action func(*gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for attempt := 0; ; attempt++ {
		err := DB.WithContext(ctx).Transaction(action)
		if err == nil || !common.UsingMainDatabase(common.DatabaseTypeSQLite) || attempt >= 4 || (!strings.Contains(err.Error(), "SQLITE_BUSY") && !strings.Contains(err.Error(), "database is locked")) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(10<<attempt) * time.Millisecond):
		}
	}
}

func lockLoginProtection(tx *gorm.DB, userID int) (*UserLoginProtection, error) {
	state := UserLoginProtection{UserID: userID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("user_id = ?", userID).Take(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

func CheckLoginProtection(userID, role int) error {
	if !operation_setting.GetLoginProtectionSetting().Enabled || role == common.RoleRootUser {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var state UserLoginProtection
	err := DB.WithContext(ctx).Select("user_id", "locked_at").Where("user_id = ?", userID).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.LockedAt != 0 {
		return ErrLoginLocked
	}
	return nil
}

// RecordLoginFailure accepts only an identity established by credential lookup
// or a primary-authenticated login flow. Unknown/stale identities create no row.
func RecordLoginFailure(userID int, authVersion int64) (bool, error) {
	cfg := operation_setting.GetLoginProtectionSetting()
	if !cfg.Enabled || userID <= 0 || authVersion <= 0 {
		return false, nil
	}
	newlyLocked := false
	err := withLoginProtectionTransaction(func(tx *gorm.DB) error {
		newlyLocked = false
		state, err := lockLoginProtection(tx, userID)
		if err != nil {
			return err
		}
		var user User
		if err := tx.Select("id", "role", "status", "auth_version").Where("id = ?", userID).Take(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errLoginAttemptObsolete
			}
			return err
		}
		if user.Role == common.RoleRootUser || user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion {
			return errLoginAttemptObsolete
		}
		if state.LockedAt != 0 {
			return nil
		}
		now := time.Now().Unix()
		state.FailedAttempts = min(state.FailedAttempts+1, 100)
		state.LastFailedAt, state.UpdatedAt = now, now
		if state.FailedAttempts >= cfg.MaxFailures {
			state.LockedAt, newlyLocked = now, true
		}
		return tx.Model(state).Select("failed_attempts", "locked_at", "last_failed_at", "updated_at").Updates(state).Error
	})
	if errors.Is(err, errLoginAttemptObsolete) {
		return false, nil
	}
	return newlyLocked && err == nil, err
}

// createProtectedLoginSessionWithTx serializes successful login with failures.
// The caller owns the transaction; a rejected/failed session never resets state.
func createProtectedLoginSessionWithTx(tx *gorm.DB, session *UserSession, role int) error {
	if !operation_setting.GetLoginProtectionSetting().Enabled || role == common.RoleRootUser {
		return createUserSessionWithTx(tx, session)
	}
	ctx, cancel := context.WithTimeout(tx.Statement.Context, 2*time.Second)
	defer cancel()
	tx = tx.WithContext(ctx)
	state, err := lockLoginProtection(tx, session.UserID)
	if err != nil {
		return err
	}
	var user User
	if err := tx.Select("id", "status", "role", "auth_version").Where("id = ?", session.UserID).Take(&user).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != session.UserAuthVersion {
		return ErrUserSessionInactive
	}
	if state.LockedAt != 0 {
		return ErrLoginLocked
	}
	if err := createUserSessionWithTx(tx, session); err != nil {
		return err
	}
	return tx.Model(state).Updates(map[string]any{"failed_attempts": 0, "last_failed_at": 0, "updated_at": time.Now().Unix()}).Error
}

// CreateProtectedLoginSession is for new logins only. Refresh and rotation keep
// using their existing session paths and cannot clear or bypass a login lock.
func CreateProtectedLoginSession(session *UserSession, role int) error {
	if !operation_setting.GetLoginProtectionSetting().Enabled || role == common.RoleRootUser {
		return CreateUserSession(session)
	}
	deadline := userSessionCacheDeadline()
	if err := withLoginProtectionTransaction(func(tx *gorm.DB) error {
		return createProtectedLoginSessionWithTx(tx, session, role)
	}); err != nil {
		return err
	}
	return publishCreatedUserSession(session, deadline)
}

func UnlockUserLogin(userID int) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := DB.WithContext(ctx).Model(&UserLoginProtection{}).
		Where("user_id = ? AND (locked_at <> 0 OR failed_attempts <> 0)", userID).
		Updates(map[string]any{"failed_attempts": 0, "locked_at": 0, "last_failed_at": 0, "updated_at": time.Now().Unix()})
	return result.RowsAffected > 0, result.Error
}

// FillUserLoginProtection adds management-only state in one query per page.
func FillUserLoginProtection(users []*User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.Id)
	}
	var states []UserLoginProtection
	if err := DB.Select("user_id", "failed_attempts", "locked_at").Where("user_id IN ?", ids).Find(&states).Error; err != nil {
		return err
	}
	byID := make(map[int]UserLoginProtection, len(states))
	for _, state := range states {
		state.Locked = state.LockedAt != 0
		byID[state.UserID] = state
	}
	for _, user := range users {
		state := byID[user.Id]
		if user.Role == common.RoleRootUser {
			state.Locked = false
		}
		user.LoginProtection = &state
	}
	return nil
}
