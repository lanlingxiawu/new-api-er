package service

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// token_counter.go getImageToken — per-model image token estimation.
// Pure CPU given decoded image dimensions; base64 image source, no network.
// ===========================================================================

func withMediaToken(t *testing.T) {
	t.Helper()
	o1, o2 := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	t.Cleanup(func() {
		constant.GetMediaToken = o1
		constant.GetMediaTokenNotStream = o2
	})
}

func imageMeta(t *testing.T, w, h int, detail string) *types.FileMeta {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString(pngBytes(t, w, h))
	return &types.FileMeta{
		FileType: types.FileTypeImage,
		Source:   types.NewBase64FileSource(b64, "image/png"),
		Detail:   detail,
	}
}

func imgTokCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestImgTok_NilSource(t *testing.T) {
	_, err := getImageToken(imgTokCtx(t), &types.FileMeta{}, "gpt-4o", true)
	assert.Error(t, err)
}

func TestImgTok_GLM4(t *testing.T) {
	// GLM-4 is a fixed 1047 short-circuit.
	got, err := getImageToken(imgTokCtx(t), imageMeta(t, 8, 8, "high"), "glm-4v", true)
	require.NoError(t, err)
	assert.Equal(t, 1047, got)
}

func TestImgTok_LowDetailShortCircuit(t *testing.T) {
	// low detail on a tile-based model returns baseTokens (85 for 4o).
	got, err := getImageToken(imgTokCtx(t), imageMeta(t, 100, 100, "low"), "gpt-4o", true)
	require.NoError(t, err)
	assert.Equal(t, 85, got)
}

func TestImgTok_MediaTokenDisabled(t *testing.T) {
	o := constant.GetMediaToken
	constant.GetMediaToken = false
	t.Cleanup(func() { constant.GetMediaToken = o })
	// GetMediaToken false => 3*baseTokens (3*85).
	got, err := getImageToken(imgTokCtx(t), imageMeta(t, 100, 100, "high"), "gpt-4o", true)
	require.NoError(t, err)
	assert.Equal(t, 255, got)
}

func TestImgTok_TileBased_4o(t *testing.T) {
	withMediaToken(t)
	// 1024x1024: fit<=2048 (scale 1), short side 768 => 768x768 => 2x2 tiles.
	// tiles(4)*tileTokens(170) + baseTokens(85) = 765.
	got, err := getImageToken(imgTokCtx(t), imageMeta(t, 1024, 1024, "high"), "gpt-4o", true)
	require.NoError(t, err)
	assert.Equal(t, 765, got)
}

func TestImgTok_PatchBased_41Mini(t *testing.T) {
	withMediaToken(t)
	// 64x64 => 2x2 = 4 patches (< 1536 cap). round(4 * 1.62) = 6.
	got, err := getImageToken(imgTokCtx(t), imageMeta(t, 64, 64, "high"), "gpt-4.1-mini", true)
	require.NoError(t, err)
	assert.Equal(t, 6, got)
}
