package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFullScanChannelSearchPagination(t *testing.T) {
	group, keyword, tag := uniq("scan-group"), uniq("scan-name"), uniq("scan-tag")
	rows := make([]*model.Channel, 0, 6)
	for i := range 6 {
		rows = append(rows, mkChannel(t, func(ch *model.Channel) {
			ch.Group, ch.Name, ch.Tag = group, fmt.Sprintf("%s-%d", keyword, i), &tag
			ch.Type = 1 + i%2
			if i == 5 {
				ch.Status = common.ChannelStatusManuallyDisabled
			}
		}))
	}
	callback := "full_scan:channel_page_size"
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, fullRows := tx.Statement.Dest.(*[]*model.Channel); fullRows && !tx.DryRun {
			require.LessOrEqual(t, tx.RowsAffected, int64(2), "search must not load all full channel records before paging")
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
	for _, tagMode := range []bool{false, true} {
		for _, tc := range []struct {
			query  string
			total  int
			ids    []int
			counts map[string]int
		}{
			{"&p=2&page_size=2", 6, []int{rows[3].Id, rows[2].Id}, map[string]int{"1": 3, "2": 3}},
			{"&status=1&type=1&p=2&page_size=2", 3, []int{rows[0].Id}, map[string]int{"1": 3, "2": 2}},
			{"&status=0&p=1&page_size=2", 1, []int{rows[5].Id}, map[string]int{"2": 1}},
			{"&p=999999&page_size=2", 6, []int{}, map[string]int{"1": 3, "2": 3}},
		} {
			path := fmt.Sprintf("/api/channel/search?keyword=%s&group=%s&id_sort=true&tag_mode=%t%s", url.QueryEscape(keyword), url.QueryEscape(group), tagMode, tc.query)
			c, rec := newCtx(t, http.MethodGet, path, nil)
			c.Set("role", common.RoleRootUser)
			SearchChannels(c)
			response := decodeResp(t, rec)
			require.True(t, response.Success, response.Message)
			var result struct {
				Items      []model.Channel `json:"items"`
				Total      int             `json:"total"`
				TypeCounts map[string]int  `json:"type_counts"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &result))
			require.Equal(t, tc.total, result.Total)
			require.Equal(t, tc.counts, result.TypeCounts)
			ids := make([]int, 0, len(result.Items))
			for _, item := range result.Items {
				ids = append(ids, item.Id)
				require.Empty(t, item.Key)
			}
			require.Equal(t, tc.ids, ids, path)
		}
	}
}

func TestFullScanChannelSearchExpandsTagsAcrossPages(t *testing.T) {
	group, keyword := uniq("scan-group"), uniq("scan-match")
	for range 2 {
		tag := uniq("scan-tag")
		for i := range 3 {
			mkChannel(t, func(ch *model.Channel) {
				ch.Group, ch.Tag = group, &tag
				if i == 0 {
					ch.Name = keyword
				}
			})
		}
	}
	tags, err := model.SearchTags(keyword, group, "", true)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	var expected []int
	for _, tag := range tags {
		var ids []int
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("tag = ?", tag).Order("id desc").Pluck("id", &ids).Error)
		expected = append(expected, ids...)
	}
	c, rec := newCtx(t, http.MethodGet, fmt.Sprintf("/api/channel/search?keyword=%s&group=%s&tag_mode=true&id_sort=true&p=2&page_size=2", keyword, group), nil)
	c.Set("role", common.RoleRootUser)
	SearchChannels(c)
	response := decodeResp(t, rec)
	require.True(t, response.Success, response.Message)
	var result struct {
		Items []model.Channel `json:"items"`
		Total int             `json:"total"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &result))
	require.Equal(t, 6, result.Total)
	require.Len(t, result.Items, 2)
	require.Equal(t, expected[2:4], []int{result.Items[0].Id, result.Items[1].Id})
}
