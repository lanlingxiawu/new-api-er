package oairesponses

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/relayconvert/internal/media"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestMain installs a deterministic media resolver so that converters that call
// relaymedia.ResolveBase64Data (Claude/Gemini media parts) return fixed fixtures
// instead of performing any network/DB access. The desired mime type / error is
// encoded in the source's raw data via marker substrings (see fakeGetBase64Data).
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	media.SetMediaResolver(media.MediaResolver{
		GetBase64Data:        fakeGetBase64Data,
		DecodeBase64FileData: fakeDecodeBase64FileData,
	})
	os.Exit(m.Run())
}

// fakeGetBase64Data returns deterministic (base64, mimeType, err) derived from
// marker substrings in the source identifier/raw data:
//
//	"trigger-error" -> returns an error
//	"pdf"           -> application/pdf   (Claude document branch)
//	"unsupported"   -> application/x-nope (Gemini unsupported-mime branch)
//	"png"           -> image/png         (supported everywhere)
//	otherwise       -> image/jpeg
func fakeGetBase64Data(_ *gin.Context, source types.FileSource, _ ...string) (string, string, error) {
	raw := source.GetRawData()
	switch {
	case strings.Contains(raw, "trigger-error"):
		return "", "", errors.New("boom: resolver failed")
	case strings.Contains(raw, "pdf"):
		return "cGRmZGF0YQ==", "application/pdf", nil
	case strings.Contains(raw, "unsupported"):
		return "dW5zdXA=", "application/x-nope", nil
	case strings.Contains(raw, "png"):
		return "iVBORw0KAAAA", "image/png", nil
	default:
		return "ZGF0YQ==", "image/jpeg", nil
	}
}

func fakeDecodeBase64FileData(base64String string) (string, string, error) {
	return base64String, "application/octet-stream", nil
}

// mustRawMessage marshals value to raw JSON via the project's JSON wrapper.
func mustRawMessage(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}

func newTestGinContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}
