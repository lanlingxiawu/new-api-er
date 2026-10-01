package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const userCacheSchemaVersion = 4

type UserBase struct {
	Id          int    `json:"id"`
	Group       string `json:"group"`
	GroupRatios string `json:"group_ratios"`
	Email       string `json:"email"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
	Role        int    `json:"role"`
	Username    string `json:"username"`
	Setting     string `json:"setting"`
	AuthVersion int64  `json:"-"`
	CacheSchema int    `json:"-"`
	// ProfileVersion is the row's profile_version at snapshot time. It is only
	// used to fence cache writes (see writeUserCache) and is not stored in the
	// hash, so it reads back as 0 from Redis.
	ProfileVersion int64 `json:"-"`

	StreamResponseTimeout     int    `json:"stream_response_timeout"`
	StreamResponseTimeoutMode string `json:"stream_response_timeout_mode"`
	StreamTotalTimeout        int    `json:"stream_total_timeout"`
	NonStreamResponseTimeout  int    `json:"non_stream_response_timeout"`
	NonStreamTotalTimeout     int    `json:"non_stream_total_timeout"`
	NonStreamTimeoutBilling   string `json:"non_stream_timeout_billing"`
	RetryTimes                int    `json:"retry_times"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeout, user.StreamResponseTimeout)
	streamResponseTimeoutMode := user.StreamResponseTimeoutMode
	if streamResponseTimeoutMode == "" {
		streamResponseTimeoutMode = constant.RelayStreamResponseTimeoutModeFirstOutput
	}
	common.SetContextKey(c, constant.ContextKeyUserStreamResponseTimeoutMode, streamResponseTimeoutMode)
	common.SetContextKey(c, constant.ContextKeyUserStreamTotalTimeout, user.StreamTotalTimeout)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamResponseTimeout, user.NonStreamResponseTimeout)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTotalTimeout, user.NonStreamTotalTimeout)
	nonStreamTimeoutBilling := user.NonStreamTimeoutBilling
	if nonStreamTimeoutBilling == "" {
		nonStreamTimeoutBilling = constant.NonStreamTimeoutBillingRefund
	}
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, nonStreamTimeoutBilling)
	common.SetContextKey(c, constant.ContextKeyUserRetryTimes, user.RetryTimes)
	// Per-user exclusive group ratios. Only parse when the feature is enabled:
	// when the flag is off the map is ignored by ResolveGroupRatio anyway, so
	// this avoids a per-request JSON unmarshal + map allocation on the auth hot
	// path for every user that has group_ratios stored. When on, the empty/"{}"
	// fast-path in ParseUserGroupRatios means only configured users pay the parse
	// (design §8.3). IsUserExclusiveGroupRatioEnabled reads an atomic bool, so a
	// runtime toggle takes effect on the next request without a restart.
	if ratio_setting.IsUserExclusiveGroupRatioEnabled() {
		if ratios := user.GetGroupRatios(); len(ratios) > 0 {
			common.SetContextKey(c, constant.ContextKeyUserGroupRatios, ratios)
		}
	}
}

// GetGroupRatios parses the per-user exclusive group ratio overrides.
// Returns nil for empty/invalid content (silent degrade to global ratios).
func (user *UserBase) GetGroupRatios() map[string]float64 {
	return ratio_setting.ParseUserGroupRatios(user.GroupRatios)
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

func userCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache is the exported version of invalidateUserCache.
// 供 controller 等上层包在用户状态变更（如禁用、删除、角色变更）后主动清理缓存。
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

func populateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	return writeUserCache(user.ToBaseUser(), true)
}

// updateUserCache refreshes non-quota user cache fields.
// Quota is maintained by atomic quota delta paths and must not be overwritten
// by stale user snapshots from profile/settings updates.
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	return writeUserCache(user.ToBaseUser(), false)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (*UserBase, error) {
	// Try getting from Redis first
	userCache, err := cacheGetUserBase(userId)
	if err == nil {
		return userCache, nil
	}

	// Redis misses and read failures both fall back to the shared database. A
	// version fence newer than the database is the one exception: allowing that
	// snapshot would re-authorize a user while a restrictive update is pending.
	user, err := GetUserById(userId, false)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		floor, floorErr := getUserAuthVersionFloor(userId)
		if floorErr == nil && floor > user.AuthVersion {
			return nil, ErrUserAuthCachePending
		}
		if err := populateUserCache(*user); err != nil {
			if errors.Is(err, ErrUserAuthCachePending) {
				return nil, err
			}
			common.SysLog("failed to synchronously populate user cache: " + err.Error())
		}
	}
	return user.ToBaseUser(), nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	// 哈希与版本下限放进同一个 pipeline：每个 relay 请求都会走到这里，两次往返合为一次。
	// 命令顺序必须保持"先哈希、后下限"——下限读在快照之后，才能拦住读快照之后
	// 才发布的 fence。
	ctx := context.Background()
	key := getUserCacheKey(userId)
	pipe := common.RDB.Pipeline()
	hashCmd := pipe.HGetAll(ctx, key)
	floorCmd := pipe.MGet(ctx, getUserAuthFenceKey(userId), getUserAuthVersionKey(userId))
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("failed to load user cache from Redis: %w", err)
	}
	var userCache UserBase
	if err := common.DecodeRedisHash(key, hashCmd.Val(), &userCache); err != nil {
		return nil, err
	}
	if userCache.Id != userId || userCache.CacheSchema != userCacheSchemaVersion || userCache.AuthVersion <= 0 {
		return nil, fmt.Errorf("user cache schema is stale")
	}
	floor, err := parseUserAuthVersionFloor(floorCmd.Val())
	if err != nil {
		return nil, err
	}
	if floor > userCache.AuthVersion {
		return nil, ErrUserAuthCachePending
	}
	return &userCache, nil
}

// Add atomic quota operations using hash fields
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHIncrBy(getUserCacheKey(userId), "Quota", delta)
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// GetUserGroupRatios returns the user's exclusive per-group ratio overrides,
// read Redis-first via the existing user cache (DB fallback). For non-hot-path
// callers (e.g. pricing display, async task settlement). Returns nil on any error.
func GetUserGroupRatios(userId int) map[string]float64 {
	cache, err := GetUserCache(userId)
	if err != nil {
		return nil
	}
	return cache.GetGroupRatios()
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserStatusCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Status, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

func updateUserQuotaCache(userId int, quota int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Quota", fmt.Sprintf("%d", quota))
}

// RefreshUserGroupCache writes the database-authoritative group into an
// existing user hash without changing the user's authentication version.
func RefreshUserGroupCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return fmt.Errorf("invalid user id")
	}
	var authoritative User
	if err := DB.Select("id", "auth_version", commonGroupCol).Where("id = ?", userId).First(&authoritative).Error; err != nil {
		return err
	}
	// Group transitions intentionally keep the same authentication version. A
	// refresh that read the previous group can therefore arrive after a newer
	// refresh and still pass the auth-version fence. Re-read after every write
	// and repair the cache when the authoritative group changed in between.
	for range 3 {
		if err := updateUserCacheFieldAtVersion(userId, "Group", authoritative.Group, authoritative.AuthVersion, profileUnfenced); err != nil {
			return err
		}

		var verified User
		if err := DB.Select("id", "auth_version", commonGroupCol).Where("id = ?", userId).First(&verified).Error; err != nil {
			return err
		}
		if verified.AuthVersion == authoritative.AuthVersion && verified.Group == authoritative.Group {
			return nil
		}
		authoritative = verified
	}

	// Preserve the freshest snapshot observed even when the row was too busy to
	// stabilize within the bounded retries. Returning an error lets best-effort
	// callers emit an operation-specific warning.
	if err := updateUserCacheFieldAtVersion(userId, "Group", authoritative.Group, authoritative.AuthVersion, profileUnfenced); err != nil {
		return err
	}
	return fmt.Errorf("user group changed repeatedly during cache refresh")
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}
