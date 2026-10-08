package common

import (
	"net/url"
	"strings"
)

// credentialQueryNames are URL query parameter names treated as credentials
// (case-insensitive exact match). TokenAuth itself accepts key (Gemini style
// ?key=<token>); the others are names clients and upstreams commonly use.
// Names containing one of credentialQueryNameParts are credentials as well,
// so access_token, x-amz-security-token, client_secret, x-amz-signature,
// x-amz-credential and the like need no entry here.
var credentialQueryNames = []string{
	"key", "api_key", "apikey", "api-key", "x-api-key", "x-goog-api-key",
	"auth", "authorization", "passwd", "sig", "awsaccesskeyid",
}

// credentialQueryNameParts are lower-case substrings that make any query
// parameter name a credential (session_token, my_secret, x-amz-signature, ...).
// This over-redacts some harmless names (token_name, max_tokens); redacting a
// harmless value is preferred to logging a credential.
var credentialQueryNameParts = []string{"token", "secret", "signature", "password", "credential"}

// RedactedCredentialValue replaces the value of a credential query parameter.
const RedactedCredentialValue = "***"

// IsCredentialQueryName reports whether a decoded query parameter name carries
// a credential. It is the single predicate for every place that logs or stores
// URLs: the access log, the request log and the relay's upstream URL logging.
// It does not allocate.
func IsCredentialQueryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, sensitive := range credentialQueryNames {
		if strings.EqualFold(name, sensitive) {
			return true
		}
	}
	for _, part := range credentialQueryNameParts {
		if containsFoldASCII(name, part) {
			return true
		}
	}
	return false
}

// containsFoldASCII reports whether s contains lowerSub ignoring ASCII case.
// lowerSub must be lower-case ASCII. Unlike strings.ToLower it never allocates.
func containsFoldASCII(s, lowerSub string) bool {
	n := len(lowerSub)
	for i := 0; i+n <= len(s); i++ {
		match := true
		for j := 0; j < n; j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerSub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// RedactRequestURI returns u.RequestURI() with credential values replaced by
// ***: credential query parameters, and credential name=value pairs a client
// encoded into the path (see RedactPathCredentials). Anything that stores or
// prints request URLs (access log, request log) must go through this: Rule 10
// forbids logging tokens.
//
// Always pass the parsed URL, never a decoded "path?query" string: a decoded
// path may itself contain "?" (from %3F), and splitting such a string at the
// first "?" would treat the real query as part of the path.
func RedactRequestURI(u *url.URL) string {
	if u == nil {
		return ""
	}
	// RequestURI escapes "?" in the path as %3F, so its first "?" starts the query.
	return RedactURIString(u.RequestURI())
}

// RedactURIString redacts credential values in a stored request URI
// (request-log entries written by RedactRequestURI, or stored before redaction
// existed). Such a string comes from URL.RequestURI(), whose path keeps "?"
// escaped as %3F, so the first "?" starts the query. Do not use it for gin's
// decoded param.Path. Idempotent: a redacted string is returned as is. It does
// not allocate when nothing matches.
func RedactURIString(uri string) string {
	path, rawQuery, hasQuery := strings.Cut(uri, "?")
	redactedPath, pathChanged := RedactPathCredentials(path)
	redactedQuery, queryChanged := rawQuery, false
	if rawQuery != "" {
		redactedQuery, queryChanged = RedactQueryCredentials(rawQuery)
	}
	if !pathChanged && !queryChanged {
		return uri
	}
	if !hasQuery {
		return redactedPath
	}
	return redactedPath + "?" + redactedQuery
}

// RedactPathCredentials masks credentials a client put into the escaped request
// path, typically a ?key=<token> percent-encoded together with the model name
// (/v1beta/models/m%3Fkey=<token>:generateContent). Percent-encoding is
// resolved, nested encodings included (%3F, %3f, %253F), so "?", "&" and "="
// are recognised however they are encoded. A name is the run of [A-Za-z0-9_-]
// right before "=" ("." ends it, so gemini.key=<token> is the name "key"); when
// IsCredentialQueryName accepts it, everything after "="
// up to the next "&", ";", "?", "#" or the end of the path becomes ***. The
// value is not cut at "/" or ":" because secrets may contain them (AWS secret
// keys, base64); the rest of such a malformed path is not logged.
// It does not allocate when nothing matches.
func RedactPathCredentials(path string) (string, bool) {
	if strings.IndexByte(path, '=') < 0 && strings.IndexByte(path, '%') < 0 {
		return path, false
	}
	var b strings.Builder
	changed := false
	written := 0 // path[:written] is already in b
	nameStart := -1
	nameEscaped := false
	var name [maxPathCredentialName]byte
	nameLen := 0
	for i := 0; i < len(path); {
		c, w := pathUnit(path, i)
		if isPathNameChar(c) {
			if nameStart < 0 {
				nameStart, nameEscaped, nameLen = i, false, 0
			}
			nameEscaped = nameEscaped || w > 1
			if nameLen < len(name) {
				name[nameLen] = c
			}
			nameLen++
			i += w
			continue
		}
		if c != '=' || nameStart < 0 || !isPathCredentialName(path[nameStart:i], nameEscaped, name[:], nameLen) {
			nameStart = -1
			i += w
			continue
		}
		nameStart = -1
		valueStart := i + w
		valueEnd := valueStart
		for valueEnd < len(path) {
			vc, vw := pathUnit(path, valueEnd)
			if vc == '&' || vc == ';' || vc == '?' || vc == '#' {
				break
			}
			valueEnd += vw
		}
		if path[valueStart:valueEnd] != RedactedCredentialValue {
			if !changed {
				changed = true
				b.Grow(len(path))
			}
			b.WriteString(path[written:valueStart])
			b.WriteString(RedactedCredentialValue)
			written = valueEnd
		}
		i = valueEnd
	}
	if !changed {
		return path, false
	}
	b.WriteString(path[written:])
	return b.String(), true
}

// maxPathCredentialName bounds the decoded buffer for a percent-encoded name.
// Credential names are far shorter; a longer encoded name is treated as one.
const maxPathCredentialName = 32

// isPathCredentialName checks a name found in the path: an unescaped name as
// its raw substring, an escaped one through its decoded bytes (the string
// conversion stays on the stack at this size).
func isPathCredentialName(raw string, escaped bool, decoded []byte, n int) bool {
	if !escaped {
		return IsCredentialQueryName(raw)
	}
	if n > len(decoded) {
		return true
	}
	return IsCredentialQueryName(string(decoded[:n]))
}

func isPathNameChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-'
}

// pathUnit decodes the character at path[i] and returns it with its encoded
// width. Nested percent-encoding is resolved: %3F, %3f, %253F and %25253F are
// all "?". A "%" not followed by two hex digits is a literal "%".
func pathUnit(path string, i int) (byte, int) {
	c := path[i]
	j := i + 1
	for c == '%' && j+1 < len(path) && isHexDigit(path[j]) && isHexDigit(path[j+1]) {
		c = hexValue(path[j])<<4 | hexValue(path[j+1])
		j += 2
	}
	return c, j - i
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// RedactQueryCredentials checks each &-separated parameter, keeping parameter
// order and the original encoding, and replaces only credential values. The
// value of any other parameter is scanned like a path (RedactPathCredentials),
// because a credential can hide in it behind an encoded "&":
// ?alt=sse%26key=<token> is one parameter "alt" whose value decodes to
// "sse&key=<token>". It does not allocate when nothing matches.
func RedactQueryCredentials(rawQuery string) (string, bool) {
	var b strings.Builder
	changed := false
	rest := rawQuery
	for {
		part, next, more := strings.Cut(rest, "&")
		name, value, hasValue := strings.Cut(part, "=")
		replacement, replace := "", false
		if hasValue {
			if isCredentialQueryParam(name) {
				replacement, replace = RedactedCredentialValue, true
			} else if redacted, valueChanged := RedactPathCredentials(value); valueChanged {
				replacement, replace = redacted, true
			}
		}
		if replace && !changed {
			changed = true
			b.Grow(len(rawQuery))
			b.WriteString(rawQuery[:len(rawQuery)-len(rest)])
		}
		if replace {
			b.WriteString(name)
			b.WriteByte('=')
			b.WriteString(replacement)
		} else if changed {
			b.WriteString(part)
		}
		if !more {
			break
		}
		if changed {
			b.WriteByte('&')
		}
		rest = next
	}
	if !changed {
		return rawQuery, false
	}
	return b.String(), true
}

// isCredentialQueryParam checks a raw (possibly percent-encoded) parameter
// name; like a path name, a dotted name counts by its last part (gemini.key).
// url.QueryUnescape only allocates when the name contains % or +.
func isCredentialQueryParam(rawName string) bool {
	name := rawName
	if unescaped, err := url.QueryUnescape(rawName); err == nil {
		name = unescaped
	}
	if IsCredentialQueryName(name) {
		return true
	}
	dot := strings.LastIndexByte(name, '.')
	return dot >= 0 && IsCredentialQueryName(name[dot+1:])
}
