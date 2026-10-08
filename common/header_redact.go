package common

import "net/http"

// credentialHeaderNameParts extend IsCredentialQueryName for HTTP header names:
// Authorization / Proxy-Authorization / X-Auth-Token, Cookie / Set-Cookie and
// the *-Api-Key family. Matching errs on the side of masking harmless headers
// such as Idempotency-Key rather than leaking a credential.
var credentialHeaderNameParts = []string{"auth", "cookie", "key"}

// IsCredentialHeaderName reports whether an HTTP header carries credential
// material. Sec-WebSocket-Protocol is included because OpenAI Realtime clients
// pass the API key inside it (openai-insecure-api-key.<key>).
func IsCredentialHeaderName(name string) bool {
	if IsCredentialQueryName(name) || containsFoldASCII(name, "sec-websocket-protocol") {
		return true
	}
	for _, part := range credentialHeaderNameParts {
		if containsFoldASCII(name, part) {
			return true
		}
	}
	return false
}

// RedactCredentialHeadersJSON masks the values of credential headers in a
// JSON-encoded http.Header (the request-log storage format). ok is false when
// raw is not a complete header object — e.g. cut at the size limit — because
// a cut value cannot be masked reliably; callers must then withhold it.
func RedactCredentialHeadersJSON(raw string) (redacted string, ok bool) {
	if raw == "" {
		return "", true
	}
	var headers http.Header
	if err := UnmarshalJsonStr(raw, &headers); err != nil {
		return "", false
	}
	changed := false
	for name, values := range headers {
		if !IsCredentialHeaderName(name) {
			continue
		}
		masked := make([]string, len(values))
		for i := range masked {
			masked[i] = "***"
		}
		headers[name] = masked
		changed = true
	}
	if !changed {
		return raw, true
	}
	encoded, err := Marshal(headers)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}
