package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFileMeta_SetsTypeAndSource(t *testing.T) {
	src := NewURLFileSource("https://example.com/a.png")
	fm := NewFileMeta(FileTypeAudio, src)

	require.NotNil(t, fm)
	require.Equal(t, FileTypeAudio, fm.FileType)
	require.Same(t, FileSource(src), fm.Source)
	require.Equal(t, "", fm.Detail)
}

func TestNewImageFileMeta_SetsImageTypeAndDetail(t *testing.T) {
	src := NewBase64FileSource("abc", "image/png")
	fm := NewImageFileMeta(src, "high")

	require.Equal(t, FileTypeImage, fm.FileType)
	require.Same(t, FileSource(src), fm.Source)
	require.Equal(t, "high", fm.Detail)
}

func TestFileMeta_GetIdentifier_WithSource(t *testing.T) {
	fm := NewFileMeta(FileTypeImage, NewURLFileSource("https://example.com/a.png"))
	require.Equal(t, "https://example.com/a.png", fm.GetIdentifier())
}

func TestFileMeta_GetIdentifier_NilSourceReturnsUnknown(t *testing.T) {
	fm := &FileMeta{FileType: FileTypeImage, Source: nil}
	require.Equal(t, "unknown", fm.GetIdentifier())
}

func TestFileMeta_IsURL(t *testing.T) {
	urlMeta := NewFileMeta(FileTypeImage, NewURLFileSource("https://example.com/a.png"))
	require.True(t, urlMeta.IsURL())

	b64Meta := NewFileMeta(FileTypeImage, NewBase64FileSource("abc", "image/png"))
	require.False(t, b64Meta.IsURL())
}

func TestFileMeta_IsURL_NilSourceReturnsFalse(t *testing.T) {
	fm := &FileMeta{FileType: FileTypeImage, Source: nil}
	require.False(t, fm.IsURL())
}

func TestFileMeta_GetRawData(t *testing.T) {
	urlMeta := NewFileMeta(FileTypeImage, NewURLFileSource("https://example.com/a.png"))
	require.Equal(t, "https://example.com/a.png", urlMeta.GetRawData())

	b64Meta := NewFileMeta(FileTypeImage, NewBase64FileSource("rawbytes", "image/png"))
	require.Equal(t, "rawbytes", b64Meta.GetRawData())
}

func TestFileMeta_GetRawData_NilSourceReturnsEmpty(t *testing.T) {
	fm := &FileMeta{FileType: FileTypeImage, Source: nil}
	require.Equal(t, "", fm.GetRawData())
}
