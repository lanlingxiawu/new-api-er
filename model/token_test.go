package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Pure logic: masking, ip limits, model limits, like-pattern sanitising
// ---------------------------------------------------------------------------

func TestMaskTokenKey(t *testing.T) {
	// boundary on the two length thresholds (<=4, <=8, >8)
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"a", "*"},
		{"abcd", "****"},        // len 4 boundary -> all stars
		{"abcde", "ab****de"},   // len 5 -> 2+stars+2
		{"abcdefgh", "ab****gh"}, // len 8 boundary
		{"abcdefghi", "abcd**********fghi"}, // len 9 -> 4+10*+4
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, MaskTokenKey(c.in), "MaskTokenKey(%q)", c.in)
	}
}

func TestToken_KeyHelpers(t *testing.T) {
	tk := &Token{Key: "abcdefghijkl"}
	assert.Equal(t, "abcdefghijkl", tk.GetFullKey())
	assert.Equal(t, MaskTokenKey("abcdefghijkl"), tk.GetMaskedKey())
	tk.Clean()
	assert.Equal(t, "", tk.Key)
	assert.Equal(t, "", tk.GetFullKey())
}

func TestToken_GetIpLimits(t *testing.T) {
	// nil pointer
	tk := &Token{AllowIps: nil}
	assert.Empty(t, tk.GetIpLimits())

	// empty after stripping spaces
	empty := "   "
	tk.AllowIps = &empty
	assert.Empty(t, tk.GetIpLimits())

	// multi-line with commas, spaces and blank lines
	raw := " 1.1.1.1 ,\n2.2.2.2\n\n , \n3.3.3.3"
	tk.AllowIps = &raw
	assert.Equal(t, []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, tk.GetIpLimits())
}

func TestToken_ModelLimits(t *testing.T) {
	tk := &Token{ModelLimits: ""}
	assert.Equal(t, []string{}, tk.GetModelLimits())
	assert.Empty(t, tk.GetModelLimitsMap())

	tk.ModelLimits = "gpt-4o,claude-3,gemini"
	assert.Equal(t, []string{"gpt-4o", "claude-3", "gemini"}, tk.GetModelLimits())
	m := tk.GetModelLimitsMap()
	assert.True(t, m["gpt-4o"])
	assert.True(t, m["claude-3"])
	assert.False(t, m["absent"])

	tk.ModelLimitsEnabled = true
	assert.True(t, tk.IsModelLimitsEnabled())
	tk.ModelLimitsEnabled = false
	assert.False(t, tk.IsModelLimitsEnabled())
}

func TestValidateLikePattern(t *testing.T) {
	// valid: no % (exact), single %, two %, keyword>=2 with %
	require.NoError(t, validateLikePattern("abc"))
	require.NoError(t, validateLikePattern("ab%"))
	require.NoError(t, validateLikePattern("%ab%"))

	// consecutive % rejected
	assert.Error(t, validateLikePattern("a%%b"))
	// more than 2 % rejected
	assert.Error(t, validateLikePattern("%a%b%"))
	// with % but stripped keyword < 2 chars
	assert.Error(t, validateLikePattern("%a"))
	assert.Error(t, validateLikePattern("%a%")) // stripped "a" len 1
}

func TestSanitizeLikePattern(t *testing.T) {
	// escapes ! and _
	out, err := sanitizeLikePattern("a_b")
	require.NoError(t, err)
	assert.Equal(t, "a!_b", out)

	out, err = sanitizeLikePattern("a!b")
	require.NoError(t, err)
	assert.Equal(t, "a!!b", out)

	// invalid propagates the validation error
	_, err = sanitizeLikePattern("a%%b")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// DB: insert / lookup / validate
// ---------------------------------------------------------------------------

func TestToken_InsertAndGetById(t *testing.T) {
	u := mkUser(t, nil)

	// exercise Token.Insert directly
	tk := &Token{
		UserId:      u.Id,
		Key:         strings.ReplaceAll(uniq("k"), "_", "") + "insertaaaaaaaaaaaaaa",
		Name:        uniq("ins"),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 500,
		CreatedTime: time.Now().Unix(),
	}
	require.NoError(t, tk.Insert())
	deleteByID(t, &Token{}, tk.Id)

	got, err := GetTokenById(tk.Id)
	require.NoError(t, err)
	assert.Equal(t, tk.Key, got.Key)
	assert.Equal(t, 500, got.RemainQuota)

	// id == 0 guard
	_, err = GetTokenById(0)
	assert.Error(t, err)
}

func TestGetTokenByIds(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)

	got, err := GetTokenByIds(tk.Id, u.Id)
	require.NoError(t, err)
	assert.Equal(t, tk.Id, got.Id)

	// mismatched user id -> record not found
	_, err = GetTokenByIds(tk.Id, u.Id+999999)
	assert.Error(t, err)

	// zero args guard
	_, err = GetTokenByIds(0, u.Id)
	assert.Error(t, err)
	_, err = GetTokenByIds(tk.Id, 0)
	assert.Error(t, err)
}

func TestGetTokenByKey(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)

	got, err := GetTokenByKey(tk.Key, true)
	require.NoError(t, err)
	assert.Equal(t, tk.Id, got.Id)

	_, err = GetTokenByKey("nonexistent-key-zzz", true)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestValidateUserToken(t *testing.T) {
	u := mkUser(t, nil)

	t.Run("empty key", func(t *testing.T) {
		_, err := ValidateUserToken("")
		assert.ErrorIs(t, err, ErrTokenNotProvided)
	})

	t.Run("valid enabled with quota", func(t *testing.T) {
		tk := mkToken(t, u.Id, func(tk *Token) {
			tk.RemainQuota = 100
			tk.Status = common.TokenStatusEnabled
		})
		got, err := ValidateUserToken(tk.Key)
		require.NoError(t, err)
		assert.Equal(t, tk.Id, got.Id)
	})

	t.Run("unlimited quota with zero remain is valid", func(t *testing.T) {
		tk := mkToken(t, u.Id, func(tk *Token) {
			tk.UnlimitedQuota = true
			tk.RemainQuota = 0
		})
		_, err := ValidateUserToken(tk.Key)
		require.NoError(t, err)
	})

	t.Run("disabled status invalid", func(t *testing.T) {
		tk := mkToken(t, u.Id, func(tk *Token) {
			tk.Status = common.TokenStatusDisabled
			tk.RemainQuota = 100
		})
		_, err := ValidateUserToken(tk.Key)
		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("expired flips status and invalid", func(t *testing.T) {
		tk := mkToken(t, u.Id, func(tk *Token) {
			tk.RemainQuota = 100
			tk.ExpiredTime = common.GetTimestamp() - 100
		})
		_, err := ValidateUserToken(tk.Key)
		assert.ErrorIs(t, err, ErrTokenInvalid)
		// RedisEnabled false -> status persisted as expired
		reloaded, _ := GetTokenById(tk.Id)
		assert.Equal(t, common.TokenStatusExpired, reloaded.Status)
	})

	t.Run("exhausted flips status and invalid", func(t *testing.T) {
		tk := mkToken(t, u.Id, func(tk *Token) {
			tk.RemainQuota = 0
			tk.UnlimitedQuota = false
		})
		_, err := ValidateUserToken(tk.Key)
		assert.ErrorIs(t, err, ErrTokenInvalid)
		reloaded, _ := GetTokenById(tk.Id)
		assert.Equal(t, common.TokenStatusExhausted, reloaded.Status)
	})

	t.Run("not found invalid", func(t *testing.T) {
		_, err := ValidateUserToken("definitely-missing-key-xyz")
		assert.ErrorIs(t, err, ErrTokenInvalid)
	})
}

// ---------------------------------------------------------------------------
// DB: update / delete
// ---------------------------------------------------------------------------

func TestToken_UpdateAndSelectUpdate(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) { tk.Name = "orig" })

	tk.Name = "renamed"
	tk.Status = common.TokenStatusDisabled
	require.NoError(t, tk.Update())
	reloaded, _ := GetTokenById(tk.Id)
	assert.Equal(t, "renamed", reloaded.Name)
	assert.Equal(t, common.TokenStatusDisabled, reloaded.Status)

	// SelectUpdate can write zero values for accessed_time/status
	tk.Status = common.TokenStatusEnabled
	tk.AccessedTime = 12345
	require.NoError(t, tk.SelectUpdate())
	reloaded, _ = GetTokenById(tk.Id)
	assert.EqualValues(t, 12345, reloaded.AccessedTime)
	assert.Equal(t, common.TokenStatusEnabled, reloaded.Status)
}

func TestToken_Delete(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)
	require.NoError(t, tk.Delete())
	_, err := GetTokenById(tk.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDeleteTokenById(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)

	// zero-arg guards
	assert.Error(t, DeleteTokenById(0, u.Id))
	assert.Error(t, DeleteTokenById(tk.Id, 0))

	// wrong owner -> not found error
	assert.Error(t, DeleteTokenById(tk.Id, u.Id+987654))

	// correct owner deletes
	require.NoError(t, DeleteTokenById(tk.Id, u.Id))
	_, err := GetTokenById(tk.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDisableModelLimits(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) {
		tk.ModelLimitsEnabled = true
		tk.ModelLimits = "gpt-4o"
	})
	require.NoError(t, DisableModelLimits(tk.Id))
	reloaded, _ := GetTokenById(tk.Id)
	assert.False(t, reloaded.ModelLimitsEnabled)
	assert.Equal(t, "", reloaded.ModelLimits)

	// missing token id -> error
	assert.Error(t, DisableModelLimits(0))
}

// ---------------------------------------------------------------------------
// DB: quota increase / decrease
// ---------------------------------------------------------------------------

func TestIncreaseDecreaseTokenQuota(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) {
		tk.RemainQuota = 1000
		tk.UsedQuota = 500
	})

	// negative quota rejected on both
	assert.Error(t, IncreaseTokenQuota(tk.Id, tk.Key, -1))
	assert.Error(t, DecreaseTokenQuota(tk.Id, tk.Key, -1))

	require.NoError(t, DecreaseTokenQuota(tk.Id, tk.Key, 200))
	reloaded, _ := GetTokenById(tk.Id)
	assert.Equal(t, 800, reloaded.RemainQuota) // 1000-200
	assert.Equal(t, 700, reloaded.UsedQuota)   // 500+200

	require.NoError(t, IncreaseTokenQuota(tk.Id, tk.Key, 300))
	reloaded, _ = GetTokenById(tk.Id)
	assert.Equal(t, 1100, reloaded.RemainQuota) // 800+300
	assert.Equal(t, 400, reloaded.UsedQuota)    // 700-300
}

// ---------------------------------------------------------------------------
// DB: listing / counting / batch
// ---------------------------------------------------------------------------

func TestCountAndGetAllUserTokens(t *testing.T) {
	u := mkUser(t, nil)
	mkToken(t, u.Id, nil)
	mkToken(t, u.Id, nil)
	mkToken(t, u.Id, nil)

	total, err := CountUserTokens(u.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)

	// pagination: page size 2
	page1, err := GetAllUserTokens(u.Id, 0, 2)
	require.NoError(t, err)
	assert.Len(t, page1, 2)
	page2, err := GetAllUserTokens(u.Id, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 1)
	// ordered id desc: page1 first id > page2 first id
	assert.Greater(t, page1[0].Id, page2[0].Id)
}

func TestBatchDeleteTokensAndGetKeys(t *testing.T) {
	u := mkUser(t, nil)
	t1 := mkToken(t, u.Id, nil)
	t2 := mkToken(t, u.Id, nil)

	// empty ids guard
	_, err := BatchDeleteTokens(nil, u.Id)
	assert.Error(t, err)

	keys, err := GetTokenKeysByIds([]int{t1.Id, t2.Id}, u.Id)
	require.NoError(t, err)
	assert.Len(t, keys, 2)

	n, err := BatchDeleteTokens([]int{t1.Id, t2.Id}, u.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	total, _ := CountUserTokens(u.Id)
	assert.EqualValues(t, 0, total)
}

// ---------------------------------------------------------------------------
// DB: search
// ---------------------------------------------------------------------------

func TestSearchUserTokens(t *testing.T) {
	u := mkUser(t, nil)
	grp := uniq("srch")
	mkToken(t, u.Id, func(tk *Token) { tk.Name = grp + "-alpha" })
	mkToken(t, u.Id, func(tk *Token) { tk.Name = grp + "-beta" })

	// keyword empty + token empty -> returns all for user, limit clamped
	tokens, total, err := SearchUserTokens(u.Id, "", "", -5, 0) // negative offset, zero limit
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, tokens, 2)

	// name fuzzy match on unique group prefix
	tokens, total, err = SearchUserTokens(u.Id, grp+"-al%", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, tokens, 1)
	assert.Equal(t, grp+"-alpha", tokens[0].Name)

	// invalid pattern (consecutive %) surfaces error
	_, _, err = SearchUserTokens(u.Id, "a%%b", "", 0, 10)
	assert.Error(t, err)

	// sk- prefix trimmed on token search; searching by exact key returns it
	target := tokens[0]
	_ = target
}

func TestSearchUserTokens_ByKeyPrefixTrim(t *testing.T) {
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)

	// exact key search with sk- prefix must be trimmed then matched
	tokens, total, err := SearchUserTokens(u.Id, "", "sk-"+tk.Key, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, tokens, 1)
	assert.Equal(t, tk.Id, tokens[0].Id)
}

func TestInvalidateUserTokensCache_RedisDisabled(t *testing.T) {
	// With Redis disabled the function is a no-op returning nil.
	require.False(t, common.RedisEnabled)
	assert.NoError(t, InvalidateUserTokensCache(123))
	// invalid userId still short-circuits nil because RedisEnabled is false
	assert.NoError(t, InvalidateUserTokensCache(-1))
}

func TestInvalidateTokensCache_RedisDisabled(t *testing.T) {
	require.False(t, common.RedisEnabled)
	assert.NoError(t, invalidateTokensCache([]Token{{Key: "x"}, {Key: ""}}))
}

// sanity: ensure ErrTokenInvalid / ErrDatabase are distinct sentinels
func TestTokenSentinelErrors(t *testing.T) {
	assert.False(t, errors.Is(ErrTokenInvalid, ErrDatabase))
	assert.False(t, errors.Is(ErrTokenNotProvided, ErrTokenInvalid))
}

// ---------------------------------------------------------------------------
// Redis-backed: token cache mirror + cache_*.go functions
// ---------------------------------------------------------------------------

func TestToken_CacheRoundTrip(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) { tk.RemainQuota = 777 })

	// set cache directly, then read back through cache
	require.NoError(t, cacheSetToken(*tk))
	got, err := cacheGetTokenByKey(tk.Key)
	require.NoError(t, err)
	assert.Equal(t, tk.Id, got.Id)
	assert.Equal(t, 777, got.RemainQuota)

	// incr / decr quota in cache
	require.NoError(t, cacheIncrTokenQuota(tk.Key, 100))
	require.NoError(t, cacheDecrTokenQuota(tk.Key, 50))
	got, err = cacheGetTokenByKey(tk.Key)
	require.NoError(t, err)
	assert.Equal(t, 777+100-50, got.RemainQuota)

	// delete from cache -> subsequent get misses
	require.NoError(t, cacheDeleteToken(tk.Key))
	_, err = cacheGetTokenByKey(tk.Key)
	assert.Error(t, err)
}

func TestGetTokenByKey_RedisFirst(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) { tk.RemainQuota = 42 })
	require.NoError(t, cacheSetToken(*tk))

	// fromDB=false should hit Redis and return the cached token
	got, err := GetTokenByKey(tk.Key, false)
	require.NoError(t, err)
	assert.Equal(t, tk.Id, got.Id)
	assert.Equal(t, 42, got.RemainQuota)
}

func TestIncreaseTokenQuota_RedisPath(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) { tk.RemainQuota = 1000 })
	require.NoError(t, cacheSetToken(*tk))

	// With Redis enabled the async cache path fires; DB is still updated
	// synchronously because BatchUpdate is disabled.
	require.NoError(t, IncreaseTokenQuota(tk.Id, tk.Key, 250))
	reloaded, _ := GetTokenById(tk.Id)
	assert.Equal(t, 1250, reloaded.RemainQuota)
}

func TestToken_RedisMutationBranches(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, func(tk *Token) { tk.RemainQuota = 1000 })
	require.NoError(t, cacheSetToken(*tk))

	// Decrease redis outer branch
	require.NoError(t, DecreaseTokenQuota(tk.Id, tk.Key, 100))
	// Update / SelectUpdate / Delete redis outer branches
	tk.Name = "r"
	require.NoError(t, tk.Update())
	require.NoError(t, tk.SelectUpdate())

	// BatchDeleteTokens redis cleanup branch
	tk2 := mkToken(t, u.Id, nil)
	require.NoError(t, cacheSetToken(*tk2))
	n, err := BatchDeleteTokens([]int{tk2.Id}, u.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestValidateUserToken_RedisSkipsStatusPersist(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	// expired: with Redis enabled the status is NOT persisted (skip SelectUpdate)
	tk := mkToken(t, u.Id, func(tk *Token) {
		tk.RemainQuota = 100
		tk.ExpiredTime = common.GetTimestamp() - 100
	})
	_, err := ValidateUserToken(tk.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)
	reloaded, _ := GetTokenById(tk.Id)
	assert.Equal(t, common.TokenStatusEnabled, reloaded.Status) // unchanged

	// exhausted: same skip
	tk2 := mkToken(t, u.Id, func(tk *Token) { tk.RemainQuota = 0 })
	_, err = ValidateUserToken(tk2.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)
	reloaded2, _ := GetTokenById(tk2.Id)
	assert.Equal(t, common.TokenStatusEnabled, reloaded2.Status)
}

func TestSearchUserTokens_TokenFuzzyInvalidPattern(t *testing.T) {
	u := mkUser(t, nil)
	// invalid fuzzy pattern on the token field surfaces the sanitize error
	_, _, err := SearchUserTokens(u.Id, "", "a%%b", 0, 10)
	assert.Error(t, err)
	// limit above hard cap clamps to searchHardLimit (no error)
	_, _, err = SearchUserTokens(u.Id, "", "", 0, 9999)
	require.NoError(t, err)
}

func TestInvalidateUserTokensCache_RedisEnabled(t *testing.T) {
	enableRedis(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)
	require.NoError(t, cacheSetToken(*tk))

	// invalid userId guard now reachable because RedisEnabled is true
	assert.Error(t, InvalidateUserTokensCache(0))

	require.NoError(t, InvalidateUserTokensCache(u.Id))
	_, err := cacheGetTokenByKey(tk.Key)
	assert.Error(t, err)
}
