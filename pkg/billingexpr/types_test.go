package billingexpr_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ExprHashString (types.go): SHA-256 hex, deterministic, collision-distinct.
// The hash keys the compile cache, so identical strings MUST hash identically
// and any difference MUST produce a different key.
// ---------------------------------------------------------------------------

func TestExprHashString(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		assert.Equal(t, billingexpr.ExprHashString("p * 0.5"), billingexpr.ExprHashString("p * 0.5"))
	})

	t.Run("distinct for different input", func(t *testing.T) {
		assert.NotEqual(t, billingexpr.ExprHashString("p * 0.5"), billingexpr.ExprHashString("p * 0.6"))
	})

	t.Run("matches crypto/sha256 hex", func(t *testing.T) {
		const e = `tier("base", p * 2.5 + c * 15)`
		sum := sha256.Sum256([]byte(e))
		assert.Equal(t, fmt.Sprintf("%x", sum), billingexpr.ExprHashString(e))
	})

	t.Run("empty string hashes to sha256 of empty", func(t *testing.T) {
		sum := sha256.Sum256([]byte(""))
		assert.Equal(t, fmt.Sprintf("%x", sum), billingexpr.ExprHashString(""))
	})

	t.Run("length is 64 hex chars", func(t *testing.T) {
		assert.Len(t, billingexpr.ExprHashString("anything"), 64)
	})
}

// ---------------------------------------------------------------------------
// TokenParams: unset optional fields default to 0 so cache-unaware expressions
// keep working (types.go contract).
// ---------------------------------------------------------------------------

func TestTokenParams_ZeroDefaults(t *testing.T) {
	var p billingexpr.TokenParams
	assert.Equal(t, 0.0, p.P)
	assert.Equal(t, 0.0, p.C)
	assert.Equal(t, 0.0, p.Len)
	assert.Equal(t, 0.0, p.CR)
	assert.Equal(t, 0.0, p.CC)
	assert.Equal(t, 0.0, p.CC1h)
	assert.Equal(t, 0.0, p.Img)
	assert.Equal(t, 0.0, p.ImgO)
	assert.Equal(t, 0.0, p.AI)
	assert.Equal(t, 0.0, p.AO)
}

// ---------------------------------------------------------------------------
// BillingSnapshot: fully serializable (no compiled-program pointers) so it can
// be frozen at pre-consume and re-hydrated at settlement.
// ---------------------------------------------------------------------------

func TestBillingSnapshot_JSONRoundTrip(t *testing.T) {
	snap := billingexpr.BillingSnapshot{
		BillingMode:               "tiered_expr",
		ModelName:                 "claude-sonnet",
		ExprString:                `tier("base", p * 3 + c * 15)`,
		ExprHash:                  billingexpr.ExprHashString(`tier("base", p * 3 + c * 15)`),
		GroupRatio:                1.5,
		EstimatedPromptTokens:     100,
		EstimatedCompletionTokens: 50,
		EstimatedQuotaBeforeGroup: 0.75,
		EstimatedQuotaAfterGroup:  1,
		EstimatedTier:             "base",
		QuotaPerUnit:              500_000,
		ExprVersion:               1,
	}
	data, err := json.Marshal(snap)
	require.NoError(t, err)

	var out billingexpr.BillingSnapshot
	require.NoError(t, json.Unmarshal(data, &out))
	assert.Equal(t, snap, out, "snapshot must round-trip losslessly")
}

// TraceResult carries the tier() side channel; Clamp is intentionally excluded
// from serialization (json:"-") since it is surfaced via a separate audit path.
func TestTieredResult_ClampNotSerialized(t *testing.T) {
	data, err := json.Marshal(billingexpr.TieredResult{MatchedTier: "base"})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "clamp", "Clamp must not be serialized (json:\"-\")")
	assert.Contains(t, string(data), "matched_tier")
}
