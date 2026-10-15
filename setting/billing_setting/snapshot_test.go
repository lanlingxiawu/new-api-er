package billing_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestModelBillingSnapshotCoherentPublication(t *testing.T) {
	original := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { original[key] = value; return nil }))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(original)) })
	require.NoError(t, config.GlobalConfig.UpdateFromMap("billing_setting", map[string]string{"atomic_pricing_enabled": "true", "billing_mode": `{"snapshot-model":"ratio"}`, "billing_expr": `{}`}))
	var workers sync.WaitGroup
	inconsistent := make(chan string, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 10000; i++ {
			mode, expression, ok := GetModelBilling("snapshot-model")
			if mode == BillingModeTieredExpr && (!ok || expression != "p*2") {
				select {
				case inconsistent <- expression:
				default:
				}
				return
			}
			data := GetPricingSyncData(nil)
			modes, _ := data[BillingModeField].(map[string]string)
			expressions, _ := data[BillingExprField].(map[string]string)
			if modes["snapshot-model"] == BillingModeTieredExpr && expressions["snapshot-model"] != "p*2" {
				select {
				case inconsistent <- "sync data pair":
				default:
				}
				return
			}
			_ = GetBillingModeCopy()
			_ = GetBillingExprCopy()
		}
	}()
	for i := 0; i < 100; i++ {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("billing_setting", map[string]string{"billing_mode": `{"snapshot-model":"tiered_expr"}`, "billing_expr": `{"snapshot-model":"p*2"}`}))
		require.NoError(t, config.GlobalConfig.UpdateFromMap("billing_setting", map[string]string{"billing_mode": `{"snapshot-model":"ratio"}`, "billing_expr": `{}`}))
	}
	workers.Wait()
	require.Empty(t, inconsistent)
}

func TestModelBillingSnapshotCanDisable(t *testing.T) {
	original := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { original[key] = value; return nil }))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(original)) })
	require.NoError(t, config.GlobalConfig.UpdateFromMap("billing_setting", map[string]string{"atomic_pricing_enabled": "false", "billing_mode": `{"snapshot-model":"tiered_expr"}`, "billing_expr": `{"snapshot-model":"p*3"}`}))
	mode, expression, ok := GetModelBilling("snapshot-model")
	require.True(t, ok)
	require.Equal(t, BillingModeTieredExpr, mode)
	require.Equal(t, "p*3", expression)
}
