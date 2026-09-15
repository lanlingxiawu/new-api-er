package model

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

// Cleanup of per-user exclusive group ratios after a pricing group is deleted
// or re-created. See docs/design/user-exclusive-group-ratio-deletion.md.
//
// The pacing below is a hard requirement, not a tuning knob: this scan shares
// the connection pool with the relay chain's user cache-miss reads, so it runs
// serially, one bounded primary-key window at a time, with a yield between
// windows. The goal is to stay out of relay's way, not to finish quickly.
const (
	// groupRatioCleanupWindow is how many users (by primary key) one step covers.
	// Windows are bounded by id rather than by "the next N matching rows": almost
	// no user has exclusive ratios, so filling a batch of matches would walk the
	// whole index in a single statement and the pause would never happen.
	groupRatioCleanupWindow = 5000
	groupRatioCleanupPause  = 50 * time.Millisecond
	// Only a sample of affected user ids is logged: a per-user log line would
	// itself slow the cleanup down when many rows match.
	groupRatioCleanupLogSample = 20
)

// Pending work is merged rather than queued: a burst of group edits must cost
// one table scan, not one scan per edit. A single worker drains the queue, so
// scans never overlap. Guarded by groupRatioQueueMu.
var (
	groupRatioQueueMu       sync.Mutex
	groupRatioQueuePending  map[string]struct{}
	groupRatioQueueRunning  map[string]struct{}
	groupRatioQueueDraining bool
)

type userGroupRatioRow struct {
	Id          int
	GroupRatios string
}

// ScheduleUserGroupRatioCleanup queues group names for removal from every
// user's group_ratios and returns immediately.
//
// Names are merged into a single pending set drained by one worker, so ten
// deletions in a row cost one pass over the users table instead of ten. Callers
// are admin request handlers; the work must not run on their goroutine.
func ScheduleUserGroupRatioCleanup(targets []string) {
	if len(targets) == 0 || DB == nil {
		return
	}
	groupRatioQueueMu.Lock()
	if groupRatioQueuePending == nil {
		groupRatioQueuePending = make(map[string]struct{}, len(targets))
	}
	for _, name := range targets {
		groupRatioQueuePending[name] = struct{}{}
	}
	if groupRatioQueueDraining {
		// A worker is already running and will pick these up on its next lap.
		groupRatioQueueMu.Unlock()
		return
	}
	groupRatioQueueDraining = true
	groupRatioQueueMu.Unlock()

	// The default pool, not relayGoPool: this is admin work and must not take
	// capacity from the relay chain.
	gopool.Go(drainUserGroupRatioCleanupQueue)
}

func drainUserGroupRatioCleanupQueue() {
	// Only the panic path clears the flag here. On the normal path it is
	// cleared while still holding the lock that proved the queue empty, so a
	// concurrent scheduler either sees "draining" and defers to this worker, or
	// sees "idle" and starts a new one — never both, and never neither.
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("panic in user group ratio cleanup queue: %v", r))
			groupRatioQueueMu.Lock()
			groupRatioQueueDraining = false
			groupRatioQueueRunning = nil
			groupRatioQueueMu.Unlock()
		}
	}()
	for {
		groupRatioQueueMu.Lock()
		batch := make([]string, 0, len(groupRatioQueuePending))
		for name := range groupRatioQueuePending {
			batch = append(batch, name)
		}
		groupRatioQueuePending = nil
		if len(batch) == 0 {
			groupRatioQueueDraining = false
			groupRatioQueueMu.Unlock()
			return
		}
		groupRatioQueueRunning = make(map[string]struct{}, len(batch))
		for _, name := range batch {
			groupRatioQueueRunning[name] = struct{}{}
		}
		groupRatioQueueMu.Unlock()

		sort.Strings(batch)
		CleanupUserGroupRatios(batch)

		groupRatioQueueMu.Lock()
		groupRatioQueueRunning = nil
		groupRatioQueueMu.Unlock()
	}
}

// GroupRatioCleanupsActive returns, sorted, the given group names whose cleanup
// is still queued or scanning.
//
// Saving a new exclusive ratio for such a group is refused (see UpdateUser): the
// scan may not have reached that user yet and would strip the new rule as a
// stale leftover. A cleanup normally finishes within about a minute.
func GroupRatioCleanupsActive(groups []string) []string {
	groupRatioQueueMu.Lock()
	defer groupRatioQueueMu.Unlock()
	var active []string
	for _, name := range groups {
		_, pending := groupRatioQueuePending[name]
		_, running := groupRatioQueueRunning[name]
		if pending || running {
			active = append(active, name)
		}
	}
	sort.Strings(active)
	return active
}

// GroupsWithEnabledChannels reports which of the given group names still have
// at least one enabled channel.
//
// It reads `channels` rather than the derived `abilities` table on purpose:
// abilities are rebuilt after a channel is saved, so a failed rebuild can leave
// them empty while the channel is still enabled and will start serving again
// after the next edit. The source table is the conservative answer.
func GroupsWithEnabledChannels(groups []string) ([]string, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	wanted := make(map[string]struct{}, len(groups))
	for _, name := range groups {
		wanted[name] = struct{}{}
	}
	var channels []*Channel
	// Select by struct field name, not by commonGroupCol: the latter arrives
	// already quoted for one dialect, while field names let GORM quote "group"
	// (a reserved word) correctly for MySQL, PostgreSQL and SQLite alike.
	if err := DB.Model(&Channel{}).
		Select([]string{"Id", "Group"}).
		Where("status = ?", common.ChannelStatusEnabled).
		Find(&channels).Error; err != nil {
		return nil, err
	}
	hit := make(map[string]struct{})
	for _, channel := range channels {
		for _, name := range channel.GetGroups() {
			if _, ok := wanted[name]; ok {
				hit[name] = struct{}{}
			}
		}
	}
	if len(hit) == 0 {
		return nil, nil
	}
	// Report in the caller's order so the error message is stable.
	blocked := make([]string, 0, len(hit))
	for _, name := range groups {
		if _, ok := hit[name]; ok {
			blocked = append(blocked, name)
			delete(hit, name)
		}
	}
	return blocked, nil
}

// CleanupUserGroupRatios removes the given group names from every user's
// group_ratios column.
//
// Best-effort by design: it stops on the first fatal query error and is not
// retried. Whatever is left behind is inert (the group no longer exists and,
// per the delete-time check, has no enabled channels) and is picked up later
// either when the same name is re-created or when the user is next edited.
//
// Callers should run this off the request goroutine.
func CleanupUserGroupRatios(targets []string) {
	if len(targets) == 0 || DB == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("panic in user group ratio cleanup: %v", r))
		}
	}()

	targetSet := make(map[string]struct{}, len(targets))
	for _, name := range targets {
		targetSet[name] = struct{}{}
	}
	drop := func(group string) bool {
		_, ok := targetSet[group]
		return ok
	}

	var (
		lastID  int
		scanned int
		cleaned int
		invalid int
		sample  []int
	)
	for {
		// Unscoped throughout: soft-deleted users keep their rules and would
		// carry them back if the account is ever restored.
		//
		// Upper bound of this window: walks at most groupRatioCleanupWindow
		// primary-key entries, however sparse the matching rows are. No bound
		// means this is the last (partial) window.
		var bounds []int
		if err := DB.Model(&User{}).Unscoped().
			Where("id > ?", lastID).
			Order("id").
			Offset(groupRatioCleanupWindow-1).
			Limit(1).
			Pluck("id", &bounds).Error; err != nil {
			common.SysError("user group ratio cleanup query failed: " + err.Error())
			break
		}
		query := DB.Model(&User{}).Unscoped().
			Select([]string{"id", "group_ratios"}).
			Where("id > ?", lastID).
			Where("group_ratios IS NOT NULL AND group_ratios <> ? AND group_ratios <> ?", "", "{}")
		if len(bounds) > 0 {
			query = query.Where("id <= ?", bounds[0])
		}
		var rows []userGroupRatioRow
		if err := query.Order("id").Find(&rows).Error; err != nil {
			common.SysError("user group ratio cleanup query failed: " + err.Error())
			break
		}
		for _, row := range rows {
			scanned++
			updated, _, changed, err := ratio_setting.FilterUserGroupRatios(row.GroupRatios, drop)
			if err != nil {
				// Corrupt JSON: never guess, never overwrite. Record and move on
				// so one bad row cannot block the rest of the cleanup.
				invalid++
				common.SysError(fmt.Sprintf("user group ratio cleanup skipped user %d: invalid group_ratios: %v", row.Id, err))
				continue
			}
			if !changed {
				continue
			}
			applied, err := casUpdateUserGroupRatios(row.Id, row.GroupRatios, updated)
			if err != nil {
				common.SysError(fmt.Sprintf("user group ratio cleanup failed for user %d: %v", row.Id, err))
				continue
			}
			if !applied {
				// Concurrently modified; leave it to the save-time self-heal.
				continue
			}
			// Relay reads group_ratios from the Redis user cache, so a DB-only write
			// would keep billing the removed ratio until the cache expires. Publish
			// is a field-level refresh that never touches the cached Quota (a DEL
			// would let a stale DB quota overwrite pending batched deductions).
			// Users with no cached copy are skipped: a later fill reads the cleaned row.
			if common.RedisEnabled && userCacheExists(row.Id) {
				if err := PublishUserAuthCache(row.Id); err != nil {
					common.SysError(fmt.Sprintf("user group ratio cleanup: failed to refresh cache for user %d: %v", row.Id, err))
				}
			}
			cleaned++
			if len(sample) < groupRatioCleanupLogSample {
				sample = append(sample, row.Id)
			}
		}
		if len(bounds) == 0 {
			break
		}
		lastID = bounds[0]
		time.Sleep(groupRatioCleanupPause)
	}

	common.SysLog(fmt.Sprintf(
		"user group ratio cleanup finished: targets=%v scanned=%d cleaned=%d invalid=%d sample=%v",
		targets, scanned, cleaned, invalid, sample,
	))
}

// casUpdateUserGroupRatios writes updated only if the row still holds expected.
//
// An admin may save the same user between the cleanup's read and its write; a
// blind update would silently discard the rules they just set. Reports whether
// the write landed.
func casUpdateUserGroupRatios(id int, expected string, updated string) (bool, error) {
	result := DB.Model(&User{}).Unscoped().
		Where("id = ? AND group_ratios = ?", id, expected).
		Update("group_ratios", updated)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
