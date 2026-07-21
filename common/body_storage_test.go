package common

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorage(t *testing.T) {
	// disk disabled -> CreateBodyStorage yields memory storage
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})

	s, err := CreateBodyStorage([]byte("payload-data"))
	require.NoError(t, err)
	assert.False(t, s.IsDisk())
	assert.Equal(t, int64(12), s.Size())

	// Read
	buf := make([]byte, 7)
	n, err := s.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(buf[:n]))

	// Seek back and read all
	_, err = s.Seek(0, io.SeekStart)
	require.NoError(t, err)
	all, err := io.ReadAll(s)
	require.NoError(t, err)
	assert.Equal(t, "payload-data", string(all))

	// Bytes returns full content
	b, err := s.Bytes()
	require.NoError(t, err)
	assert.Equal(t, "payload-data", string(b))

	// after Close every op errors with ErrStorageClosed
	require.NoError(t, s.Close())
	_, err = s.Read(buf)
	assert.ErrorIs(t, err, ErrStorageClosed)
	_, err = s.Seek(0, io.SeekStart)
	assert.ErrorIs(t, err, ErrStorageClosed)
	_, err = s.Bytes()
	assert.ErrorIs(t, err, ErrStorageClosed)
	// double Close is a no-op
	assert.NoError(t, s.Close())
}

func TestDiskStorage(t *testing.T) {
	// threshold 0 forces even tiny payloads onto disk
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 100})
	ResetDiskCacheUsage()

	s, err := CreateBodyStorage([]byte("disk-payload"))
	require.NoError(t, err)
	require.True(t, s.IsDisk())
	assert.Equal(t, int64(12), s.Size())

	b, err := s.Bytes()
	require.NoError(t, err)
	assert.Equal(t, "disk-payload", string(b))

	// Bytes preserves the read cursor; a follow-up read continues correctly
	_, err = s.Seek(0, io.SeekStart)
	require.NoError(t, err)
	head := make([]byte, 4)
	_, err = io.ReadFull(s, head)
	require.NoError(t, err)
	assert.Equal(t, "disk", string(head))
	_, err = s.Bytes() // must not disturb the cursor
	require.NoError(t, err)
	rest, err := io.ReadAll(s)
	require.NoError(t, err)
	assert.Equal(t, "-payload", string(rest))

	require.NoError(t, s.Close())
	_, err = s.Read(head)
	assert.ErrorIs(t, err, ErrStorageClosed)
	_, err = s.Bytes()
	assert.ErrorIs(t, err, ErrStorageClosed)
	assert.NoError(t, s.Close()) // idempotent
}

func TestCreateBodyStorageFromReader_Memory(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})

	s, err := CreateBodyStorageFromReader(strings.NewReader("abc"), 3, 1<<20)
	require.NoError(t, err)
	assert.False(t, s.IsDisk())
	b, _ := s.Bytes()
	assert.Equal(t, "abc", string(b))
	_ = s.Close()
}

func TestCreateBodyStorageFromReader_TooLargeMemory(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})

	_, err := CreateBodyStorageFromReader(strings.NewReader("0123456789"), 10, 4)
	require.Error(t, err)
	assert.True(t, IsRequestBodyTooLargeError(err))
}

func TestCreateBodyStorageFromReader_Disk(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 100})
	ResetDiskCacheUsage()

	data := "disk-reader-payload"
	s, err := CreateBodyStorageFromReader(strings.NewReader(data), int64(len(data)), 1<<20)
	require.NoError(t, err)
	assert.True(t, s.IsDisk())
	b, _ := s.Bytes()
	assert.Equal(t, data, string(b))
	_ = s.Close()
}

func TestCreateBodyStorageFromReader_DiskTooLarge(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 100})
	ResetDiskCacheUsage()

	data := "0123456789ABCDEF"
	// contentLength triggers disk path; maxBytes smaller than the payload
	_, err := CreateBodyStorageFromReader(strings.NewReader(data), int64(len(data)), 4)
	require.Error(t, err)
	assert.True(t, IsRequestBodyTooLargeError(err))
}

func TestReaderOnly(t *testing.T) {
	r := ReaderOnly(bytes.NewReader([]byte("x")))
	_, isCloser := r.(io.Closer)
	assert.False(t, isCloser, "ReaderOnly must hide io.Closer")

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "x", string(out))
}

func TestCleanupOldCacheFilesNoPanic(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})
	assert.NotPanics(t, func() { CleanupOldCacheFiles() })
}
