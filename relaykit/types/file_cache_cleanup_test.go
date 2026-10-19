package types

import (
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCachedFileRemovalRetry(t *testing.T) {
	cache := NewDiskCachedData("owned", "text/plain", 1)
	calls := 0
	cache.RemoveFile = func(path string) error {
		calls++
		if calls == 1 {
			return errors.New("busy")
		}
		return nil
	}
	require.Error(t, cache.Close())
	_, err := cache.GetBase64Data()
	require.Error(t, err)
	require.NoError(t, cache.Close())
	require.NoError(t, cache.Close())
	require.Equal(t, 2, calls)
}
