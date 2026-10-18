package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestRequestLogStartupContinuesAfterDeleteFailure(t *testing.T) {
	root := requestLogTestStore(t)
	locked := filepath.Join(root, "2026-01-01", "locked.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(locked), 0o755))
	require.NoError(t, os.WriteFile(locked, []byte("{}"), 0o600))
	name, err := windows.UTF16PtrFromString(locked)
	require.NoError(t, err)
	// Omit FILE_SHARE_DELETE to simulate a file held by another application.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, windows.CloseHandle(handle)) })
	require.NoError(t, os.Mkdir(filepath.Join(root, "2026-01-02"), 0o755))
	stats := clearRequestLogHistoryFiles()
	require.Equal(t, 1, stats.Errors)
	require.Equal(t, 1, stats.Deleted)
	require.FileExists(t, locked)
	require.NoDirExists(t, filepath.Join(root, "2026-01-02"))
}
