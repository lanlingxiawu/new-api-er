package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/stretchr/testify/require"
)

type awsMemoryDiagnosticBody struct {
	io.Reader
	closed *int
}

func (b *awsMemoryDiagnosticBody) Close() error { *b.closed++; return nil }

func TestAwsMemoryDiagnosticInternalRetries(t *testing.T) {
	calls, closed := 0, 0
	client := newAwsTestClient(awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Request: req, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Errortype": {"ServiceUnavailableException"}}, Body: &awsMemoryDiagnosticBody{Reader: strings.NewReader(`{"message":"busy"}`), closed: &closed}}, nil
	}))
	_, err := client.InvokeModel(context.Background(), &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsTestModel), Body: []byte(`{}`)}, func(options *bedrockruntime.Options) {
		options.Retryer = retry.NewStandard(func(r *retry.StandardOptions) {
			r.MaxAttempts = 3
			r.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
		})
	})
	require.Error(t, err)
	require.Equal(t, 3, calls, "one adapter invoke can contain multiple SDK HTTP attempts before outer retry policy sees the error")
	require.Equal(t, calls, closed)
}

// Exercise the actual SDK with an in-process HTTP transport; never call AWS.
func TestAwsMemoryDiagnosticInvokeModelBuffer(t *testing.T) {
	for _, size := range []int{256*1024 - 1, 256 * 1024, 256*1024 + 1, 4 * 1024 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := []byte(strings.Repeat("x", size))
			client := newAwsTestClient(awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Request: req, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: -1}, nil
			}))
			output, err := client.InvokeModel(context.Background(), &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsTestModel), Body: []byte(`{}`)})
			require.NoError(t, err)
			require.Equal(t, payload, output.Body, "SDK buffers even malformed JSON before the adapter validates it")
		})
	}
}
