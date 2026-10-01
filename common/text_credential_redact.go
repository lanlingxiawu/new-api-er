package common

import (
	"net/url"
	"regexp"
)

// credentialAssignmentPattern finds `name=value` pairs inside free text (log
// bodies, error messages). The value stops at whitespace, quotes, JSON/HTML
// punctuation and URL separators, so a match never swallows the rest of the
// text the way path-oriented redaction would. Percent-encoded names such as
// k%65y are matched and decoded before the credential check.
var credentialAssignmentPattern = regexp.MustCompile(`([A-Za-z0-9_.%-]+)=([^\s"'&;#?<>\\,)}\]]+)`)

// RedactTextCredentials masks the values of credential-looking `name=value`
// pairs (names per IsCredentialQueryName, including dotted names such as
// gemini.key) in free text. It is meant for admin views of stored request /
// response bodies — e.g. an error that echoes a model name the client built
// as `model?key=<token>` — not for the relay hot path.
func RedactTextCredentials(text string) string {
	if text == "" {
		return text
	}
	return credentialAssignmentPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := credentialAssignmentPattern.FindStringSubmatch(match)
		if len(groups) != 3 || groups[2] == "***" {
			return match
		}
		name := groups[1]
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		if !isCredentialAssignmentName(name) {
			return match
		}
		return groups[1] + "=***"
	})
}

// isCredentialAssignmentName applies IsCredentialQueryName to the whole name
// and to the part after its last '.', so `gemini.key` counts as `key`.
func isCredentialAssignmentName(name string) bool {
	if IsCredentialQueryName(name) {
		return true
	}
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return i+1 < len(name) && IsCredentialQueryName(name[i+1:])
		}
	}
	return false
}
