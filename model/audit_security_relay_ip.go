package model

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Only successful log persistence calls this observer. Its queue is independent
// of relay work queues and never retains a request, body, header or credential.
type SecurityRelayIP struct {
	UserID       int
	IP           string
	At, QueuedAt int64
}

var securityRelayIPQueue = make(chan SecurityRelayIP, 1024)
var SecurityRelayIPDropped atomic.Uint64
var SecurityRelayIPMissing atomic.Uint64
var SecurityRelayIPLastObserved atomic.Int64

func QueueSecurityRelayIP(userID int, ip string, at int64) {
	cfg := operation_setting.GetAuditSecuritySetting()
	if !cfg.Enabled || !cfg.IPIntelligenceEnabled || !cfg.RelayIPEnabled || cfg.Suspended || userID <= 0 {
		return
	}
	if ip == "" {
		SecurityRelayIPMissing.Add(1)
		return
	}
	now := time.Now().Unix()
	if len(ip) > 64 || at < now-120 || at > now+60 {
		SecurityRelayIPDropped.Add(1)
		return
	}
	select {
	case securityRelayIPQueue <- SecurityRelayIP{UserID: userID, IP: strings.Clone(ip), At: at, QueuedAt: now}:
	default:
		SecurityRelayIPDropped.Add(1)
	}
}

func TakeSecurityRelayIP() (SecurityRelayIP, bool) {
	select {
	case entry := <-securityRelayIPQueue:
		return entry, true
	default:
		return SecurityRelayIP{}, false
	}
}

func SecurityRelayIPQueued() int { return len(securityRelayIPQueue) }
