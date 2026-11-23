package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func jsonBodyContext(t *testing.T, body []byte) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Set(common.KeyRequestBody, body)
	return c
}

// ---------------------------------------------------------------------------
// ResolveIncomingBillingExprRequestInput
// ---------------------------------------------------------------------------

func TestResolveIncomingBillingExprRequestInput_ReadsBody(t *testing.T) {
	body := []byte(`{"service_tier":"fast"}`)
	c := jsonBodyContext(t, body)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "application/json"}}

	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	require.Equal(t, body, input.Body)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
}

func TestResolveIncomingBillingExprRequestInput_PreloadedMergesHeaders(t *testing.T) {
	c := jsonBodyContext(t, []byte(`{"ignored":true}`))
	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"X-From-Info": "info", "Shared": "info-wins-no"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Shared": "preloaded-wins", "X-From-Input": "input"},
			Body:    []byte(`{"preloaded":true}`),
		},
	}
	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	// Preloaded body wins; request body is not re-read.
	require.Equal(t, []byte(`{"preloaded":true}`), input.Body)
	// RequestHeaders form the base, preloaded input.Headers override on conflict.
	require.Equal(t, "info", input.Headers["X-From-Info"])
	require.Equal(t, "input", input.Headers["X-From-Input"])
	require.Equal(t, "preloaded-wins", input.Headers["Shared"])
}

func TestResolveIncomingBillingExprRequestInput_NilInfo(t *testing.T) {
	c := jsonBodyContext(t, []byte(`{"a":1}`))
	input, err := ResolveIncomingBillingExprRequestInput(c, nil)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"a":1}`), input.Body)
	require.Empty(t, input.Headers)
}

func TestResolveIncomingBillingExprRequestInput_NonJSONContentType(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("plain"))
	c.Request.Header.Set("Content-Type", "text/plain")
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "text/plain"}}

	input, err := ResolveIncomingBillingExprRequestInput(c, info)
	require.NoError(t, err)
	require.Nil(t, input.Body, "non-JSON body is not read for billing expr")
}

// ---------------------------------------------------------------------------
// BuildBillingExprRequestInputFromRequest
// ---------------------------------------------------------------------------

func TestBuildBillingExprRequestInputFromRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:     "gemini-3.1-pro-preview",
		Stream:    lo.ToPtr(true),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens: lo.ToPtr(uint(3000)),
	}
	input, err := BuildBillingExprRequestInputFromRequest(request, map[string]string{
		"Content-Type": "application/json",
		"X-Test":       "1",
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, "1", input.Headers["X-Test"])
	require.True(t, ojson.GetBytes(input.Body, "stream").Bool())
	require.Equal(t, "user", ojson.GetBytes(input.Body, "messages.0.role").String())
	require.Equal(t, float64(3000), ojson.GetBytes(input.Body, "max_tokens").Float())
}

func TestBuildBillingExprRequestInputFromRequest_NilRequest(t *testing.T) {
	input, err := BuildBillingExprRequestInputFromRequest(nil, map[string]string{"X": "y"})
	require.NoError(t, err)
	require.Equal(t, "y", input.Headers["X"])
	require.Nil(t, input.Body)
}

// ---------------------------------------------------------------------------
// isJSONContentType / cloneStringMap / cloneRequestInput
// ---------------------------------------------------------------------------

func TestIsJSONContentType(t *testing.T) {
	require.True(t, isJSONContentType("application/json"))
	require.True(t, isJSONContentType("  APPLICATION/JSON  "))
	require.True(t, isJSONContentType("application/json; charset=utf-8"))
	require.False(t, isJSONContentType("text/plain"))
	require.False(t, isJSONContentType(""))
}

func TestCloneStringMap(t *testing.T) {
	require.Equal(t, map[string]string{}, cloneStringMap(nil))
	require.Equal(t, map[string]string{}, cloneStringMap(map[string]string{}))

	src := map[string]string{"a": "1", "  ": "blank-key-dropped", "": "empty-key-dropped", "b": "2"}
	got := cloneStringMap(src)
	require.Equal(t, map[string]string{"a": "1", "b": "2"}, got)

	// It is a copy, not the same reference.
	got["a"] = "changed"
	require.Equal(t, "1", src["a"])
}

func TestCloneRequestInput(t *testing.T) {
	src := billingexpr.RequestInput{
		Headers: map[string]string{"a": "1"},
		Body:    []byte("body"),
	}
	dst := cloneRequestInput(src)
	require.Equal(t, src.Headers, dst.Headers)
	require.Equal(t, src.Body, dst.Body)

	// Deep copy: mutating dst does not touch src.
	dst.Body[0] = 'X'
	require.Equal(t, byte('b'), src.Body[0])

	// Empty body -> nil body clone.
	empty := cloneRequestInput(billingexpr.RequestInput{Headers: map[string]string{}})
	require.Nil(t, empty.Body)
}

// Counterfactual checks for the memory investigation: execute the real pricing
// and settlement code with and without its frozen body. No production behavior
// is changed here. A probe used only at settlement still needs the snapshot.
func TestBillingBodyDependencyMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		expr      string
		usesParam bool
		pre       int
		settled   int
		without   int
	}{
		{"tokens", `tier("base", p * 2 + c * 4)`, false, 1000, 1200, 1200},
		{"header", `tier("base", p * 2 + c * 4) * (header("X-Speed") == "fast" ? 2 : 1)`, false, 2000, 2400, 2400},
		{"clock", `tier("base", p * (hour("UTC") >= 0 ? 2 : 4) + c * 4)`, false, 1000, 1200, 1200},
		{"fixed", `tier("request", fixed(0.01))`, false, 5000, 5000, 5000},
		{"param", `tier("base", p * 2 + c * 4) * (param("service_tier") == "fast" ? 2 : 1)`, true, 2000, 2400, 1200},
		{"settlement_only", `c > 0 ? tier("complete", (param("service_tier") == "fast" ? p * 4 + c * 8 : p * 2 + c * 4)) : tier("input", p * 2)`, true, 1000, 2400, 1200},
		{"dynamic_path", `tier("base", p * 2 + c * 4) * (param(header("X-Path")) == "fast" ? 2 : 1)`, true, 2000, 2400, 1200},
		{"unknown_nested_field", `tier("base", p * 2 + c * 4) * (param("custom.billing.factor") == 3 ? 3 : 1)`, true, 3000, 3600, 1200},
		{"explicit_false", `tier("base", p * 2 + c * 4) * (param("flag") == false ? 2 : 1)`, true, 2000, 2400, 1200},
		{"explicit_zero", `tier("base", p * 2 + c * 4) * (param("level") == 0 ? 2 : 1)`, true, 2000, 2400, 1200},
		{"missing_field", `tier("base", p * 2 + c * 4) * (param("missing") == nil ? 2 : 1)`, true, 2000, 2400, 2400},
		{"array_selector", `tier("base", p * 2 + c * 4) * (param("items.#(kind==\"fast\").factor") == 3 ? 3 : 1)`, true, 3000, 3600, 1200},
		{"probe_alias", `let probe = param; tier("base", p * 2 + c * 4) * (probe("service_tier") == "fast" ? 2 : 1)`, true, 2000, 2400, 1200},
		// Metadata conservatively retains probes in a constant-false branch.
		{"dead_branch", `false ? tier("dead", param("level")) : tier("base", p * 2 + c * 4)`, true, 1000, 1200, 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expressions, err := common.Marshal(map[string]string{"body-audit-model": tc.expr})
			require.NoError(t, err)
			loadTieredConfig(t, `{"body-audit-model":"tiered_expr"}`, string(expressions), `{"default":1}`)
			previous := common.GetDiskCacheConfig()
			t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
			common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 0, MaxSizeMB: 8, Path: t.TempDir()})
			c := jsonBodyContext(t, []byte(`{"service_tier":"fast","flag":false,"level":0,"custom":{"billing":{"factor":3}},"items":[{"kind":"fast","factor":3}]}`))
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			info := &relaycommon.RelayInfo{
				OriginModelName: "body-audit-model", UserGroup: "default", UsingGroup: "default",
				RequestHeaders: map[string]string{"X-Speed": "fast", "X-Path": "service_tier"},
			}
			// Exercise both cold and cached AST metadata, including a param alias.
			for range 2 {
				require.Equal(t, tc.usesParam, billingexpr.UsedVarsByHash(tc.expr, billingexpr.ExprHashString(tc.expr))["param"])
			}
			price, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})
			require.NoError(t, err)
			require.Equal(t, tc.pre, price.QuotaToPreConsume)
			require.NotNil(t, info.BillingRequestInput)
			frozen := *info.BillingRequestInput
			// Replayed/transformed HTTP bodies and request cleanup must not alter
			// the original billing parameters used by late settlement.
			common.CleanupBodyStorage(c)
			c.Request.Body = io.NopCloser(strings.NewReader(`{"service_tier":"slow"}`))
			params := billingexpr.TokenParams{P: 1000, C: 100, Len: 1000}
			ok, quota, full := service.TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, full)
			require.Equal(t, tc.settled, quota)
			withoutBody := frozen
			withoutBody.Body = nil
			info.BillingRequestInput = &withoutBody
			ok, quota, reduced := service.TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, reduced)
			require.Equal(t, tc.without, quota)
			if !tc.usesParam {
				require.Equal(t, full, reduced, "quota, tier, billing unit and rule traces must all match")
			}
			// Input-only repricing is used by timeout accounting. Preserve both
			// its numeric result and traces when the body is not a dependency.
			params.C = 0
			withInput, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, params, frozen)
			require.NoError(t, err)
			withoutInput, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, params, withoutBody)
			require.NoError(t, err)
			require.Equal(t, tc.pre, withInput.ActualQuotaAfterGroup)
			if !tc.usesParam {
				require.Equal(t, withInput, withoutInput)
			}
			t.Logf("pre=%d settlement=%d without_body=%d param_dependency=%t", tc.pre, tc.settled, tc.without, tc.usesParam)
		})
	}
}

func TestBillingBodyImageScalarSnapshot(t *testing.T) {
	loadTieredConfig(t, `{"body-audit-image":"tiered_expr"}`, `{"body-audit-image":"tier(\"image\", fixed(0.01)) * image_count * (param(\"quality\") == \"high\" ? 2 : 1)"}`, `{"default":1}`)
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 0, MaxSizeMB: 8, Path: t.TempDir()})
	c := jsonBodyContext(t, []byte(`{"model":"body-audit-image","prompt":"private fixture prompt","image":"data:image/png;base64,AAAA","n":2,"quality":"high"}`))
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	info := &relaycommon.RelayInfo{
		OriginModelName: "body-audit-image", UserGroup: "default", UsingGroup: "default",
		Request: &dto.ImageRequest{Model: "body-audit-image", Prompt: "private fixture prompt", N: lo.ToPtr(uint(2)), Quality: "high"},
	}
	price, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 20000, price.QuotaToPreConsume)
	frozen := *info.BillingRequestInput
	require.NotContains(t, string(frozen.Body), "private fixture prompt")
	require.NotContains(t, string(frozen.Body), "base64")
	require.Equal(t, int64(2), ojson.GetBytes(frozen.Body, "n").Int())
	require.Equal(t, "high", ojson.GetBytes(frozen.Body, "quality").String())
	common.CleanupBodyStorage(c)
	info.BillingImageCount = lo.ToPtr(1)
	ok, quota, result := service.TryTieredSettle(info, billingexpr.TokenParams{})
	require.True(t, ok)
	require.NotNil(t, result)
	require.Equal(t, 10000, quota)
	require.Equal(t, int64(2), ojson.GetBytes(frozen.Body, "n").Int(), "actual count must not rewrite the frozen request")
	without := frozen
	without.Body = nil
	info.BillingRequestInput = &without
	ok, quota, result = service.TryTieredSettle(info, billingexpr.TokenParams{})
	require.True(t, ok)
	require.NotNil(t, result)
	require.Equal(t, 5000, quota, "dropping even the scalar quality snapshot undercharges")
	t.Logf("scalar_body_bytes=%d pre=20000 actual_count=1 settled=10000 without_scalar_body=5000", len(frozen.Body))
}

func TestBillingBodyEnvironmentAccessMustRetainBody(t *testing.T) {
	for name, expression := range map[string]string{
		"member":        `tier("base", p * 2 + c * 4) * ($env.param("service_tier") == "fast" ? 2 : 1)`,
		"indexed_alias": `let probe = $env["param"]; tier("base", p * 2 + c * 4) * (probe("service_tier") == "fast" ? 2 : 1)`,
		"dynamic_alias": `let probe = get($env, header("X-Probe")); tier("base", p * 2 + c * 4) * (probe("service_tier") == "fast" ? 2 : 1)`,
		"env_alias":     `let e = $env; tier("base", p * 2 + c * 4) * (e.param("service_tier") == "fast" ? 2 : 1)`,
	} {
		t.Run(name, func(t *testing.T) {
			expressions, err := common.Marshal(map[string]string{"body-audit-model": expression})
			require.NoError(t, err)
			loadTieredConfig(t, `{"body-audit-model":"tiered_expr"}`, string(expressions), `{"default":1}`)
			c := jsonBodyContext(t, []byte(`{"service_tier":"fast"}`))
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			info := &relaycommon.RelayInfo{
				OriginModelName: "body-audit-model", UserGroup: "default", UsingGroup: "default",
				RequestHeaders: map[string]string{"X-Probe": "param"},
			}
			vars := billingexpr.UsedVars(expression)
			require.False(t, vars["param"], "identifier metadata alone misses an environment member")
			require.True(t, vars["$env"], "environment access must conservatively retain the body")
			price, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})
			require.NoError(t, err)
			require.Equal(t, 2000, price.QuotaToPreConsume)
			ok, quota, result := service.TryTieredSettle(info, billingexpr.TokenParams{P: 1000, C: 100, Len: 1000})
			require.True(t, ok)
			require.NotNil(t, result)
			require.Equal(t, 2400, quota)
			info.BillingRequestInput.Body = nil
			ok, quota, result = service.TryTieredSettle(info, billingexpr.TokenParams{P: 1000, C: 100, Len: 1000})
			require.True(t, ok)
			require.NotNil(t, result)
			require.Equal(t, 1200, quota)
			t.Log("param identifier absent, env identifier present: settlement=2400, without_body=1200")
		})
	}
}

func TestBillingBodyDiskRetentionDiagnostic(t *testing.T) {
	loadTieredConfig(t, `{"body-audit-model":"tiered_expr"}`, `{"body-audit-model":"tier(\"base\", p * 2 + c * 4)"}`, `{"default":1}`)
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 1, MaxSizeMB: 64, Path: t.TempDir()})
	const requests = 6
	const payloadBytes = 4 << 20
	contexts := make([]*gin.Context, 0, requests)
	infos := make([]*relaycommon.RelayInfo, 0, requests)
	t.Cleanup(func() {
		for _, c := range contexts {
			common.CleanupBodyStorage(c)
		}
	})
	for range requests {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/body-audit-model:generateContent", strings.NewReader(`{"data":"`+strings.Repeat("A", payloadBytes)+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		require.True(t, storage.IsDisk())
		c.Request.Body = http.NoBody
		contexts = append(contexts, c)
		infos = append(infos, &relaycommon.RelayInfo{OriginModelName: "body-audit-model", UserGroup: "default", UsingGroup: "default"})
	}
	statsBefore := common.GetDiskCacheStats()
	runtime.GC()
	runtime.GC()
	var before, held, closed, released runtime.MemStats
	runtime.ReadMemStats(&before)
	retainedBytes := 0
	for i, c := range contexts {
		_, err := ModelPriceHelper(c, infos[i], 1000, &types.TokenCountMeta{})
		require.NoError(t, err)
		retainedBytes += len(infos[i].BillingRequestInput.Body)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&held)
	statsHeld := common.GetDiskCacheStats()
	require.Equal(t, statsBefore.BodyMemoryReservedBytes, statsHeld.BodyMemoryReservedBytes)
	require.Equal(t, statsBefore.CurrentMemoryUsageBytes, statsHeld.CurrentMemoryUsageBytes)
	require.GreaterOrEqual(t, retainedBytes, requests*payloadBytes)
	for _, c := range contexts {
		common.CleanupBodyStorage(c)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&closed)
	for _, info := range infos {
		// Removing only the redundant body is the experiment's intervention;
		// headers, snapshot, expression and RelayInfo remain live.
		info.BillingRequestInput.Body = nil
		ok, quota, result := service.TryTieredSettle(info, billingexpr.TokenParams{P: 1000, C: 100, Len: 1000})
		require.True(t, ok)
		require.NotNil(t, result)
		require.Equal(t, 1200, quota)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&released)
	runtime.KeepAlive(infos)
	runtime.KeepAlive(contexts)
	require.Greater(t, int64(held.HeapAlloc)-int64(before.HeapAlloc), int64(20<<20))
	require.Greater(t, int64(closed.HeapAlloc)-int64(released.HeapAlloc), int64(20<<20))
	t.Logf("requests=%d payload_each=%d frozen_body_bytes=%d budget=1MiB accounted_memory_before=%d accounted_memory_held=%d heap_before=%d heap_held=%d heap_after_storage_close=%d heap_without_billing_body=%d", requests, payloadBytes, retainedBytes, statsBefore.BodyMemoryReservedBytes, statsHeld.BodyMemoryReservedBytes, before.HeapAlloc, held.HeapAlloc, closed.HeapAlloc, released.HeapAlloc)
}
