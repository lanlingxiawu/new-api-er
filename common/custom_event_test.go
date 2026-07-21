package common

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomEventRender(t *testing.T) {
	rec := httptest.NewRecorder()
	ev := CustomEvent{Data: "hello world"}
	require.NoError(t, ev.Render(rec))

	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "hello world", rec.Body.String())
}

func TestCustomEventRender_DataPrefixAppendsBlankLine(t *testing.T) {
	rec := httptest.NewRecorder()
	ev := CustomEvent{Data: "data: payload"}
	require.NoError(t, ev.Render(rec))
	// writeData appends "\n\n" when the payload begins with "data"
	assert.Equal(t, "data: payload\n\n", rec.Body.String())
}

func TestWriteContentTypePreservesExistingCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Cache-Control", "max-age=5")
	ev := CustomEvent{}
	ev.WriteContentType(rec)
	assert.Equal(t, "max-age=5", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}

// stringWriterStub implements the stringWriter interface to exercise the
// checkWriter fast path (writer already supports writeString).
type stringWriterStub struct {
	buf bytes.Buffer
}

func (s *stringWriterStub) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *stringWriterStub) writeString(str string) (int, error) {
	return s.buf.WriteString(str)
}

func TestCheckWriterBranches(t *testing.T) {
	// wrapper branch: plain io.Writer gets wrapped
	var plain bytes.Buffer
	w := checkWriter(&plain)
	_, ok := w.(stringWrapper)
	assert.True(t, ok)

	// fast path: writer already a stringWriter is returned as-is
	stub := &stringWriterStub{}
	w2 := checkWriter(stub)
	assert.Equal(t, stub, w2)
}

func TestEncodeWritesData(t *testing.T) {
	stub := &stringWriterStub{}
	require.NoError(t, encode(stub, CustomEvent{Data: "data first"}))
	assert.Equal(t, "data first\n\n", stub.buf.String())
}

func TestWriteData_NonStringDataPanics(t *testing.T) {
	// Documents a latent limitation: writeData type-asserts Data.(string) and
	// panics on non-string payloads. Kept as a guard so a future safety fix is
	// intentional rather than accidental.
	stub := &stringWriterStub{}
	assert.Panics(t, func() {
		_ = encode(stub, CustomEvent{Data: 12345})
	})
}
