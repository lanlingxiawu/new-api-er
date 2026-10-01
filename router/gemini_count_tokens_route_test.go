package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D-1: :countTokens used to be relayed as generateContent, running and billing
// a real generation. It is answered like an unregistered route instead
// (upstream #7388), before auth and channel selection on /v1beta, so no
// pre-consume or consume log can be written for it.
func TestGeminiCountTokensRejectedAsUnknownRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	serve := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	for _, path := range []string{
		"/v1beta/models/gemini-2.5-flash:countTokens",
		"/v1beta/models/gemini-2.5-flash:countTokens?key=unused",
		"/v1beta/models/gemini-2.5-flash%3AcountTokens",
		"/v1/models/gemini-2.5-flash:countTokens",
		"/v1/models/gemini-2.5-flash:countTokens?key=unused",
	} {
		rec := serve(path)
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "Invalid URL", path)
	}

	// Generation, embedding and other /v1 relay routes still go through auth.
	for _, path := range []string{
		"/v1beta/models/gemini-2.5-flash:generateContent",
		"/v1beta/models/gemini-2.5-flash:streamGenerateContent",
		"/v1beta/models/gemini-embedding-001:embedContent",
		"/v1/models/gemini-2.5-flash:generateContent",
		"/v1/chat/completions",
	} {
		assert.Equal(t, http.StatusUnauthorized, serve(path).Code, path)
	}
}

// Both routes that relay native Gemini paths run the guard before auth, rate
// limiting and channel selection (so a rejected call takes no rate-limit slot
// and cannot fail with 503), and therefore before pre-consume and billing.
func TestGeminiCountTokensGuardPrecedesRelayHandler(t *testing.T) {
	chains := collectRouteChains(t)
	found := map[string]bool{}
	for _, chain := range chains {
		key := chain.method + " " + chain.path
		if key != "POST /v1beta/models/*path" && key != "POST /v1/models/*path" {
			continue
		}
		found[key] = true
		guard := -1
		for i, name := range chain.names {
			if strings.HasSuffix(name, ".rejectGeminiCountTokens") {
				guard = i
			}
		}
		require.GreaterOrEqual(t, guard, 0, "%s has no countTokens guard: %v", key, chain.names)
		for _, later := range []string{".TokenAuth.", ".ModelRequestRateLimit.", ".Distribute."} {
			for i, name := range chain.names {
				if strings.Contains(name, later) {
					assert.Less(t, guard, i, "%s guard must run before %s", key, later)
				}
			}
		}
		assert.Less(t, guard, len(chain.names)-1, "%s guard must run before the relay handler", key)
	}
	assert.Len(t, found, 2)
}
