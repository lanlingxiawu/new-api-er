package common

import (
	"sync/atomic"
	"time"
)

// UpstreamExchangeMark is one upstream HTTP attempt, measured from request start.
// Missing fields were not observed or were unknown. Zero is a real value.
type UpstreamExchangeMark struct {
	GetConnMs        *int64 `json:"get_conn_ms,omitempty"`
	ConnMs           *int64 `json:"conn_ms,omitempty"`
	WroteMs          *int64 `json:"wrote_ms,omitempty"`
	FirstByteMs      *int64 `json:"first_byte_ms,omitempty"`
	EndMs            *int64 `json:"end_ms,omitempty"`
	WroteOK          *bool  `json:"wrote_ok,omitempty"`
	RequestBodyBytes *int64 `json:"request_body_bytes,omitempty"`
}

// Published once so a snapshot cannot pair a write time with a retry's result.
type upstreamWriteMark struct {
	at int64
	ok bool
}

// UpstreamExchangeClock collects transport callbacks for one attempt.
// Callbacks may run on transport goroutines. Snapshot after the request returns.
type UpstreamExchangeClock struct {
	getConn atomic.Int64
	conn    atomic.Int64
	wrote   atomic.Pointer[upstreamWriteMark]
	first   atomic.Int64
}

func (c *UpstreamExchangeClock) MarkGetConn(at time.Time) {
	if c == nil || at.IsZero() {
		return
	}
	c.getConn.CompareAndSwap(0, at.UnixNano())
}

func (c *UpstreamExchangeClock) MarkConn(at time.Time) {
	if c == nil || at.IsZero() {
		return
	}
	c.conn.CompareAndSwap(0, at.UnixNano())
}

func (c *UpstreamExchangeClock) MarkWrote(at time.Time, ok bool) {
	if c == nil || at.IsZero() || c.wrote.Load() != nil {
		return
	}
	c.wrote.CompareAndSwap(nil, &upstreamWriteMark{at: at.UnixNano(), ok: ok})
}

func (c *UpstreamExchangeClock) MarkFirstByte(at time.Time) {
	if c == nil || at.IsZero() {
		return
	}
	c.first.CompareAndSwap(0, at.UnixNano())
}

func (c *UpstreamExchangeClock) Snapshot(start, end time.Time) UpstreamExchangeMark {
	mark := UpstreamExchangeMark{EndMs: millisBetween(start, end)}
	if c == nil {
		return mark
	}
	mark.GetConnMs = millisSince(start, c.getConn.Load())
	mark.ConnMs = millisSince(start, c.conn.Load())
	if wrote := c.wrote.Load(); wrote != nil {
		mark.WroteMs = millisSince(start, wrote.at)
		if mark.WroteMs != nil {
			ok := wrote.ok
			mark.WroteOK = &ok
		}
	}
	mark.FirstByteMs = millisSince(start, c.first.Load())
	return mark
}

func (info *RelayInfo) AppendUpstreamExchange(mark UpstreamExchangeMark) {
	if info == nil || mark.empty() {
		return
	}
	info.UpstreamExchanges = append(info.UpstreamExchanges, mark)
}

func (mark UpstreamExchangeMark) empty() bool {
	return mark.GetConnMs == nil && mark.ConnMs == nil && mark.WroteMs == nil && mark.FirstByteMs == nil && mark.EndMs == nil &&
		mark.WroteOK == nil && mark.RequestBodyBytes == nil
}

func millisBetween(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	ms := end.Sub(start).Milliseconds()
	return &ms
}

func millisSince(start time.Time, unixNano int64) *int64 {
	if start.IsZero() || unixNano <= 0 {
		return nil
	}
	return millisBetween(start, time.Unix(0, unixNano))
}
