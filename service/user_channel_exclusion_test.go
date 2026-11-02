package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestUserChannelExclusionContextAlwaysApplies(t *testing.T) {
	for _, raw := range []string{"[2,4]", "", "[]", "invalid"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		user := model.UserBase{ExcludedChannelIDs: raw}
		user.WriteContext(c)
		first := GetChannelConstraints(c)
		second := GetChannelConstraints(c)
		require.Same(t, first, second)
		empty := raw == "" || raw == "[]"
		require.Equal(t, empty, CheckUserChannelExclusion(c, 2, "origin_task"))
		require.Equal(t, raw != "invalid", CheckUserChannelExclusion(c, 3, "origin_task"))
		if !empty {
			require.Len(t, first.Filters, 1)
			require.Equal(t, dto.FilterUserExcludedChannels, first.Filters[0].Kind)
		} else {
			require.Empty(t, first.Filters)
			require.Nil(t, UserChannelExclusionPolicy(c))
		}
	}
}

func TestUserChannelExclusionAutoGroupsAndRetryBudget(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const name = "exclusion-auto-model"
	createChannelSelectAutoGroupsChannel(t, db, 2391, "vip", name)
	createChannelSelectAutoGroupsChannel(t, db, 2392, "default", name)
	createChannelSelectAutoGroupsChannel(t, db, 2393, "outside-token-groups", name)
	model.InitChannelCache()
	for _, ids := range [][]int{{2391}, {2391, 2392}} {
		ctx, param := autoGroupRetryContext(t, name)
		ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		ctx.Set(dto.UserChannelExclusionContextKey, &dto.UserChannelExclusionPolicy{IDs: ids})
		param.CountAttempt()
		param.CountAttempt()
		selected, group, err := CacheGetRandomSatisfiedChannel(param)
		require.NoError(t, err)
		require.Equal(t, 2, param.TotalAttempts(), "local filtering must not consume or reset the request budget")
		if len(ids) == 1 {
			require.NotNil(t, selected)
			require.Equal(t, 2392, selected.Id)
			require.Equal(t, "default", group)
			require.Empty(t, UserChannelExclusionPolicy(ctx).FailureReason)
		} else {
			require.Nil(t, selected, "no fallback outside the token's authorized auto groups")
			require.Equal(t, "no_eligible_channel_after_user_exclusion", UserChannelExclusionPolicy(ctx).FailureReason)
		}
	}
}

func TestUserChannelExclusionCleanupWorker(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.UserChannelExclusion{}, &model.UserChannelExclusionCleanup{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	for _, count := range []int{0, 1, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			truncate(t)
			channelID := 973000 + count
			var userIDs []int
			for i := range count {
				u := model.User{Username: svcUniq("exclusion-worker"), AffCode: svcUniq("aff"), Password: "test", Quota: 700, ExcludedChannelIDs: fmt.Sprintf("[%d]", channelID)}
				require.NoError(t, model.DB.Create(&u).Error)
				userIDs = append(userIDs, u.Id)
				require.NoError(t, model.DB.Create(&model.UserChannelExclusion{UserID: u.Id, ChannelID: channelID}).Error)
				if i == 0 {
					require.NoError(t, model.DB.Delete(&u).Error)
				} // Include soft-deleted users.
			}
			require.NoError(t, model.DB.Create(&model.UserChannelExclusionCleanup{ChannelID: channelID, NextAttemptAt: -1}).Error)
			t.Cleanup(func() {
				model.DB.Where("channel_id = ?", channelID).Delete(&model.UserChannelExclusion{})
				model.DB.Where("channel_id = ?", channelID).Delete(&model.UserChannelExclusionCleanup{})
				if len(userIDs) > 0 {
					model.DB.Unscoped().Where("id IN ?", userIDs).Delete(&model.User{})
				}
			})
			handler := userChannelExclusionCleanupHandler{}
			require.True(t, handler.Enabled())
			for run := range 2 {
				task, err := model.CreateSystemTask(handler.Type(), nil, nil)
				require.NoError(t, err)
				claimed, ok, err := model.ClaimSystemTask(task.ID, handler.Type(), "exclusion-worker", time.Now().Add(time.Minute).Unix())
				require.NoError(t, err)
				require.True(t, ok)
				handler.Run(context.Background(), claimed, "exclusion-worker")
				stored, err := model.GetSystemTaskByTaskID(task.TaskID)
				require.NoError(t, err)
				require.Equal(t, model.SystemTaskStatusSucceeded, stored.Status, stored.Result)
				var remaining int64
				require.NoError(t, model.DB.Model(&model.UserChannelExclusion{}).Where("channel_id = ?", channelID).Count(&remaining).Error)
				if run == 0 {
					require.EqualValues(t, max(0, count-100), remaining)
				} else {
					require.Zero(t, remaining)
				}
			}
			if len(userIDs) > 0 {
				var users []model.User
				require.NoError(t, model.DB.Unscoped().Where("id IN ?", userIDs).Find(&users).Error)
				require.Len(t, users, count)
				for _, u := range users {
					require.Equal(t, "[]", u.ExcludedChannelIDs)
					require.Equal(t, 700, u.Quota)
				}
			}
			var queued int64
			require.NoError(t, model.DB.Model(&model.UserChannelExclusionCleanup{}).Where("channel_id = ?", channelID).Count(&queued).Error)
			require.Zero(t, queued)
		})
	}
}

func TestUserChannelExclusionCleanupWorkerRedisFailure(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserChannelExclusion{}, &model.UserChannelExclusionCleanup{}))
	u := model.User{Username: svcUniq("exclusion-retry"), AffCode: svcUniq("aff"), Password: "test", ExcludedChannelIDs: "[974001]"}
	require.NoError(t, model.DB.Create(&u).Error)
	require.NoError(t, model.DB.Create(&model.UserChannelExclusion{UserID: u.Id, ChannelID: 974001}).Error)
	require.NoError(t, model.DB.Create(&model.UserChannelExclusionCleanup{ChannelID: 974001, NextAttemptAt: -1}).Error)
	t.Cleanup(func() {
		model.DB.Unscoped().Delete(&u)
		model.DB.Where("channel_id = ?", 974001).Delete(&model.UserChannelExclusion{})
		model.DB.Where("channel_id = ?", 974001).Delete(&model.UserChannelExclusionCleanup{})
	})
	previous, enabled := common.RDB, common.RedisEnabled
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	require.NoError(t, broken.Close())
	common.RDB, common.RedisEnabled = broken, true
	t.Cleanup(func() { common.RDB, common.RedisEnabled = previous, enabled })
	handler := userChannelExclusionCleanupHandler{}
	task, err := model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, handler.Type(), "exclusion-retry", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	handler.Run(context.Background(), claimed, "exclusion-retry")
	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusFailed, stored.Status)
	require.Contains(t, stored.Result, "cache_publish")
	var queue model.UserChannelExclusionCleanup
	require.NoError(t, model.DB.First(&queue, "channel_id = ?", 974001).Error)
	require.Equal(t, &u.Id, queue.PendingUserID)
	require.Zero(t, queue.AfterUserID)
	require.Equal(t, 1, queue.Attempts)
	// Recover in a new claimed task, honoring the durable pending user.
	common.RDB, common.RedisEnabled = previous, false
	require.NoError(t, model.DB.Model(&queue).Update("next_attempt_at", -1).Error)
	task, err = model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	claimed, ok, err = model.ClaimSystemTask(task.ID, handler.Type(), "exclusion-restarted", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	handler.Run(context.Background(), claimed, "exclusion-restarted")
	stored, err = model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusSucceeded, stored.Status)
	var remaining int64
	require.NoError(t, model.DB.Model(&model.UserChannelExclusionCleanup{}).Where("channel_id = ?", 974001).Count(&remaining).Error)
	require.Zero(t, remaining)
}

func TestUserChannelExclusionFirstFailureLog(t *testing.T) {
	require.NotNil(t, model.LOG_DB)
	previous := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previous })
	requestID := fmt.Sprintf("exclusion-%d", time.Now().UnixNano())
	t.Cleanup(func() { model.LOG_DB.Where("request_id = ?", requestID).Delete(&model.Log{}) })
	model.StartRelayLogFlushLoop()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, requestID)
	c.Set(dto.UserChannelExclusionContextKey, &dto.UserChannelExclusionPolicy{
		IDs: []int{42}, RejectedIDs: []int{42}, RejectedCount: 1,
		FailureReason: "no_eligible_channel_after_user_exclusion", Phase: "selection",
	})
	for range 2 {
		RecordUserChannelRoutingRejection(c, 503, "model_not_found", "No available channel")
	}
	model.DrainRelayLogsSync(5 * time.Second)
	var rows []model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestID).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, model.LogTypeError, rows[0].Type)
	require.Zero(t, rows[0].ChannelId)
	require.Zero(t, rows[0].Quota)
	require.NotContains(t, rows[0].Content, "no_eligible_channel_after_user_exclusion")
	require.Contains(t, rows[0].Other, "no_eligible_channel_after_user_exclusion")
	require.Contains(t, rows[0].Other, "admin_info")
}

func TestUserChannelExclusionDiagnosticEmptySample(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(dto.UserChannelExclusionContextKey, &dto.UserChannelExclusionPolicy{Invalid: true, FailureReason: "user_channel_policy_unavailable", Phase: "selection"})
	other := model.NewLogOther()
	AppendUserChannelRoutingDiagnostic(c, other)
	var payload map[string]any
	require.NoError(t, common.UnmarshalJsonStr(other.JSONString(), &payload))
	diagnostic := payload["admin_info"].(map[string]any)["user_channel_routing"].(map[string]any)
	require.Equal(t, []any{}, diagnostic["channel_ids"], "the log detail UI expects an array even when no candidate was inspected")
}
