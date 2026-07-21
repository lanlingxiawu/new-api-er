package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveChats(t *testing.T) {
	t.Helper()
	orig := Chats
	t.Cleanup(func() { Chats = orig })
}

func TestUpdateChatsByJsonString_Valid(t *testing.T) {
	saveChats(t)
	err := UpdateChatsByJsonString(`[{"Name":"url"}]`)
	require.NoError(t, err)
	require.Len(t, Chats, 1)
	assert.Equal(t, "url", Chats[0]["Name"])
}

func TestUpdateChatsByJsonString_EmptyArray(t *testing.T) {
	saveChats(t)
	err := UpdateChatsByJsonString(`[]`)
	require.NoError(t, err)
	assert.Empty(t, Chats)
}

func TestUpdateChatsByJsonString_Invalid(t *testing.T) {
	saveChats(t)
	err := UpdateChatsByJsonString(`{bad`)
	require.Error(t, err)
	// Reset to empty slice happens before unmarshal.
	assert.Empty(t, Chats)
}

func TestChats2JsonString(t *testing.T) {
	saveChats(t)
	Chats = []map[string]string{{"A": "1"}, {"B": "2"}}
	assert.JSONEq(t, `[{"A":"1"},{"B":"2"}]`, Chats2JsonString())
}

func TestChats2JsonString_Empty(t *testing.T) {
	saveChats(t)
	Chats = []map[string]string{}
	assert.Equal(t, `[]`, Chats2JsonString())
}

func TestChats2JsonString_RoundTrip(t *testing.T) {
	saveChats(t)
	original := Chats2JsonString()
	require.NoError(t, UpdateChatsByJsonString(original))
	assert.Equal(t, original, Chats2JsonString())
}
