package operation_setting

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryStatusValidation(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"a":""}`, `{"a":"100,599"}`, `{"a":"429， 500 - 503,501"}`,
		`{"a":"` + strings.Repeat("500,", 127) + `500"}`,
		strings.Repeat(" ", 65534) + `{}`,
	} {
		require.NoError(t, ValidateGroupRetryStatusSetting(GroupRetryStatusSetting{Rules: raw}), raw)
	}
	for _, raw := range []string{
		``, `null`, `[]`, `{"a":null}`, `{"a":1}`, `{"a":true}`, `{"a":[]}`,
		`{"":"500"}`, `{"a":"99"}`, `{"a":"600"}`, `{"a":"503-500"}`,
		`{"a":"5xx"}`, `{"a":"500-501-502"}`,
		`{"a":"` + strings.Repeat("500,", 128) + `500"}`,
		strings.Repeat(" ", 65537),
	} {
		assert.ErrorIs(t, ValidateGroupRetryStatusSetting(GroupRetryStatusSetting{Rules: raw}), ErrGroupRetryStatusInvalid, raw)
	}
	groups := map[string]string{}
	for i := 0; i < 256; i++ {
		groups[fmt.Sprint(i)] = "500"
	}
	encoded, err := common.Marshal(groups)
	require.NoError(t, err)
	require.NoError(t, ValidateGroupRetryStatusSetting(GroupRetryStatusSetting{Rules: string(encoded)}))
	groups["extra"] = "500"
	encoded, err = common.Marshal(groups)
	require.NoError(t, err)
	require.Error(t, ValidateGroupRetryStatusSetting(GroupRetryStatusSetting{Rules: string(encoded)}))
}

func TestGroupRetryStatusSnapshot(t *testing.T) {
	old := GetGroupRetryStatusSetting()
	t.Cleanup(func() { ReplaceGroupRetryStatusSetting(old) })
	ReplaceGroupRetryStatusSetting(GroupRetryStatusSetting{Enabled: true, Rules: `{"a":"429,500-599","b":"","edges":"100,599"}`})
	for _, tc := range []struct {
		group               string
		code                int
		allowed, overridden bool
	}{
		{"a", 429, true, true}, {"a", 500, true, true}, {"a", 400, false, true},
		{"a", 504, false, true}, {"a", 524, false, true}, {"a", 200, false, true},
		{"b", 500, false, true}, {"missing", 500, false, false},
		{"edges", 100, true, true}, {"edges", 599, true, true}, {"edges", 99, false, true},
	} {
		allowed, overridden := GroupRetryStatusAllowed(tc.group, tc.code)
		assert.Equal(t, tc.allowed, allowed, "%+v", tc)
		assert.Equal(t, tc.overridden, overridden, "%+v", tc)
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap("group_retry_status_setting", map[string]string{"rules": `{"a":null}`}))
	allowed, found := GroupRetryStatusAllowed("a", 500)
	assert.True(t, allowed)
	assert.True(t, found, "invalid hot reload keeps the last valid snapshot")
	draft := GetGroupRetryStatusSetting()
	draft.Enabled = false
	ReplaceGroupRetryStatusSetting(draft)
	_, found = GroupRetryStatusAllowed("a", 500)
	assert.False(t, found)
}

func TestGroupRetryStatusConcurrentPublish(t *testing.T) {
	old := GetGroupRetryStatusSetting()
	t.Cleanup(func() { ReplaceGroupRetryStatusSetting(old) })
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				GroupRetryStatusAllowed("a", 500)
			}
		}()
	}
	for i := 0; i < 50; i++ {
		ReplaceGroupRetryStatusSetting(GroupRetryStatusSetting{Enabled: true, Rules: `{"a":"500"}`})
		ReplaceGroupRetryStatusSetting(GroupRetryStatusSetting{Enabled: true, Rules: `{"a":""}`})
	}
	wg.Wait()
}
