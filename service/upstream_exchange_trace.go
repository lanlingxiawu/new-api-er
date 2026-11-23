package service

import (
	"context"
	"net/http/httptrace"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// AttachUpstreamExchangeTrace records connection, body write and first response
// byte without replacing trace hooks already installed for timeout billing.
func AttachUpstreamExchangeTrace(parent context.Context, clock *relaycommon.UpstreamExchangeClock) context.Context {
	if parent == nil || clock == nil {
		return parent
	}
	extra := &httptrace.ClientTrace{
		GetConn: func(string) {
			clock.MarkGetConn(time.Now())
		},
		GotConn: func(httptrace.GotConnInfo) {
			clock.MarkConn(time.Now())
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			clock.MarkWrote(time.Now(), info.Err == nil)
		},
		GotFirstResponseByte: func() {
			clock.MarkFirstByte(time.Now())
		},
	}
	// WithClientTrace already composes every existing hook. Pre-composing the
	// parent trace here would run its timeout/billing callbacks twice.
	return httptrace.WithClientTrace(parent, extra)
}
