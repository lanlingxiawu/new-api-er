package service

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
)

// A fixed() billing expression is priced per request like a fixed model price:
// "input only" has nothing to charge, so a timeout in input mode must refund.
func TestRelayTimeoutPerCall(t *testing.T) {
	cases := map[string]struct {
		info relaycommon.RelayInfo
		want bool
	}{
		"fixed model price": {info: relaycommon.RelayInfo{PriceData: types.PriceData{UsePrice: true}}, want: true},
		"fixed() expression": {info: relaycommon.RelayInfo{TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", EstimatedBillingUnit: billingexpr.BillingUnitRequest,
		}}, want: true},
		"token expression": {info: relaycommon.RelayInfo{TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr",
		}}, want: false},
		"ratio pricing": {info: relaycommon.RelayInfo{}, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, relayTimeoutPerCall(&tc.info))
		})
	}
}
