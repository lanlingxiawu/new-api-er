package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// task_polling.go — pure helpers.
// Rule 0: polling runs off the relay hot path (background worker).
// ===========================================================================

func TestPolling_TruncateBase64(t *testing.T) {
	assert.Equal(t, "short", truncateBase64("short"))
	long := strings.Repeat("a", 300)
	got := truncateBase64(long)
	assert.Equal(t, 256+3, len(got))
	assert.True(t, strings.HasSuffix(got, "..."))
}

func TestPolling_RedactVideoResponseBody(t *testing.T) {
	// Non-JSON returned unchanged.
	assert.Equal(t, []byte("not json"), redactVideoResponseBody([]byte("not json")))

	body := []byte(`{"response":{"bytesBase64Encoded":"secret","video":"` + strings.Repeat("x", 300) + `","videos":[{"bytesBase64Encoded":"s2","id":"v1"}]}}`)
	out := redactVideoResponseBody(body)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	resp := m["response"].(map[string]any)
	assert.NotContains(t, resp, "bytesBase64Encoded")
	assert.LessOrEqual(t, len(resp["video"].(string)), 259)
	vids := resp["videos"].([]any)
	assert.NotContains(t, vids[0].(map[string]any), "bytesBase64Encoded")
	assert.Equal(t, "v1", vids[0].(map[string]any)["id"])
}

func TestPolling_FormatPollingTaskLogID(t *testing.T) {
	assert.Equal(t, "", formatPollingTaskLogID(nil, nil))

	task := &model.Task{TaskID: "t-1"}
	// nil channel => plain task id.
	assert.Equal(t, "t-1", formatPollingTaskLogID(task, nil))
	// Non-SD2 channel => plain task id.
	assert.Equal(t, "t-1", formatPollingTaskLogID(task, &model.Channel{Type: 1}))
}
