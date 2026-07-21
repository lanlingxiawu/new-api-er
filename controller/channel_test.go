package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// validateChannel: path coverage across the add/update, model-length, VertexAI
// and Codex validation branches.
// ---------------------------------------------------------------------------

func TestValidateChannel_NilRejected(t *testing.T) {
	assert.Error(t, validateChannel(nil, true))
}

func TestValidateChannel_AddRequiresKey(t *testing.T) {
	ch := &model.Channel{Type: 1, Models: "gpt-4o", Group: "default"}
	assert.Error(t, validateChannel(ch, true), "add with empty key must fail")
	// update path does not require a key
	assert.NoError(t, validateChannel(ch, false))
}

func TestValidateChannel_ModelNameTooLong(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'm'
	}
	ch := &model.Channel{Type: 1, Key: "k", Models: string(long), Group: "default"}
	assert.Error(t, validateChannel(ch, true))
}

func TestValidateChannel_VertexAI(t *testing.T) {
	// missing region (Other empty)
	ch := &model.Channel{Type: constant.ChannelTypeVertexAi, Key: "k", Models: "gemini", Group: "default"}
	assert.Error(t, validateChannel(ch, true))

	// invalid JSON region
	ch.Other = "not-json"
	assert.Error(t, validateChannel(ch, true))

	// valid JSON but missing default field
	ch.Other = `{"region2":"us-east1"}`
	assert.Error(t, validateChannel(ch, true))

	// valid region map
	ch.Other = `{"default":"us-central1"}`
	assert.NoError(t, validateChannel(ch, true))
}

func TestValidateChannel_Codex(t *testing.T) {
	base := func() *model.Channel {
		return &model.Channel{Type: constant.ChannelTypeCodex, Models: "gpt", Group: "default"}
	}
	// non-JSON key
	ch := base()
	ch.Key = "plain-key"
	assert.Error(t, validateChannel(ch, true))

	// JSON missing access_token
	ch = base()
	ch.Key = `{"account_id":"acc"}`
	assert.Error(t, validateChannel(ch, true))

	// JSON missing account_id
	ch = base()
	ch.Key = `{"access_token":"tok"}`
	assert.Error(t, validateChannel(ch, true))

	// valid Codex key
	ch = base()
	ch.Key = `{"access_token":"tok","account_id":"acc"}`
	assert.NoError(t, validateChannel(ch, true))
}

func TestValidateChannel_NormalOK(t *testing.T) {
	ch := &model.Channel{Type: 1, Key: "sk-abc", Models: "gpt-4o", Group: "default"}
	assert.NoError(t, validateChannel(ch, true))
}

// ---------------------------------------------------------------------------
// getVertexArrayKeys: JSON array parsing, empty and invalid inputs.
// ---------------------------------------------------------------------------

func TestGetVertexArrayKeys(t *testing.T) {
	// empty input -> nil, no error
	keys, err := getVertexArrayKeys("")
	require.NoError(t, err)
	assert.Nil(t, keys)

	// array of string keys
	keys, err = getVertexArrayKeys(`["k1"," k2 "]`)
	require.NoError(t, err)
	assert.Equal(t, []string{"k1", "k2"}, keys)

	// array of object keys -> re-encoded JSON strings
	keys, err = getVertexArrayKeys(`[{"a":1}]`)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.JSONEq(t, `{"a":1}`, keys[0])

	// invalid JSON
	_, err = getVertexArrayKeys(`not-json`)
	assert.Error(t, err)

	// empty array -> error
	_, err = getVertexArrayKeys(`[]`)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetAllChannels: list response omits the key column (never leak channel keys).
// ---------------------------------------------------------------------------

func TestGetAllChannels_OmitsKey(t *testing.T) {
	requireDB(t)
	group := uniq("cg")
	ch := mkChannel(t, func(ch *model.Channel) {
		ch.Group = group
		ch.Key = "super-secret-channel-key"
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/?p=1&page_size=100&group="+group, nil)
	asAdmin(ctx, 1)
	GetAllChannels(ctx)

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var page struct {
		Items []model.Channel `json:"items"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &page))

	var found bool
	for _, item := range page.Items {
		if item.Id == ch.Id {
			found = true
			assert.Empty(t, item.Key, "channel key must be omitted from list response")
		}
	}
	assert.True(t, found, "seeded channel not present in filtered listing")
	assert.NotContains(t, rec.Body.String(), "super-secret-channel-key")
}

// ---------------------------------------------------------------------------
// UpdateChannel guard branches (return before touching the channel cache).
// ---------------------------------------------------------------------------

func TestUpdateChannel_RejectsStatusField(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", `{"id":1,"status":2}`)
	asAdmin(ctx, 1)
	UpdateChannel(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestUpdateChannel_MalformedBody(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/channel/", `{"id":`)
	asAdmin(ctx, 1)
	UpdateChannel(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}
