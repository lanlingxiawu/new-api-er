package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExportChannelOptions(t *testing.T) {
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
				t.Cleanup(func() {
					model.DB = previous
					sqlDB, err := db.DB()
					require.NoError(t, err)
					require.NoError(t, sqlDB.Close())
				})
			}
			// A channel must be searchable even without any logs or a valid time range.
			previousLogDB := model.LOG_DB
			model.LOG_DB = nil
			t.Cleanup(func() { model.LOG_DB = previousLogDB })
			prefix := fmt.Sprintf("export-picker-%d", nextTestID())
			channels := make([]*model.Channel, 51)
			for i := range channels {
				channels[i] = mkChannel(t, func(ch *model.Channel) {
					ch.Name = fmt.Sprintf("%s-%02d", prefix, i)
					ch.Status = common.ChannelStatusManuallyDisabled
				})
			}
			special := mkChannel(t, func(ch *model.Channel) { ch.Name = prefix + "-A_100%!中文" })
			id := strconv.Itoa(channels[0].Id)
			numericName := mkChannel(t, func(ch *model.Channel) { ch.Name = id + "-numeric-name" })
			deleted := mkChannel(t, nil)
			require.NoError(t, model.DB.Delete(deleted).Error)
			deletedID := strconv.Itoa(deleted.Id)
			for _, tc := range []struct {
				name, keyword, cursor string
				want                  []model.LogExportFilterOption
				next                  string
			}{
				{"name without logs", channels[0].Name, "", []model.LogExportFilterOption{{Value: id, Label: channels[0].Name}}, ""},
				{"ID before name matches", id, "", []model.LogExportFilterOption{{Value: id, Label: channels[0].Name}, {Value: strconv.Itoa(numericName.Id), Label: numericName.Name}}, ""},
				{"hash ID", " #" + id + " ", "", []model.LogExportFilterOption{{Value: id, Label: channels[0].Name}}, ""},
				{"literal case insensitive name", strings.ToUpper(special.Name), "", []model.LogExportFilterOption{{Value: strconv.Itoa(special.Id), Label: special.Name}}, ""},
				{"wildcards are literal", prefix + "-A%", "", []model.LogExportFilterOption{}, ""},
				{"historical ID", deletedID, "", []model.LogExportFilterOption{{Value: deletedID, Label: deletedID}}, ""},
				{"last page", prefix, strconv.Itoa(channels[49].Id), []model.LogExportFilterOption{{Value: strconv.Itoa(channels[50].Id), Label: channels[50].Name}, {Value: strconv.Itoa(special.Id), Label: special.Name}}, ""},
				{"past last page", prefix, strconv.Itoa(special.Id), []model.LogExportFilterOption{}, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/options?field=channel&keyword="+url.QueryEscape(tc.keyword)+"&cursor="+tc.cursor, nil)
					GetLogExportFilterOptions(ctx)
					response := decodeResp(t, rec)
					require.True(t, response.Success, response.Message)
					var data struct {
						Items []model.LogExportFilterOption
						Next  string `json:"next_cursor"`
					}
					require.NoError(t, common.Unmarshal(response.Data, &data))
					require.Equal(t, tc.want, data.Items)
					require.Equal(t, tc.next, data.Next)
					require.NotContains(t, rec.Body.String(), `"key"`)
					require.NotContains(t, rec.Body.String(), `"base_url"`)
				})
			}
			ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/options?field=channel&keyword="+prefix, nil)
			GetLogExportFilterOptions(ctx)
			response := decodeResp(t, rec)
			require.True(t, response.Success, response.Message)
			var page struct {
				Items []model.LogExportFilterOption
				Next  string `json:"next_cursor"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &page))
			require.Len(t, page.Items, 50)
			require.Equal(t, strconv.Itoa(channels[49].Id), page.Next)
			for i, item := range page.Items {
				require.Equal(t, strconv.Itoa(channels[i].Id), item.Value)
			}
			for _, query := range []string{"cursor=-1", "cursor=not-a-cursor", "keyword=" + strings.Repeat("x", 129)} {
				ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/options?field=channel&"+query, nil)
				GetLogExportFilterOptions(ctx)
				require.False(t, decodeResp(t, rec).Success)
			}
			ctx, rec = newCtx(t, http.MethodGet, "/api/log/export/options?field=channel&keyword="+prefix, nil)
			cancelled, cancel := context.WithCancel(ctx.Request.Context())
			cancel()
			ctx.Request = ctx.Request.WithContext(cancelled)
			GetLogExportFilterOptions(ctx)
			require.False(t, decodeResp(t, rec).Success)
			require.NotContains(t, rec.Body.String(), "context canceled")
			ctx, rec = newCtx(t, http.MethodGet, "/api/user/employee/export/options?field=channel&keyword="+prefix, nil)
			GetLogExportFilterOptions(ctx)
			require.Equal(t, http.StatusForbidden, rec.Code)
			var denied struct{ Success bool }
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &denied))
			require.False(t, denied.Success)
		})
	}
}

func TestExportUserOptionsSearchIdentity(t *testing.T) {
	employeeExportFilterTestDB(t)
	employee, customer, _ := employeeControllerExportFixture(t)
	other := mkUser(t, nil)
	var owned model.User
	require.NoError(t, model.DB.First(&owned, customer).Error)
	for _, tc := range []struct {
		path, keyword string
		want          int
	}{
		{"/api/log/export/options", strconv.Itoa(other.Id), other.Id},
		{"/api/log/export/options", other.Username, other.Id},
		{"/api/user/employee/export/options", strconv.Itoa(customer), customer},
		{"/api/user/employee/export/options", owned.Username, customer},
		{"/api/user/employee/export/options", strconv.Itoa(other.Id), 0},
		{"/api/user/employee/export/options", other.Username, 0},
		{"/api/log/export/options", "not-a-user-" + strconv.Itoa(other.Id), 0},
	} {
		t.Run(tc.path+"/"+tc.keyword, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, tc.path+"?field=customer&keyword="+url.QueryEscape(tc.keyword), nil)
			GetLogExportFilterOptions(asUser(ctx, employee))
			response := decodeResp(t, rec)
			require.True(t, response.Success)
			var data struct{ Items []model.LogExportFilterOption }
			require.NoError(t, common.Unmarshal(response.Data, &data))
			if tc.want == 0 {
				require.Empty(t, data.Items)
				return
			}
			require.Len(t, data.Items, 1)
			require.Equal(t, strconv.Itoa(tc.want), data.Items[0].Value)
		})
	}
}
