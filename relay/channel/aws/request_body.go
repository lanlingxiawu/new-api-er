package aws

import (
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// awsRequestBodyHTTPClient keeps Smithy's early request-body Close from making
// net/http's empty tail probe return a WriterTo error after a response starts.
type awsRequestBodyHTTPClient struct {
	Client bedrockruntime.HTTPClient
}

func (c awsRequestBodyHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return c.Client.Do(req)
	}
	if _, ok := req.Body.(io.WriterTo); !ok {
		return c.Client.Do(req)
	}
	// Retain Read/Close, request metadata and GetBody. In particular, genuine
	// short bodies and non-EOF read errors still reach the HTTP transport.
	request := *req
	request.Body = struct{ io.ReadCloser }{req.Body}
	return c.Client.Do(&request)
}
