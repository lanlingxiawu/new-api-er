package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pickTwoDaysOtherThanToday 返回当月两个不等于今天的日期号。
// 取 1..28 保证任何月份都存在，避开 today 保证 checked_in_today 断言稳定。
func pickTwoDaysOtherThanToday(today int) [2]int {
	days := make([]int, 0, 2)
	for day := 1; day <= 28 && len(days) < 2; day++ {
		if day != today {
			days = append(days, day)
		}
	}
	return [2]int{days[0], days[1]}
}

// ---------------------------------------------------------------------------
// checkin.go — daily check-in reward, once-per-day guard, stats.
//
// UserCheckin mutates the GLOBAL checkin_setting struct (via GetCheckinSetting
// which returns a pointer). Every test that flips it snapshots the prior value
// and restores it via t.Cleanup so it never poisons sibling tests.
// Checkin rows use an autoIncrement id + a UNIQUE (user_id, checkin_date)
// constraint; each test uses a fresh factory user so days never collide, and
// cleans up all rows it created for that user.
// ---------------------------------------------------------------------------

// withCheckinSetting snapshots the global checkin setting, applies enabled +
// [min,max] for the duration of the test, and restores on cleanup.
func withCheckinSetting(t *testing.T, enabled bool, min, max int) {
	t.Helper()
	s := operation_setting.GetCheckinSetting()
	prev := *s
	s.Enabled = enabled
	s.MinQuota = min
	s.MaxQuota = max
	t.Cleanup(func() { *s = prev })
}

// cleanupCheckins hard-deletes every checkin row for a user after the test.
func cleanupCheckins(t *testing.T, userId int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", userId).Delete(&Checkin{})
		}
	})
}

func TestUserCheckin_DisabledGuard(t *testing.T) {
	requireDB(t)
	withCheckinSetting(t, false, 1000, 1000)
	u := mkUser(t, nil)

	got, err := UserCheckin(u.Id)
	assert.Nil(t, got)
	require.Error(t, err)
	assert.Equal(t, "签到功能未启用", err.Error())
}

func TestUserCheckin_HappyPathAndOncePerDay(t *testing.T) {
	requireDB(t)
	// Fixed reward: MinQuota == MaxQuota => deterministic award of exactly 1234.
	withCheckinSetting(t, true, 1234, 1234)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	cleanupCheckins(t, u.Id)

	// Not checked in yet.
	has, err := HasCheckedInToday(u.Id)
	require.NoError(t, err)
	assert.False(t, has)

	// First check-in credits exactly the fixed award.
	ci, err := UserCheckin(u.Id)
	require.NoError(t, err)
	require.NotNil(t, ci)
	assert.Equal(t, 1234, ci.QuotaAwarded)
	assert.Equal(t, time.Now().Format("2006-01-02"), ci.CheckinDate)
	assert.Greater(t, ci.Id, 0)

	// User quota increased by exactly the award (no double-credit).
	reloaded, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1234, reloaded.Quota)

	// Now flagged as checked-in today.
	has, err = HasCheckedInToday(u.Id)
	require.NoError(t, err)
	assert.True(t, has)

	// Second check-in same day is rejected by the once-per-day guard, and the
	// quota must NOT change (no second credit).
	ci2, err := UserCheckin(u.Id)
	assert.Nil(t, ci2)
	require.Error(t, err)
	assert.Equal(t, "今日已签到", err.Error())

	reloaded2, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1234, reloaded2.Quota) // unchanged
}

func TestUserCheckin_RandomRewardWithinRange(t *testing.T) {
	requireDB(t)
	withCheckinSetting(t, true, 100, 200)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	cleanupCheckins(t, u.Id)

	ci, err := UserCheckin(u.Id)
	require.NoError(t, err)
	require.NotNil(t, ci)
	// rand.Intn(max-min+1) => inclusive [min, max].
	assert.GreaterOrEqual(t, ci.QuotaAwarded, 100)
	assert.LessOrEqual(t, ci.QuotaAwarded, 200)

	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, ci.QuotaAwarded, reloaded.Quota)
}

func TestHasCheckedInToday_NoRows(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	has, err := HasCheckedInToday(u.Id)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestGetUserCheckinRecords(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	cleanupCheckins(t, u.Id)

	// Seed three explicit rows on distinct dates.
	rows := []Checkin{
		{UserId: u.Id, CheckinDate: "2025-01-01", QuotaAwarded: 10, CreatedAt: time.Now().Unix()},
		{UserId: u.Id, CheckinDate: "2025-01-05", QuotaAwarded: 20, CreatedAt: time.Now().Unix()},
		{UserId: u.Id, CheckinDate: "2025-02-01", QuotaAwarded: 30, CreatedAt: time.Now().Unix()},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
	}

	// Range that includes only January rows.
	jan, err := GetUserCheckinRecords(u.Id, "2025-01-01", "2025-01-31")
	require.NoError(t, err)
	require.Len(t, jan, 2)
	// Ordered by checkin_date DESC.
	assert.Equal(t, "2025-01-05", jan[0].CheckinDate)
	assert.Equal(t, "2025-01-01", jan[1].CheckinDate)

	// Empty range.
	none, err := GetUserCheckinRecords(u.Id, "2024-01-01", "2024-12-31")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestGetUserCheckinStats(t *testing.T) {
	requireDB(t)
	withCheckinSetting(t, true, 500, 500)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	cleanupCheckins(t, u.Id)

	now := time.Now()
	month := now.Format("2006-01")
	// Seed two rows in the current month, deliberately avoiding today: the test
	// asserts checked_in_today == false. Hardcoding "-02"/"-03" made this fail on
	// the 2nd and 3rd of every month.
	seedDays := pickTwoDaysOtherThanToday(now.Day())
	seed := []Checkin{
		{UserId: u.Id, CheckinDate: fmt.Sprintf("%s-%02d", month, seedDays[0]), QuotaAwarded: 500, CreatedAt: now.Unix()},
		{UserId: u.Id, CheckinDate: fmt.Sprintf("%s-%02d", month, seedDays[1]), QuotaAwarded: 700, CreatedAt: now.Unix()},
	}
	for i := range seed {
		require.NoError(t, DB.Create(&seed[i]).Error)
	}

	stats, err := GetUserCheckinStats(u.Id, month)
	require.NoError(t, err)

	assert.EqualValues(t, 1200, stats["total_quota"]) // 500 + 700
	assert.EqualValues(t, 2, stats["total_checkins"]) // all-time count
	assert.Equal(t, 2, stats["checkin_count"])        // this month
	assert.Equal(t, false, stats["checked_in_today"]) // no row for "today"
	records, ok := stats["records"].([]CheckinRecord)
	require.True(t, ok)
	assert.Len(t, records, 2)
}
