package channel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestReturnsUpstreamRedirectWithoutFollowing(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	sharedClient := service.GetHttpClient()
	require.NotNil(t, sharedClient)
	require.NotNil(t, sharedClient.CheckRedirect)
	originalRedirectPolicy := reflect.ValueOf(sharedClient.CheckRedirect).Pointer()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	const responseBody = "redirect response"
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			targetRequests.Store(0)
			var sourceRequests atomic.Int32
			type sourceResult struct {
				body []byte
				err  error
			}
			sourceResultCh := make(chan sourceResult, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				body, err := io.ReadAll(r.Body)
				sourceResultCh <- sourceResult{body: body, err: err}
				w.Header().Set("Location", target.URL+"/redirect-target")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer source.Close()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

			req, err := http.NewRequest(http.MethodPost, source.URL, bytes.NewReader([]byte("request body")))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			gotSource := <-sourceResultCh
			require.NoError(t, gotSource.err)

			assert.Equal(t, statusCode, resp.StatusCode)
			assert.Equal(t, target.URL+"/redirect-target", resp.Header.Get("Location"))
			assert.Equal(t, responseBody, string(body))
			assert.Equal(t, []byte("request body"), gotSource.body)
			assert.EqualValues(t, 1, sourceRequests.Load())
			assert.Zero(t, targetRequests.Load())
		})
	}

	assert.Equal(t, originalRedirectPolicy, reflect.ValueOf(sharedClient.CheckRedirect).Pointer(), "the cached client must not be mutated")
}

func TestDoRequestUpstreamExchangeCanBeDisabled(t *testing.T) {
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	for _, disabled := range []string{"false", "true"} {
		t.Run(disabled, func(t *testing.T) {
			t.Setenv("UPSTREAM_EXCHANGE_TRACE_DISABLED", disabled)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
			req, err := http.NewRequest(http.MethodPost, upstream.URL, http.NoBody)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, StartTime: time.Now()}
			response, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusNoContent, response.StatusCode)
			if disabled == "true" {
				require.Empty(t, info.UpstreamExchanges)
				return
			}
			require.Len(t, info.UpstreamExchanges, 1)
			require.NotNil(t, info.UpstreamExchanges[0].GetConnMs)
			require.NotNil(t, info.UpstreamExchanges[0].ConnMs)
			require.NotNil(t, info.UpstreamExchanges[0].WroteMs)
			require.NotNil(t, info.UpstreamExchanges[0].FirstByteMs)
			require.NotNil(t, info.UpstreamExchanges[0].EndMs)
		})
	}
}

func TestDoRequestUpstreamExchangeMetadata(t *testing.T) {
	service.InitHttpClient()
	t.Setenv("UPSTREAM_EXCHANGE_TRACE_DISABLED", "false")
	for _, tc := range []struct {
		name    string
		body    io.Reader
		length  int64
		known   bool
		payload string
	}{
		{name: "known", body: strings.NewReader("payload"), length: 7, known: true, payload: "payload"},
		{name: "empty", body: http.NoBody, known: true},
		{name: "unknown zero", body: io.NopCloser(strings.NewReader("payload")), payload: "payload"},
		{name: "unknown negative", body: io.NopCloser(strings.NewReader("payload")), length: -1, payload: "payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				bodies <- string(body)
				_, _ = io.WriteString(w, "response intact")
			}))
			t.Cleanup(upstream.Close)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
			req, err := http.NewRequest(http.MethodPost, upstream.URL, tc.body)
			require.NoError(t, err)
			req.ContentLength = tc.length
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, StartTime: time.Now()}
			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "response intact", string(body))
			require.Equal(t, tc.payload, <-bodies)
			require.Len(t, info.UpstreamExchanges, 1)
			mark := info.UpstreamExchanges[0]
			serialized, err := common.Marshal(mark)
			require.NoError(t, err)
			require.NotContains(t, string(serialized), `"protocol"`)
			if tc.known {
				require.NotNil(t, mark.RequestBodyBytes)
				require.Equal(t, tc.length, *mark.RequestBodyBytes)
			} else {
				require.Nil(t, mark.RequestBodyBytes)
			}
		})
	}
}

func TestUpstreamExchangeMetadataInLog(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	zero := int64(0)
	failed := false
	info.AppendUpstreamExchange(relaycommon.UpstreamExchangeMark{
		GetConnMs: &zero, WroteMs: &zero, WroteOK: &failed, RequestBodyBytes: &zero,
	})
	other := model.NewLogOther()
	service.AppendRelayLogAdminInfo(ctx, info, other)
	serialized := other.JSONString()
	require.Contains(t, serialized, `"admin_info"`)
	require.Contains(t, serialized, `"upstream_exchange"`)
	require.Contains(t, serialized, `"get_conn_ms":0`)
	require.Contains(t, serialized, `"wrote_ms":0`)
	require.Contains(t, serialized, `"wrote_ok":false`)
	require.Contains(t, serialized, `"request_body_bytes":0`)
	require.NotContains(t, serialized, `"protocol"`)
}
