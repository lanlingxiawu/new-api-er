package cachex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntCodec(t *testing.T) {
	c := IntCodec{}

	s, err := c.Encode(42)
	require.NoError(t, err)
	assert.Equal(t, "42", s)

	v, err := c.Decode("42")
	require.NoError(t, err)
	assert.Equal(t, 42, v)

	v, err = c.Decode("  7  ")
	require.NoError(t, err)
	assert.Equal(t, 7, v, "surrounding whitespace trimmed")

	_, err = c.Decode("")
	assert.Error(t, err, "empty string is not a valid int")

	_, err = c.Decode("not-a-number")
	assert.Error(t, err)
}

func TestStringCodec(t *testing.T) {
	c := StringCodec{}

	s, err := c.Encode("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", s)

	v, err := c.Decode("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", v)

	// StringCodec is identity: empty is a valid value, not an error.
	v, err = c.Decode("")
	require.NoError(t, err)
	assert.Equal(t, "", v)
}

type codecPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestJSONCodec_RoundTrip(t *testing.T) {
	c := JSONCodec[codecPayload]{}
	in := codecPayload{Name: "x", Count: 3}

	s, err := c.Encode(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"x","count":3}`, s)

	out, err := c.Decode(s)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestJSONCodec_DecodeErrors(t *testing.T) {
	c := JSONCodec[codecPayload]{}

	_, err := c.Decode("")
	assert.Error(t, err, "empty json value rejected")

	_, err = c.Decode("   ")
	assert.Error(t, err, "whitespace-only json value rejected")

	_, err = c.Decode("{not valid json")
	assert.Error(t, err)
}

func TestJSONCodec_EncodeError(t *testing.T) {
	// A map with a channel value cannot be marshaled to JSON.
	c := JSONCodec[map[string]any]{}
	_, err := c.Encode(map[string]any{"bad": make(chan int)})
	assert.Error(t, err)
}
