package billing_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	AtomicPricingEnabled bool              `json:"atomic_pricing_enabled"`
	BillingMode          map[string]string `json:"billing_mode"`
	BillingExpr          map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	AtomicPricingEnabled: true,
	BillingMode:          make(map[string]string),
	BillingExpr:          make(map[string]string),
}

var billingSnapshot config.Snapshot[BillingSetting]

func publishBillingSnapshot() {
	billingSnapshot.Publish(BillingSetting{AtomicPricingEnabled: billingSetting.AtomicPricingEnabled, BillingMode: lo.Assign(billingSetting.BillingMode), BillingExpr: lo.Assign(billingSetting.BillingExpr)})
}

func init() {
	config.GlobalConfig.RegisterSnapshot("billing_setting", &billingSetting, publishBillingSnapshot)
}

// GetModelBilling returns a coherent mode/expression pair without locking the
// relay. Configuration maps are copied only when administrators publish updates.
func GetModelBilling(model string) (string, string, bool) {
	snapshot := billingSnapshot.Load()
	if !snapshot.AtomicPricingEnabled {
		expression, ok := GetBillingExpr(model)
		return GetBillingMode(model), expression, ok
	}
	mode, exists := snapshot.BillingMode[model]
	if !exists {
		mode = BillingModeRatio
	}
	expression, ok := snapshot.BillingExpr[model]
	return mode, expression, ok
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	var result map[string]string
	config.WithConfigDraft(func() { result = lo.Assign(billingSetting.BillingMode) })
	return result
}

func GetBillingExprCopy() map[string]string {
	var result map[string]string
	config.WithConfigDraft(func() { result = lo.Assign(billingSetting.BillingExpr) })
	return result
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	config.WithConfigDraft(func() {
		if len(billingSetting.BillingMode) > 0 {
			extra[BillingModeField] = lo.Assign(billingSetting.BillingMode)
		}
		if len(billingSetting.BillingExpr) > 0 {
			extra[BillingExprField] = lo.Assign(billingSetting.BillingExpr)
		}
	})
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
