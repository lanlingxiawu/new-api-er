package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// URLSource
// ---------------------------------------------------------------------------

func TestURLSource_IsURL(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	require.True(t, u.IsURL())
}

func TestURLSource_GetRawData(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	require.Equal(t, "https://example.com/a.png", u.GetRawData())
}

func TestURLSource_ClearRawData_NoOp(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	u.ClearRawData()
	require.Equal(t, "https://example.com/a.png", u.GetRawData(), "URL source raw data is never cleared")
}

func TestURLSource_GetIdentifier_ShortNotTruncated(t *testing.T) {
	u := NewURLFileSource("https://example.com/short")
	require.Equal(t, "https://example.com/short", u.GetIdentifier())
}

func TestURLSource_GetIdentifier_Exactly100NotTruncated(t *testing.T) {
	url := strings.Repeat("a", 100)
	u := NewURLFileSource(url)
	require.Equal(t, url, u.GetIdentifier(), "len==100 is the boundary, not > 100")
}

func TestURLSource_GetIdentifier_101Truncated(t *testing.T) {
	url := strings.Repeat("a", 101)
	u := NewURLFileSource(url)
	got := u.GetIdentifier()
	require.Equal(t, strings.Repeat("a", 100)+"...", got)
	require.Len(t, got, 103) // 100 chars + "..."
}

// ---------------------------------------------------------------------------
// Base64Source
// ---------------------------------------------------------------------------

func TestBase64Source_IsURL(t *testing.T) {
	b := NewBase64FileSource("abc", "image/png")
	require.False(t, b.IsURL())
}

func TestBase64Source_GetRawData(t *testing.T) {
	b := NewBase64FileSource("rawdata", "image/png")
	require.Equal(t, "rawdata", b.GetRawData())
}

func TestBase64Source_GetIdentifier_ShortNotTruncated(t *testing.T) {
	b := NewBase64FileSource("abc", "image/png")
	require.Equal(t, "base64:abc", b.GetIdentifier())
}

func TestBase64Source_GetIdentifier_Exactly50NotTruncated(t *testing.T) {
	data := strings.Repeat("x", 50)
	b := NewBase64FileSource(data, "image/png")
	require.Equal(t, "base64:"+data, b.GetIdentifier(), "len==50 is the boundary, not > 50")
}

func TestBase64Source_GetIdentifier_51Truncated(t *testing.T) {
	data := strings.Repeat("x", 51)
	b := NewBase64FileSource(data, "image/png")
	require.Equal(t, "base64:"+strings.Repeat("x", 50)+"...", b.GetIdentifier())
}

func TestBase64Source_ClearRawData_Exactly1024NotCleared(t *testing.T) {
	data := strings.Repeat("y", 1024)
	b := NewBase64FileSource(data, "image/png")
	b.ClearRawData()
	require.Equal(t, data, b.GetRawData(), "len==1024 is not > 1024, so not cleared")
}

func TestBase64Source_ClearRawData_1025Cleared(t *testing.T) {
	data := strings.Repeat("y", 1025)
	b := NewBase64FileSource(data, "image/png")
	b.ClearRawData()
	require.Equal(t, "", b.GetRawData(), "len>1024 clears the inline base64 data")
}

func TestBase64Source_ClearRawData_SmallNotCleared(t *testing.T) {
	b := NewBase64FileSource("small", "image/png")
	b.ClearRawData()
	require.Equal(t, "small", b.GetRawData())
}

// ---------------------------------------------------------------------------
// NewFileSourceFromData
// ---------------------------------------------------------------------------

func TestNewFileSourceFromData_HTTP(t *testing.T) {
	src := NewFileSourceFromData("http://example.com/a.png", "image/png")
	require.True(t, src.IsURL())
	_, ok := src.(*URLSource)
	require.True(t, ok)
}

func TestNewFileSourceFromData_HTTPS(t *testing.T) {
	src := NewFileSourceFromData("https://example.com/a.png", "image/png")
	require.True(t, src.IsURL())
	_, ok := src.(*URLSource)
	require.True(t, ok)
}

func TestNewFileSourceFromData_Base64Fallback(t *testing.T) {
	src := NewFileSourceFromData("iVBORw0KGgo=", "image/png")
	require.False(t, src.IsURL())
	b, ok := src.(*Base64Source)
	require.True(t, ok)
	require.Equal(t, "image/png", b.MimeType)
}

func TestNewFileSourceFromData_NonHttpSchemeIsBase64(t *testing.T) {
	// "ftp://" is not http/https, so it is treated as base64 raw data.
	src := NewFileSourceFromData("ftp://example.com/a.png", "image/png")
	require.False(t, src.IsURL())
	_, ok := src.(*Base64Source)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// baseFileSource cache/registered/mutex plumbing
// ---------------------------------------------------------------------------

func TestBaseFileSource_CacheLifecycle(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	require.False(t, u.HasCache())
	require.Nil(t, u.GetCache())

	cd := NewMemoryCachedData("data", "image/png", 4)
	u.SetCache(cd)
	require.True(t, u.HasCache())
	require.Same(t, cd, u.GetCache())

	u.ClearCache()
	require.False(t, u.HasCache())
	require.Nil(t, u.GetCache())
}

func TestBaseFileSource_HasCache_NilDataAfterSet(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	// cacheLoaded becomes true but cachedData is nil -> HasCache must be false.
	u.SetCache(nil)
	require.False(t, u.HasCache())
}

func TestBaseFileSource_ClearCache_NilIsSafe(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	require.NotPanics(t, func() { u.ClearCache() })
}

func TestBaseFileSource_RegisteredFlag(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	require.False(t, u.IsRegistered())
	u.SetRegistered(true)
	require.True(t, u.IsRegistered())
	u.SetRegistered(false)
	require.False(t, u.IsRegistered())
}

func TestBaseFileSource_Mu_ReturnsUsableMutex(t *testing.T) {
	u := NewURLFileSource("https://example.com/a.png")
	mu := u.Mu()
	require.NotNil(t, mu)
	require.Same(t, mu, u.Mu(), "returns the same underlying mutex each call")
	// Lockable without panic.
	mu.Lock()
	mu.Unlock()
}

// ---------------------------------------------------------------------------
// CachedFileData — memory mode
// ---------------------------------------------------------------------------

func TestCachedFileData_Memory_GetBase64(t *testing.T) {
	c := NewMemoryCachedData("hello", "text/plain", 5)
	require.False(t, c.IsDisk())

	data, err := c.GetBase64Data()
	require.NoError(t, err)
	require.Equal(t, "hello", data)
}

func TestCachedFileData_Memory_SetBase64(t *testing.T) {
	c := NewMemoryCachedData("hello", "text/plain", 5)
	c.SetBase64Data("world")
	data, err := c.GetBase64Data()
	require.NoError(t, err)
	require.Equal(t, "world", data)
}

func TestCachedFileData_Memory_CloseClearsData(t *testing.T) {
	c := NewMemoryCachedData("hello", "text/plain", 5)
	err := c.Close()
	require.NoError(t, err)
	data, err := c.GetBase64Data()
	require.NoError(t, err)
	require.Equal(t, "", data, "memory Close clears the inline data")
}

// ---------------------------------------------------------------------------
// CachedFileData — disk mode (uses a real temp file via t.TempDir)
// ---------------------------------------------------------------------------

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.bin")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestCachedFileData_Disk_GetBase64ReadsFile(t *testing.T) {
	path := writeTempFile(t, "disk-content")
	c := NewDiskCachedData(path, "application/octet-stream", 12)
	require.True(t, c.IsDisk())

	data, err := c.GetBase64Data()
	require.NoError(t, err)
	require.Equal(t, "disk-content", data)
}

func TestCachedFileData_Disk_SetBase64IsNoOp(t *testing.T) {
	path := writeTempFile(t, "disk-content")
	c := NewDiskCachedData(path, "application/octet-stream", 12)
	// SetBase64Data does nothing in disk mode; file read still wins.
	c.SetBase64Data("ignored")
	data, err := c.GetBase64Data()
	require.NoError(t, err)
	require.Equal(t, "disk-content", data)
}

func TestCachedFileData_Disk_GetBase64MissingFileErrors(t *testing.T) {
	c := NewDiskCachedData(filepath.Join(t.TempDir(), "does-not-exist"), "x", 0)
	_, err := c.GetBase64Data()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read from disk cache")
}

func TestCachedFileData_Disk_CloseRemovesFileAndFiresOnClose(t *testing.T) {
	path := writeTempFile(t, "disk-content")
	c := NewDiskCachedData(path, "x", 12)
	c.DiskSize = 999

	var reportedSize int64 = -1
	callbackCount := 0
	c.OnClose = func(size int64) {
		reportedSize = size
		callbackCount++
	}

	err := c.Close()
	require.NoError(t, err)
	require.NoFileExists(t, path, "Close must remove the disk file")
	require.Equal(t, int64(999), reportedSize, "OnClose receives DiskSize")
	require.Equal(t, 1, callbackCount)

	// After close, reads must fail.
	_, err = c.GetBase64Data()
	require.Error(t, err)
	require.Contains(t, err.Error(), "already closed")
}

func TestCachedFileData_Disk_CloseIdempotent(t *testing.T) {
	path := writeTempFile(t, "disk-content")
	c := NewDiskCachedData(path, "x", 12)

	callbackCount := 0
	c.OnClose = func(size int64) { callbackCount++ }

	require.NoError(t, c.Close())
	// Second close is a no-op: file already gone, callback not fired again.
	require.NoError(t, c.Close())
	require.Equal(t, 1, callbackCount, "OnClose fires at most once")
}

func TestCachedFileData_Disk_CloseWithoutOnCloseCallback(t *testing.T) {
	path := writeTempFile(t, "disk-content")
	c := NewDiskCachedData(path, "x", 12)
	// No OnClose set; must not panic.
	require.NoError(t, c.Close())
	require.NoFileExists(t, path)
}

func TestCachedFileData_Disk_CloseEmptyPathReturnsNil(t *testing.T) {
	// Disk mode but empty diskPath: Close hits the trailing "return nil" branch.
	c := &CachedFileData{isDisk: true, diskPath: ""}
	require.NoError(t, c.Close())
}
