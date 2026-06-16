package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMatchRequestLogUsername(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("username", " Alice ")

	previous := common.RequestLogUsername
	t.Cleanup(func() {
		common.RequestLogUsername = previous
	})

	common.RequestLogUsername = "alice"
	username, matched := matchRequestLogUsername(c)
	require.True(t, matched)
	require.Equal(t, "Alice", username)

	common.RequestLogUsername = "bob"
	_, matched = matchRequestLogUsername(c)
	require.False(t, matched)

	common.RequestLogUsername = ""
	_, matched = matchRequestLogUsername(c)
	require.True(t, matched)
}

func TestCaptureRequestBodyResetsNonTextualBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	body := "binary-data"
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	t.Cleanup(func() {
		common.CleanupBodyStorage(c)
	})

	content, truncated, size := captureRequestBody(c, 4)
	require.Equal(t, "[non-textual request body omitted]", content)
	require.False(t, truncated)
	require.EqualValues(t, len(body), size)

	data, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(data))
}
