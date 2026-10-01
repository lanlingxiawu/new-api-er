package ratio_setting

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

// DefaultUserGroupRatioCacheMax is the memoization cap used until the option is
// set, and when a stored value is unusable.
const DefaultUserGroupRatioCacheMax = 4096

func init() {
	userExclusiveGroupRatioEnabled.Store(false)
	parsedUserGroupRatiosMax.Store(DefaultUserGroupRatioCacheMax)
}

// SetUserGroupRatioCacheMax sets the memoization cap (0 = disable caching).
// Negative input is clamped to 0.
func SetUserGroupRatioCacheMax(n int) {
	if n < 0 {
		n = 0
	}
	parsedUserGroupRatiosMax.Store(int64(n))
}

// ParseUserGroupRatioCacheMax parses the UserExclusiveGroupRatioCacheMax option:
// a whole number >= 0 (0 disables the memo cache). Anything else is an error so
// the option is never stored with a value the runtime would ignore.
func ParseUserGroupRatioCacheMax(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("UserExclusiveGroupRatioCacheMax must be a whole number >= 0, got %q", value)
	}
	return n, nil
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

// DecodeUserGroupRatios decodes a raw group_ratios JSON object without touching
// the memo table. A null amount is not a ratio: decoding it into a float64
// would yield 0, i.e. a free-usage override. Such entries are left out of the
// map and reported in nullGroups (sorted) so callers can reject or skip them;
// an explicit 0 stays a deliberate free override. Any other non-number value is
// a decode error.
func DecodeUserGroupRatios(raw string) (ratios map[string]float64, nullGroups []string, err error) {
	decoded := make(map[string]*float64)
	if err := common.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, nil, err
	}
	ratios = make(map[string]float64, len(decoded))
	for name, r := range decoded {
		if r == nil {
			nullGroups = append(nullGroups, name)
			continue
		}
		ratios[name] = *r
	}
	sort.Strings(nullGroups)
	return ratios, nullGroups, nil
}

// userGroupRatioLogIntervalSeconds bounds how often a bad stored group_ratios
// value is logged: ParseUserGroupRatios runs per relay request for values it
// does not memoize (corrupt JSON always, everything when the memo cache is
// disabled), and one bad row must not flood the log. Null entries and corrupt
// JSON are limited separately so one kind cannot hide the other.
const userGroupRatioLogIntervalSeconds = 60

var (
	nullUserGroupRatioLastLog    atomic.Int64
	corruptUserGroupRatioLastLog atomic.Int64
)

// allowUserGroupRatioLog reports whether the caller may log now, at most once
// per interval per limiter.
func allowUserGroupRatioLog(last *atomic.Int64) bool {
	now := time.Now().Unix()
	prev := last.Load()
	return now-prev >= userGroupRatioLogIntervalSeconds && last.CompareAndSwap(prev, now)
}

func logNullUserGroupRatios(groups []string) {
	if allowUserGroupRatioLog(&nullUserGroupRatioLastLog) {
		common.SysError("user group_ratios contains null amounts, ignored (configured pricing applies) for groups: " + strings.Join(groups, ", "))
	}
}

func logCorruptUserGroupRatios(err error) {
	if allowUserGroupRatioLog(&corruptUserGroupRatioLastLog) {
		common.SysError("failed to parse user group_ratios (configured pricing applies): " + err.Error())
	}
}

// ParseUserGroupRatios parses the per-user exclusive group ratio JSON
// (e.g. `{"vip":0.8}`). Returns nil for empty/invalid input so callers
// silently degrade to the global ratio logic. Null amounts are skipped (see
// DecodeUserGroupRatios), so those groups fall back to configured pricing.
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
	m, nullGroups, err := DecodeUserGroupRatios(jsonStr)
	if err != nil {
		// Not memoized: a corrupt value is repaired by an edit and must not
		// hold a cap slot until evicted. The log is rate limited instead.
		logCorruptUserGroupRatios(err)
		return nil
	}
	if len(nullGroups) > 0 {
		logNullUserGroupRatios(nullGroups)
	}
	if len(m) == 0 {
		if len(nullGroups) == 0 {
			return nil // "{ }", "null": cheap to decode, not worth a cap slot
		}
		// All entries null: memoized as nil, otherwise every relay request of the
		// user would decode it again.
		m = nil
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

// FilterUserGroupRatios removes every entry of a user's raw group_ratios JSON
// for which drop reports true, and re-serializes what is left. It returns the
// cleaned JSON, the removed group names (sorted, for stable logging) and
// whether anything actually changed.
//
// Unlike ParseUserGroupRatios this deliberately bypasses the memo table: the
// callers are cleanup paths whose input strings are about to disappear, so
// caching them would only evict live entries.
func FilterUserGroupRatios(raw string, drop func(group string) bool) (string, []string, bool, error) {
	if raw == "" || raw == "{}" || drop == nil {
		return raw, nil, false, nil
	}
	// Null amounts never take effect; re-serializing them through a float64 map
	// would turn them into explicit free-usage zeros, so they are dropped with
	// whatever else gets removed.
	ratios, _, err := DecodeUserGroupRatios(raw)
	if err != nil {
		return raw, nil, false, err
	}
	var removed []string
	for name := range ratios {
		if drop(name) {
			removed = append(removed, name)
		}
	}
	if len(removed) == 0 {
		return raw, nil, false, nil
	}
	sort.Strings(removed)
	for _, name := range removed {
		delete(ratios, name)
	}
	cleaned, err := common.Marshal(ratios)
	if err != nil {
		return raw, nil, false, err
	}
	return string(cleaned), removed, true, nil
}
