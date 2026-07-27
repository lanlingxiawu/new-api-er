package dto

import (
	"encoding/json"
	"reflect"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageRequest_Unmarshal_ExtraFields(t *testing.T) {
	raw := `{"model":"dall-e-3","prompt":"a cat","size":"1024x1024","custom_vendor_field":{"k":"v"},"another":1}`
	var r ImageRequest
	require.NoError(t, kitutil.Unmarshal([]byte(raw), &r))
	assert.Equal(t, "dall-e-3", r.Model)
	assert.Equal(t, "a cat", r.Prompt)
	assert.Equal(t, "1024x1024", r.Size)
	// unknown fields captured in Extra
	require.Contains(t, r.Extra, "custom_vendor_field")
	require.Contains(t, r.Extra, "another")
	// known fields NOT in Extra
	assert.NotContains(t, r.Extra, "model")
	assert.NotContains(t, r.Extra, "prompt")
}

func TestImageRequest_Unmarshal_Error(t *testing.T) {
	assert.Error(t, (&ImageRequest{}).UnmarshalJSON([]byte(`{`)))
	assert.Error(t, (&ImageRequest{}).UnmarshalJSON([]byte(`"not an object"`)))
}

func TestImageRequest_Marshal_DoesNotFlattenExtra(t *testing.T) {
	// per source: Extra must NOT be merged back on marshal
	r := ImageRequest{
		Model:  "dall-e-3",
		Prompt: "p",
		Extra:  map[string]json.RawMessage{"x": json.RawMessage(`1`)},
	}
	data, err := kitutil.Marshal(r)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, kitutil.Unmarshal(data, &m))
	assert.Contains(t, m, "model")
	assert.Contains(t, m, "prompt")
	assert.NotContains(t, m, "x", "Extra must not be flattened into output")
}

func TestImageRequest_Rule5_Pointers(t *testing.T) {
	// absent => nil
	var r ImageRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"m","prompt":"p"}`), &r))
	assert.Nil(t, r.N)
	assert.Nil(t, r.Stream)
	assert.Nil(t, r.Watermark)

	// explicit zero/false => non-nil
	var r2 ImageRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"m","prompt":"p","n":0,"stream":false,"watermark":false}`), &r2))
	require.NotNil(t, r2.N)
	assert.Equal(t, uint(0), *r2.N)
	require.NotNil(t, r2.Stream)
	require.NotNil(t, r2.Watermark)
}

func TestImageRequest_GetTokenCountMeta_DallE(t *testing.T) {
	tests := []struct {
		name      string
		req       ImageRequest
		wantRatio float64
	}{
		{"dall-e 256", ImageRequest{Model: "dall-e-2", Size: "256x256"}, 0.4},
		{"dall-e 512", ImageRequest{Model: "dall-e-2", Size: "512x512"}, 0.45},
		{"dall-e 1024", ImageRequest{Model: "dall-e-2", Size: "1024x1024"}, 1},
		{"dall-e tall", ImageRequest{Model: "dall-e-2", Size: "1024x1792"}, 2},
		{"dall-e wide", ImageRequest{Model: "dall-e-2", Size: "1792x1024"}, 2},
		{"dall-e-3 hd square", ImageRequest{Model: "dall-e-3", Size: "1024x1024", Quality: "hd"}, 1 * 2.0},
		{"dall-e-3 hd tall", ImageRequest{Model: "dall-e-3", Size: "1024x1792", Quality: "hd"}, 2 * 1.5},
		{"non dall-e", ImageRequest{Model: "gpt-image-1", Size: "256x256"}, 1.0},
		{"dall-e unknown size", ImageRequest{Model: "dall-e-2", Size: "999x999"}, 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := tc.req.GetTokenCountMeta()
			assert.Equal(t, tc.wantRatio, meta.ImagePriceRatio)
			assert.Equal(t, 1584, meta.MaxTokens)
			assert.Equal(t, tc.req.Prompt, meta.CombineText)
		})
	}
}

func TestImageRequest_GetTokenCountMeta_NCount(t *testing.T) {
	// nil N => 1
	meta := (&ImageRequest{Model: "dall-e-2"}).GetTokenCountMeta()
	assert.Equal(t, 1.0, meta.BillingRatios["n"])
	// explicit N
	n := uint(4)
	meta = (&ImageRequest{Model: "dall-e-2", N: &n}).GetTokenCountMeta()
	assert.Equal(t, 4.0, meta.BillingRatios["n"])
	// zero N => treated as 1
	zero := uint(0)
	meta = (&ImageRequest{Model: "dall-e-2", N: &zero}).GetTokenCountMeta()
	assert.Equal(t, 1.0, meta.BillingRatios["n"])
}

func TestImageRequest_IsStream(t *testing.T) {
	tr := true
	fa := false
	assert.False(t, (&ImageRequest{}).IsStream(nil))
	assert.True(t, (&ImageRequest{Stream: &tr}).IsStream(nil))
	assert.False(t, (&ImageRequest{Stream: &fa}).IsStream(nil))
}

func TestImageRequest_SetModelName(t *testing.T) {
	r := &ImageRequest{Model: "a"}
	r.SetModelName("")
	assert.Equal(t, "a", r.Model)
	r.SetModelName("b")
	assert.Equal(t, "b", r.Model)
}

func TestGetJSONFieldNames(t *testing.T) {
	type sample struct {
		A string `json:"a"`
		B int    `json:"b,omitempty"`
		C string `json:"-"`
		D string // no tag
		E string `json:"e"`
	}
	names := GetJSONFieldNames(reflect.TypeOf(sample{}))
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b") // omitempty stripped
	assert.Contains(t, names, "e")
	assert.NotContains(t, names, "c") // json:"-"
	assert.NotContains(t, names, "d") // no tag
	assert.NotContains(t, names, "")
}

func TestIndexComma(t *testing.T) {
	assert.Equal(t, -1, indexComma("abc"))
	assert.Equal(t, 3, indexComma("abc,omitempty"))
	assert.Equal(t, 0, indexComma(",x"))
}
