package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type aliasTestConfig struct {
	Items  []string          `json:"items"`
	Limits *map[string]int   `json:"limits"`
	Tags   map[string]string `json:"tags"`
}

// 已发布快照是草稿的浅拷贝：切片与 map 必须换新，不能写进快照仍在引用的底层内存；
// 指针字段则原地更新，因为 types.RWMap 这类并发安全对象被其他代码持有同一指针。
func TestUpdateConfigFromMapReplacesSlicesAndMapsButUpdatesPointersInPlace(t *testing.T) {
	limits := map[string]int{"a": 1}
	draft := aliasTestConfig{
		Items:  make([]string, 2, 8),
		Limits: &limits,
		Tags:   map[string]string{"k": "v"},
	}
	draft.Items[0], draft.Items[1] = "old-0", "old-1"
	published := draft

	require.NoError(t, updateConfigFromMap(&draft, map[string]string{
		"items":  `["new-0","new-1","new-2"]`,
		"limits": `{"b":2}`,
		"tags":   `{"x":"y"}`,
	}))

	require.Equal(t, []string{"new-0", "new-1", "new-2"}, draft.Items)
	require.Equal(t, map[string]string{"x": "y"}, draft.Tags)
	require.Equal(t, []string{"old-0", "old-1"}, published.Items)
	require.Equal(t, "old-0", published.Items[:3][0], "backing array must not be reused")
	require.Equal(t, map[string]string{"k": "v"}, published.Tags)

	require.Same(t, published.Limits, draft.Limits, "pointer targets are updated in place")
	require.Equal(t, 2, (*draft.Limits)["b"])
}
