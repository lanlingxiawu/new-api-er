package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An error before the first byte of a stream must go out as JSON, not as a
// JSON body labelled text/event-stream (c.JSON keeps an existing Content-Type).
func TestClearEventStreamHeadersBeforeFirstByte(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetEventStreamHeaders(c)
	require.Equal(t, "text/event-stream", c.Writer.Header().Get("Content-Type"))

	ClearEventStreamHeaders(c)
	c.JSON(http.StatusBadRequest, gin.H{"error": "x"})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Empty(t, rec.Header().Get("X-Accel-Buffering"))
}

// Once the stream has started, the headers are already on the wire and must
// be left alone.
func TestClearEventStreamHeadersAfterWriteIsNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetEventStreamHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write([]byte("data: {}\n\n"))

	ClearEventStreamHeaders(c)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}

// Without SetEventStreamHeaders nothing is removed (a Content-Type set by
// other code stays).
func TestClearEventStreamHeadersWithoutStreamIsNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Writer.Header().Set("Content-Type", "application/x-custom")
	ClearEventStreamHeaders(c)
	assert.Equal(t, "application/x-custom", c.Writer.Header().Get("Content-Type"))
}
