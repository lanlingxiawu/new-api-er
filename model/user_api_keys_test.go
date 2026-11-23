package model

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserAPIKeysBatchBoundaryAndIsolation(t *testing.T) {
	for _, count := range []int{0, 1, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			operator := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
			user := mkUser(t, nil)
			other := mkUser(t, nil)
			unrelated := mkToken(t, other.Id, nil)
			for range count {
				mkToken(t, user.Id, nil)
			}
			upper, err := UserTokenDeletionUpperBound(context.Background(), user.Id)
			require.NoError(t, err)
			newKey := mkToken(t, user.Id, nil)
			var total int64
			for {
				deleted, done, err := DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, upper)
				require.NoError(t, err)
				require.LessOrEqual(t, deleted, int64(100))
				total += deleted
				if done {
					break
				}
			}
			assert.Equal(t, int64(count), total)
			for _, key := range []*Token{newKey, unrelated} {
				_, err = GetTokenByKey(key.Key, true)
				require.NoError(t, err)
			}
		})
	}
}

func TestUserAPIKeysCacheFailureAndRetry(t *testing.T) {
	enableRedis(t)
	operator := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	user := mkUser(t, nil)
	key := mkToken(t, user.Id, nil)
	primeTokenCache(t, key)
	client := common.RDB
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { common.RDB = client; _ = broken.Close() })
	common.RDB = broken
	count, _, err := DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, key.Id)
	require.Error(t, err)
	assert.Zero(t, count)
	var stored Token
	require.NoError(t, DB.First(&stored, key.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	common.RDB = client
	count, done, err := DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, key.Id)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, int64(1), count)
	// An in-flight reader holding the enabled snapshot cannot refill the cache.
	code, err := cacheInitToken(*key)
	require.NoError(t, err)
	assert.Zero(t, code)
	_, err = cacheApplyTokenQuotaDelta(key.Id, key.Key, 1)
	require.NoError(t, err)
	_, err = ValidateUserToken(key.Key)
	require.Error(t, err)
	// Metadata writes and quota settlement carrying an old struct cannot
	// resurrect a soft-deleted row or a valid credential cache.
	require.NoError(t, key.Update())
	require.NoError(t, key.SelectUpdate())
	_, err = ValidateUserToken(key.Key)
	require.Error(t, err)
}

func TestUserAPIKeysDatabaseFailureAndCancellation(t *testing.T) {
	operator := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	user := mkUser(t, nil)
	key := mkToken(t, user.Id, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := DeleteUserTokensBatch(ctx, operator.Id, user.Id, key.Id)
	require.ErrorIs(t, err, context.Canceled)
	var stored Token
	require.NoError(t, DB.First(&stored, key.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
	const callback = "test:api_key_delete_failure"
	require.NoError(t, DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			_ = tx.AddError(errors.New("injected delete failure"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Delete().Remove(callback) })
	count, done, err := DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, key.Id)
	require.Error(t, err)
	assert.Zero(t, count)
	assert.False(t, done)
	require.NoError(t, DB.First(&stored, key.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	require.NoError(t, DB.Callback().Delete().Remove(callback))
	count, done, err = DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, key.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	assert.True(t, done)
}

func TestUserAPIKeysConcurrentDeletion(t *testing.T) {
	enableRedis(t)
	operator := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	user := mkUser(t, nil)
	key := mkToken(t, user.Id, nil)
	primeTokenCache(t, key)
	type result struct {
		count int64
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			count, _, err := DeleteUserTokensBatch(context.Background(), operator.Id, user.Id, key.Id)
			results <- result{count, err}
		}()
	}
	close(start)
	var total int64
	for range 2 {
		r := <-results
		require.NoError(t, r.err)
		total += r.count
	}
	assert.EqualValues(t, 1, total)
	_, err := ValidateUserToken(key.Key)
	require.Error(t, err)
}

func TestUserAPIKeysBatchRechecksCurrentRoles(t *testing.T) {
	for _, test := range []struct {
		name                                     string
		operatorRole, operatorStatus, targetRole int
	}{
		{"same role", 10, 1, 10}, {"higher role", 10, 1, 100},
		{"disabled operator", 10, 2, 1}, {"demoted operator", 1, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			operator := mkUser(t, func(u *User) { u.Role = test.operatorRole; u.Status = test.operatorStatus })
			user := mkUser(t, func(u *User) { u.Role = test.targetRole })
			key := mkToken(t, user.Id, nil)
			_, _, err := DeleteUserTokensBatch(t.Context(), operator.Id, user.Id, key.Id)
			require.ErrorIs(t, err, ErrUserAPIKeyClearForbidden)
			var stored Token
			require.NoError(t, DB.First(&stored, key.Id).Error)
			require.Equal(t, common.TokenStatusEnabled, stored.Status)
		})
	}
}

func TestUserAPIKeysConcurrentPromotionIsCheckedUnderLock(t *testing.T) {
	operator := mkUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	user := mkUser(t, nil)
	key := mkToken(t, user.Id, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tx := DB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	var target User
	require.NoError(t, lockForUpdate(tx).First(&target, user.Id).Error)
	require.NoError(t, tx.Model(&User{}).Where("id = ?", user.Id).Update("role", common.RoleAdminUser).Error)
	reading := make(chan struct{})
	var userReads atomic.Int32
	const callback = "test:api_key_role_lock"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(query *gorm.DB) {
		if query.Statement.Table == "users" && userReads.Add(1) == 2 {
			close(reading)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	result := make(chan error, 1)
	go func() {
		_, _, err := DeleteUserTokensBatch(ctx, operator.Id, user.Id, key.Id)
		result <- err
	}()
	select {
	case <-reading:
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "clear must attempt the locked target read")
	}
	require.NoError(t, tx.Commit().Error)
	require.ErrorIs(t, <-result, ErrUserAPIKeyClearForbidden)
	var stored Token
	require.NoError(t, DB.First(&stored, key.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, stored.Status)
}
