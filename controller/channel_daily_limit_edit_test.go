package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 单渠道编辑路径上「每日金额上限」的写入语义。
//
// 背景：Channel.Update() 走 DB.Updates(struct)，GORM 会跳过结构体零值，于是
// daily_quota_limit = 0（清除上限）与 daily_limit_recover_minutes = 0（回到按日模式）都会被
// 静默丢弃；Update() 也刻意不写限时恢复的两列。批量与按 Tag 两条路径早已改用 map 更新绕开
// 这个坑，applyChannelDailyLimitEdit 是给单渠道路径补的第三条。

func reloadTestChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	ch, err := model.GetChannelById(id, false)
	require.NoError(t, err)
	return ch
}

// TestApplyChannelDailyLimitEdit_ClearsLimit 核心回归：显式传 0 必须落库。
func TestApplyChannelDailyLimitEdit_ClearsLimit(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 500000
		c.DailyLimitRecoverMinutes = 30
	})

	patch := &PatchChannel{}
	patch.Channel = *ch
	patch.DailyQuotaLimit = 0

	require.NoError(t, applyChannelDailyLimitEdit(ch.Id, buildChannelDailyLimitEdit(patch, map[string]any{
		"daily_quota_limit": float64(0),
	})))

	got := reloadTestChannel(t, ch.Id)
	assert.EqualValues(t, 0, got.DailyQuotaLimit, "an explicit zero must clear the daily limit")
	assert.Equal(t, 30, got.DailyLimitRecoverMinutes, "an untouched field must keep its value")
}

// TestApplyChannelDailyLimitEdit_IgnoresAbsentFields 锁住「以 requestData 是否带该字段
// 为准」：没传的字段一律不动，否则任何一次无关的渠道保存都会把上限清零。
func TestApplyChannelDailyLimitEdit_IgnoresAbsentFields(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) { c.DailyQuotaLimit = 500000 })

	patch := &PatchChannel{}
	patch.Channel = *ch
	patch.DailyQuotaLimit = 0 // 客户端没传该字段时，解码后就是零值

	require.NoError(t, applyChannelDailyLimitEdit(ch.Id, buildChannelDailyLimitEdit(patch, map[string]any{
		"name": "unrelated edit",
	})))

	assert.EqualValues(t, 500000, reloadTestChannel(t, ch.Id).DailyQuotaLimit,
		"a save that does not carry the field must leave the limit alone")
}

// 按日模式 + 关闭自动恢复：auto_recover = 0 与 recover_minutes = 0 都是「零值有意义」的取值。
func TestApplyChannelDailyLimitEdit_WritesDayModeWithoutAutoRecover(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 100
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 1_000
	})

	off := 0
	patch := &PatchChannel{}
	patch.Channel = *ch
	patch.DailyQuotaLimit = 900000
	patch.DailyLimitAutoRecover = &off
	patch.DailyLimitRecoverMinutes = 0

	require.NoError(t, applyChannelDailyLimitEdit(ch.Id, buildChannelDailyLimitEdit(patch, map[string]any{
		"daily_quota_limit":           float64(900000),
		"daily_limit_auto_recover":    float64(0),
		"daily_limit_recover_minutes": float64(0),
	})))

	got := reloadTestChannel(t, ch.Id)
	assert.EqualValues(t, 900000, got.DailyQuotaLimit)
	assert.Zero(t, got.DailyLimitRecoverMinutes, "switching back to day mode must persist the zero")
	assert.False(t, got.GetDailyLimitAutoRecover(), "auto recover must be turned off")
}

// 限时模式：隐含自动恢复；原样回传同一个间隔不重置本轮，改间隔才开启新一轮。
func TestApplyChannelDailyLimitEdit_TimedModeKeepsRoundUnlessIntervalChanges(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 900000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 1_000
	})

	off := 0
	patch := &PatchChannel{}
	patch.Channel = *ch
	patch.DailyLimitAutoRecover = &off
	patch.DailyLimitRecoverMinutes = 30
	echo := map[string]any{"daily_limit_auto_recover": float64(0), "daily_limit_recover_minutes": float64(30)}
	require.NoError(t, applyChannelDailyLimitEdit(ch.Id, buildChannelDailyLimitEdit(patch, echo)))
	got := reloadTestChannel(t, ch.Id)
	assert.EqualValues(t, 1_000, got.DailyLimitPeriodStart, "an echoed interval must keep the current round")
	assert.True(t, got.GetDailyLimitAutoRecover(), "timed mode implies auto recovery")

	before := common.GetTimestamp()
	patch.DailyLimitRecoverMinutes = 45
	require.NoError(t, applyChannelDailyLimitEdit(ch.Id, buildChannelDailyLimitEdit(patch, map[string]any{
		"daily_limit_recover_minutes": float64(45),
	})))
	got = reloadTestChannel(t, ch.Id)
	assert.Equal(t, 45, got.DailyLimitRecoverMinutes)
	assert.GreaterOrEqual(t, got.DailyLimitPeriodStart, before, "a changed interval starts a new round")
}

// TestUpdateChannel_ClearsDailyLimitEndToEnd 走完整的 PUT /api/channel/ 处理函数，
// 把 applyChannelDailyLimitEdit 钉在调用点上。
func TestUpdateChannel_ClearsDailyLimitEndToEnd(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) { c.DailyQuotaLimit = 500000 })

	// 与前端编辑抽屉一致：清空金额后仍然带上 daily_quota_limit: 0 提交。
	body := fmt.Sprintf(
		`{"id":%d,"name":%q,"type":%d,"key":%q,"models":%q,"group":%q,"daily_quota_limit":0}`,
		ch.Id, ch.Name, ch.Type, ch.Key, ch.Models, ch.Group,
	)
	// 每日上限是敏感字段（ChannelSensitiveWrite），普通 admin 会被拒；用 root 提交。
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", body)
	asRoot(ctx, 1)
	UpdateChannel(ctx)

	require.True(t, decodeResp(t, rec).Success, "update must succeed: %s", rec.Body.String())
	assert.EqualValues(t, 0, reloadTestChannel(t, ch.Id).DailyQuotaLimit,
		"clearing the daily limit in the edit drawer must persist")
}

// 编辑抽屉保存无关字段时会原样回传恢复间隔与（只读的）轮次：本轮不能因此被重置，
// 客户端传来的 period_start 也不能被写进去。
func TestUpdateChannel_UnrelatedSaveKeepsCurrentRound(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 900000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 1_000
	})

	body := fmt.Sprintf(
		`{"id":%d,"name":%q,"type":%d,"key":%q,"models":%q,"group":%q,`+
			`"daily_quota_limit":900000,"daily_limit_auto_recover":1,"daily_limit_recover_minutes":30,"daily_limit_period_start":42}`,
		ch.Id, ch.Name+"-renamed", ch.Type, ch.Key, ch.Models, ch.Group,
	)
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", body)
	asRoot(ctx, 1)
	UpdateChannel(ctx)

	require.True(t, decodeResp(t, rec).Success, "update must succeed: %s", rec.Body.String())
	got := reloadTestChannel(t, ch.Id)
	assert.Equal(t, ch.Name+"-renamed", got.Name)
	assert.Equal(t, 30, got.DailyLimitRecoverMinutes)
	assert.EqualValues(t, 1_000, got.DailyLimitPeriodStart, "an unrelated save must not reset or forge the round")
}

// 每日上限的校验报错必须是翻译过的文案，不能是裸 i18n key（Rule 9 / Rule 13）。

// rawDailyLimitKeys 是绝不允许出现在响应里的裸键名。
var rawDailyLimitKeys = []string{
	"channel.daily_limit.invalid_amount",
	"channel.daily_limit.invalid_recover_minutes",
}

func assertNoRawI18nKey(t *testing.T, body string) {
	t.Helper()
	for _, key := range rawDailyLimitKeys {
		assert.NotContains(t, body, key, "response must not leak the raw i18n key")
	}
}

func TestAddChannel_DailyLimitErrorIsTranslated(t *testing.T) {
	requireDB(t)
	// 100 quota 远低于一分钱下限，必然触发 invalid_amount。
	body := `{"mode":"single","channel":{"name":"zz-i18n-add","type":1,"key":"k",
	  "models":"m","group":"default","daily_quota_limit":100}}`
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/channel/", body)
	asRoot(ctx, 1)
	AddChannel(ctx)

	resp := decodeResp(t, rec)
	require.False(t, resp.Success, "a below-minimum limit must be rejected")
	assertNoRawI18nKey(t, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "channel setting",
		"a daily-limit error must not be reported as a settings-JSON format error")
}

func TestUpdateChannel_DailyLimitErrorIsTranslated(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) { c.DailyQuotaLimit = 1_000_000 })

	body := fmt.Sprintf(
		`{"id":%d,"name":%q,"type":%d,"key":%q,"models":%q,"group":%q,"daily_quota_limit":100}`,
		ch.Id, ch.Name, ch.Type, ch.Key, ch.Models, ch.Group,
	)
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", body)
	asRoot(ctx, 1)
	UpdateChannel(ctx)

	resp := decodeResp(t, rec)
	require.False(t, resp.Success, "a below-minimum limit must be rejected")
	assertNoRawI18nKey(t, rec.Body.String())
	// 被拒绝的编辑不得改动原有配置。
	assert.EqualValues(t, 1_000_000, reloadTestChannel(t, ch.Id).DailyQuotaLimit,
		"a rejected edit must leave the stored limit untouched")
}

func TestUpdateChannel_InvalidRecoverMinutesErrorIsTranslated(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *model.Channel) { c.DailyQuotaLimit = 1_000_000 })

	body := fmt.Sprintf(
		`{"id":%d,"name":%q,"type":%d,"key":%q,"models":%q,"group":%q,"daily_limit_recover_minutes":%d}`,
		ch.Id, ch.Name, ch.Type, ch.Key, ch.Models, ch.Group, model.MaxDailyLimitRecoverMinutes+1,
	)
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", body)
	asRoot(ctx, 1)
	UpdateChannel(ctx)

	require.False(t, decodeResp(t, rec).Success)
	assertNoRawI18nKey(t, rec.Body.String())
	assert.Zero(t, reloadTestChannel(t, ch.Id).DailyLimitRecoverMinutes)
}

// 恢复间隔是金额管控的一部分，改动需要 ChannelSensitiveWrite；原样回传不算修改。
// 轮次由服务端维护、统计口径已停用，两者都按只读忽略——旧客户端回传 daily_limit_basis
// 不能让只有普通渠道写权限的管理员连改个名字都被拒。
func TestChannelAuthz_DailyLimitFieldClassification(t *testing.T) {
	origin := &model.Channel{DailyLimitRecoverMinutes: 30, DailyLimitPeriodStart: 1_000}

	patch := &PatchChannel{}
	patch.DailyLimitRecoverMinutes = 30
	assert.False(t, channelHasSensitiveChanges(patch, origin, map[string]any{"daily_limit_recover_minutes": float64(30)}),
		"an echoed interval is not a change")

	patch.DailyLimitRecoverMinutes = 60
	assert.True(t, channelHasSensitiveChanges(patch, origin, map[string]any{"daily_limit_recover_minutes": float64(60)}),
		"changing the interval is a sensitive change")

	readOnly := &PatchChannel{}
	readOnly.DailyLimitPeriodStart = 42
	request := map[string]any{"daily_limit_period_start": float64(42), "daily_limit_basis": "cost"}
	clearChannelReadOnlyFields(readOnly, request)
	assert.Zero(t, readOnly.DailyLimitPeriodStart, "the round is server-managed")
	assert.False(t, channelHasSensitiveChanges(readOnly, origin, request),
		"read-only daily-limit fields must not count as sensitive changes")
}

// 列表回填：限时渠道带本轮用量与预计恢复时刻，按日渠道只有今日用量。
func TestFillChannelDailyUsage_TimedChannelGetsRoundUsage(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_900_000)
	disabledAt := int64(1_701_950_000)
	timed := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 1_000_000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 8_000
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyLimitDisabledAt = disabledAt
	})
	daily := mkChannel(t, func(c *model.Channel) { c.DailyQuotaLimit = 1_000_000 })
	t.Cleanup(func() {
		_, _ = model.DeleteChannelLimitPeriodUsageByChannelIds([]int{timed.Id})
		_, _ = model.DeleteChannelDailyUsageByChannelIds([]int{timed.Id, daily.Id})
	})
	_, err := model.UpsertChannelDailyUsage([]model.ChannelDailyDelta{
		{StatDate: statDate, ChannelId: timed.Id, CostQuota: 700},
		{StatDate: statDate, ChannelId: daily.Id, CostQuota: 300},
	})
	require.NoError(t, err)
	_, err = model.UpsertChannelLimitPeriodUsage([]model.ChannelPeriodDelta{
		{ChannelId: timed.Id, PeriodStart: 8_000, CostQuota: 500},
	})
	require.NoError(t, err)

	channels := []*model.Channel{reloadTestChannel(t, timed.Id), reloadTestChannel(t, daily.Id)}
	fillChannelDailyUsage(channels, statDate)

	timedView := channels[0].DailyUsage
	require.NotNil(t, timedView)
	assert.EqualValues(t, 700, timedView.CostQuota)
	assert.Equal(t, 30, timedView.RecoverMinutes)
	assert.EqualValues(t, 8_000, timedView.PeriodStart)
	assert.EqualValues(t, 500, timedView.PeriodCostQuota)
	assert.EqualValues(t, disabledAt+30*60, timedView.RecoverAt)

	dailyView := channels[1].DailyUsage
	require.NotNil(t, dailyView)
	assert.EqualValues(t, 300, dailyView.CostQuota)
	assert.Zero(t, dailyView.RecoverMinutes)
	assert.Zero(t, dailyView.PeriodCostQuota)
	assert.Zero(t, dailyView.RecoverAt)
	assert.Zero(t, dailyView.DisabledAt)
	assert.EqualValues(t, disabledAt, timedView.DisabledAt, "the view mirrors the channel's own disable mark")
}

// 渠道恢复后，列表不能继续显示「已达上限」：禁用时刻取自渠道上的标记列（恢复时清零），
// 而不是当天用量行（写入后不会清）。
func TestFillChannelDailyUsage_RecoveredChannelIsNotShownAsReached(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_990_000)
	ch := mkChannel(t, func(c *model.Channel) {
		c.DailyQuotaLimit = 1_000_000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 9_000
	})
	t.Cleanup(func() { _, _ = model.DeleteChannelDailyUsageByChannelIds([]int{ch.Id}) })
	_, err := model.UpsertChannelDailyUsage([]model.ChannelDailyDelta{{StatDate: statDate, ChannelId: ch.Id, CostQuota: 1_200_000}})
	require.NoError(t, err)

	ok, err := model.DisableChannelForTimedLimit(ch.Id, 9_000, statDate, "round limit")
	require.NoError(t, err)
	require.True(t, ok)
	disabled := []*model.Channel{reloadTestChannel(t, ch.Id)}
	fillChannelDailyUsage(disabled, statDate)
	assert.Positive(t, disabled[0].DailyUsage.DisabledAt)

	due := disabled[0].DailyLimitDisabledAt + 30*60
	ok, err = model.RecoverChannelFromTimedLimit(ch.Id, due)
	require.NoError(t, err)
	require.True(t, ok)
	recovered := []*model.Channel{reloadTestChannel(t, ch.Id)}
	fillChannelDailyUsage(recovered, statDate)
	assert.Zero(t, recovered[0].DailyUsage.DisabledAt, "a recovered channel must not stay flagged as limit reached")
	assert.Zero(t, recovered[0].DailyUsage.RecoverAt)
}
