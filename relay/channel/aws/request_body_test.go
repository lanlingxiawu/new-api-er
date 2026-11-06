package aws

import (
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type forkAWSRequestErrorBody struct{ err error }

func (b forkAWSRequestErrorBody) Read([]byte) (int, error)         { return 0, b.err }
func (b forkAWSRequestErrorBody) Close() error                     { return nil }
func (b forkAWSRequestErrorBody) WriteTo(io.Writer) (int64, error) { return 0, b.err }

type forkAWSClosingBody struct {
	*strings.Reader
	closes int
	err    error
}

func (b *forkAWSClosingBody) Close() error { b.closes++; return b.err }

func TestAWSRequestBodyCompatibilityForwardsClose(t *testing.T) {
	t.Run("guard", func(t *testing.T) {
		closeErr := errors.New("local request close failure")
		body := &forkAWSClosingBody{Reader: strings.NewReader("abc"), err: closeErr}
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Body = body
		client := awsRequestBodyHTTPClient{Client: awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			return nil, req.Body.Close()
		})}
		_, err := client.Do(request)
		require.ErrorIs(t, err, closeErr)
		require.Equal(t, 1, body.closes)
	})
}

func TestAWSRequestBodyCompatibilityTransportErrors(t *testing.T) {
	readErr := errors.New("local request reader failure")
	for _, tc := range []struct {
		name    string
		body    func() io.ReadCloser
		length  int64
		wantErr string
	}{
		{"exact", func() io.ReadCloser { return io.NopCloser(strings.NewReader("abc")) }, 3, ""},
		{"short", func() io.ReadCloser { return io.NopCloser(strings.NewReader("ab")) }, 3, "ContentLength=3 with Body length 2"},
		{"long", func() io.ReadCloser { return io.NopCloser(strings.NewReader("abcd")) }, 3, "ContentLength=3 with Body length 4"},
		{"non_eof", func() io.ReadCloser { return forkAWSRequestErrorBody{readErr} }, 3, readErr.Error()},
		{"nil", func() io.ReadCloser { return nil }, 0, ""},
		{"no_body", func() io.ReadCloser { return http.NoBody }, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://loopback.invalid/invoke", nil)
			request.RequestURI = ""
			request.Body, request.ContentLength = tc.body(), tc.length
			original := request.Body
			client := awsRequestBodyHTTPClient{Client: awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
				_, hasWriterTo := req.Body.(io.WriterTo)
				if req.Body != http.NoBody {
					require.False(t, hasWriterTo)
				}
				return nil, req.Write(io.Discard)
			})}
			_, err := client.Do(request)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			require.Equal(t, original, request.Body, "do not change the SDK-owned request")
		})
	}
}

func TestAWSRequestBodyCompatibilityPreservesRequest(t *testing.T) {
	t.Run("guard", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://loopback.invalid/model/invoke?fixture=1", bytes.NewReader([]byte("request-body")))
		require.NoError(t, err)
		request.Header.Set("Authorization", "fixture-signature")
		request.Header.Set("X-Amz-Content-Sha256", "fixture-hash")
		request.TransferEncoding = []string{"identity"}
		original := request.Body
		client := awsRequestBodyHTTPClient{Client: awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			require.Same(t, request.Context(), req.Context())
			require.Equal(t, request.Header, req.Header)
			require.Equal(t, request.URL, req.URL)
			require.Equal(t, request.Host, req.Host)
			require.Equal(t, request.ContentLength, req.ContentLength)
			require.Equal(t, request.TransferEncoding, req.TransferEncoding)
			require.NotNil(t, req.GetBody)
			fresh, err := req.GetBody()
			require.NoError(t, err)
			defer fresh.Close()
			freshBytes, err := io.ReadAll(fresh)
			require.NoError(t, err)
			require.Equal(t, "request-body", string(freshBytes))
			require.NotSame(t, request, req)
			_, err = io.Copy(io.Discard, req.Body)
			require.NoError(t, err)
			return nil, req.Body.Close()
		})}
		_, err = client.Do(request)
		require.NoError(t, err)
		require.Equal(t, original, request.Body)
	})
}

func TestAWSRequestBodyCompatibilityNilAndReadOnly(t *testing.T) {
	for _, nilRequest := range []bool{false, true} {
		t.Run("nil_"+strconv.FormatBool(nilRequest), func(t *testing.T) {
			var request *http.Request
			if !nilRequest {
				request = httptest.NewRequest(http.MethodPost, "/", nil)
				request.Body = struct{ io.ReadCloser }{io.NopCloser(strings.NewReader("abc"))}
			}
			client := awsRequestBodyHTTPClient{Client: awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
				require.True(t, request == req, "transparent when there is no WriterTo to hide")
				return nil, nil
			})}
			_, err := client.Do(request)
			require.NoError(t, err)
		})
	}
}

func TestAWSClientAlwaysGuardsRequestBody(t *testing.T) {
	info := newAwsTestRelayInfo()
	info.ApiKey = "fixture-access|fixture-secret|us-east-1"
	c := newAwsTestContext(httptest.NewRecorder(), context.Background())
	client, err := newAwsClient(c, info)
	require.NoError(t, err)
	guard, ok := client.Options().HTTPClient.(awsRequestBodyHTTPClient)
	require.True(t, ok)
	require.NotNil(t, guard.Client)
}
