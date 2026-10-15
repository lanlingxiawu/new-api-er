package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestPricingPublicationCoalescesModeAndExpression(t *testing.T) {
	usePricingOptionsDB(t)
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	initial := map[string]string{"billing_setting.billing_mode": `{"publication-a":"ratio","publication-b":"tiered_expr"}`, "billing_setting.billing_expr": `{"publication-b":"p*2"}`, "billing_setting.atomic_pricing_enabled": "true"}
	next := map[string]string{"billing_setting.billing_mode": `{"publication-a":"tiered_expr","publication-b":"ratio"}`, "billing_setting.billing_expr": `{"publication-a":"p*3"}`}
	require.NoError(t, publishModelPricingOptions(initial))
	stop := make(chan struct{})
	failed := make(chan string, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, name := range []string{"publication-a", "publication-b"} {
				mode, expression, ok := billing_setting.GetModelBilling(name)
				if mode == billing_setting.BillingModeTieredExpr && (!ok || expression == "") {
					select {
					case failed <- name:
					default:
					}
					return
				}
			}
		}
	}()
	for i := 0; i < 100; i++ {
		require.NoError(t, publishModelPricingOptions(next))
		require.NoError(t, publishModelPricingOptions(initial))
	}
	close(stop)
	readers.Wait()
	require.Empty(t, failed)
}
