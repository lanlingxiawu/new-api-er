package controller

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePlatformStatusTradeNos(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{name: "one", input: []string{"ALI-1"}, want: []string{"ALI-1"}},
		{name: "trim and deduplicate", input: []string{" ALI-1 ", "INF-2", "ALI-1"}, want: []string{"ALI-1", "INF-2"}},
		{name: "empty list", input: nil, wantErr: true},
		{name: "blank value", input: []string{"ALI-1", "  "}, wantErr: true},
		{name: "one hundred", input: makeTradeNos(100), want: makeTradeNos(100)},
		{name: "one hundred one", input: makeTradeNos(101), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizePlatformStatusTradeNos(test.input)
			if test.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestMapAlipayPlatformPaymentStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   alipay.TradeStatus
		subCode  string
		want     platformPaymentSnapshot
		accepted bool
	}{
		{name: "success", status: alipay.TradeStatusSuccess, want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: "TRADE_SUCCESS"}, accepted: true},
		{name: "finished", status: alipay.TradeStatusFinished, want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: "TRADE_FINISHED"}, accepted: true},
		{name: "waiting", status: alipay.TradeStatusWaitBuyerPay, want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "WAIT_BUYER_PAY"}, accepted: true},
		{name: "closed", status: alipay.TradeStatusClosed, want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "TRADE_CLOSED"}, accepted: true},
		{name: "not found", subCode: "ACQ.TRADE_NOT_EXIST", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "TRADE_NOT_EXIST"}, accepted: true},
		{name: "unknown status", status: "FUTURE_STATUS", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusUnknown, Raw: "FUTURE_STATUS"}, accepted: true},
		{name: "gateway failure", subCode: "ACQ.SYSTEM_ERROR", accepted: false},
		{name: "empty", accepted: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, accepted := mapAlipayPlatformPaymentStatus(test.status, test.subCode)
			assert.Equal(t, test.accepted, accepted)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestMapInfiniPlatformPaymentStatus(t *testing.T) {
	tests := []struct {
		raw      string
		want     platformPaymentSnapshot
		accepted bool
	}{
		{raw: "paid", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: "paid"}, accepted: true},
		{raw: " processing ", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "processing"}, accepted: true},
		{raw: "PARTIAL_PAID", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "partial_paid"}, accepted: true},
		{raw: "future_status", want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusUnknown, Raw: "future_status"}, accepted: true},
		{raw: "", accepted: false},
	}

	for _, test := range tests {
		t.Run(strings.TrimSpace(test.raw), func(t *testing.T) {
			got, accepted := mapInfiniPlatformPaymentStatus(test.raw)
			assert.Equal(t, test.accepted, accepted)
			assert.Equal(t, test.want, got)
		})
	}
}

type stubAlipayTradeQuerier struct {
	response *alipay.TradeQueryRsp
	err      error
	tradeNo  string
}

func (stub *stubAlipayTradeQuerier) TradeQuery(_ context.Context, request alipay.TradeQuery) (*alipay.TradeQueryRsp, error) {
	stub.tradeNo = request.OutTradeNo
	return stub.response, stub.err
}

func TestQueryAlipayPlatformPaymentStatus(t *testing.T) {
	original := getAlipayTradeQuerier
	t.Cleanup(func() { getAlipayTradeQuerier = original })

	tests := []struct {
		name     string
		response *alipay.TradeQueryRsp
		queryErr error
		want     platformPaymentSnapshot
		wantErr  bool
	}{
		{
			name: "paid",
			response: &alipay.TradeQueryRsp{
				Error:       alipay.Error{Code: alipay.CodeSuccess},
				TradeStatus: alipay.TradeStatusSuccess,
			},
			want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: "TRADE_SUCCESS"},
		},
		{
			name: "trade does not exist",
			response: &alipay.TradeQueryRsp{
				Error: alipay.Error{Code: alipay.CodeBusinessFailed, SubCode: "ACQ.TRADE_NOT_EXIST"},
			},
			want: platformPaymentSnapshot{Status: model.PlatformPaymentStatusNotCredited, Raw: "TRADE_NOT_EXIST"},
		},
		{name: "sdk failure", queryErr: errors.New("gateway unavailable"), wantErr: true},
		{name: "business failure", response: &alipay.TradeQueryRsp{Error: alipay.Error{Code: alipay.CodeBusinessFailed, SubCode: "ACQ.SYSTEM_ERROR"}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &stubAlipayTradeQuerier{response: test.response, err: test.queryErr}
			getAlipayTradeQuerier = func() alipayTradeQuerier { return stub }
			got, err := queryAlipayPlatformPaymentStatus(context.Background(), &model.TopUp{TradeNo: "ALI-123"})
			if test.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.want, got)
			}
			assert.Equal(t, "ALI-123", stub.tradeNo)
		})
	}
}

func TestQueryInfiniPlatformPaymentStatus(t *testing.T) {
	originalBaseURL := infiniPlatformStatusBaseURL
	originalClient := platformStatusHTTPClient
	public := infiniV2TestCredentials(t)
	t.Cleanup(func() { infiniPlatformStatusBaseURL = originalBaseURL; platformStatusHTTPClient = originalClient })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "provider id/+", request.URL.Query().Get("order_id"))
		require.Equal(t, "/v2/acquiring/order", request.URL.Path)
		payload := strings.Join([]string{"INFINI-OPENAPI-V2", request.Header.Get("X-API-Key-ID"), request.Method, request.URL.RequestURI(), request.Header.Get("X-API-Timestamp"), request.Header.Get("X-API-Nonce"), request.Header.Get("Digest")}, "\n") + "\n"
		signature, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(request.Header.Get("Authorization"), `Ed25519 signature="`), `"`))
		require.NoError(t, err)
		require.True(t, ed25519.Verify(public, []byte(payload), signature))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"message":"","data":{"order_id":"provider id/+","status":"paid","client_reference":"INF-123"}}`)
	}))
	defer server.Close()
	infiniPlatformStatusBaseURL = func() string { return server.URL }
	platformStatusHTTPClient = server.Client()

	got, err := queryInfiniPlatformPaymentStatus(context.Background(), &model.TopUp{
		TradeNo:         "INF-123",
		ProviderOrderId: "provider id/+",
	})
	require.NoError(t, err)
	assert.Equal(t, platformPaymentSnapshot{Status: model.PlatformPaymentStatusCredited, Raw: "paid"}, got)
}

func TestQueryInfiniPlatformPaymentStatusFailures(t *testing.T) {
	originalBaseURL := infiniPlatformStatusBaseURL
	originalClient := platformStatusHTTPClient
	infiniV2TestCredentials(t)
	t.Cleanup(func() { infiniPlatformStatusBaseURL = originalBaseURL; platformStatusHTTPClient = originalClient })

	tests := []struct {
		name   string
		body   string
		status int
		delay  time.Duration
	}{
		{name: "http failure", status: http.StatusBadGateway},
		{name: "business failure", status: http.StatusOK, body: `{"code":42,"message":"denied"}`},
		{name: "invalid json", status: http.StatusOK, body: `{`},
		{name: "empty status", status: http.StatusOK, body: `{"code":0,"data":{"order_id":"provider-id"}}`},
		{name: "missing order id", status: http.StatusOK, body: `{"status":"paid"}`},
		{name: "wrong order id", status: http.StatusOK, body: `{"order_id":"other","status":"paid"}`},
		{name: "wrong reference", status: http.StatusOK, body: `{"order_id":"provider-id","status":"paid","client_reference":"other"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			infiniPlatformStatusBaseURL = func() string { return server.URL }
			platformStatusHTTPClient = server.Client()

			_, err := queryInfiniPlatformPaymentStatus(context.Background(), &model.TopUp{TradeNo: "INF-123", ProviderOrderId: "provider-id"})
			assert.Error(t, err)
		})
	}

	_, err := queryInfiniPlatformPaymentStatus(context.Background(), &model.TopUp{TradeNo: "INF-123"})
	assert.Error(t, err)
}

func makeTradeNos(count int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = strings.Repeat("x", i+1)
	}
	return values
}
