package ratio_setting

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

// userExclusiveGroupRatioEnabled gates the per-user exclusive group ratio feature.
// Default off so behavior is identical to before until an admin enables it.
var userExclusiveGroupRatioEnabled atomic.Bool

// userGroupRatioEntry is a memo-cache value: the parsed (read-only) map plus a
// CLOCK "referenced" bit used for second-chance eviction.
type userGroupRatioEntry struct {
	ratios map[string]float64 // read-only, shared across requests
	hot    atomic.Bool        // set on hit; cleared/evicted by the sweep
}

// parsedUserGroupRatios memoizes parsed group_ratios keyed by the raw JSON string.
// Keying by content makes it self-versioning (an edited config is simply a new
// key, no explicit invalidation) and bounds the cache by the number of DISTINCT
// configs — far smaller than the number of users, since many users share the same
// config. When the cap is reached, a CLOCK (second-chance) sweep evicts only the
// entries not accessed since the previous sweep, keeping hot configs cached.
var (
	parsedUserGroupRatios      sync.Map // string(json) -> *userGroupRatioEntry
	parsedUserGroupRatiosCount atomic.Int64
	parsedUserGroupRatiosSweep atomic.Bool // ensures a single concurrent sweeper
	// parsedUserGroupRatiosMax caps the memo table to guard against pathological
	// growth (e.g. every user a unique config). 0 disables the memo cache entirely
	// (always parse). Runtime configurable via UserExclusiveGroupRatioCacheMax.
	parsedUserGroupRatiosMax atomic.Int64
)

const defaultUserGroupRatioCacheMax = 4096

func init() {
	userExclusiveGroupRatioEnabled.Store(false)
	parsedUserGroupRatiosMax.Store(defaultUserGroupRatioCacheMax)
}

// SetUserGroupRatioCacheMax sets the memoization cap (0 = disable caching).
// Negative input is clamped to 0.
func SetUserGroupRatioCacheMax(n int) {
	if n < 0 {
		n = 0
	}
	parsedUserGroupRatiosMax.Store(int64(n))
}

// GetUserGroupRatioCacheMax returns the current memoization cap.
func GetUserGroupRatioCacheMax() int {
	return int(parsedUserGroupRatiosMax.Load())
}

func SetUserExclusiveGroupRatioEnabled(enabled bool) {
	userExclusiveGroupRatioEnabled.Store(enabled)
}

func IsUserExclusiveGroupRatioEnabled() bool {
	return userExclusiveGroupRatioEnabled.Load()
}

// ParseUserGroupRatios parses the per-user exclusive group ratio JSON
// (e.g. `{"vip":0.8}`). Returns nil for empty/invalid input so callers
// silently degrade to the global ratio logic.
//
// The result is memoized by the raw JSON string, so repeated calls with the same
// config avoid re-deserializing on the hot path. Because a cached map is shared
// across all callers/requests, the returned map MUST be treated as read-only and
// never mutated in place (see design §8.5) — mutating it would corrupt the shared
// entry and race with concurrent readers.
func ParseUserGroupRatios(jsonStr string) map[string]float64 {
	if jsonStr == "" || jsonStr == "{}" {
		return nil
	}
	if cached, ok := parsedUserGroupRatios.Load(jsonStr); ok {
		e := cached.(*userGroupRatioEntry)
		if !e.hot.Load() {
			e.hot.Store(true) // mark referenced for the CLOCK sweep
		}
		return e.ratios
	}
	m := make(map[string]float64)
	if err := common.Unmarshal([]byte(jsonStr), &m); err != nil {
		common.SysError("failed to parse user group_ratios: " + err.Error())
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	max := parsedUserGroupRatiosMax.Load()
	if max <= 0 {
		return m // caching disabled
	}
	// At/over cap: run a second-chance sweep to reclaim inactive entries.
	if parsedUserGroupRatiosCount.Load() >= max {
		sweepUserGroupRatioCache()
	}
	e := &userGroupRatioEntry{ratios: m}
	e.hot.Store(true) // new entries start referenced (one full cycle of grace)
	if _, loaded := parsedUserGroupRatios.LoadOrStore(jsonStr, e); !loaded {
		parsedUserGroupRatiosCount.Add(1)
	}
	return m
}

// sweepUserGroupRatioCache runs one CLOCK pass: entries referenced since the last
// sweep are kept (their bit is cleared, giving a second chance); unreferenced
// entries are evicted. A single sweeper runs at a time; concurrent callers skip
// (and may briefly exceed the cap, self-corrected on the next sweep). Races with
// concurrent readers marking an entry hot are harmless — worst case a just-active
// entry is evicted and re-parsed on next access.
func sweepUserGroupRatioCache() {
	if !parsedUserGroupRatiosSweep.CompareAndSwap(false, true) {
		return
	}
	defer parsedUserGroupRatiosSweep.Store(false)
	parsedUserGroupRatios.Range(func(key, val any) bool {
		e := val.(*userGroupRatioEntry)
		if e.hot.Load() {
			e.hot.Store(false) // second chance
		} else {
			parsedUserGroupRatios.Delete(key)
			parsedUserGroupRatiosCount.Add(-1)
		}
		return true
	})
}

// lookupUserExclusive returns the user's exclusive ratio for usingGroup.
// It is nil-safe (reading a nil map returns the zero value) and skips
// missing or non-finite/negative values, so it can never panic.
func lookupUserExclusive(userGroupRatios map[string]float64, usingGroup string) (float64, bool) {
	r, ok := userGroupRatios[usingGroup]
	if !ok || r < 0 || math.IsNaN(r) || math.IsInf(r, 0) {
		return 0, false
	}
	return r, true
}

// ResolveGroupRatio resolves the effective group ratio with priority:
//  1. per-user exclusive ratio (only when the feature is enabled);
//  2. global group-group ratio (GetGroupGroupRatio);
//  3. global group ratio (GetGroupRatio).
//
// The second return value reports whether a "special" (non-base) ratio was
// applied — matching the existing GroupSpecialRatio/HasSpecialRatio semantics.
// Any anomaly in the per-user branch falls through to the original two-step
// logic, so the result for an unconfigured user is byte-for-byte unchanged.
func ResolveGroupRatio(userGroupRatios map[string]float64, userGroup, usingGroup string) (float64, bool) {
	if userExclusiveGroupRatioEnabled.Load() {
		if r, ok := lookupUserExclusive(userGroupRatios, usingGroup); ok {
			return r, true
		}
	}
	if r, ok := GetGroupGroupRatio(userGroup, usingGroup); ok {
		return r, true
	}
	return GetGroupRatio(usingGroup), false
}
