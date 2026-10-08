package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(redactTaskArtifactAccessQuery())
	server.Use(gin.LoggerWithFormatter(formatAccessLog))
}

// formatAccessLog renders one access-log line. Gemini-style clients send their
// token as ?key=, so credential query values are redacted before the line
// reaches stdout / log files.
//
// The line is built from the parsed request URL, not from param.Path: gin sets
// param.Path to the decoded URL.Path + "?" + RawQuery, and a %3F in the path
// decodes to "?", so re-splitting param.Path would treat the real query as part
// of the path and log ?key=<token> unredacted.
func formatAccessLog(param gin.LogFormatterParams) string {
	var requestID string
	if param.Keys != nil {
		requestID, _ = param.Keys[common.RequestIdKey].(string)
	}
	tag, _ := param.Keys[RouteTagKey].(string)
	if tag == "" {
		tag = "web"
	}
	return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
		param.TimeStamp.Format("2006/01/02 - 15:04:05"),
		tag,
		requestID,
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		accessLogURI(param),
	)
}

// accessLogURI returns the escaped request path with credential query values
// redacted. Without a request (gin always sets one) only the path part of
// param.Path is kept and the query is dropped.
func accessLogURI(param gin.LogFormatterParams) string {
	if param.Request == nil || param.Request.URL == nil {
		path, _, _ := strings.Cut(param.Path, "?")
		return path
	}
	u := param.Request.URL
	if isOAuthCallbackPath(u.Path) {
		// OAuth callbacks carry one-time codes and state in the query string.
		// Redact the log value only; the handler still needs the original query.
		return common.RedactRequestURI(&url.URL{Path: u.Path, RawPath: u.RawPath})
	}
	if requested, ok := accessLogRequestedPath(param.Path, u.RawQuery); ok && requested != u.Path {
		// A handler rewrote the path (the kling / jimeng adapters route to
		// /v1/video/generations); log the path the client requested.
		original := *u
		original.Path, original.RawPath = requested, ""
		return common.RedactRequestURI(&original)
	}
	return common.RedactRequestURI(u)
}

// accessLogRequestedPath recovers the decoded path gin captured before the
// handlers ran: param.Path is that path, plus "?" + RawQuery when there is a
// query. It only succeeds when the query suffix is exactly the current
// RawQuery and the remaining path has no "?", so a query can never be taken
// for part of the path; otherwise the caller logs the parsed URL.
func accessLogRequestedPath(ginPath, rawQuery string) (string, bool) {
	path := ginPath
	if rawQuery != "" {
		n := len(ginPath) - len(rawQuery) - 1
		if n < 0 || ginPath[n] != '?' || ginPath[n+1:] != rawQuery {
			return "", false
		}
		path = ginPath[:n]
	}
	if strings.IndexByte(path, '?') >= 0 {
		return "", false
	}
	return path, true
}

func isOAuthCallbackPath(path string) bool {
	return strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/oauth/")
}
