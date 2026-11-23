package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type resendTestTransport func(*http.Request) (*http.Response, error)

func (f resendTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSharedResendDelivery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"sent", 200, `{"id":"message-id"}`, false},
		{"rejected", 401, `{"message":"private-provider-detail"}`, true},
		{"empty_id", 200, `{"id":""}`, true},
		{"blank_id", 200, `{"id":"  "}`, true},
		{"invalid_json", 200, `invalid`, true},
		{"oversized_response", 200, strings.Repeat(" ", 4096) + `{"id":"message-id"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client := &http.Client{Transport: resendTestTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "https://api.resend.com/emails", req.URL.String())
				require.Equal(t, "Bearer fixture-key", req.Header.Get("Authorization"))
				deadline, ok := req.Context().Deadline()
				require.True(t, ok)
				parentDeadline, _ := ctx.Deadline()
				require.Equal(t, parentDeadline, deadline)
				var payload struct {
					From    string   `json:"from"`
					To      []string `json:"to"`
					Subject string   `json:"subject"`
					HTML    string   `json:"html"`
				}
				require.NoError(t, DecodeJson(req.Body, &payload))
				require.Equal(t, `"Sender" <sender@example.com>`, payload.From)
				require.Equal(t, []string{"one@example.com", "two@example.com"}, payload.To)
				require.Equal(t, "subject", payload.Subject)
				require.Equal(t, "<p>body</p>", payload.HTML)
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			err := SendResendEmail(ctx, client, " fixture-key ", "Sender <sender@example.com>", "subject", "one@example.com; Two <two@example.com>", "<p>body</p>")
			if tc.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-provider-detail")
				require.NotContains(t, err.Error(), "fixture-key")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSharedResendCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: resendTestTransport(func(req *http.Request) (*http.Response, error) {
		cancel()
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	defer cancel()
	err := SendResendEmail(ctx, client, "fixture-key", "sender@example.com", "subject", "to@example.com", "body")
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
