package relay_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type governedProviderFixture struct {
	name     string
	format   types.RelayFormat
	stream   bool
	response string
	handler  func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	quota    int
}

func governedProviderFixtures() []governedProviderFixture {
	oai := `{"id":"fixed","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":20}}}`
	cla := `{"id":"fixed","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":7,"cache_read_input_tokens":20}}`
	gem := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":7,"totalTokenCount":107,"cachedContentTokenCount":20}}`
	oaiStream := "data: {\"id\":\"fixed\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"fixed\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":7,\"total_tokens\":107,\"prompt_tokens_details\":{\"cached_tokens\":20}}}\n\ndata: [DONE]\n\n"
	claStream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixed\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1,\"cache_read_input_tokens\":20}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	return []governedProviderFixture{
		{"openai", types.RelayFormatOpenAI, false, oai, openai.OpenaiHandler, 117},
		{"claude", types.RelayFormatClaude, false, cla, func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Response) (*dto.Usage, *types.NewAPIError) {
			return claude.ClaudeHandler(c, r, i)
		}, 137},
		{"gemini", types.RelayFormatGemini, false, gem, gemini.GeminiTextGenerationHandler, 117},
		{"openai_stream", types.RelayFormatOpenAI, true, oaiStream, openai.OaiStreamHandler, 117},
		{"claude_stream", types.RelayFormatClaude, true, claStream, func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Response) (*dto.Usage, *types.NewAPIError) {
			return claude.ClaudeStreamHandler(c, r, i)
		}, 137},
		{"gemini_stream", types.RelayFormatGemini, true, "data: " + gem + "\n\n", gemini.GeminiTextGenerationStreamHandler, 117},
	}
}

type governedProviderResult struct {
	usage *dto.Usage
	body  string
	quota int
}

// Component fixture: real cache, HTTP transport, provider response parser and
// pricing expression. Deliberately excludes auth/channel selection and DB writes.
func runGovernedProvider(client *http.Client, url string, payload []byte, f governedProviderFixture) (governedProviderResult, error) {
	var result governedProviderResult
	in, err := common.CreateBodyStorageFromReader(bytes.NewReader(payload), -1, int64(len(payload)+1))
	if err != nil {
		return result, err
	}
	defer in.Close()
	encoded, err := in.Bytes()
	if err != nil {
		return result, err
	}
	out, err := common.CreateBodyStorage(encoded)
	if err != nil {
		return result, err
	}
	defer out.Close()
	// Seek/read twice exercises request-body replay without introducing retries.
	replay, err := io.ReadAll(out)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(payload, replay) {
		return result, fmt.Errorf("replay payload mismatch")
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	req, err := http.NewRequest("POST", url, common.ReaderOnly(out))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return result, fmt.Errorf("mock upstream status %d", resp.StatusCode)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/", nil)
	info := &relaycommon.RelayInfo{IsStream: f.stream, RelayFormat: f.format, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture", SupportStreamOptions: true}}
	if f.stream {
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
	}
	usage, apiErr := f.handler(c, info, resp)
	if apiErr != nil {
		return result, fmt.Errorf("provider handler: %v", apiErr)
	}
	if f.stream {
		usage = service.FinalizeStreamUsage(c, info, usage)
	}
	if usage == nil {
		return result, fmt.Errorf("missing usage")
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 7 || usage.PromptTokensDetails.CachedTokens != 20 {
		return result, fmt.Errorf("unexpected usage: %+v", usage)
	}
	expr := `p * 2 + c * 10 + cr * 0.2`
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 500000}
	params := service.BuildTieredTokenParams(usage, f.format == types.RelayFormatClaude, map[string]bool{"cr": true})
	ok, quota, _ := service.TryTieredSettle(info, params)
	if !ok || quota != f.quota {
		return result, fmt.Errorf("quota=%d want=%d applied=%t params=%+v", quota, f.quota, ok, params)
	}
	result = governedProviderResult{usage: usage, body: rec.Body.String(), quota: quota}
	if !strings.Contains(result.body, "hello") {
		return result, fmt.Errorf("missing downstream content")
	}
	return result, nil
}

func TestProviderBodyGovernanceParity(t *testing.T) {
	old := common.GetDiskCacheConfig()
	defer common.SetDiskCacheConfig(old)
	payload := []byte(`{"model":"fixture","temperature":0,"stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	for _, f := range governedProviderFixtures() {
		t.Run(f.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(payload, got) {
					w.WriteHeader(400)
					return
				}
				calls.Add(1)
				if f.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				_, _ = io.WriteString(w, f.response)
			}))
			defer upstream.Close()
			var baseline governedProviderResult
			for _, mode := range []string{"off", "memory", "legacy_disk", "disk"} {
				common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: mode == "memory" || mode == "disk", MemoryBudgetMB: 16, Enabled: mode == "disk" || mode == "legacy_disk", ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
				got, err := runGovernedProvider(upstream.Client(), upstream.URL, payload, f)
				require.NoError(t, err, mode)
				if mode == "off" {
					baseline = got
				} else {
					require.Equal(t, baseline.usage, got.usage, mode)
					require.Equal(t, baseline.body, got.body, mode)
					require.Equal(t, baseline.quota, got.quota, mode)
				}
				require.Zero(t, common.GetBodyMemoryReserved())
				require.Zero(t, common.GetDiskCacheStats().DiskReservedBytes)
			}
			require.Equal(t, int64(4), calls.Load())
		})
	}
}

func TestProviderBodyGovernanceLoad(t *testing.T) {
	if os.Getenv("BODY_PROVIDER_LOAD") != "1" {
		t.Skip("opt-in local provider component load")
	}
	old := common.GetDiskCacheConfig()
	defer common.SetDiskCacheConfig(old)
	fixtures := governedProviderFixtures()
	requests, targetRPS := 3000, 0
	if raw := os.Getenv("BODY_PROVIDER_REQUESTS"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.Greater(t, value, 0)
		require.LessOrEqual(t, value, 100000)
		requests = value
	}
	if raw := os.Getenv("BODY_PROVIDER_RPS"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.Greater(t, value, 0)
		require.LessOrEqual(t, value, 5000)
		targetRPS = value
	}
	payload := []byte(`{"model":"fixture","messages":[{"role":"user","content":"` + strings.Repeat("x", 64<<10) + `"}],"temperature":0}`)
	for _, mode := range []string{"off", "memory", "legacy_disk", "disk"} {
		t.Run(mode, func(t *testing.T) {
			common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: mode == "memory" || mode == "disk", MemoryBudgetMB: 128, Enabled: mode == "disk" || mode == "legacy_disk", ThresholdMB: 0, MaxSizeMB: 128, Path: t.TempDir()})
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(got, payload) {
					w.WriteHeader(400)
					return
				}
				var idx int
				if _, err = fmt.Sscanf(r.URL.Path, "/%d", &idx); err != nil || idx < 0 || idx >= len(fixtures) {
					w.WriteHeader(400)
					return
				}
				calls.Add(1)
				f := fixtures[idx]
				if f.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				_, _ = io.WriteString(w, f.response)
			}))
			defer upstream.Close()
			const workers = 64
			latencies := make([]time.Duration, requests)
			errors := make(chan error, requests)
			jobs := make(chan int, workers)
			start := time.Now()
			go func() {
				defer close(jobs)
				for i := 0; i < requests; i++ {
					if targetRPS > 0 {
						if wait := time.Until(start.Add(time.Duration(i) * time.Second / time.Duration(targetRPS))); wait > 0 {
							time.Sleep(wait)
						}
					}
					jobs <- i
				}
			}()
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range jobs {
						beg := time.Now()
						_, err := runGovernedProvider(upstream.Client(), fmt.Sprintf("%s/%d", upstream.URL, i%len(fixtures)), payload, fixtures[i%len(fixtures)])
						latencies[i] = time.Since(beg)
						if err != nil {
							errors <- err
						}
					}
				}()
			}
			wg.Wait()
			elapsed := time.Since(start)
			close(errors)
			for err := range errors {
				require.NoError(t, err)
			}
			require.Equal(t, int64(requests), calls.Load())
			require.Zero(t, common.GetBodyMemoryReserved())
			require.Zero(t, common.GetDiskCacheStats().DiskReservedBytes)
			count, _, err := common.GetDiskCacheInfo()
			require.NoError(t, err)
			require.Zero(t, count)
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			t.Logf("mode=%s requests=%d workers=%d targetRPS=%d elapsed=%s rpm=%.0f p50=%s p95=%s p99=%s", mode, requests, workers, targetRPS, elapsed, float64(requests)*60/elapsed.Seconds(), latencies[requests/2], latencies[requests*95/100], latencies[requests*99/100])
		})
	}
}
