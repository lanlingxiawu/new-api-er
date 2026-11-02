package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserChannelExclusionUpdateContract(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	require.NoError(t, authz.Init(model.DB))
	require.NoError(t, model.DB.AutoMigrate(&model.UserChannelExclusion{}, &model.UserChannelExclusionCleanup{}))
	u := mkUser(t, nil)
	ch := mkChannel(t, nil)
	t.Cleanup(func() { model.DB.Where("user_id = ?", u.Id).Delete(&model.UserChannelExclusion{}) })
	for _, step := range []struct {
		name    string
		extra   map[string]any
		success bool
		want    string
	}{
		{"set", map[string]any{"excluded_channel_ids": []int{ch.Id, ch.Id}}, true, "set"},
		{"omitted", nil, true, "set"},
		{"null", map[string]any{"excluded_channel_ids": nil}, false, "set"},
		{"negative", map[string]any{"excluded_channel_ids": []int{-1}}, false, "set"},
		{"clear", map[string]any{"excluded_channel_ids": []int{}}, true, "[]"},
	} {
		t.Run(step.name, func(t *testing.T) {
			payload := map[string]any{"id": u.Id, "username": u.Username, "display_name": "test", "group": u.Group}
			for k, v := range step.extra {
				payload[k] = v
			}
			ctx, rec := newCtx(t, http.MethodPut, "/api/user/", payload)
			asAdmin(ctx, nextTestID())
			ctx.Set("role", common.RoleRootUser)
			UpdateUser(ctx)
			response := decodeResp(t, rec)
			require.Equal(t, step.success, response.Success, response.Message)
			var stored model.User
			require.NoError(t, model.DB.First(&stored, u.Id).Error)
			ids, err := model.ParseUserChannelExclusions(stored.ExcludedChannelIDs)
			require.NoError(t, err)
			if step.want == "set" {
				require.Equal(t, []int{ch.Id}, ids)
			} else {
				require.Empty(t, ids)
			}
		})
	}
}

// Run the same search contract on the configured real database and SQLite.
// Fixtures are row-scoped; no shared channels are changed or removed.
func TestUserChannelExclusionOptionsSearch(t *testing.T) {
	for _, dialect := range []string{"configured", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			if dialect == "configured" {
				requireDB(t)
			} else {
				previous := model.DB
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				require.NoError(t, db.AutoMigrate(&model.Channel{}))
				model.DB = db
				t.Cleanup(func() { model.DB = previous; sqlDB, _ := db.DB(); require.NoError(t, sqlDB.Close()) })
			}
			prefix := fmt.Sprintf("picker-%d", nextTestID())
			a := mkChannel(t, func(ch *model.Channel) { ch.Name = prefix + "-A_100%!中文"; ch.Status = 2 })
			b := mkChannel(t, func(ch *model.Channel) { ch.Name = prefix + "-B" })
			for _, tc := range []struct {
				keyword      string
				after, limit int
				want         []int
				more         bool
			}{
				{prefix, 0, 1, []int{a.Id}, true},
				{prefix, a.Id, 1, []int{b.Id}, false},
				{prefix, b.Id, 1, []int{}, false},
				{strings.ToUpper(prefix) + "-a_100%!中文", 0, 50, []int{a.Id}, false},
				{prefix + "-A%", 0, 50, []int{}, false},
				{fmt.Sprint(a.Id), 0, 50, []int{a.Id}, false},
				{fmt.Sprintf(" #%d ", b.Id), 0, 50, []int{b.Id}, false},
			} {
				c, rec := newCtx(t, http.MethodGet, fmt.Sprintf("/api/channel/exclusion-options?keyword=%s&after_id=%d&limit=%d", url.QueryEscape(tc.keyword), tc.after, tc.limit), nil)
				UserChannelExclusionOptions(c)
				result := decodeResp(t, rec)
				require.True(t, result.Success, result.Message)
				var data struct {
					Items []struct{ ID, Status int }
					More  bool `json:"has_more"`
				}
				require.NoError(t, common.Unmarshal(result.Data, &data))
				ids := make([]int, 0, len(data.Items))
				for _, item := range data.Items {
					ids = append(ids, item.ID)
					if item.ID == a.Id {
						require.Equal(t, 2, item.Status)
					}
				}
				require.Equal(t, tc.want, ids, tc.keyword)
				require.Equal(t, tc.more, data.More)
			}
			for _, query := range []string{"?ids=1&keyword=test", "?keyword=" + strings.Repeat("x", 129)} {
				c, rec := newCtx(t, http.MethodGet, "/api/channel/exclusion-options"+query, nil)
				UserChannelExclusionOptions(c)
				require.False(t, decodeResp(t, rec).Success)
			}
		})
	}
}

func TestUserChannelExclusionWebSocketFirstFailureLog(t *testing.T) {
	previousLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousLog
	})
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(*websocket.Conn, *http.Request) {
		require.Fail(t, "excluded request must never reach upstream")
	})
	require.NoError(t, model.DB.Model(fixture.user).Update("excluded_channel_ids", fmt.Sprintf("[%d]", fixture.channel.Id)).Error)
	require.NoError(t, model.PublishUserAuthCache(fixture.user.Id))
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"hi"}`)))
	response := readResponsesWSTestEvent(t, fixture.client)
	require.Equal(t, "error", response["type"])
	require.Equal(t, float64(http.StatusServiceUnavailable), response["status"])
	fixture.closeAndWait(t)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("type = ? AND token_id = ?", model.LogTypeError, fixture.token.Id).Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Zero(t, logs[0].ChannelId)
	require.Zero(t, logs[0].Quota)
	require.Contains(t, logs[0].Other, "no_eligible_channel_after_user_exclusion")
	require.NotContains(t, logs[0].Content, "user_channel")
	require.EqualValues(t, 0, fixture.connections.Load())
}

func TestUserChannelExclusionOptionsContract(t *testing.T) {
	requireDB(t)
	a := mkChannel(t, func(ch *model.Channel) { ch.Key = "secret-must-not-leak" })
	b := mkChannel(t, nil)
	for _, tc := range []struct {
		query   string
		success bool
	}{
		{fmt.Sprintf("?ids=%d,%d", a.Id, b.Id), true},
		{fmt.Sprintf("?after_id=%d&limit=1", a.Id-1), true},
		{"?limit=0", false}, {"?limit=101", false}, {"?after_id=-1", false},
		{"?ids=1&after_id=0", false}, {"?ids=null", false}, {"?ids=-1", false},
	} {
		c, rec := newCtx(t, http.MethodGet, "/api/channel/exclusion-options"+tc.query, nil)
		UserChannelExclusionOptions(c)
		result := decodeResp(t, rec)
		require.Equal(t, tc.success, result.Success, tc.query)
		require.NotContains(t, rec.Body.String(), "secret-must-not-leak")
		require.NotContains(t, rec.Body.String(), `"key"`)
		if tc.success {
			var data struct {
				Items []map[string]any `json:"items"`
				Next  int              `json:"next_after_id"`
				More  bool             `json:"has_more"`
			}
			require.NoError(t, common.Unmarshal(result.Data, &data))
			require.NotEmpty(t, data.Items)
			for _, item := range data.Items {
				require.Len(t, item, 4)
				require.Contains(t, item, "id")
				require.Contains(t, item, "name")
				require.Contains(t, item, "type")
				require.Contains(t, item, "status")
			}
			if tc.query == fmt.Sprintf("?after_id=%d&limit=1", a.Id-1) {
				require.Len(t, data.Items, 1)
				require.Equal(t, a.Id, data.Next)
				require.True(t, data.More)
			} else {
				require.Len(t, data.Items, 2)
			}
		}
	}
}
