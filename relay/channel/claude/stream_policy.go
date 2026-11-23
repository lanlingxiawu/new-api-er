package claude

import (
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// prepareClaudeMainStream hands ownership to the upstream scanner. Disabled
// semantic wrappers become transparent; bounded raw capture remains attached.
// Only Claude response handlers call this, so other upstreams keep their policy.
func prepareClaudeMainStream(resp *http.Response, info *relaycommon.RelayInfo) {
	// An upstream SSE response must not change the client's frozen non-stream mode.
	if !info.UseStrictClaudeStream() {
		return
	}
	enabled := info.ClaudeStreamValidationEnabled()
	info.ApplyClaudeStreamPolicy()
	info.ClaudeStreamPolicyApplied = true
	if enabled {
		if info.StreamSession != nil {
			info.StreamSession.RequireClaudeProtocol = true
		}
		return
	}
	info.ClaudeStreamMainMode = true
	info.StreamSession.Disable()
	if info.StreamDiagnostic != nil {
		info.StreamDiagnostic.Observe(resp)
	}
}
