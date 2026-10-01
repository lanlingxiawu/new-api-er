package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// User auth cache fencing uses three Redis keys per user: the cached user
// hash, a short-lived pending fence published before a restrictive database
// transaction, and a monotonic committed version floor published after
// commit. Cache writes below either floor are rejected, readers below the
// effective floor fall back to the database, and the pending fence outlives
// every user-hash TTL so a rolled-back transaction heals without allowing a
// stale snapshot to re-authorize the user.

var ErrUserAuthCachePending = errors.New("user authentication state update is pending")

var ErrUserAuthVersionConflict = errors.New("user authentication version update conflicted")

func getUserAuthFenceKey(userId int) string {
	return fmt.Sprintf("auth:user:fence:%d", userId)
}

func getUserAuthVersionKey(userId int) string {
	return fmt.Sprintf("auth:user:version:%d", userId)
}

// getUserProfileVersionKey holds the highest users.profile_version written to
// the cache. Writes of the non-auth cached fields (group_ratios, setting,
// timeouts, retry, ...) keep auth_version unchanged but bump profile_version,
// so without this floor a cache fill that read the row before such a write
// could land after its publish with an equal auth_version and cache the old
// values until the hash expires. The floor expires with the pending-fence TTL
// (longer than any user hash): a fill delayed past that is not plausible, and
// the expiry bounds the damage should the column ever fall below the floor
// (every fill rejected until then). No code path writes profile_version back
// from an earlier read. The key only exists for users written recently.
func getUserProfileVersionKey(userId int) string {
	return fmt.Sprintf("auth:user:profile:%d", userId)
}

// A pending fence only covers the interval between publishing the next
// version and the surrounding database transaction reaching a decision. Its
// TTL must outlive every user hash that could have been populated before the
// fence, while still allowing an automatically rolled-back transaction to
// recover without an operator repairing Redis.
func userAuthFenceTTLSeconds() int {
	cacheTTL := userCacheTTLSeconds()
	extra := cacheTTL
	if extra < 60 {
		extra = 60
	}
	return cacheTTL + extra
}

func writeUserCache(user *UserBase, includeQuota bool) error {
	if user == nil || user.Id <= 0 || !common.RedisEnabled {
		return nil
	}
	user.CacheSchema = userCacheSchemaVersion
	if user.AuthVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	includeQuotaArg := "0"
	if includeQuota {
		includeQuotaArg = "1"
	}
	ttl := userCacheTTLSeconds()
	const script = `
local incoming = tonumber(ARGV[1])
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
local committed = tonumber(redis.call('GET', KEYS[3]) or '0')
local current = tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0')
if pending > incoming or committed > incoming or current > incoming then
  return 0
end
local incomingProfile = tonumber(ARGV[21])
local profileFloor = tonumber(redis.call('GET', KEYS[4]) or '0')
if profileFloor > incomingProfile then
  return 2
end
if committed < incoming then
  redis.call('SET', KEYS[3], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
if profileFloor < incomingProfile then
  redis.call('SET', KEYS[4], ARGV[21], 'EX', ARGV[22])
end
if ARGV[10] == '0' and redis.call('EXISTS', KEYS[1]) == 0 then
  return 1
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[2], 'Group', ARGV[3], 'Email', ARGV[4],
  'Status', ARGV[5], 'Role', ARGV[6], 'Username', ARGV[7],
  'Setting', ARGV[8], 'AuthVersion', ARGV[1], 'CacheSchema', ARGV[9],
  'GroupRatios', ARGV[13],
  'StreamResponseTimeout', ARGV[14], 'StreamResponseTimeoutMode', ARGV[15],
  'StreamTotalTimeout', ARGV[16],
  'NonStreamResponseTimeout', ARGV[17], 'NonStreamTotalTimeout', ARGV[18],
  'NonStreamTimeoutBilling', ARGV[19], 'RetryTimes', ARGV[20])
if ARGV[10] == '1' and redis.call('HEXISTS', KEYS[1], 'Quota') == 0 then
  redis.call('HSET', KEYS[1], 'Quota', ARGV[11])
end
redis.call('EXPIRE', KEYS[1], ARGV[12])
return 1`
	result, err := common.RDB.Eval(context.Background(), script,
		[]string{getUserCacheKey(user.Id), getUserAuthFenceKey(user.Id), getUserAuthVersionKey(user.Id),
			getUserProfileVersionKey(user.Id)},
		user.AuthVersion, user.Id, user.Group, user.Email, user.Status, user.Role,
		user.Username, user.Setting, user.CacheSchema, includeQuotaArg, user.Quota, ttl,
		user.GroupRatios,
		user.StreamResponseTimeout, user.StreamResponseTimeoutMode, user.StreamTotalTimeout,
		user.NonStreamResponseTimeout, user.NonStreamTotalTimeout,
		user.NonStreamTimeoutBilling, user.RetryTimes,
		user.ProfileVersion, userAuthFenceTTLSeconds(),
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserAuthCachePending
	}
	// result 2: the snapshot predates a profile edit that is already published
	// and the newer snapshot owns the cache. Not an error for the caller — a
	// fill still serves its row to the current request, a publish has nothing
	// left to do.
	return nil
}

func getUserAuthVersionFloor(userId int) (int64, error) {
	if !common.RedisEnabled {
		return 0, nil
	}
	values, err := common.RDB.MGet(context.Background(), getUserAuthFenceKey(userId), getUserAuthVersionKey(userId)).Result()
	if err != nil {
		return 0, err
	}
	return parseUserAuthVersionFloor(values)
}

// parseUserAuthVersionFloor 取 fence 与已提交版本中较大者；两个键都不存在时为 0。
func parseUserAuthVersionFloor(values []interface{}) (int64, error) {
	var floor int64
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil {
			return 0, err
		}
		if parsed > floor {
			floor = parsed
		}
	}
	return floor, nil
}

// SetUserAuthVersionFence publishes a fail-closed version before a restrictive
// database update. Pending fences expire only after every pre-existing user
// hash must have expired; a committed update is promoted separately to a
// permanent monotonic version floor.
func SetUserAuthVersionFence(userId int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid user auth fence")
	}
	const script = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local incoming = tonumber(ARGV[1])
if current < incoming then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
elseif current == incoming then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
elseif redis.call('TTL', KEYS[1]) < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1`
	return common.RDB.Eval(context.Background(), script, []string{getUserAuthFenceKey(userId)}, authVersion, userAuthFenceTTLSeconds()).Err()
}

// publishCommittedUserAuthVersion records the durable lower bound used to
// reject an arbitrarily delayed cache fill after a committed security change.
// It also removes this transaction's now-obsolete pending fence.
func publishCommittedUserAuthVersion(userId int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid committed user auth version")
	}
	const script = `
local incoming = tonumber(ARGV[1])
local committed = tonumber(redis.call('GET', KEYS[1]) or '0')
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
if committed < incoming then
  redis.call('SET', KEYS[1], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
return 1`
	return common.RDB.Eval(context.Background(), script,
		[]string{getUserAuthVersionKey(userId), getUserAuthFenceKey(userId)}, authVersion,
	).Err()
}

// IncrementUserAuthVersionWithTx locks the user, publishes the next deny
// fence, then persists the version in the caller's transaction. Unscoped is
// intentional so the same fail-closed path also covers hard deletion of an
// already soft-deleted user.
func IncrementUserAuthVersionWithTx(tx *gorm.DB, userId int) (int64, error) {
	if tx == nil || userId <= 0 {
		return 0, fmt.Errorf("invalid user auth version update")
	}
	for range 3 {
		var user User
		if err := lockForUpdate(tx.Unscoped()).Select("id", "auth_version").Where("id = ?", userId).First(&user).Error; err != nil {
			return 0, err
		}
		current := user.AuthVersion
		if current < 1 {
			current = 1
		}
		next := current + 1
		if err := SetUserAuthVersionFence(userId, next); err != nil {
			return 0, err
		}
		result := tx.Unscoped().Model(&User{}).
			Where("id = ? AND auth_version = ?", userId, user.AuthVersion).
			Update("auth_version", next)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 1 {
			return next, nil
		}
	}
	return 0, ErrUserAuthVersionConflict
}

// BumpUserAuthVersion is the transaction-owning variant used by password,
// role, status and security-factor changes outside another transaction.
func BumpUserAuthVersion(userId int) (int64, error) {
	var next int64
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		next, err = IncrementUserAuthVersionWithTx(tx, userId)
		return err
	}); err != nil {
		return 0, err
	}
	if err := PublishUserAuthCache(userId); err != nil {
		return next, err
	}
	return next, nil
}

// PublishUserAuthCache refreshes the current database state after a successful
// auth-sensitive transaction without touching the cached quota field.
func PublishUserAuthCache(userId int) error {
	user, err := GetUserById(userId, false)
	if err != nil {
		return err
	}
	return updateUserCache(*user)
}

// advanceProfileFloorUnlessCached is the bulk cleanup's per-user Redis step,
// one round trip. When the user's hash exists it returns true and the caller
// publishes the row (a DB read + EVAL). Otherwise it only advances the profile
// floor to profileVersion, so a fill that read the row before the caller's
// write cannot cache the old value, and the DB read is skipped (a later fill
// reads the new row anyway). profileVersion must not exceed the committed
// profile_version, or fills of the current row would be rejected. On a Redis
// error it answers true: attempting a publish is safer than skipping.
func advanceProfileFloorUnlessCached(userId int, profileVersion int64) bool {
	const script = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 1
end
if tonumber(redis.call('GET', KEYS[2]) or '0') < tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], ARGV[1], 'EX', ARGV[2])
end
return 0`
	cached, err := common.RDB.Eval(context.Background(), script,
		[]string{getUserCacheKey(userId), getUserProfileVersionKey(userId)},
		profileVersion, userAuthFenceTTLSeconds(),
	).Int()
	if err != nil {
		return true
	}
	return cached == 1
}

// InitializeUserAuthVersions must run after AutoMigrate when upgrading an
// existing database. It is idempotent and portable across all supported DBs.
func InitializeUserAuthVersions() error {
	return DB.Model(&User{}).Where("auth_version IS NULL OR auth_version < ?", 1).Update("auth_version", 1).Error
}

// profileUnfenced is passed as the profile version for fields outside the
// profile fence (Group follows the auth-version / re-read scheme instead).
const profileUnfenced int64 = -1

// updateUserCacheFieldAtVersion writes one field into an existing user hash.
// authVersion (and profileVersion, unless profileUnfenced) must come from the
// same row read as value, so a value read before an edit cannot overwrite the
// edit's published hash.
//
// The write never touches CacheSchema, and it still happens when the hash was
// written under another schema (rolling deploy). Stamping the current schema
// onto an old node's hash would make the fields that schema lacks (e.g.
// GroupRatios) read as empty on new nodes; skipping the write would leave old
// nodes serving the stale field (e.g. the previous Group) until the hash
// expires. Writing only the field is correct for both: every field written
// here means the same in all schemas, old nodes see the fresh value, and new
// nodes keep treating the foreign hash as stale and refill it in full.
// Deleting the foreign hash instead is not an option: it would drop the
// Redis-only quota deductions that the batch updater has not flushed yet.
func updateUserCacheFieldAtVersion(userId int, field string, value interface{}, authVersion int64, profileVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	const script = `
local incoming = tonumber(ARGV[1])
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
local committed = tonumber(redis.call('GET', KEYS[3]) or '0')
local current = tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0')
if pending > incoming or committed > incoming or current > incoming then
  return 0
end
if committed < incoming then
  redis.call('SET', KEYS[3], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 1
end
if current ~= incoming then
  return 1
end
local incomingProfile = tonumber(ARGV[4])
if incomingProfile >= 0 and tonumber(redis.call('GET', KEYS[4]) or '0') > incomingProfile then
  return 1
end
redis.call('HSET', KEYS[1], ARGV[2], ARGV[3])
return 1`
	result, err := common.RDB.Eval(context.Background(), script,
		[]string{getUserCacheKey(userId), getUserAuthFenceKey(userId), getUserAuthVersionKey(userId),
			getUserProfileVersionKey(userId)},
		authVersion, field, value, profileVersion,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserAuthCachePending
	}
	return nil
}
