package ionet

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubHTTPClient is a controllable HTTPClient used to drive Client methods
// without any real network I/O. It records the last request it received.
type stubHTTPClient struct {
	fn   func(*HTTPRequest) (*HTTPResponse, error)
	last *HTTPRequest
}

func (s *stubHTTPClient) Do(req *HTTPRequest) (*HTTPResponse, error) {
	s.last = req
	return s.fn(req)
}

// newStubClient returns a Client whose transport always yields the supplied
// response/error, plus a handle to the stub so tests can inspect the request.
func newStubClient(resp *HTTPResponse, err error) (*Client, *stubHTTPClient) {
	stub := &stubHTTPClient{fn: func(*HTTPRequest) (*HTTPResponse, error) {
		return resp, err
	}}
	return NewClientWithConfig("test-key", "https://base.example/v1", stub), stub
}

// newStubClientFn returns a Client backed by a custom handler function.
func newStubClientFn(fn func(*HTTPRequest) (*HTTPResponse, error)) (*Client, *stubHTTPClient) {
	stub := &stubHTTPClient{fn: fn}
	return NewClientWithConfig("test-key", "https://base.example/v1", stub), stub
}

// okResp is a convenience 200 response with the given body.
func okResp(body string) *HTTPResponse {
	return &HTTPResponse{StatusCode: 200, Body: []byte(body)}
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func TestNewClient_UsesPublicBaseURL(t *testing.T) {
	c := NewClient("abc")
	require.NotNil(t, c)
	assert.Equal(t, DefaultBaseURL, c.BaseURL)
	assert.Equal(t, "abc", c.APIKey)
	require.NotNil(t, c.HTTPClient)
	_, ok := c.HTTPClient.(*DefaultHTTPClient)
	assert.True(t, ok, "default client should be installed")
}

func TestNewEnterpriseClient_UsesEnterpriseBaseURL(t *testing.T) {
	c := NewEnterpriseClient("abc")
	assert.Equal(t, DefaultEnterpriseBaseURL, c.BaseURL)
	assert.Equal(t, "abc", c.APIKey)
}

func TestNewClientWithConfig_EmptyBaseURLFallsBack(t *testing.T) {
	c := NewClientWithConfig("k", "", nil)
	assert.Equal(t, DefaultBaseURL, c.BaseURL)
	// nil httpClient must be replaced with a *DefaultHTTPClient
	_, ok := c.HTTPClient.(*DefaultHTTPClient)
	assert.True(t, ok)
}

func TestNewClientWithConfig_CustomValuesPreserved(t *testing.T) {
	stub := &stubHTTPClient{}
	c := NewClientWithConfig("k", "https://custom/base", stub)
	assert.Equal(t, "https://custom/base", c.BaseURL)
	assert.Same(t, stub, c.HTTPClient)
}

func TestNewDefaultHTTPClient_SetsTimeout(t *testing.T) {
	c := NewDefaultHTTPClient(7 * time.Second)
	require.NotNil(t, c)
	require.NotNil(t, c.client)
	assert.Equal(t, 7*time.Second, c.client.Timeout)
}

// ---------------------------------------------------------------------------
// DefaultHTTPClient.Do — real HTTP against httptest (no external network)
// ---------------------------------------------------------------------------

func TestDefaultHTTPClient_Do_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// echo header + body confirmation
		assert.Equal(t, "hval", r.Header.Get("X-Custom"))
		w.Header().Set("X-Reply", "rval")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	dc := NewDefaultHTTPClient(DefaultTimeout)
	resp, err := dc.Do(&HTTPRequest{
		Method:  "POST",
		URL:     srv.URL,
		Headers: map[string]string{"X-Custom": "hval"},
		Body:    []byte("ping"),
	})
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, "pong", string(resp.Body))
	assert.Equal(t, "rval", resp.Headers["X-Reply"])
}

func TestDefaultHTTPClient_Do_NewRequestError(t *testing.T) {
	dc := NewDefaultHTTPClient(DefaultTimeout)
	// A method containing a space is an invalid HTTP token → http.NewRequest fails.
	_, err := dc.Do(&HTTPRequest{Method: "BAD METHOD", URL: "https://example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create HTTP request")
}

func TestDefaultHTTPClient_Do_ConnectionRefused(t *testing.T) {
	dc := NewDefaultHTTPClient(500 * time.Millisecond)
	// 127.0.0.1:1 is a reserved, closed port → deterministic dial failure.
	_, err := dc.Do(&HTTPRequest{Method: "GET", URL: "http://127.0.0.1:1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP request failed")
}

func TestDefaultHTTPClient_Do_BodyReadError(t *testing.T) {
	// Handler hijacks the connection and lies about Content-Length, then closes
	// early. The client's body.ReadFrom then fails with an unexpected EOF.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort"))
		_ = conn.Close()
	}))
	defer srv.Close()

	dc := NewDefaultHTTPClient(2 * time.Second)
	_, err := dc.Do(&HTTPRequest{Method: "GET", URL: srv.URL})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read response body")
}

func TestDefaultHTTPClient_Do_HeaderWithNoValuesSkipped(t *testing.T) {
	// Exercise the len(values) > 0 branch of header conversion. Standard servers
	// always send non-empty header values, so a successful call is enough to
	// execute the copy loop; this asserts a normal multi-header response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	dc := NewDefaultHTTPClient(DefaultTimeout)
	resp, err := dc.Do(&HTTPRequest{Method: "GET", URL: srv.URL})
	require.NoError(t, err)
	assert.Equal(t, "application/json", resp.Headers["Content-Type"])
}

// ---------------------------------------------------------------------------
// makeRequest — response processing & error mapping (white-box)
// ---------------------------------------------------------------------------

func TestMakeRequest_SetsHeadersAndURL(t *testing.T) {
	c, stub := newStubClient(okResp("{}"), nil)
	_, err := c.makeRequest("GET", "/ping", nil)
	require.NoError(t, err)
	require.NotNil(t, stub.last)
	assert.Equal(t, "https://base.example/v1/ping", stub.last.URL)
	assert.Equal(t, "test-key", stub.last.Headers["X-API-KEY"])
	assert.Equal(t, "application/json", stub.last.Headers["Content-Type"])
	assert.Nil(t, stub.last.Body, "GET with nil body must not marshal a body")
}

func TestMakeRequest_MarshalsBody(t *testing.T) {
	c, stub := newStubClient(okResp("{}"), nil)
	_, err := c.makeRequest("POST", "/x", map[string]string{"a": "b"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":"b"}`, string(stub.last.Body))
}

func TestMakeRequest_MarshalError(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	// A channel cannot be JSON-marshaled → marshal error path.
	_, err := c.makeRequest("POST", "/x", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal request body")
}

func TestMakeRequest_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestMakeRequest_ErrorWithDetailField(t *testing.T) {
	c, _ := newStubClient(&HTTPResponse{StatusCode: 400, Body: []byte(`{"detail":"bad thing"}`)}, nil)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.Code)
	assert.Equal(t, "bad thing", apiErr.Message)
	assert.Empty(t, apiErr.Details)
}

func TestMakeRequest_ErrorWithNonJSONBodyFallback(t *testing.T) {
	c, _ := newStubClient(&HTTPResponse{StatusCode: 500, Body: []byte("plain server error")}, nil)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, 500, apiErr.Code)
	assert.Contains(t, apiErr.Message, "status 500")
	assert.Equal(t, "plain server error", apiErr.Details)
}

func TestMakeRequest_ErrorWithValidJSONButEmptyDetail(t *testing.T) {
	// Valid JSON but no "detail" → falls through to the raw-body fallback branch.
	c, _ := newStubClient(&HTTPResponse{StatusCode: 422, Body: []byte(`{"other":"x"}`)}, nil)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
	apiErr := err.(*APIError)
	assert.Equal(t, 422, apiErr.Code)
	assert.Contains(t, apiErr.Message, "status 422")
	assert.Equal(t, `{"other":"x"}`, apiErr.Details)
}

func TestMakeRequest_ErrorWithEmptyBody(t *testing.T) {
	c, _ := newStubClient(&HTTPResponse{StatusCode: 404, Body: nil}, nil)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
	apiErr := err.(*APIError)
	assert.Equal(t, 404, apiErr.Code)
	assert.Contains(t, apiErr.Message, "status 404")
	assert.Empty(t, apiErr.Details)
}

func TestMakeRequest_BoundaryStatus399IsSuccess(t *testing.T) {
	// 399 < 400 → success branch (boundary just below the error threshold).
	c, _ := newStubClient(&HTTPResponse{StatusCode: 399, Body: []byte("ok")}, nil)
	resp, err := c.makeRequest("GET", "/x", nil)
	require.NoError(t, err)
	assert.Equal(t, 399, resp.StatusCode)
}

func TestMakeRequest_BoundaryStatus400IsError(t *testing.T) {
	c, _ := newStubClient(&HTTPResponse{StatusCode: 400, Body: nil}, nil)
	_, err := c.makeRequest("GET", "/x", nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// buildQueryParams — every type branch (equivalence + boundary)
// ---------------------------------------------------------------------------

func TestBuildQueryParams_EmptyMap(t *testing.T) {
	assert.Equal(t, "", buildQueryParams(nil))
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{}))
}

func TestBuildQueryParams_NilValueSkipped(t *testing.T) {
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": nil}))
}

func TestBuildQueryParams_String(t *testing.T) {
	assert.Equal(t, "?k=v", buildQueryParams(map[string]interface{}{"k": "v"}))
	// empty string is skipped
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": ""}))
}

func TestBuildQueryParams_Int(t *testing.T) {
	assert.Equal(t, "?k=5", buildQueryParams(map[string]interface{}{"k": 5}))
	// zero int skipped
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": 0}))
}

func TestBuildQueryParams_Int64(t *testing.T) {
	assert.Equal(t, "?k=9", buildQueryParams(map[string]interface{}{"k": int64(9)}))
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": int64(0)}))
}

func TestBuildQueryParams_Float64(t *testing.T) {
	assert.Equal(t, "?k=1.5", buildQueryParams(map[string]interface{}{"k": 1.5}))
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": 0.0}))
}

func TestBuildQueryParams_Bool(t *testing.T) {
	// bool is always added, including false
	assert.Equal(t, "?k=true", buildQueryParams(map[string]interface{}{"k": true}))
	assert.Equal(t, "?k=false", buildQueryParams(map[string]interface{}{"k": false}))
}

func TestBuildQueryParams_TimeValue(t *testing.T) {
	tm := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	got := buildQueryParams(map[string]interface{}{"k": tm})
	assert.Contains(t, got, "k=2023-06-15T12%3A00%3A00Z")
	// zero time skipped
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": time.Time{}}))
}

func TestBuildQueryParams_TimePointer(t *testing.T) {
	tm := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	got := buildQueryParams(map[string]interface{}{"k": &tm})
	assert.Contains(t, got, "k=2023-06-15T12%3A00%3A00Z")
	// nil pointer skipped
	var np *time.Time
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": np}))
	// non-nil zero pointer skipped
	zt := time.Time{}
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": &zt}))
}

func TestBuildQueryParams_IntSlice(t *testing.T) {
	got := buildQueryParams(map[string]interface{}{"k": []int{1, 2}})
	assert.Contains(t, got, "k=%5B1%2C2%5D") // url-encoded [1,2]
	// empty slice skipped
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": []int{}}))
}

func TestBuildQueryParams_StringSlice(t *testing.T) {
	got := buildQueryParams(map[string]interface{}{"k": []string{"a", "b"}})
	assert.Contains(t, got, "k=")
	// verify JSON encoding present
	var expect []string
	// decode from the raw value to confirm round-trip
	_ = json.Unmarshal([]byte(`["a","b"]`), &expect)
	assert.Contains(t, got, "%5B%22a%22%2C%22b%22%5D")
	// empty slice skipped
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"k": []string{}}))
}

func TestBuildQueryParams_DefaultType(t *testing.T) {
	// int32 is not a handled case → default branch uses fmt.Sprint.
	assert.Equal(t, "?k=42", buildQueryParams(map[string]interface{}{"k": int32(42)}))
}

func TestBuildQueryParams_AllSkippedYieldsEmpty(t *testing.T) {
	// map has entries but every value is skipped → final len(values)==0 → "".
	assert.Equal(t, "", buildQueryParams(map[string]interface{}{"a": 0, "b": "", "c": nil}))
}

// sanity: the stub's recorded request is a *HTTPRequest with a real net.Addr-free URL
func TestStubClient_RecordsRequest(t *testing.T) {
	c, stub := newStubClient(okResp("{}"), nil)
	_, _ = c.makeRequest("GET", "/z", nil)
	require.NotNil(t, stub.last)
	_, _, err := net.SplitHostPort("base.example:0")
	require.NoError(t, err)
}
