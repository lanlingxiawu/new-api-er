package media

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The media package holds a process-global MediaResolver injected by the host.
// ResolveBase64Data / DecodeBase64FileData delegate to it, or error when the
// relevant function is not configured. Tests reset the resolver to the zero
// value afterwards so ordering cannot leak state.

func resetResolver() { SetMediaResolver(MediaResolver{}) }

func TestResolveBase64Data_NotConfigured(t *testing.T) {
	resetResolver()
	defer resetResolver()

	data, mime, err := ResolveBase64Data(nil, nil, "reason")
	require.Error(t, err)
	assert.Equal(t, "", data)
	assert.Equal(t, "", mime)
	assert.Contains(t, err.Error(), "not configured")
}

func TestResolveBase64Data_Delegates(t *testing.T) {
	defer resetResolver()

	var gotReason []string
	SetMediaResolver(MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			gotReason = reason
			return "ZGF0YQ==", "image/png", nil
		},
	})

	data, mime, err := ResolveBase64Data(nil, nil, "formatting image")
	require.NoError(t, err)
	assert.Equal(t, "ZGF0YQ==", data)
	assert.Equal(t, "image/png", mime)
	assert.Equal(t, []string{"formatting image"}, gotReason)
}

func TestResolveBase64Data_PropagatesError(t *testing.T) {
	defer resetResolver()

	SetMediaResolver(MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "", "", errors.New("boom")
		},
	})

	_, _, err := ResolveBase64Data(nil, nil)
	require.Error(t, err)
	assert.Equal(t, "boom", err.Error())
}

func TestDecodeBase64FileData_NotConfigured(t *testing.T) {
	resetResolver()
	defer resetResolver()

	// Only GetBase64Data set: DecodeBase64FileData is still nil -> error.
	SetMediaResolver(MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "", "", nil
		},
	})

	format, b64, err := DecodeBase64FileData("data:image/png;base64,ZGF0YQ==")
	require.Error(t, err)
	assert.Equal(t, "", format)
	assert.Equal(t, "", b64)
	assert.Contains(t, err.Error(), "not configured")
}

func TestDecodeBase64FileData_Delegates(t *testing.T) {
	defer resetResolver()

	var gotInput string
	SetMediaResolver(MediaResolver{
		DecodeBase64FileData: func(base64String string) (string, string, error) {
			gotInput = base64String
			return "png", "ZGF0YQ==", nil
		},
	})

	format, b64, err := DecodeBase64FileData("data:image/png;base64,ZGF0YQ==")
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, "ZGF0YQ==", b64)
	assert.Equal(t, "data:image/png;base64,ZGF0YQ==", gotInput)
}

func TestSetMediaResolver_Overwrites(t *testing.T) {
	defer resetResolver()

	SetMediaResolver(MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "first", "text/plain", nil
		},
	})
	SetMediaResolver(MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "second", "text/plain", nil
		},
	})

	data, _, err := ResolveBase64Data(nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "second", data)
}
