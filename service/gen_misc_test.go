package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers (all prefixed with `misc` to avoid collisions)
// ---------------------------------------------------------------------------

func miscTinyPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ---------------------------------------------------------------------------
// channel_select.go — RetryParam pure methods
// ---------------------------------------------------------------------------

func TestMiscRetryParamGetSetRetry(t *testing.T) {
	t.Parallel()

	p := &RetryParam{}
	require.Equal(t, 0, p.GetRetry(), "nil Retry pointer should read as 0")

	p.SetRetry(5)
	require.Equal(t, 5, p.GetRetry())

	p.SetRetry(0)
	require.Equal(t, 0, p.GetRetry(), "explicit zero must be preserved")
}

func TestMiscRetryParamIncreaseRetry(t *testing.T) {
	t.Parallel()

	p := &RetryParam{}
	p.IncreaseRetry() // nil -> allocate -> 1
	require.Equal(t, 1, p.GetRetry())
	p.IncreaseRetry()
	require.Equal(t, 2, p.GetRetry())
}

func TestMiscRetryParamResetRetryNextTry(t *testing.T) {
	t.Parallel()

	p := &RetryParam{}
	p.SetRetry(3)

	// Arm the "skip once" flag: next IncreaseRetry is consumed without incrementing.
	p.ResetRetryNextTry()
	p.IncreaseRetry()
	require.Equal(t, 3, p.GetRetry(), "first IncreaseRetry after reset must be a no-op")

	// Subsequent calls increment normally.
	p.IncreaseRetry()
	require.Equal(t, 4, p.GetRetry())
}

// ---------------------------------------------------------------------------
// image.go — pure base64/config helpers
// ---------------------------------------------------------------------------

func TestMiscDecodeBase64ImageData(t *testing.T) {
	t.Parallel()

	pngB64 := miscTinyPNGBase64(t)

	t.Run("raw png", func(t *testing.T) {
		cfg, format, clean, err := DecodeBase64ImageData(pngB64)
		require.NoError(t, err)
		require.Equal(t, "png", format)
		require.Equal(t, 2, cfg.Width)
		require.Equal(t, 3, cfg.Height)
		require.Equal(t, pngB64, clean)
	})

	t.Run("data url prefix is stripped", func(t *testing.T) {
		cfg, format, clean, err := DecodeBase64ImageData("data:image/png;base64," + pngB64)
		require.NoError(t, err)
		require.Equal(t, "png", format)
		require.Equal(t, 2, cfg.Width)
		require.Equal(t, pngB64, clean, "clean output must have the prefix removed")
	})

	t.Run("empty string", func(t *testing.T) {
		_, _, _, err := DecodeBase64ImageData("")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty")
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, _, _, err := DecodeBase64ImageData("!!!not-base64!!!")
		require.Error(t, err)
	})

	t.Run("valid base64 but not an image", func(t *testing.T) {
		notImage := base64.StdEncoding.EncodeToString([]byte("this is plainly not an image"))
		_, _, _, err := DecodeBase64ImageData(notImage)
		require.Error(t, err)
	})
}

func TestMiscDecodeBase64FileData(t *testing.T) {
	t.Parallel()

	pngB64 := miscTinyPNGBase64(t)

	t.Run("no comma falls back to image decode", func(t *testing.T) {
		mime, data, err := DecodeBase64FileData(pngB64)
		require.NoError(t, err)
		require.Equal(t, "image/png", mime)
		require.Equal(t, pngB64, data)
	})

	t.Run("full data url returns declared mime without decoding", func(t *testing.T) {
		// The mime-prefixed branch trusts the declared type and does not validate the body.
		mime, data, err := DecodeBase64FileData("data:application/pdf;base64,SGVsbG8=")
		require.NoError(t, err)
		require.Equal(t, "application/pdf", mime)
		require.Equal(t, "SGVsbG8=", data)
	})

	t.Run("comma but no semicolon falls back to image decode", func(t *testing.T) {
		// mimeType has no ';' -> DecodeBase64ImageData on the payload; payload is a png.
		mime, _, err := DecodeBase64FileData("garbageprefix," + pngB64)
		require.NoError(t, err)
		require.Equal(t, "image/png", mime)
	})
}

func TestMiscGetImageConfig(t *testing.T) {
	t.Parallel()

	pngB64 := miscTinyPNGBase64(t)
	raw, err := base64.StdEncoding.DecodeString(pngB64)
	require.NoError(t, err)

	cfg, format, err := getImageConfig(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 2, cfg.Width)
	require.Equal(t, 3, cfg.Height)

	_, _, err = getImageConfig(bytes.NewReader([]byte("nonsense bytes")))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// audio.go — parseAudio / DecodeBase64AudioData
// ---------------------------------------------------------------------------

func TestMiscParseAudio(t *testing.T) {
	t.Parallel()

	encode := func(n int) string {
		return base64.StdEncoding.EncodeToString(make([]byte, n))
	}

	testCases := []struct {
		name     string
		bytes    int
		format   string
		expected float64
	}{
		{"pcm16 24kHz", 48000, "pcm16", 1.0},        // 24000 samples / 24000
		{"g711 ulaw 8kHz", 8000, "g711_ulaw", 1.0}, // 8000 samples / 8000
		{"g711 alaw 8kHz", 4000, "g711_alaw", 0.5},
		{"default branch 8kHz", 8000, "unknown-format", 1.0},
		{"empty payload", 0, "pcm16", 0.0},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dur, err := parseAudio(encode(tc.bytes), tc.format)
			require.NoError(t, err)
			require.InDelta(t, tc.expected, dur, 1e-9)
		})
	}

	t.Run("invalid base64", func(t *testing.T) {
		t.Parallel()
		_, err := parseAudio("!!!bad!!!", "pcm16")
		require.Error(t, err)
	})
}

func TestMiscDecodeBase64AudioData(t *testing.T) {
	t.Parallel()

	t.Run("strips data prefix", func(t *testing.T) {
		out, err := DecodeBase64AudioData("data:audio/mp3;base64,SGVsbG8=")
		require.NoError(t, err)
		require.Equal(t, "SGVsbG8=", out)
	})

	t.Run("no prefix passes through", func(t *testing.T) {
		out, err := DecodeBase64AudioData("SGVsbG8=")
		require.NoError(t, err)
		require.Equal(t, "SGVsbG8=", out)
	})

	t.Run("invalid base64 errors", func(t *testing.T) {
		_, err := DecodeBase64AudioData("data:audio/mp3;base64,!!!!")
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// error.go — wrappers not covered by error_test.go
// ---------------------------------------------------------------------------

func TestMiscMidjourneyErrorWrapper(t *testing.T) {
	t.Parallel()

	resp := MidjourneyErrorWrapper(42, "boom")
	require.NotNil(t, resp)
	require.Equal(t, 42, resp.Code)
	require.Equal(t, "boom", resp.Description)

	withStatus := MidjourneyErrorWithStatusCodeWrapper(42, "boom", http.StatusBadGateway)
	require.NotNil(t, withStatus)
	require.Equal(t, http.StatusBadGateway, withStatus.StatusCode)
	require.Equal(t, 42, withStatus.Response.Code)
	require.Equal(t, "boom", withStatus.Response.Description)
}

func TestMiscClaudeErrorWrapper(t *testing.T) {
	t.Parallel()

	t.Run("plain message is preserved", func(t *testing.T) {
		e := ClaudeErrorWrapper(errors.New("something bad happened"), "code", http.StatusBadRequest)
		require.Equal(t, "something bad happened", e.Error.Message)
		require.Equal(t, "new_api_error", e.Error.Type)
		require.Equal(t, http.StatusBadRequest, e.StatusCode)
		require.False(t, e.LocalError)
	})

	t.Run("upstream network wording is masked", func(t *testing.T) {
		e := ClaudeErrorWrapper(errors.New("dial tcp 1.2.3.4: connection refused"), "code", http.StatusBadGateway)
		require.Equal(t, "请求上游地址失败", e.Error.Message)
	})

	t.Run("get file base64 prefix keeps original", func(t *testing.T) {
		msg := "get file base64 from url http://x failed"
		e := ClaudeErrorWrapper(errors.New(msg), "code", http.StatusBadRequest)
		require.Equal(t, msg, e.Error.Message)
	})

	t.Run("local variant sets LocalError", func(t *testing.T) {
		e := ClaudeErrorWrapperLocal(errors.New("oops"), "code", http.StatusBadRequest)
		require.True(t, e.LocalError)
	})
}

func TestMiscTaskErrorWrapperLocal(t *testing.T) {
	t.Parallel()

	e := TaskErrorWrapperLocal(errors.New("plain task failure"), "task_code", http.StatusBadRequest)
	require.NotNil(t, e)
	require.True(t, e.LocalError)
	require.Equal(t, "task_code", e.Code)
	require.Equal(t, http.StatusBadRequest, e.StatusCode)
	require.Equal(t, "plain task failure", e.Message)
}

func TestMiscTaskErrorFromAPIError(t *testing.T) {
	t.Parallel()

	require.Nil(t, TaskErrorFromAPIError(nil))

	apiErr := types.NewOpenAIError(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	e := TaskErrorFromAPIError(apiErr)
	require.NotNil(t, e)
	require.Equal(t, http.StatusBadGateway, e.StatusCode)
	require.Equal(t, apiErr.Error(), e.Message)
	require.NotEmpty(t, e.Code)
}

func TestMiscCleanErrorText(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", cleanErrorText(nil))
	require.Equal(t, "boom", cleanErrorText(errors.New("boom")))
	// StripRequestIds removes trailing "(request id: ...)" segments.
	require.Equal(t, "boom", cleanErrorText(errors.New("boom (request id: abc)")))
}

func TestMiscParseStatusCodeMappingValue(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		value   any
		wantVal int
		wantOK  bool
	}{
		{"string numeric", "503", 503, true},
		{"string empty", "", 0, false},
		{"string non-numeric", "abc", 0, false},
		{"float integral", float64(503), 503, true},
		{"float fractional", float64(503.5), 0, false},
		{"int", 200, 200, true},
		{"json.Number valid", json.Number("404"), 404, true},
		{"json.Number invalid", json.Number("x"), 0, false},
		{"unsupported type", true, 0, false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseStatusCodeMappingValue(tc.value)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantVal, got)
		})
	}
}

// ---------------------------------------------------------------------------
// exchange_rate.go — in-memory cache math only (no HTTP, no Redis)
// ---------------------------------------------------------------------------

func TestMiscExchangeRateFallbackConstant(t *testing.T) {
	t.Parallel()
	require.InDelta(t, 7.3, exchangeRateFallback, 1e-9)
}

func TestMiscResolveUSDCNYRateFromMemoryCache(t *testing.T) {
	if common.RedisEnabled {
		t.Skip("redis enabled; memory-cache branch not exercised")
	}

	// Save & restore the package-level memory cache.
	memExchangeRateLock.Lock()
	oldRate, oldTime := memExchangeRate, memExchangeRateTime
	memExchangeRate = 6.55
	memExchangeRateTime = time.Now()
	memExchangeRateLock.Unlock()
	t.Cleanup(func() {
		memExchangeRateLock.Lock()
		memExchangeRate, memExchangeRateTime = oldRate, oldTime
		memExchangeRateLock.Unlock()
	})

	rate, ok := resolveUSDCNYRate()
	require.True(t, ok)
	require.InDelta(t, 6.55, rate, 1e-9)

	rate2, ok2 := GetUSDCNYRateWithOK()
	require.True(t, ok2)
	require.InDelta(t, 6.55, rate2, 1e-9)

	require.InDelta(t, 6.55, GetUSDCNYRate(), 1e-9)
}

// ---------------------------------------------------------------------------
// codex_models.go — validation branches + cache + httptest fixtures
// ---------------------------------------------------------------------------

func TestMiscFetchCodexModelsValidation(t *testing.T) {
	t.Parallel()

	validKey := &CodexOAuthKey{AccessToken: "tok", AccountID: "acct"}

	t.Run("nil client", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), nil, "http://x", validKey, "1.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil http client")
	})

	t.Run("nil oauth key", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), http.DefaultClient, "http://x", nil, "1.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil oauth key")
	})

	t.Run("empty base url", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), http.DefaultClient, "   ", validKey, "1.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty baseURL")
	})

	t.Run("missing access token", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), http.DefaultClient, "http://x", &CodexOAuthKey{AccountID: "acct"}, "1.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "access_token")
	})

	t.Run("missing account id", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), http.DefaultClient, "http://x", &CodexOAuthKey{AccessToken: "tok"}, "1.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "account_id")
	})

	t.Run("missing client version", func(t *testing.T) {
		_, _, err := FetchCodexModels(context.Background(), http.DefaultClient, "http://x", validKey, "   ")
		require.Error(t, err)
		require.Contains(t, err.Error(), "client_version")
	})
}

func TestMiscFetchCodexModelsHTTP(t *testing.T) {
	t.Parallel()

	validKey := &CodexOAuthKey{AccessToken: "tok", AccountID: "acct"}

	t.Run("success dedups and drops empty slugs", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			require.Equal(t, "acct", r.Header.Get("ChatGPT-Account-Id"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5"},{"slug":"gpt-5"},{"slug":""},{"slug":"o3"}]}`))
		}))
		defer server.Close()

		status, models, err := FetchCodexModels(context.Background(), server.Client(), server.URL, validKey, "1.0")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, []string{"gpt-5", "o3"}, models)
	})

	t.Run("non-2xx returns status and nil models", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		status, models, err := FetchCodexModels(context.Background(), server.Client(), server.URL, validKey, "1.0")
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, status)
		require.Nil(t, models)
	})
}

func TestMiscCodexClientVersionCacheGet(t *testing.T) {
	t.Parallel()

	now := time.Now()

	t.Run("returns unexpired cached value without calling fetch", func(t *testing.T) {
		cache := &codexClientVersionCache{version: "9.9.9", expiresAt: now.Add(time.Hour)}
		// nil client would error if fetch were reached; it must not be reached.
		v, err := cache.get(context.Background(), nil, "http://unused", now)
		require.NoError(t, err)
		require.Equal(t, "9.9.9", v)
	})

	t.Run("expired with fetch failure falls back to stale value", func(t *testing.T) {
		cache := &codexClientVersionCache{version: "8.8.8", expiresAt: now.Add(-time.Hour)}
		v, err := cache.get(context.Background(), nil, "http://unused", now)
		require.NoError(t, err)
		require.Equal(t, "8.8.8", v, "stale value served when refresh fails")
	})
}

func TestMiscFetchLatestCodexClientVersion(t *testing.T) {
	t.Parallel()

	t.Run("nil client", func(t *testing.T) {
		_, err := fetchLatestCodexClientVersion(context.Background(), nil, "http://x")
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil http client")
	})

	t.Run("stable release", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"name":"1.2.3","draft":false,"prerelease":false}`))
		}))
		defer server.Close()
		v, err := fetchLatestCodexClientVersion(context.Background(), server.Client(), server.URL)
		require.NoError(t, err)
		require.Equal(t, "1.2.3", v)
	})

	t.Run("prerelease rejected", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"name":"1.2.3","draft":false,"prerelease":true}`))
		}))
		defer server.Close()
		_, err := fetchLatestCodexClientVersion(context.Background(), server.Client(), server.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not stable")
	})

	t.Run("empty name rejected", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"name":"  ","draft":false,"prerelease":false}`))
		}))
		defer server.Close()
		_, err := fetchLatestCodexClientVersion(context.Background(), server.Client(), server.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no version name")
	})

	t.Run("non-2xx status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		_, err := fetchLatestCodexClientVersion(context.Background(), server.Client(), server.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "status=500")
	})
}

// ---------------------------------------------------------------------------
// codex_channel_models.go — pure guard clauses (return before any network)
// ---------------------------------------------------------------------------

func TestMiscFetchCodexChannelModelsGuards(t *testing.T) {
	t.Parallel()

	t.Run("nil channel", func(t *testing.T) {
		_, err := FetchCodexChannelModels(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not Codex")
	})

	t.Run("wrong channel type", func(t *testing.T) {
		_, err := FetchCodexChannelModels(&model.Channel{Type: constant.ChannelTypeOpenAI})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not Codex")
	})

	t.Run("multi-key codex channel unsupported", func(t *testing.T) {
		ch := &model.Channel{Type: constant.ChannelTypeCodex}
		ch.ChannelInfo.IsMultiKey = true
		_, err := FetchCodexChannelModels(ch)
		require.Error(t, err)
		require.Contains(t, err.Error(), "multi-key")
	})
}

// ---------------------------------------------------------------------------
// download.go — worker-disabled guard (no network)
// ---------------------------------------------------------------------------

func TestMiscDoWorkerRequestDisabled(t *testing.T) {
	t.Parallel()

	// EnableWorker() is false by default in the test binary.
	_, err := DoWorkerRequest(&WorkerRequest{URL: "https://example.com/x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "worker not enabled")
}
