package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestStreamSizeLimit200MiB(t *testing.T) {
	require.Equal(t, 209715200, MaxStreamFrameBytes)
}

// Real complete image JSON, including its envelope, is measured in encoded bytes.
// Copy to Discard avoids an additional test-side full response buffer.
func TestStreamImageJSONSizeBoundaries(t *testing.T) {
	const limit = 209715200
	for _, size := range []int{(8 << 20) + 1, (100 << 20) + 1, limit - 1, limit, limit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			prefix, suffix := `{"data":[{"b64_json":"`, `"}]}`
			body := io.MultiReader(strings.NewReader(prefix), io.LimitReader(streamImagePaddingReader{}, int64(size-len(prefix)-len(suffix))), strings.NewReader(suffix))
			s := NewStreamSession(types.RelayFormatOpenAIImage)
			s.ExpectedImages = 1
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(body)}
			s.ObserveHTTP(resp)
			defer resp.Body.Close()
			n, err := io.Copy(io.Discard, resp.Body)
			if size <= limit {
				require.NoError(t, err)
				require.Equal(t, int64(size), n)
				require.True(t, s.ProtocolComplete())
				require.Equal(t, 1, s.Snapshot().Evidence["image_count"])
			} else {
				require.ErrorContains(t, err, "exceeds limit")
				require.Zero(t, n)
				require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
			}
		})
	}
}

// Generates valid JSON string bytes without retaining a second 200 MiB fixture.
type streamImagePaddingReader struct{}

func (streamImagePaddingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}
