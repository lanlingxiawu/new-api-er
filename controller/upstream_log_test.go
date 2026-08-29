package controller

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAndValidateFilters(t *testing.T) {
	// happy path with trimming
	f, ok := normalizeAndValidateFilters(upstreamLogFiltersDTO{
		Username:  "  alice  ",
		RequestId: " req_1 ",
		Type:      2,
	})
	require.True(t, ok)
	assert.Equal(t, "alice", f.Username)
	assert.Equal(t, "req_1", f.RequestId)

	// control characters rejected
	_, ok = normalizeAndValidateFilters(upstreamLogFiltersDTO{RequestId: "bad\x00id"})
	assert.False(t, ok)

	// over-length request id rejected (129 chars)
	_, ok = normalizeAndValidateFilters(upstreamLogFiltersDTO{RequestId: strings.Repeat("a", 129)})
	assert.False(t, ok)

	// exactly 128 accepted
	_, ok = normalizeAndValidateFilters(upstreamLogFiltersDTO{RequestId: strings.Repeat("a", 128)})
	assert.True(t, ok)

	// start > end rejected
	_, ok = normalizeAndValidateFilters(upstreamLogFiltersDTO{StartTimestamp: 200, EndTimestamp: 100})
	assert.False(t, ok)

	// negative log id rejected
	_, ok = normalizeAndValidateFilters(upstreamLogFiltersDTO{LogId: -1})
	assert.False(t, ok)
}

func TestNormalizeLocalRequestId(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{name: "empty", input: "", ok: false},
		{name: "whitespace", input: "   ", ok: false},
		{name: "trimmed", input: "  req-local  ", want: "req-local", ok: true},
		{name: "one character", input: "a", want: "a", ok: true},
		{name: "64 characters", input: strings.Repeat("a", 64), want: strings.Repeat("a", 64), ok: true},
		{name: "65 characters", input: strings.Repeat("a", 65), ok: false},
		{name: "control character", input: "req\nlocal", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeLocalRequestId(tt.input)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func intPtr(i int) *int { return &i }

func TestSelectChannelKey_SingleKey(t *testing.T) {
	ch := &model.Channel{Type: constant.ChannelTypeNewAPI, Key: "sk-single"}

	key, idx, isMulti, errKey := selectChannelKey(ch, nil)
	assert.Empty(t, errKey)
	assert.Equal(t, "sk-single", key)
	assert.Equal(t, 0, idx)
	assert.False(t, isMulti)

	// key_index != 0 on single-key channel is invalid
	_, _, _, errKey = selectChannelKey(ch, intPtr(2))
	assert.Equal(t, i18n.MsgUpstreamLogKeyIndexInvalid, errKey)

	// empty key
	empty := &model.Channel{Type: constant.ChannelTypeNewAPI, Key: "  "}
	_, _, _, errKey = selectChannelKey(empty, nil)
	assert.Equal(t, i18n.MsgUpstreamLogKeyMissing, errKey)
}

func TestSelectChannelKey_MultiKey(t *testing.T) {
	ch := &model.Channel{Type: constant.ChannelTypeNewAPI, Key: "k0\nk1\nk2"}
	ch.ChannelInfo.IsMultiKey = true

	// missing index
	_, _, _, errKey := selectChannelKey(ch, nil)
	assert.Equal(t, i18n.MsgUpstreamLogKeyIndexRequired, errKey)

	// valid index
	key, idx, isMulti, errKey := selectChannelKey(ch, intPtr(1))
	assert.Empty(t, errKey)
	assert.Equal(t, "k1", key)
	assert.Equal(t, 1, idx)
	assert.True(t, isMulti)

	// out of range
	_, _, _, errKey = selectChannelKey(ch, intPtr(9))
	assert.Equal(t, i18n.MsgUpstreamLogKeyIndexInvalid, errKey)

	// negative
	_, _, _, errKey = selectChannelKey(ch, intPtr(-1))
	assert.Equal(t, i18n.MsgUpstreamLogKeyIndexInvalid, errKey)
}
